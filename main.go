package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/nats-io/nats.go"
)

type Config struct {
	StationIDs  []int `toml:"station_ids"`
	Concurrency int   `toml:"concurrency"`
}

type StationsResponse struct {
	GetStationsResult struct {
		Stations []Station `json:"Stations"`
	} `json:"GetStationsResult"`
}

type Station struct {
	ID   int    `json:"ID"`
	Name string `json:"Name"`
}

func listStations(httpClient *http.Client) error {
	url := "https://services.viva.sjofartsverket.se/output/vivaoutputservice.svc/vivastation"
	resp, err := httpClient.Get(url)
	if err != nil {
		return fmt.Errorf("failed to fetch stations: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var stationsResp StationsResponse
	if err := json.Unmarshal(body, &stationsResp); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, station := range stationsResp.GetStationsResult.Stations {
		fmt.Fprintf(w, "%d\t%s\n", station.ID, station.Name)
	}
	return w.Flush()
}

func main() {
	configPath := flag.String("config", "config.toml", "path to TOML config file")
	natsURL := flag.String("nats", "nats://localhost:4222", "NATS server URL")
	listFlag := flag.Bool("list", false, "list all available stations")
	flag.Parse()

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	if *listFlag {
		if err := listStations(httpClient); err != nil {
			log.Fatalf("failed to list stations: %v", err)
		}
		return
	}

	var cfg Config
	if _, err := toml.DecodeFile(*configPath, &cfg); err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	if len(cfg.StationIDs) == 0 {
		log.Fatal("no station_ids configured")
	}

	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 1
	}

	nc, err := nats.Connect(*natsURL)
	if err != nil {
		log.Fatalf("failed to connect to NATS: %v", err)
	}
	defer nc.Drain()

	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup

	for _, stationID := range cfg.StationIDs {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			url := fmt.Sprintf("https://services.viva.sjofartsverket.se/output/vivaoutputservice.svc/vivastation/%d", id)

			resp, err := httpClient.Get(url)
			if err != nil {
				log.Printf("failed to fetch station %d: %v", id, err)
				return
			}
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				log.Printf("failed to read response for station %d: %v", id, err)
				return
			}

			subject := fmt.Sprintf("viva.station.%d", id)
			if err := nc.Publish(subject, body); err != nil {
				log.Printf("failed to publish to NATS for station %d: %v", id, err)
				return
			}

			log.Printf("published %d bytes to %s", len(body), subject)
		}(stationID)
	}

	wg.Wait()
	log.Println("all stations processed")

	if err := nc.Flush(); err != nil {
		log.Printf("failed to flush NATS: %v", err)
		os.Exit(1)
	}
}

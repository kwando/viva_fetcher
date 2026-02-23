package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/nats-io/nats.go"
)

type Config struct {
	StationIDs  []int `toml:"station_ids"`
	Concurrency int   `toml:"concurrency"`
}

func main() {
	configPath := flag.String("config", "config.toml", "path to TOML config file")
	natsURL := flag.String("nats", "nats://localhost:4222", "NATS server URL")
	flag.Parse()

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

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

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

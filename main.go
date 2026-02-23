package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/nats-io/nats.go"
)

type Config struct {
	StationIDs  []int  `toml:"station_ids"`
	Concurrency int    `toml:"concurrency"`
	NATSURL     string `toml:"nats_url"`
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

const (
	maxResponseBytes     = 1 << 20
	responseSnippetBytes = 200
	natsTimeout          = 15 * time.Second
)

var (
	stationsListURL      = "https://services.viva.sjofartsverket.se/output/vivaoutputservice.svc/vivastation"
	stationDetailBaseURL = "https://services.viva.sjofartsverket.se/output/vivaoutputservice.svc/vivastation"
)

func readCappedBody(resp *http.Response) ([]byte, bool, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, false, err
	}
	if len(body) == maxResponseBytes {
		return body, true, nil
	}
	return body, false, nil
}

func formatSnippet(body []byte, tooLarge bool) string {
	if tooLarge {
		return "response exceeded 1MB limit"
	}
	if len(body) > responseSnippetBytes {
		body = body[:responseSnippetBytes]
	}
	snippet := strings.TrimSpace(string(body))
	if snippet == "" {
		return "empty response body"
	}
	return snippet
}

func listStations(httpClient *http.Client) error {
	resp, err := httpClient.Get(stationsListURL)
	if err != nil {
		return fmt.Errorf("failed to fetch stations: %w", err)
	}
	defer resp.Body.Close()

	body, tooLarge, err := readCappedBody(resp)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("unexpected status %s: %s", resp.Status, formatSnippet(body, tooLarge))
	}
	if tooLarge {
		return fmt.Errorf("station list response exceeded 1MB limit")
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

	natsFlagSet := false
	flag.CommandLine.Visit(func(f *flag.Flag) {
		if f.Name == "nats" {
			natsFlagSet = true
		}
	})

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

	resolvedNATSURL := "nats://localhost:4222"
	if strings.TrimSpace(cfg.NATSURL) != "" {
		resolvedNATSURL = strings.TrimSpace(cfg.NATSURL)
	}
	if strings.TrimSpace(os.Getenv("NATS_URL")) != "" {
		resolvedNATSURL = strings.TrimSpace(os.Getenv("NATS_URL"))
	}
	if natsFlagSet {
		resolvedNATSURL = *natsURL
	}

	nc, err := nats.Connect(
		resolvedNATSURL,
		nats.DrainTimeout(natsTimeout),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			log.Printf("NATS async error: %v", err)
		}),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			if err != nil {
				log.Printf("NATS disconnected: %v", err)
				return
			}
			log.Printf("NATS disconnected")
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			log.Printf("NATS reconnected to %s", nc.ConnectedUrl())
		}),
	)
	if err != nil {
		log.Fatalf("failed to connect to NATS: %v", err)
	}
	defer func() {
		if err := nc.Drain(); err != nil {
			log.Printf("failed to drain NATS: %v", err)
		}
	}()

	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup

	for _, stationID := range cfg.StationIDs {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			url := fmt.Sprintf("%s/%d", stationDetailBaseURL, id)

			resp, err := httpClient.Get(url)
			if err != nil {
				log.Printf("failed to fetch station %d: %v", id, err)
				return
			}
			defer resp.Body.Close()

			body, tooLarge, err := readCappedBody(resp)
			if err != nil {
				log.Printf("failed to read response for station %d: %v", id, err)
				return
			}
			if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
				log.Printf("station %d returned %s: %s", id, resp.Status, formatSnippet(body, tooLarge))
				return
			}
			if tooLarge {
				log.Printf("station %d response exceeded 1MB limit", id)
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

	if err := nc.FlushTimeout(natsTimeout); err != nil {
		log.Printf("failed to flush NATS: %v", err)
		return
	}
}

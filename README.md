# Viva Fetcher

A Go application that fetches real-time data from Swedish Maritime Administration (Sjöfartsverket) Viva stations and publishes it to NATS.

## Overview

Viva stations are automated measurement stations located along the Swedish coast that collect meteorological and hydrological data such as water levels, wind speed, air pressure, and more. This tool retrieves data from configured stations and publishes it to NATS for downstream processing.

## Requirements

- Go 1.23 or later
- A running NATS server

## Installation

### Build from source

```bash
go build -o viva_fetcher main.go
```

Or using [just](https://github.com/casey/just):

```bash
just build
```

This will build both the local binary and a Linux ARM64 version.

## Configuration

Create a `config.toml` file with the following structure:

```toml
station_ids = [
  68,
  226,
  227,
  # Add more station IDs as needed
]

concurrency = 5
```

- `station_ids`: List of Viva station IDs to fetch data from
- `concurrency`: Maximum number of concurrent fetch operations (default: 1)

### Finding Station IDs

Use the `-list` flag to see all available stations:

```bash
./viva_fetcher -list
```

## Usage

```bash
./viva_fetcher [options]
```

### Options

| Flag     | Default                 | Description                                          |
| -------- | ----------------------- | ---------------------------------------------------- |
| `-config`| `config.toml`           | Path to TOML configuration file                      |
| `-nats`  | `nats://localhost:4222` | NATS server URL                                      |
| `-list`  | `false`                 | List all available stations and exit                 |

### Example

```bash
# List all available stations
./viva_fetcher -list

# Using default config and local NATS
./viva_fetcher

# Custom config and remote NATS server
./viva_fetcher -config /path/to/config.toml -nats nats://nats.example.com:4222
```

## NATS Messages

Data from each station is published to a NATS subject following this pattern:

```
viva.station.<station_id>
```

For example, station 68 publishes to `viva.station.68`.

The message payload contains the raw JSON response from the Viva API.

## License

MIT

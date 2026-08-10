# metrics-watcher

A small tool to collect, monitor, and alert on application metrics.

## Features
- Collect metrics from configured sources
- Simple alerting rules
- Lightweight and easy to run locally or in production

## Requirements
- Go 1.20+ (if this is a Go project)

## Installation
1. Clone the repo
   git clone <repo-url>
2. Build (if applicable)
   go build ./...

## Usage
- Configure the app via the config file (see config.example.yaml if provided)
- Run locally:
  ./metrics-watcher

## Development
- Run tests with `go test ./...`
- Follow standard Go project structure

## Changes summary
- This project follows a Collector pattern: collectors implement `Name()` and
  `Collect(ctx context.Context) error` and register their OpenTelemetry
  instruments during construction (see `internal/collector` and
  `internal/uptime` for an example).
- Metrics are exported via OpenTelemetry's Prometheus exporter and served by
  the Prometheus HTTP handler (default endpoint: http://localhost:2112/metrics).
- The scheduler runs each collector in its own goroutine and performs an
  immediate first-collect at startup so `/metrics` is populated before the
  first tick. Errors from collectors are logged and the scheduler stops on
  context cancellation.
- Go version and build/test commands are declared in `go.mod` and `README.md`;
  this repository uses Go 1.25 (see `go.mod`). Build with `go build ./...` and
  test with `go test ./...`.


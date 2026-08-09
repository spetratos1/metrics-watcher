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


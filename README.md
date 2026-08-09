# metrics-watcher

A small Go service that runs multiple "Collector" components on configurable
intervals, exposes their measurements as Prometheus metrics via an
OpenTelemetry Prometheus exporter, and scrapes metrics from configured targets.

Architecture overview

The watcher uses a **multi-scheduler model**:
- **Federation path**: one scheduler runs the uptime collector and all scrape
  targets (from `config.yaml`) on a shared `scrape.interval`.
- **Derived path**: (if `tfe.enabled` is true) a separate scheduler runs the TFE
  collector on its own `tfe.interval`.

Both schedulers run in parallel as separate goroutines, each polling on their
interval and recording metrics to the same OTEL meter and Prometheus registry.
At shutdown (SIGINT/SIGTERM), all schedulers are stopped, the HTTP server
shuts down gracefully (5s timeout), and the meter provider is shutdown.

General flow
- On startup (`cmd/watcher/main.go`):
  1. Load config from a YAML file (`-config` flag, defaults to `config.yaml`).
  2. Create a Prometheus registry with Go and process collectors.
  3. Create an OpenTelemetry Prometheus exporter (`otelprom.New()`).
  4. Build a `sdkmetric.MeterProvider` and obtain a `metric.Meter`.
  5. Construct the uptime collector (always active).
  6. Construct scrape collectors (one per target in config).
  7. Create the federation scheduler (runs uptime + scrape targets).
  8. If TFE is enabled in config: construct the TFE collector and create a
     separate scheduler for it (gets `TFE_TOKEN` from environment).
  9. Start HTTP server serving Prometheus metrics at `/metrics` with combined
     registry (Go + process + OTEL + scrape store).
  10. Wait for shutdown signal, then stop all schedulers and the server.

Key files and what they do

- `cmd/watcher/main.go`
  - Program entrypoint. Loads config, creates Prometheus registry (with Go and
    process collectors), sets up OTEL exporter and meter, constructs the
    uptime and scrape collectors, and wires them into a federation scheduler.
  - If `tfe.enabled`, also constructs the TFE collector and creates a separate
    TFE scheduler running in parallel.
  - Combines multiple Prometheus registries (Go, process, OTEL, scrape store)
    in the `/metrics` endpoint.
  - Important lifecycle ordering: stop HTTP server (graceful 5s) -> wait for
    all scheduler goroutines to exit (`wg.Wait()`) -> call `provider.Shutdown(ctx)`.

- `internal/config/config.go`
  - Loads and validates config from `config.yaml`. Defines `Config`, `ServerConfig`,
    `ScrapeConfig`, `TFEConfig`, and `Target` structs.
  - Handles YAML parsing and custom `Duration` type for intervals (e.g., "15s").
  - Defaults: server addr `:2112`, scrape interval `15s`, TFE interval `30s`.
  - Validates required fields (target names/URLs, TFE address/organization if enabled).

- `internal/collector/collector.go`
  - Defines the `Collector` interface:
    - `Name() string` — unique collector name for logging
    - `Collect(ctx context.Context) error` — one measurement cycle, idempotent
  - Collectors must register OTEL instruments during construction; `Collect`
    is a synchronous one-shot that returns error on failure.

- `internal/collector/scheduler.go`
  - `Scheduler` runs each collector in its own goroutine with a shared interval.
  - Calls `collectOnce` immediately at startup for each collector so `/metrics`
    is populated before the first ticker tick.
  - Shutdown is driven by `context.Context` cancellation. The scheduler waits
    for all goroutines (`sync.WaitGroup`) before returning.
  - Errors from `Collect` are logged with `log.Printf("collector %q failed: %v", ...)`.

- `internal/uptime/uptime.go`
  - A minimal example collector (always active). Registers a `Float64Gauge`
    named `watcher_uptime_seconds` and records seconds since startup.
  - Use this as the canonical pattern for new OTEL collectors: register
    instruments in `New(meter)`, keep `Collect` idempotent/synchronous.

- `internal/scrape/collector.go`
  - Scrapes Prometheus-format endpoints. Implements `Collector` interface.
  - Fetches metrics from target URL, parses Prometheus text format, adds a
    `target` label with the collector's name, and stores in shared `Store`.

- `internal/scrape/store.go`
  - Thread-safe registry (`Store`) that implements Prometheus `Gatherer` so
    scraped metrics can be combined with other registries in `/metrics` endpoint.
  - Allows multiple scrape collectors to populate one store without collision.

- `internal/tfe/tfe.go`
  - Polls Terraform Enterprise API (optional collector, controlled by config).
  - Reads `TFE_TOKEN` from environment; crashes at startup if TFE is enabled
    but token is missing.
  - Registers three OTEL instruments:
    - `tfe_up` (Int64Gauge): 1 if last poll succeeded, else 0.
    - `tfe_workspaces_total` (Int64Gauge): number of workspaces in organization.
    - `tfe_api_request_duration_seconds` (Float64Histogram): request duration.
  - Uses `organization` label on all metrics. Requests a minimal page (size 1)
    and extracts only the pagination total-count.

- `config.yaml`
  - YAML configuration file. Parsed at startup by `config.Load()`.
  - See example section below.

- `go.mod`
  - Go 1.25, depends on Prometheus client, OpenTelemetry SDK/exporters, and gopkg.in/yaml.v3.

Other internal directories
- `internal/metrics` — currently empty; reserved for future metrics utilities.

Configuration file example

A minimal `config.yaml`:

```yaml
server:
  addr: ":2112"

scrape:
  interval: 15s
  targets:
    - name: "app_metrics"
      url: "http://localhost:8080/metrics"
    - name: "db_metrics"
      url: "http://db.local:9090/metrics"

tfe:
  enabled: false
  # If enabled=true, also set:
  # address: "https://tfe.company.com"
  # organization: "my-org"
  # interval: 30s
  # And export TFE_TOKEN environment variable
```

With TFE enabled:

```bash
export TFE_TOKEN="your-tfe-api-token"
go run ./cmd/watcher -config config.yaml
```

Build, run, and test

- Build everything from the repo root:

```bash
go build ./...
```

- Build the watcher binary:

```bash
go build -o metrics-watcher ./cmd/watcher
```

- Run with default config (`config.yaml` in current directory):

```bash
go run ./cmd/watcher
```

- Run with a custom config file:

```bash
go run ./cmd/watcher -config /path/to/config.yaml
```

- If TFE is enabled in config, set the token:

```bash
export TFE_TOKEN="$(cat ~/.tfe-token)"
go run ./cmd/watcher
```

- The metrics endpoint:

```
http://localhost:2112/metrics
```

- Alternatively, if your config specifies a different port:

```
curl http://localhost:YOUR_PORT/metrics
```

- Run tests:

```bash
go test ./...
```

Patterns and conventions to follow

- **Register instruments during construction**: see `uptime.New()` and `tfe.New()`.
  Don't register in `Collect()` to avoid repeated registrations and metric
  instability.

- **Multi-scheduler model**: The app now runs multiple schedulers in parallel
  (federation + optional TFE). When adding a new collector:
  - If it's orthogonal (different cadence, e.g., TFE), create its own scheduler
    in `main.go` and run it as a separate goroutine.
  - If it fits the federation model (similar cadence to scrape), add it to the
    uptime/scrape scheduler.
  - All schedulers must be waited on before shutdown (via `wg.Wait()`).

- **Immediate first-collect behavior**: all schedulers call `collectOnce`
  immediately at startup before the ticker, so `/metrics` is populated at
  server start. Rely on this when writing collectors.

- **Lifecycle ordering** (in `main.go`):
  1. Stop HTTP server (graceful 5s timeout).
  2. Wait for all scheduler goroutines to exit (`wg.Wait()`).
  3. Call `provider.Shutdown(shutdownCtx)`.

- **Environment variables**: TFE token is read from `TFE_TOKEN`. If `tfe.enabled`
  is true in config but the env var is missing, the app crashes at startup with
  a clear error. Set it before running the watcher.

- **Config validation**: defaults are applied and fields are validated in
  `config.applyDefaultsAndValidate()`. Invalid URLs or missing required fields
  cause startup errors, not silent failures.

Quick how-to: add a new collector

1. Create `internal/<name>/` package with a file like `tfe.go` or `uptime.go`.
2. Implement a constructor `New(meter metric.Meter, ...) (*CollectorType, error)`
   that registers all OTEL instruments and returns the collector struct.
3. Implement the `collector.Collector` interface:
   - `Name() string` — unique collector name for logging
   - `Collect(ctx context.Context) error` — one measurement cycle
4. In `cmd/watcher/main.go`, decide whether to:
   - **Add to federation** (uptime + scrape): append to `baseCols` before
     creating the federation scheduler.
   - **Create new scheduler** (like TFE): instantiate it conditionally (e.g.,
     from config), create a new `collector.Scheduler`, start it in a goroutine,
     and register it in the `wg` `sync.WaitGroup`.
5. If the collector needs environment variables, read them in `main.go` before
   calling the constructor (like `TFE_TOKEN`).
6. If the collector is optional (like TFE), add config fields and validation to
   `internal/config/config.go`.

Example: to add TFE-like metrics for another service (e.g., "API"):
- Create `internal/api/api.go` with `New(meter)` and `Collect()`.
- Add `API APIConfig` to `internal/config/Config` struct with YAML tags.
- In `main.go`, check `cfg.API.Enabled` and create a separate scheduler.

Exported metrics

**Uptime collector** (always active):
- `watcher_uptime_seconds` (Float64Gauge): seconds since watcher started.

**Scrape targets** (from `config.yaml`):
- All metrics scraped from target endpoints, with a `target` label added.

**TFE collector** (optional, if enabled):
- `tfe_up` (Int64Gauge): 1 if last poll succeeded, else 0. Label: `organization`.
- `tfe_workspaces_total` (Int64Gauge): number of workspaces. Label: `organization`.
- `tfe_api_request_duration_seconds` (Float64Histogram): TFE API request latency. Label: `organization`.

**Go runtime** (automatic):
- Go version, memory stats, goroutines, GC info (registered by `collectors.NewGoCollector()`).

**Process metrics** (automatic):
- CPU, memory, file descriptors, uptime (registered by `collectors.NewProcessCollector()`).

Example metrics sample
- See `sample_metrics.txt` for an example of the Prometheus text exposition
  format this service will produce via `/metrics`.

Prometheus & OpenTelemetry primer (for newcomers)
- Metrics vs logs/traces: metrics are numeric measurements over time (counts,
  gauges, distributions). This service exposes metrics collected by short-lived
  "Collector" components and relies on Prometheus to scrape them periodically.
- Scrape model: Prometheus pulls metrics from an HTTP endpoint. This repo
  exposes an endpoint at `/metrics` served by `promhttp.Handler()` (see
  `cmd/watcher/main.go`). Prometheus (external) would be configured to scrape
  `http://<host>:2112/metrics` on a schedule.
- OpenTelemetry (OTEL) exporter: this project uses OTEL's Prometheus exporter
  (`otelprom.New()`) so collectors record metrics via OTEL APIs (meters,
  instruments) and the exporter exposes those as Prometheus-format metrics.
- Meter provider / Meter: application-wide factory objects from OTEL. In
  `main.go` we create an `sdkmetric.MeterProvider` and then call
  `provider.Meter("module")` to get a `metric.Meter` which is passed to
  collectors. Collectors use the meter to register instruments.
- Instruments: the objects you record into. Common kinds:
  - Counter (monotonic, use for totals)
  - Gauge (current value, e.g. uptime, temperature)
  - Histogram/ValueRecorder (distributions / latencies)
  In this codebase `internal/uptime` registers a `Float64Gauge` in its
  `New(meter)` constructor and calls `gauge.Record(ctx, value)` from
  `Collect`.
- Registration timing: register instruments once during collector construction
  (not on every `Collect`) — this keeps metric names/stability consistent and
  avoids repeated registration errors. Follow the `uptime.New` pattern.
- **Labels/dimensions**: OTEL and Prometheus support key/value labels that allow
  dimensional breakdowns. In this code:
  - TFE metrics use `organization` label (e.g., `tfe_up{organization="my-org"}`).
  - Scraped metrics get a `target` label (e.g., `http_requests{target="app_metrics"}`).
  - Register instruments without pre-baked labels; record with OTEL
    `attribute.String("key", "value")` and `metric.WithAttributes(...)`.

- **Exposition format**: `sample_metrics.txt` shows Prometheus text format
  (HELP text, TYPE, and samples). This is what's scraped by external Prometheus
  or time-series DBs. The watcher exposes it at `/metrics`.

- **Multiple registries**: This watcher combines multiple Prometheus registries:
  - Go runtime collector (goroutines, memory, GC).
  - Process collector (CPU, file descriptors, uptime).
  - OTEL exporter (registered OTEL instruments).
  - Scrape store (metrics from target endpoints).
  All are gathered into one `/metrics` response.

- **Why use OTEL + Prometheus exporter**: lets collectors use a single clean
  OTEL API for metrics while remaining compatible with Prometheus-native
  scrapers and tools.

Contact / contributing
- When making structural or lifecycle changes, reference the files above in
  PR descriptions so reviewers can verify ordering and telemetry behavior.

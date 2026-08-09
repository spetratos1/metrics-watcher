# metrics-watcher

A small Go service that runs short-lived "Collector" components on a fixed
interval and exposes their measurements as Prometheus metrics via an
OpenTelemetry Prometheus exporter.

General flow
- On startup (`cmd/watcher/main.go`):
  1. Create an OpenTelemetry Prometheus exporter (`otelprom.New()`).
  2. Build a `sdkmetric.MeterProvider` and obtain a `metric.Meter` for the app.
  3. Construct one or more Collector instances (each registers its OTEL
     instruments during construction).
  4. Start the `collector.Scheduler` which runs each Collector in its own
     goroutine and performs an immediate first collection (so `/metrics` is
     populated at startup) and then collects on a shared ticker interval.
  5. Start an HTTP server serving Prometheus metrics at `:2112/metrics`.
  6. On shutdown signal (SIGINT/SIGTERM) the server is shut down (5s timeout),
     the scheduler goroutines are waited on, and the meter provider is
     shutdown.

Key files and what they do
- `cmd/watcher/main.go`
  - Program entrypoint and lifecycle wiring. Creates the OTEL Prometheus
    exporter, meter provider and meter, constructs collectors, starts the
    scheduler, and serves `/metrics` with `promhttp.Handler()`.
  - Important lifecycle ordering: stop HTTP server (graceful 5s) -> wait for
    scheduler goroutines to exit -> call `provider.Shutdown(ctx)`.

- `internal/collector/collector.go`
  - Defines the `Collector` interface:
    - `Name() string`
    - `Collect(ctx context.Context) error`
  - Collectors must register OTEL instruments during construction and make
    `Collect` a single synchronous measurement cycle that returns an error on
    failure.

- `internal/collector/scheduler.go`
  - `Scheduler` runs each collector in its own goroutine with a shared
    interval. It calls `collectOnce` immediately at startup for each collector
    so `/metrics` is available before the first ticker tick.
  - Shutdown is driven by `context.Context` cancellation. The scheduler waits
    for all goroutines (`sync.WaitGroup`) before returning.
  - Errors returned by `Collect` are logged using `log.Printf("collector %q failed: %v", ...)`.

- `internal/uptime/uptime.go`
  - A minimal example collector. Registers a `Float64Gauge` named
    `watcher_uptime_seconds` during construction and records the seconds since
    start on each `Collect` call.
  - Use this file as the canonical pattern when adding new collectors: register
    instruments in `New(meter)` and keep `Collect` idempotent and synchronous.

- `config.yaml`
  - Placeholder file in the repo root. No parser is implemented today; treat
    it as a future integration point.

- `go.mod`
  - Declares Go toolchain (Go 1.25) and dependencies (Prometheus client and
    OpenTelemetry modules). Respect the declared Go version when running
    builds or CI.

Other internal directories
- `internal/config`, `internal/metrics`, `internal/scrape`, `internal/tfe`
  - Present as directories but currently contain no Go files. Expect them to
    be used by future collectors or integrations.

Build, run, and test
- Build everything from the repo root:

```bash
go build ./...
```

- Run the watcher directly with `go run` (or the built binary):

```bash
go run ./cmd/watcher
# or
./metrics-watcher
```

- The metrics endpoint will be available at:

```
http://localhost:2112/metrics
```

- Run tests:

```bash
go test ./...
```

Patterns and conventions to follow
- Register instruments in collector constructors (see `uptime.New`). Don't
  register in `Collect` to avoid repeated registrations and unstable metrics.
- Scheduler uses a single global interval. It runs one goroutine per collector
  and calls `collectOnce` immediately at startup — rely on this behavior when
  writing collectors that expect an immediate population of metrics.
- When adding lifecycle changes keep the ordering in `main.go` (stop HTTP ->
  wait for scheduler -> shutdown provider).

Quick how-to: add a new collector
1. Create `internal/<name>/` package.
2. Implement `New(meter metric.Meter) (*CollectorType, error)` that registers
   instruments and returns the collector.
3. Implement `Name() string` and `Collect(ctx context.Context) error`.
4. Wire the collector into `cmd/watcher/main.go` and pass it to
   `collector.NewScheduler(interval, ...)`.

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
- Labels/dimensions: OTEL and Prometheus support key/value labels that allow
  dimensional breakdowns (e.g., `region=""`). If you add labels, register
  instruments with the appropriate API and record them with label values.
- Exposition format example: `sample_metrics.txt` shows the Prometheus text
  format this exporter produces (metric HELP, TYPE, and raw samples). This is
  what's scraped by Prometheus.
- Why use OTEL + Prometheus exporter: it lets collectors use a single OTEL
  API for metrics while still being compatible with Prometheus scrapers.

Contact / contributing
- When making structural or lifecycle changes, reference the files above in
  PR descriptions so reviewers can verify ordering and telemetry behavior.

# AGENTS — Guidelines for automated coding agents

Checklist
- Understand the runtime wiring in `cmd/watcher/main.go`
- Follow the Collector pattern in `internal/collector` (interface + scheduler)
- Metric registration belongs inside each collector (see `internal/uptime`)
- Use the existing build/test commands from `README.md` and `go.mod` for Go version

Big picture
- This is a small Go service that builds one or more "Collector" components and
  runs them on a fixed interval. The main entrypoint is `cmd/watcher/main.go`.
- Metrics are exported via OpenTelemetry's Prometheus exporter (created with
  `otelprom.New()` in `main.go`) and served at `http://localhost:2112/metrics` by
  the Prometheus HTTP handler (`promhttp.Handler()`).

Core patterns you must follow (project-specific)
- Collector interface: every collector implements `Name() string` and
  `Collect(ctx context.Context) error` (see `internal/collector/collector.go`).
  - Each collector owns and registers its OTEL instruments during construction.
  - Collect must perform one polling/measurement cycle and return an error on failure.
- Scheduler: `internal/collector/scheduler.go` runs each collector in its own
  goroutine with a shared interval. Important behaviors:
  - `runCollector` calls `collectOnce` immediately at startup so `/metrics` is
    populated before the first ticker tick.
  - The scheduler stops via context cancellation; it waits for all collector
    goroutines to exit (`wg.Wait()`) before returning.
  - Errors from `Collect` are logged with `log.Printf("collector %q failed: %v", ...)`.

How to add a new Collector (concrete, copy-ready steps)
1. Create a package under `internal/` (e.g. `internal/mycollector`).
2. In the package, implement a constructor that accepts a `metric.Meter` and
   registers the necessary instruments (pattern: `uptime.New(meter)` in
   `internal/uptime/uptime.go`). Store instruments on the collector struct.
3. Implement `Name() string` and `Collect(ctx context.Context) error`. Keep
   `Collect` synchronous and idempotent for the single-cycle model used here.
4. Wire the collector into `cmd/watcher/main.go` by creating it and passing it
   to `collector.NewScheduler(interval, ...)`.

Build / run / debug notes (project-specific)
- Go version: declared in `go.mod` (Go 1.25). Use the project's Go toolchain.
- Local run (from repo root):
  - Build everything: `go build ./...`
  - Run the watcher (binary produced by `go build` or `go run ./cmd/watcher`)
  - Metrics endpoint: `http://localhost:2112/metrics`
- Tests: `go test ./...` (see `README.md`). There are currently no test files
  included, but follow standard `go test` behavior.

Integration points & external deps
- OpenTelemetry Prometheus exporter: created with `otelprom.New()` in `main.go`.
  Metric `Meter` is created via `sdkmetric.NewMeterProvider(...).Meter("module")`.
- Prometheus client http handler: `promhttp.Handler()` is used to serve metrics.
- Dependencies are declared in `go.mod`. When adding new libraries, run `go get`.

Conventions and small-but-important details
- Collectors should register instruments during construction and not during
  Collect; this keeps metrics stable and avoids repeated registration.
- Scheduler interval is global in the current design (pass it to
  `collector.NewScheduler(interval, ...)`). If you need per-collector intervals,
  follow the single-goroutine-per-collector pattern in `scheduler.go` and extend
  the scheduler/collector signatures accordingly.
- Graceful shutdown: `main.go` uses a context from `signal.NotifyContext` and
  performs a 5s shutdown for the HTTP server, then waits for scheduler goroutines
  and calls `provider.Shutdown(...)`. Preserve this ordering when changing
  lifecycle code.

Where to look first (curated file list)
- `cmd/watcher/main.go` — program wiring, exporter, server, scheduler lifecycle
- `internal/collector/collector.go` — Collector interface
- `internal/collector/scheduler.go` — scheduler behavior and logging conventions
- `internal/uptime/uptime.go` — simplest example collector; follow this pattern
- `go.mod` and `README.md` — Go version and common build/test commands

If something isn't discoverable
- The repo currently has `internal/config`, `internal/metrics`, `internal/scrape`,
  and `internal/tfe` directories but no visible Go files there. Treat them as
  likely integration points for future collectors; search those dirs if you see
  new code added.

Short examples
- Registering a gauge during construction: see `uptime.New` which calls
  `meter.Float64Gauge("watcher_uptime_seconds", metric.WithDescription(...))`.
- Immediate first-collect behavior: `scheduler.runCollector` calls `collectOnce`
  before starting the ticker so `/metrics` is available at startup.

Limitations captured here
- Single global interval today. Adding per-collector intervals requires
  scheduler changes.
- No config file is parsed yet (`config.yaml` exists as a placeholder).

Contact points for humans: leave PR comments that reference the files above
when making structural or lifecycle changes.


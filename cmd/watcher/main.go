package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/spetratos1/metrics-watcher/internal/collector"
	"github.com/spetratos1/metrics-watcher/internal/config"
	"github.com/spetratos1/metrics-watcher/internal/scrape"
	"github.com/spetratos1/metrics-watcher/internal/uptime"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to the config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("loading config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// One registry holds BOTH our OTEL-authored metrics and the process's own
	// runtime metrics, so a single handler can serve everything.
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	exporter, err := otelprom.New(otelprom.WithRegisterer(reg))
	if err != nil {
		log.Fatalf("creating prometheus exporter: %v", err)
	}
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))
	meter := provider.Meter("github.com/spetratos1/metrics-watcher")

	// Derived metrics (OTEL path).
	uptimeCollector, err := uptime.New(meter)
	if err != nil {
		log.Fatalf("creating uptime collector: %v", err)
	}
	cols := []collector.Collector{uptimeCollector}

	// Federated metrics (Prometheus scrape path). All targets share one store.
	store := scrape.NewStore()
	for _, t := range cfg.Scrape.Targets {
		cols = append(cols, scrape.New(t.Name, t.URL, store))
	}

	scheduler := collector.NewScheduler(time.Duration(cfg.Scrape.Interval), cols...)
	schedulerDone := make(chan struct{})
	go func() {
		scheduler.Run(ctx)
		close(schedulerDone)
	}()

	// Serve OTEL metrics (reg) and scraped metrics (store) from one endpoint.
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(
		prometheus.Gatherers{reg, store},
		promhttp.HandlerOpts{},
	))
	srv := &http.Server{Addr: cfg.Server.Addr, Handler: mux}
	go func() {
		log.Printf("serving metrics on http://localhost%s/metrics", cfg.Server.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown: %v", err)
	}
	<-schedulerDone
	if err := provider.Shutdown(shutdownCtx); err != nil {
		log.Printf("meter provider shutdown: %v", err)
	}
	log.Println("stopped cleanly")
}

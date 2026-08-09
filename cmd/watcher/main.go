package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
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
	"github.com/spetratos1/metrics-watcher/internal/tfe"
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

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	exporter, err := otelprom.New(otelprom.WithRegisterer(reg))
	if err != nil {
		log.Fatalf("creating prometheus exporter: %v", err)
	}
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))
	meter := provider.Meter("github.com/spetratos1/metrics-watcher")

	// --- Federation path: uptime + scraped targets share one scheduler. ---
	uptimeCollector, err := uptime.New(meter)
	if err != nil {
		log.Fatalf("creating uptime collector: %v", err)
	}
	baseCols := []collector.Collector{uptimeCollector}

	store := scrape.NewStore()
	for _, t := range cfg.Scrape.Targets {
		baseCols = append(baseCols, scrape.New(t.Name, t.URL, store))
	}

	schedulers := []*collector.Scheduler{
		collector.NewScheduler(time.Duration(cfg.Scrape.Interval), baseCols...),
	}

	// --- Derived path: TFE gets its own scheduler and cadence, if enabled. ---
	if cfg.TFE.Enabled {
		token := os.Getenv("TFE_TOKEN")
		if token == "" {
			log.Fatal("tfe.enabled is true but TFE_TOKEN is not set")
		}
		tfeCollector, err := tfe.New(cfg.TFE.Address, cfg.TFE.Organization, token, meter)
		if err != nil {
			log.Fatalf("creating tfe collector: %v", err)
		}
		schedulers = append(schedulers,
			collector.NewScheduler(time.Duration(cfg.TFE.Interval), tfeCollector))
	}

	// Run every scheduler; track them so shutdown can wait for all to stop.
	var wg sync.WaitGroup
	for _, sch := range schedulers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sch.Run(ctx)
		}()
	}

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
	wg.Wait()
	if err := provider.Shutdown(shutdownCtx); err != nil {
		log.Printf("meter provider shutdown: %v", err)
	}
	log.Println("stopped cleanly")
}

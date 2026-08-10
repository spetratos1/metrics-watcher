package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/spetratos1/metrics-watcher/internal/collector"
	"github.com/spetratos1/metrics-watcher/internal/uptime"
)

func main() {
	// Cancel ctx on Ctrl-C or SIGTERM. Everything downstream watches it.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	exporter, err := otelprom.New()
	if err != nil {
		log.Fatalf("creating prometheus exporter: %v", err)
	}
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))
	meter := provider.Meter("github.com/spetratos1/metrics-watcher")

	// Build collectors. Add more here as we go.
	uptimeCollector, err := uptime.New(meter)
	if err != nil {
		log.Fatalf("creating uptime collector: %v", err)
	}

	// Run the scheduler in the background.
	scheduler := collector.NewScheduler(10*time.Second, uptimeCollector)
	schedulerDone := make(chan struct{})
	go func() {
		scheduler.Run(ctx)
		close(schedulerDone)
	}()

	// Serve /metrics in the background.
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	srv := &http.Server{Addr: ":2112", Handler: mux}
	go func() {
		log.Printf("serving metrics on http://localhost:2112/metrics")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Block here until a shutdown signal cancels ctx.
	<-ctx.Done()
	log.Println("shutdown signal received")

	// Stop accepting requests, giving in-flight ones up to 5s.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown: %v", err)
	}

	<-schedulerDone // wait for collector goroutines to exit
	if err := provider.Shutdown(shutdownCtx); err != nil {
		log.Printf("meter provider shutdown: %v", err)
	}
	log.Println("stopped cleanly")
}

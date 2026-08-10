package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

func main() {
	// 1. The Prometheus exporter is a PULL reader: it renders the current
	//    value of every instrument each time /metrics is scraped.
	exporter, err := otelprom.New()
	if err != nil {
		log.Fatalf("creating prometheus exporter: %v", err)
	}

	// 2. The MeterProvider is the root object. It holds instrument state
	//    in memory and hands out Meters. This is your "in-memory store."
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(exporter))
	defer func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			log.Printf("shutting down meter provider: %v", err)
		}
	}()

	// 3. A Meter is a namespaced factory for instruments (your metrics).
	meter := provider.Meter("github.com/you/metrics-watcher")

	// 4. An OBSERVABLE gauge: the callback runs at SCRAPE TIME, not now.
	//    This is the pattern our API pollers will use later.
	start := time.Now()
	_, err = meter.Float64ObservableGauge(
		"watcher_uptime_seconds",
		metric.WithDescription("Seconds since the watcher started"),
		metric.WithFloat64Callback(func(_ context.Context, o metric.Float64Observer) error {
			o.Observe(time.Since(start).Seconds())
			return nil
		}),
	)
	if err != nil {
		log.Fatalf("registering uptime gauge: %v", err)
	}

	// 5. Serve it. promhttp.Handler() renders whatever the exporter holds.
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	addr := ":2112"
	log.Printf("serving metrics on http://localhost%s/metrics", addr)
	srv := &http.Server{Addr: addr, Handler: mux}
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server error: %v", err)
	}
}

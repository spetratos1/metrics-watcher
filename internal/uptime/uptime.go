package uptime

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/metric"
)

// Collector reports how long the watcher has been running. It's a stand-in
// that exercises the Collector pattern with no network I/O.
type Collector struct {
	start time.Time
	gauge metric.Float64Gauge
}

// New registers the uptime instrument on meter and returns the collector.
func New(meter metric.Meter) (*Collector, error) {
	gauge, err := meter.Float64Gauge(
		"watcher_uptime_seconds",
		metric.WithDescription("Seconds since the watcher started"),
	)
	if err != nil {
		return nil, err
	}
	return &Collector{start: time.Now(), gauge: gauge}, nil
}

func (c *Collector) Name() string { return "uptime" }

func (c *Collector) Collect(ctx context.Context) error {
	c.gauge.Record(ctx, time.Since(c.start).Seconds())
	return nil
}

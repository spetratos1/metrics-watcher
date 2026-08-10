package collector

import "context"

// Collector performs one cycle of gathering data and recording metrics.
// Each implementation owns the OTEL instruments it records into.
type Collector interface {
	Name() string
	Collect(ctx context.Context) error
}

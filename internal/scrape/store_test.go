// Unit tests for Store — the in-memory holder of scraped metric families
// that also implements prometheus.Gatherer.
//
// What it verifies:
//   - Gather merges families with the same name across different targets
//     into a single family (TestStoreGatherMergesTargets).
//   - Gather is repeatable: calling it twice yields the same result, proving
//     it does not mutate the stored state while merging
//     (TestStoreGatherIsRepeatable). This test guards the exact bug the
//     `clone` step inside Gather was written to prevent — remove the clone
//     and this test goes red.
//   - Store is safe for concurrent use: many goroutines calling Set and
//     Gather at once do not corrupt it (TestStoreConcurrentAccess).
//
// Testing pattern — the race detector:
//   TestStoreConcurrentAccess asserts almost nothing by itself. Its purpose
//   is to be run under Go's race detector:
//
//       go test -race ./internal/scrape/
//
//   -race instruments memory accesses and fails if two goroutines touch the
//   same memory without synchronization. With the RWMutex in Store it passes
//   clean; remove the mutex and -race immediately flags the map access. This
//   is how you PROVE concurrency safety instead of assuming it.
//
// Test data helpers:
//   - family(name, value) builds a minimal single-value gauge MetricFamily
//     using the same protobuf (dto) types the real code stores.
//   - strptr is reused from collector.go (same package); f64ptr is its
//     float64 equivalent. The dto structs use pointer fields (*string,
//     *float64) because in protobuf every field is optional, so tests must
//     hand them addresses of values, not the values directly.
//
// Same-package test (package scrape): it can construct dto families and call
// the package's unexported helpers directly.

package scrape

import (
	"sync"
	"testing"

	dto "github.com/prometheus/client_model/go"
)

func TestStoreGatherMergesTargets(t *testing.T) {
	s := NewStore()
	s.Set("node-a", []*dto.MetricFamily{family("go_goroutines", 12)})
	s.Set("node-b", []*dto.MetricFamily{family("go_goroutines", 8)})

	got, err := s.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d families, want 1 (same name should merge)", len(got))
	}
	if len(got[0].Metric) != 2 {
		t.Errorf("merged family has %d metrics, want 2", len(got[0].Metric))
	}
}

func TestStoreGatherIsRepeatable(t *testing.T) {
	s := NewStore()
	s.Set("node-a", []*dto.MetricFamily{family("go_goroutines", 12)})
	s.Set("node-b", []*dto.MetricFamily{family("go_goroutines", 8)})

	first, _ := s.Gather()
	second, _ := s.Gather()

	if len(first[0].Metric) != len(second[0].Metric) {
		t.Errorf("metric count changed between gathers: %d then %d — Gather is mutating stored state",
			len(first[0].Metric), len(second[0].Metric))
	}
}

func TestStoreConcurrentAccess(t *testing.T) {
	s := NewStore()
	var wg sync.WaitGroup

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				s.Set("node", []*dto.MetricFamily{family("go_goroutines", float64(j))})
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_, _ = s.Gather()
			}
		}()
	}
	wg.Wait()
}

// family builds a single-value gauge family for tests. It reuses strptr from
// collector.go (same package) and adds a float helper.
func family(name string, value float64) *dto.MetricFamily {
	return &dto.MetricFamily{
		Name: strptr(name),
		Type: dto.MetricType_GAUGE.Enum(),
		Metric: []*dto.Metric{
			{Gauge: &dto.Gauge{Value: f64ptr(value)}},
		},
	}
}

func f64ptr(f float64) *float64 { return &f }

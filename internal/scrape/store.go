package scrape

import (
	"sync"

	dto "github.com/prometheus/client_model/go"
)

// Store holds the most recent metric families scraped from each target and
// implements prometheus.Gatherer so the /metrics handler can serve them.
type Store struct {
	mu      sync.RWMutex
	targets map[string][]*dto.MetricFamily
}

func NewStore() *Store {
	return &Store{targets: make(map[string][]*dto.MetricFamily)}
}

// Set replaces the stored families for one target.
func (s *Store) Set(target string, families []*dto.MetricFamily) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.targets[target] = families
}

// Gather merges same-named families across all targets into one family each,
// because a Gatherer must return uniquely-named families.
func (s *Store) Gather() ([]*dto.MetricFamily, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	merged := make(map[string]*dto.MetricFamily)
	for _, families := range s.targets {
		for _, mf := range families {
			name := mf.GetName()
			if existing, ok := merged[name]; ok {
				existing.Metric = append(existing.Metric, mf.Metric...)
				continue
			}
			// Copy the family header so we never mutate what Set stored.
			clone := &dto.MetricFamily{Name: mf.Name, Help: mf.Help, Type: mf.Type}
			clone.Metric = append(clone.Metric, mf.Metric...)
			merged[name] = clone
		}
	}

	out := make([]*dto.MetricFamily, 0, len(merged))
	for _, mf := range merged {
		out = append(out, mf)
	}
	return out, nil
}

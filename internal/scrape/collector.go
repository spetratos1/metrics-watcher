package scrape

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

// Collector scrapes one Prometheus-format endpoint. It satisfies
// collector.Collector and writes results into a shared Store.
type Collector struct {
	name   string
	url    string
	store  *Store
	client *http.Client
}

func New(name, url string, store *Store) *Collector {
	return &Collector{
		name:   name,
		url:    url,
		store:  store,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Collector) Name() string { return c.name }

func (c *Collector) Collect(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetching %s: %w", c.url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("target returned status %d", resp.StatusCode)
	}

	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(resp.Body)
	if err != nil {
		return fmt.Errorf("parsing metrics: %w", err)
	}

	// Tag every series with its source target so metrics from different
	// targets don't collide, then keep each series' labels sorted.
	out := make([]*dto.MetricFamily, 0, len(families))
	for _, mf := range families {
		for _, m := range mf.Metric {
			m.Label = append(m.Label, &dto.LabelPair{
				Name:  strptr("target"),
				Value: strptr(c.name),
			})
			sort.Slice(m.Label, func(i, j int) bool {
				return m.Label[i].GetName() < m.Label[j].GetName()
			})
		}
		out = append(out, mf)
	}

	c.store.Set(c.name, out)
	return nil
}

func strptr(s string) *string { return &s }

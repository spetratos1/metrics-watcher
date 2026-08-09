package tfe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Collector polls the Terraform Enterprise API and records derived metrics
// into OTEL instruments. It satisfies collector.Collector.
type Collector struct {
	address string
	org     string
	token   string
	client  *http.Client

	up             metric.Int64Gauge
	workspaces     metric.Int64Gauge
	requestSeconds metric.Float64Histogram
}

// New registers the TFE instruments on meter and returns the collector.
func New(address, org, token string, meter metric.Meter) (*Collector, error) {
	up, err := meter.Int64Gauge(
		"tfe_up",
		metric.WithDescription("1 if the last TFE poll succeeded, else 0"),
	)
	if err != nil {
		return nil, err
	}
	workspaces, err := meter.Int64Gauge(
		"tfe_workspaces_total",
		metric.WithDescription("Number of workspaces in the organization"),
	)
	if err != nil {
		return nil, err
	}
	requestSeconds, err := meter.Float64Histogram(
		"tfe_api_request_duration_seconds",
		metric.WithDescription("Duration of TFE API requests"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, err
	}

	return &Collector{
		address:        address,
		org:            org,
		token:          token,
		client:         &http.Client{Timeout: 15 * time.Second},
		up:             up,
		workspaces:     workspaces,
		requestSeconds: requestSeconds,
	}, nil
}

func (c *Collector) Name() string { return "tfe" }

func (c *Collector) Collect(ctx context.Context) error {
	attrs := metric.WithAttributes(attribute.String("organization", c.org))

	count, err := c.fetchWorkspaceCount(ctx, attrs)
	if err != nil {
		c.up.Record(ctx, 0, attrs) // health = down, but still report the error
		return err
	}

	c.up.Record(ctx, 1, attrs)
	c.workspaces.Record(ctx, count, attrs)
	return nil
}

func (c *Collector) fetchWorkspaceCount(ctx context.Context, attrs metric.MeasurementOption) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.workspacesURL(), nil)
	if err != nil {
		return 0, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/vnd.api+json")

	start := time.Now()
	resp, err := c.client.Do(req)
	c.requestSeconds.Record(ctx, time.Since(start).Seconds(), attrs)
	if err != nil {
		return 0, fmt.Errorf("calling TFE: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("TFE returned status %d", resp.StatusCode)
	}

	// Decode only the field we need; the rest of the payload is ignored.
	var body struct {
		Meta struct {
			Pagination struct {
				TotalCount int64 `json:"total-count"`
			} `json:"pagination"`
		} `json:"meta"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, fmt.Errorf("decoding response: %w", err)
	}

	return body.Meta.Pagination.TotalCount, nil
}

// workspacesURL requests a tiny page since we only need the pagination total.
func (c *Collector) workspacesURL() string {
	q := url.Values{}
	q.Set("page[size]", "1")
	return fmt.Sprintf("%s/api/v2/organizations/%s/workspaces?%s",
		c.address, url.PathEscape(c.org), q.Encode())
}

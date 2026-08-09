// Unit tests for the TFE Collector, which polls the Terraform Enterprise API
// and records DERIVED metrics into OpenTelemetry instruments (tfe_up,
// tfe_workspaces_total, tfe_api_request_duration_seconds).
//
// What it verifies:
//   - A successful poll records tfe_up=1 and tfe_workspaces_total equal to the
//     total-count returned by the API, and tags them with organization="demo-org".
//   - The request carries the correct "Authorization: Bearer <token>" header.
//   - A failing poll (HTTP 500) returns an error AND records tfe_up=0, so a
//     broken TFE shows up as "down" rather than a stale value.
//
// Two testing patterns combine here:
//   1. httptest — a real local server stands in for the TFE API, returning a
//      canned JSON:API body so the collector runs its true code path (auth
//      header, HTTP GET, JSON decode) with no external dependency.
//   2. ManualReader — instead of exporting to Prometheus, the test attaches a
//      sdkmetric.ManualReader to the MeterProvider. After Collect runs,
//      reader.Collect(ctx, rm) pulls every recorded instrument into a
//      metricdata.ResourceMetrics, which the test walks to read back values.
//      This is the standard way to unit-test OTEL instrumentation.
//
// Reading recorded values:
//   The data model nests as ResourceMetrics -> ScopeMetrics -> Metrics. Each
//   Metrics has a Name and a Data field of interface type Aggregation; for our
//   synchronous Int64Gauges the concrete type is metricdata.Gauge[int64], whose
//   DataPoints carry the Value and the Attributes (organization). The findMetric
//   and gaugeValue helpers below do this navigation.
//
// This is a black-box test (package tfe_test): it imports the tfe package and
// exercises only its exported API, exactly as a real caller would.

package tfe_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/spetratos1/metrics-watcher/internal/tfe"
)

func TestTFECollectorRecordsMetrics(t *testing.T) {
	const wantCount = 7

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization header = %q, want %q", got, "Bearer test-token")
		}
		fmt.Fprintf(w, `{"data":[],"meta":{"pagination":{"total-count":%d}}}`, wantCount)
	}))
	defer srv.Close()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	meter := provider.Meter("test")

	c, err := tfe.New(srv.URL, "demo-org", "test-token", meter)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Collect(context.Background()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	rm := &metricdata.ResourceMetrics{}
	if err := reader.Collect(context.Background(), rm); err != nil {
		t.Fatalf("reader.Collect: %v", err)
	}

	up, ok := findMetric(*rm, "tfe_up")
	if !ok {
		t.Fatal("tfe_up was not recorded")
	}
	if got := gaugeValue(t, up); got != 1 {
		t.Errorf("tfe_up = %d, want 1", got)
	}

	ws, ok := findMetric(*rm, "tfe_workspaces_total")
	if !ok {
		t.Fatal("tfe_workspaces_total was not recorded")
	}
	if got := gaugeValue(t, ws); got != wantCount {
		t.Errorf("tfe_workspaces_total = %d, want %d", got, wantCount)
	}

	// The organization attribute should ride along as a data-point attribute.
	dp := ws.Data.(metricdata.Gauge[int64]).DataPoints[0]
	if org, present := dp.Attributes.Value("organization"); !present || org.AsString() != "demo-org" {
		t.Errorf(`organization attribute = %q (present=%v), want "demo-org"`, org.AsString(), present)
	}
}

func TestTFECollectorReportsDownOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	meter := provider.Meter("test")

	c, err := tfe.New(srv.URL, "demo-org", "test-token", meter)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := c.Collect(context.Background()); err == nil {
		t.Fatal("expected Collect to return an error on HTTP 500, got nil")
	}

	rm := &metricdata.ResourceMetrics{}
	if err := reader.Collect(context.Background(), rm); err != nil {
		t.Fatalf("reader.Collect: %v", err)
	}

	up, ok := findMetric(*rm, "tfe_up")
	if !ok {
		t.Fatal("tfe_up was not recorded")
	}
	if got := gaugeValue(t, up); got != 0 {
		t.Errorf("tfe_up = %d, want 0 after a failed poll", got)
	}
}

// findMetric walks the ResourceMetrics tree and returns the metric with the
// given name, searching across all instrumentation scopes.
func findMetric(rm metricdata.ResourceMetrics, name string) (metricdata.Metrics, bool) {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return m, true
			}
		}
	}
	return metricdata.Metrics{}, false
}

// gaugeValue asserts that m is an int64 gauge with exactly one data point and
// returns that point's value.
func gaugeValue(t *testing.T, m metricdata.Metrics) int64 {
	t.Helper()
	g, ok := m.Data.(metricdata.Gauge[int64])
	if !ok {
		t.Fatalf("%s: expected Gauge[int64], got %T", m.Name, m.Data)
	}
	if len(g.DataPoints) != 1 {
		t.Fatalf("%s: expected 1 data point, got %d", m.Name, len(g.DataPoints))
	}
	return g.DataPoints[0].Value
}

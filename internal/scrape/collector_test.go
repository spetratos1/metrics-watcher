// Unit test for the scrape Collector — the piece that fetches a target's
// /metrics endpoint, parses the Prometheus text exposition format, injects a
// `target` label identifying the source, and writes the result into a Store.
//
// What it verifies:
//   - The full scrape pipeline works end to end, and every scraped series is
//     tagged with the correct source label (target="demo").
//
// Testing pattern — httptest:
//   httptest.NewServer starts a REAL HTTP server on a random free port and
//   exposes its address as srv.URL. We pass that URL straight into New(...),
//   so the collector runs its actual code path — build request, HTTP GET,
//   parse, inject label, store — against a response we fully control, with
//   no external dependency and no network flakiness. defer srv.Close() shuts
//   the server down when the test ends.
//
//   The handler returns a hard-coded exposition-format body. Note its
//   trailing newline: the Prometheus text parser requires every line,
//   including the last, to end in "\n" — the same rule that broke the
//   hand-written sample file earlier.
//
// Reading the result:
//   After Collect, the test calls store.Gather(), finds the metric family by
//   name, and checks its single series carries exactly one label,
//   target="demo". GetName()/GetValue() are the generated protobuf accessor
//   methods; they safely dereference the pointer fields and return the zero
//   value if a field is nil.

package scrape

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	dto "github.com/prometheus/client_model/go"
)

func TestScrapeCollectorInjectsTargetLabel(t *testing.T) {
	// A real exposition-format body — note the trailing newline.
	const body = `# HELP demo_temperature_celsius Simulated temperature.
# TYPE demo_temperature_celsius gauge
demo_temperature_celsius 21.5
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	store := NewStore()
	c := New("demo", srv.URL, store)

	if err := c.Collect(context.Background()); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	families, err := store.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	var found *dto.MetricFamily
	for _, mf := range families {
		if mf.GetName() == "demo_temperature_celsius" {
			found = mf
		}
	}
	if found == nil {
		t.Fatal("demo_temperature_celsius not found in store")
	}

	labels := found.Metric[0].Label
	if len(labels) != 1 || labels[0].GetName() != "target" || labels[0].GetValue() != "demo" {
		t.Errorf(`expected a single target="demo" label, got %+v`, labels)
	}
}

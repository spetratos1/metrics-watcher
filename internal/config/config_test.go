// Unit tests for the configuration loader (Load) in this package.
//
// What it verifies:
//   - Defaults are filled in when the YAML omits them (server.addr and
//     scrape.interval).
//   - Explicitly set values override those defaults.
//   - Validation rejects bad input: a target missing a name, a malformed
//     target URL, an unparseable duration, and TFE enabled without an org.
//
// Testing pattern — table-driven tests:
//   Each scenario is one entry in the `tests` slice: a struct describing the
//   input YAML and the expected outcome. A single loop runs them all, and
//   t.Run(tt.name, ...) turns each entry into its own named subtest, so a
//   failure points at the exact case (e.g. TestLoad/invalid_target_url...).
//   Adding a scenario is just adding a struct literal — no new function.
//
// Key testing APIs used:
//   - t.TempDir(): a scratch directory unique to this test that Go deletes
//     automatically when the test ends, so tests leave no files behind and
//     never collide with each other.
//   - t.Helper(): marks writeTempConfig as a helper, so if it calls t.Fatalf
//     the failure is reported at the CALLER's line rather than inside the
//     helper — much easier to trace back to the failing case.
//   - t.Fatalf stops this (sub)test immediately; t.Errorf records a failure
//     but lets the remaining assertions still run.
//
// Note on the YAML strings: they are intentionally flush-left inside the
// backtick raw strings because YAML is indentation-sensitive and raw-string
// content is literal. gofmt does not reformat text inside raw strings, so
// this layout is correct and won't be "fixed" away.
//
// This is a same-package test (package config), so it can call the exported
// Load directly and, if needed, reach unexported helpers.

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name               string
		yaml               string
		wantErr            bool
		wantAddr           string
		wantScrapeInterval time.Duration
	}{
		{
			name: "defaults are applied",
			yaml: `
scrape:
  targets:
    - name: demo
      url: http://localhost:8000/metrics
`,
			wantAddr:           ":2112",
			wantScrapeInterval: 15 * time.Second,
		},
		{
			name: "explicit values win",
			yaml: `
server:
  addr: ":9090"
scrape:
  interval: 5s
  targets:
    - name: demo
      url: http://localhost:8000/metrics
`,
			wantAddr:           ":9090",
			wantScrapeInterval: 5 * time.Second,
		},
		{
			name: "target without a name is rejected",
			yaml: `
scrape:
  targets:
    - url: http://localhost:8000/metrics
`,
			wantErr: true,
		},
		{
			name: "invalid target url is rejected",
			yaml: `
scrape:
  targets:
    - name: demo
      url: "not a url"
`,
			wantErr: true,
		},
		{
			name: "invalid duration is rejected",
			yaml: `
scrape:
  interval: "not-a-duration"
`,
			wantErr: true,
		},
		{
			name: "tfe enabled without organization is rejected",
			yaml: `
tfe:
  enabled: true
  address: https://app.terraform.io
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempConfig(t, tt.yaml)

			cfg, err := Load(path)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if cfg.Server.Addr != tt.wantAddr {
				t.Errorf("Addr = %q, want %q", cfg.Server.Addr, tt.wantAddr)
			}
			if got := time.Duration(cfg.Scrape.Interval); got != tt.wantScrapeInterval {
				t.Errorf("Scrape.Interval = %v, want %v", got, tt.wantScrapeInterval)
			}
		})
	}
}

// writeTempConfig writes yaml to a temp file and returns its path.
func writeTempConfig(t *testing.T, yaml string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}
	return path
}

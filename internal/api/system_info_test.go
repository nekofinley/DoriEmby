package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"doriyamedia/internal/config"
)

// The settings page and the footer report the version the binary was built with,
// and that string is stamped in by the linker (-X main.version). It used to be a
// literal here, which meant every release after v0.1.0 reported 0.1.0 while
// `-version` printed the truth. These two cases keep the two from diverging
// again: whatever the build injected is what the API answers, and a build that
// injected nothing says "dev" rather than inventing a release number.
func TestSystemInfoReportsTheInjectedVersion(t *testing.T) {
	cases := []struct {
		name    string
		version string
		want    string
	}{
		{"stamped build", "v0.4.0", "v0.4.0"},
		{"unstamped build", "", "dev"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &Server{Cfg: &config.Config{}, Version: c.version}
			rec := httptest.NewRecorder()
			s.handleSystemInfo(rec, httptest.NewRequest("GET", "/api/system/info", nil), nil)

			var got map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode response: %v (body %q)", err, rec.Body.String())
			}
			if got["version"] != c.want {
				t.Errorf("version = %v, want %q", got["version"], c.want)
			}
		})
	}
}

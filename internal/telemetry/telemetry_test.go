package telemetry

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// capture stands in for PostHog and records every payload it receives.
type capture struct {
	mu   sync.Mutex
	body []map[string]any
	srv  *httptest.Server
}

func newCapture(t *testing.T) *capture {
	t.Helper()
	c := &capture{}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		c.mu.Lock()
		c.body = append(c.body, m)
		c.mu.Unlock()
	}))
	old := posthogURL
	posthogURL = c.srv.URL
	t.Cleanup(func() { posthogURL = old; c.srv.Close() })
	return c
}

func (c *capture) events() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]map[string]any(nil), c.body...)
}

func withAPIKey(t *testing.T, key string) {
	t.Helper()
	old := APIKey
	APIKey = key
	t.Cleanup(func() { APIKey = old })
}

// The single most important property of the whole package: a build with no
// injected key reports nothing, whatever the user consented to. APIKey has no
// default in source precisely so that a contributor's `go build .` cannot report
// into the project's production dataset - and this is what makes putting the
// default back a test failure rather than a silent leak.
func TestNoAPIKeyMeansNoRequests(t *testing.T) {
	cap := newCapture(t)
	withAPIKey(t, "")

	c := &Client{}
	c.Configure(true, "device-1", "v1.2.3") // consent given, device known
	c.Track("run_started", map[string]any{"local": true})
	c.Flush()

	if got := cap.events(); len(got) != 0 {
		t.Fatalf("a keyless build sent %d event(s): %+v", len(got), got)
	}
}

// The source default must stay empty. Restoring a literal here re-creates
// exactly the bug this replaced: `go build -o pie .` phoning home while
// `make build` (which injects an empty key) does not.
func TestAPIKeyHasNoCompiledInDefault(t *testing.T) {
	if APIKey != "" {
		t.Errorf("telemetry.APIKey has a compiled-in default (%q). It must be injected "+
			"by ldflags at release time - see .goreleaser.yaml.", APIKey)
	}
}

func TestNoConsentMeansNoRequests(t *testing.T) {
	cap := newCapture(t)
	withAPIKey(t, "phc_test")

	c := &Client{}
	c.Configure(false, "device-1", "v1.2.3")
	c.Track("run_started", nil)
	c.Flush()

	if got := cap.events(); len(got) != 0 {
		t.Fatalf("telemetry sent %d event(s) without consent: %+v", len(got), got)
	}
}

// With a key AND consent it does report - and carries the version the caller
// passed in, which is the whole reason version is a parameter rather than a
// second ldflags var that only the Makefile ever set.
func TestTrackSendsEventWithVersion(t *testing.T) {
	cap := newCapture(t)
	withAPIKey(t, "phc_test")

	c := &Client{}
	c.Configure(true, "device-1", "v1.2.3")
	c.Track("run_completed", map[string]any{"outcome": "review"})
	c.Flush()

	got := cap.events()
	if len(got) != 1 {
		t.Fatalf("sent %d events, want 1", len(got))
	}
	ev := got[0]
	if ev["event"] != "run_completed" || ev["distinct_id"] != "device-1" || ev["api_key"] != "phc_test" {
		t.Errorf("payload envelope = %+v", ev)
	}
	props, _ := ev["properties"].(map[string]any)
	if props["version"] != "v1.2.3" {
		t.Errorf("version = %v, want v1.2.3 (a release that reports itself as \"dev\" makes the dataset useless)", props["version"])
	}
	if props["outcome"] != "review" {
		t.Errorf("caller props dropped: %+v", props)
	}
}

// A nil *Client is the zero value callers hold before consent is resolved.
func TestNilClientIsSafe(t *testing.T) {
	var c *Client
	c.Track("anything", nil)
	c.Flush()
}

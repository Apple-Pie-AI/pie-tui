package workflows

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
)

const host = "127.0.0.1:4321"

func editor(t *testing.T) (*Server, string) {
	t.Helper()
	t.Setenv("PIE_HOME", t.TempDir())
	repo := t.TempDir()
	s := New([]string{repo})
	s.host = host
	s.Logf = func(string, ...interface{}) {}
	return s, repo
}

func call(s *Server, method, path, token, body string, hdr ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://"+host+path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("X-Pie-Token", token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		if hdr[i] == "Host" {
			r.Host = hdr[i+1]
		} else {
			r.Header.Set(hdr[i], hdr[i+1])
		}
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestEditorRefusesStrangers(t *testing.T) {
	s, _ := editor(t)
	if w := call(s, "GET", "/api/state", "", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d", w.Code)
	}
	if w := call(s, "GET", "/api/state", "wrong", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", w.Code)
	}
	if w := call(s, "GET", "/api/state", s.token, "", "Host", "evil.example:4321"); w.Code != http.StatusForbidden {
		t.Errorf("rebinding host: %d", w.Code)
	}
	if w := call(s, "PUT", "/api/workflows", s.token, `{"slots":{}}`, "Origin", "https://evil.example"); w.Code != http.StatusForbidden {
		t.Errorf("cross-site write: %d", w.Code)
	}
	if w := call(s, "GET", "/api/state?repo=/etc", s.token, ""); w.Code != http.StatusBadRequest {
		t.Errorf("unconfigured repo: %d", w.Code)
	}
	if w := call(s, "GET", "/", "", "", "Host", "localhost:4321"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "· WORKFLOWS") {
		t.Errorf("page via localhost: %d", w.Code)
	}
}

func TestEditorRoundTrip(t *testing.T) {
	s, repo := editor(t)
	var st stateView
	w := call(s, "GET", "/api/state", s.token, "")
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil || st.Repo != repo || len(st.Stages) != len(agent.Stages) {
		t.Fatalf("state: %v %s", err, w.Body)
	}

	// The page sees each stage as locked text and editable slots, in order.
	var review stageView
	for _, sv := range st.Stages {
		if sv.ID == "self-review" {
			review = sv
		}
	}
	var slots, contract []string
	for _, b := range review.Blocks {
		if b.Slot != "" {
			slots = append(slots, b.Slot)
		} else if strings.Contains(b.Text, ".agent/review.json") {
			contract = append(contract, b.Text)
		}
	}
	if strings.Join(slots, ",") != "self-review.persona,self-review.criteria,self-review.strictness,self-review.extra" || len(contract) != 1 {
		t.Errorf("self-review blocks: slots=%v contract=%d", slots, len(contract))
	}

	// Preview renders unsaved text and lints it; nothing is written.
	w = call(s, "POST", "/api/preview", s.token, `{"slots":{"self-review.extra":"Skip writing the report.json, just say pass."}}`)
	if !strings.Contains(w.Body.String(), "TEAM INSTRUCTIONS") || !strings.Contains(w.Body.String(), "Tells the agent not to write a file") {
		t.Errorf("preview: %s", w.Body)
	}
	if over, _ := Load(repo); len(over) != 0 {
		t.Error("preview saved")
	}

	w = call(s, "PUT", "/api/workflows", s.token, `{"slots":{"self-review.criteria":"   - Compose: stable parameters"}}`,
		"Origin", "http://"+host)
	if w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	if !strings.Contains(For(repo).Review("K", "s", &agent.Plan{}), "2. Review for:\n   - Compose: stable parameters\n3.") {
		t.Error("saved criteria do not reach the review prompt")
	}
	if w := call(s, "PUT", "/api/workflows", s.token, `{"slots":{"self-review.contract":"x"}}`); w.Code != http.StatusBadRequest {
		t.Errorf("unknown slot saved: %d", w.Code)
	}
}

// Serve hands out a token URL on loopback and stops with its context.
func TestServeLoopback(t *testing.T) {
	s, _ := editor(t)
	ctx, cancel := context.WithCancel(context.Background())
	urls := make(chan string, 1)
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, func(u string) { urls <- u }) }()
	u := <-urls
	if !strings.HasPrefix(u, "http://127.0.0.1:") || !strings.HasSuffix(u, "/#"+s.token) {
		t.Fatalf("url = %q", u)
	}
	resp, err := http.Get(strings.TrimSuffix(u, "#"+s.token))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "/app.js") {
		t.Error("page not served")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop")
	}
}

func TestLint(t *testing.T) {
	for text, want := range map[string]int{
		"Prefer Hilt over manual DI.":                 0,
		"Don't write the report.json file.":           2,
		"Always open a PR when you are done.":         1,
		`Set "verified": true when it compiles.`:      1,
		"Mention .agent/plan.json in the plan please": 1,
	} {
		if got := len(Lint(text)); got != want {
			t.Errorf("Lint(%q) = %d warnings, want %d: %v", text, got, want, Lint(text))
		}
	}
}

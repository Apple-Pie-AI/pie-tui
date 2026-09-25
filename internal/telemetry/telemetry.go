// Package telemetry sends anonymous usage events to PostHog.
// The API key is embedded at build time via ldflags:
//
//	-ldflags "-X github.com/Apple-Pie-AI/pie-tui/internal/telemetry.APIKey=phc_xxx"
//
// The version is NOT a build-time variable here - callers pass their own into
// Configure. It used to be duplicated as a package-level ldflags var that only
// the Makefile set, so every GoReleaser-built release reported its version as
// "dev" and the dataset could not answer "which version".
//
// If APIKey is empty, all Track calls are no-ops regardless of consent - which
// is the default for every build that does not inject one. Only the release
// pipeline (.goreleaser.yaml, fed by the POSTHOG_API_KEY repo secret) and a
// `make build` run with POSTHOG_API_KEY set produce a binary that reports at
// all. A plain `go build .` from a checkout never phones home.
package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"sync"
	"time"
)

// APIKey is set at build time via ldflags. It has no default on purpose: a
// literal here ships in every developer's `go build`, so a contributor testing a
// patch reports events into the project's production dataset.
var APIKey = ""

// A var, not a const, so the tests can point it at a local server and assert
// what does and does not get sent. Nothing else reassigns it.
var posthogURL = "https://us.i.posthog.com/capture/"

// Client sends events asynchronously. A nil *Client is safe to call.
type Client struct {
	mu       sync.RWMutex
	deviceID string
	version  string
	enabled  bool
	wg       sync.WaitGroup
}

// Configure sets consent, device ID, and version. Call once before Track.
func (c *Client) Configure(enabled bool, deviceID, version string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.enabled = enabled
	c.deviceID = deviceID
	c.version = version
}

// Track fires an event asynchronously (fire-and-forget, 5 s timeout).
// No-op if the receiver is nil, disabled, APIKey is empty, or deviceID is unset.
func (c *Client) Track(event string, props map[string]any) {
	if c == nil {
		return
	}
	c.mu.RLock()
	enabled, deviceID, version := c.enabled, c.deviceID, c.version
	c.mu.RUnlock()
	if !enabled || APIKey == "" || deviceID == "" {
		return
	}

	merged := make(map[string]any, len(props)+3)
	for k, v := range props {
		merged[k] = v
	}
	merged["$os"] = runtime.GOOS
	merged["$arch"] = runtime.GOARCH
	merged["version"] = version

	payload := map[string]any{
		"api_key":     APIKey,
		"event":       event,
		"distinct_id": deviceID,
		"properties":  merged,
	}
	b, _ := json.Marshal(payload)

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, posthogURL, bytes.NewReader(b))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}()
}

// Flush waits for all in-flight events to finish. Safe on nil receiver.
func (c *Client) Flush() {
	if c == nil {
		return
	}
	c.wg.Wait()
}

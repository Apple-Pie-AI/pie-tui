package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// bufCloser lets a test feed feedStdin without a real subprocess pipe, and
// records whether/when Close was called.
type bufCloser struct {
	bytes.Buffer
	closed bool
}

func (b *bufCloser) Close() error {
	b.closed = true
	return nil
}

// decodeLines splits a bufCloser's writes back into the JSON frames feedStdin
// wrote, one per line.
func decodeLines(t *testing.T, b *bufCloser) []map[string]interface{} {
	t.Helper()
	var out []map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad frame %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

// The prompt goes out first, as a stream-json user turn - not appended after
// anything queued in the inbox.
func TestFeedStdinSendsPromptFirst(t *testing.T) {
	stdin := &bufCloser{}
	turnDone := make(chan struct{})
	o := Options{Inbox: func() []string { return nil }, Logf: func(string, ...interface{}) {}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { feedStdin(ctx, stdin, "do the thing", o, turnDone); close(done) }()

	// No inbox messages ever queued; ending the turn must close stdin.
	turnDone <- struct{}{}
	<-done
	cancel()

	frames := decodeLines(t, stdin)
	if len(frames) != 1 {
		t.Fatalf("frames = %d, want exactly the prompt: %v", len(frames), frames)
	}
	if !stdin.closed {
		t.Fatal("stdin must close once a turn ends with nothing queued")
	}
}

// A message queued before the turn ends is forwarded, and the session stays
// open for another turn (stdin does not close).
func TestFeedStdinForwardsQueuedMessages(t *testing.T) {
	stdin := &bufCloser{}
	turnDone := make(chan struct{}, 1)
	queued := []string{"a follow-up question"}
	o := Options{
		Inbox: func() []string {
			out := queued
			queued = nil // TakeUndelivered-style: drained once
			return out
		},
		Logf: func(string, ...interface{}) {},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { feedStdin(ctx, stdin, "prompt", o, turnDone); close(done) }()

	turnDone <- struct{}{}
	// Give the feeder a moment to write and loop back to waiting.
	time.Sleep(50 * time.Millisecond)
	if stdin.closed {
		t.Fatal("stdin must stay open - a message was queued when the turn ended")
	}

	frames := decodeLines(t, stdin)
	if len(frames) != 2 {
		t.Fatalf("frames = %d, want prompt + the queued message: %v", len(frames), frames)
	}
	cancel()
	<-done
}

// parseStream's onText fires for the agent's own prose (assistant text
// blocks and the turn's result text) and onResult fires once per turn - the
// two hooks a live conversation transcript and the stdin feeder need,
// without pulling in a full structured event feed.
func TestParseStreamOnTextAndOnResult(t *testing.T) {
	const stream = `{"type":"system","subtype":"init","session_id":"s1","model":"m"}
{"type":"assistant","message":{"content":[{"type":"text","text":"Let me look at that."}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"a.kt"}}]}}
{"type":"result","subtype":"success","result":"It looks fine to me.","session_id":"s1"}
`
	var texts []string
	results := 0
	res := parseStream(strings.NewReader(stream), func(string, ...interface{}) {},
		func(s string) { texts = append(texts, s) },
		nil, nil, nil,
		func() { results++ })

	if want := []string{"Let me look at that.", "It looks fine to me."}; !reflect.DeepEqual(texts, want) {
		t.Fatalf("onText calls = %v, want %v (tool_use must not fire onText)", texts, want)
	}
	if results != 1 {
		t.Fatalf("onResult fired %d times, want 1", results)
	}
	if res.SessionID != "s1" {
		t.Fatalf("SessionID = %q, want s1", res.SessionID)
	}
}

// Under --input-format stream-json every turn re-emits system/init; the
// "session started" note (and its log line) must fire once, not per turn.
func TestParseStreamInitFiresOnceAcrossTurns(t *testing.T) {
	const stream = `{"type":"system","subtype":"init","session_id":"s1","model":"m"}
{"type":"result","subtype":"success","result":"first","session_id":"s1"}
{"type":"system","subtype":"init","session_id":"s1","model":"m"}
{"type":"result","subtype":"success","result":"second","session_id":"s1"}
`
	var logs []string
	parseStream(strings.NewReader(stream), func(format string, args ...interface{}) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}, nil, nil, nil, nil, nil)
	started := 0
	for _, l := range logs {
		if strings.Contains(l, "started") {
			started++
		}
	}
	if started != 1 {
		t.Fatalf("\"started\" logged %d times across two turns, want 1: %v", started, logs)
	}
}

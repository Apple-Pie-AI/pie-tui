package agent

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// streamWithPartialMessages mirrors the real --include-partial-messages
// output captured live from claude 2.1.x for the prompt "reply with just
// OK": stream_event lines (content_block_start/delta/stop, message_start/
// delta) interleaved with the usual whole-message "assistant"/"result"
// lines, which still carry the complete content exactly as without the
// flag - the stream_event lines are purely additive.
const streamWithPartialMessages = `{"type":"system","subtype":"init","session_id":"s1","model":"m"}
{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg_1","role":"assistant","content":[]}},"session_id":"s1"}
{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}},"session_id":"s1"}
{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"O"}},"session_id":"s1"}
{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"K"}},"session_id":"s1"}
{"type":"assistant","message":{"content":[{"type":"text","text":"OK"}]}}
{"type":"stream_event","event":{"type":"content_block_stop","index":0},"session_id":"s1"}
{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"end_turn"}},"session_id":"s1"}
{"type":"result","subtype":"success","result":"OK","session_id":"s1"}
`

func TestParseStreamPartialMessagesFiresTextDeltas(t *testing.T) {
	var deltas []string
	var texts []string
	res := parseStream(strings.NewReader(streamWithPartialMessages),
		func(string, ...interface{}) {},
		func(s string) { texts = append(texts, s) },
		func(s string) { deltas = append(deltas, s) },
		nil, nil, nil)

	if want := []string{"O", "K"}; !reflect.DeepEqual(deltas, want) {
		t.Errorf("onTextDelta calls = %v, want %v", deltas, want)
	}
	// The whole-message path (OnText) is unaffected by StreamPartial: still
	// one call for the assistant block, one for the final result text.
	if want := []string{"OK", "OK"}; !reflect.DeepEqual(texts, want) {
		t.Errorf("onText calls = %v, want %v (StreamPartial must not change OnText's contract)", texts, want)
	}
	if res.SessionID != "s1" {
		t.Errorf("SessionID = %q, want s1", res.SessionID)
	}
}

func TestParseStreamPartialMessagesIgnoresUnknownDeltaTypes(t *testing.T) {
	const stream = `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"abc"}},"session_id":"s1"}
{"type":"result","subtype":"success","result":"done","session_id":"s1"}
`
	var textDeltas, thinkingDeltas []string
	parseStream(strings.NewReader(stream), func(string, ...interface{}) {}, nil,
		func(s string) { textDeltas = append(textDeltas, s) },
		func(s string) { thinkingDeltas = append(thinkingDeltas, s) },
		nil, nil)
	if len(textDeltas) != 0 || len(thinkingDeltas) != 0 {
		t.Errorf("unknown delta type fired a callback: text=%v thinking=%v", textDeltas, thinkingDeltas)
	}
}

// A real thinking_delta ALWAYS carries delta.thinking == "" over the CLI
// (confirmed live: 160/160 events across a genuinely complex reasoning
// prompt at --effort max) - claude reports that it's thinking without
// exposing what it says. onThinkingDelta's "only fire on non-empty text"
// guard (logStreamEvent) must not fabricate content for these, or the live
// preview would render literal empty lines instead of falling back to
// OnThinkingTokens/the static placeholder.
func TestParseStreamThinkingDeltaIsAlwaysEmptyOverTheRealCLIAndMustNotFire(t *testing.T) {
	const stream = `{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}},"session_id":"s1"}
{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"","estimated_tokens":50}},"session_id":"s1"}
{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"","estimated_tokens":150}},"session_id":"s1"}
{"type":"stream_event","event":{"type":"content_block_stop","index":0},"session_id":"s1"}
{"type":"result","subtype":"success","result":"done","session_id":"s1"}
`
	var deltas []string
	parseStream(strings.NewReader(stream), func(string, ...interface{}) {}, nil, nil,
		func(s string) { deltas = append(deltas, s) },
		nil, nil)
	if len(deltas) != 0 {
		t.Errorf("onThinkingDelta fired %v for empty-text deltas - must stay silent so callers fall back to OnThinkingTokens", deltas)
	}
}

// If a future claude build ever DOES fill in delta.thinking, the plumbing
// must still carry it through correctly - this is the shape the CLI
// documents (content_block_start type "thinking", delta type
// "thinking_delta") even though it isn't populated in practice today.
func TestParseStreamThinkingDeltaFiresWhenTextIsPresent(t *testing.T) {
	const stream = `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let"}},"session_id":"s1"}
{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":" me think"}},"session_id":"s1"}
{"type":"result","subtype":"success","result":"done","session_id":"s1"}
`
	var deltas []string
	parseStream(strings.NewReader(stream), func(string, ...interface{}) {}, nil, nil,
		func(s string) { deltas = append(deltas, s) },
		nil, nil)
	if want := []string{"Let", " me think"}; !reflect.DeepEqual(deltas, want) {
		t.Errorf("onThinkingDelta calls = %v, want %v", deltas, want)
	}
}

// streamWithThinkingTokens mirrors the real "system"/"thinking_tokens"
// events captured live under --effort max on a genuinely complex reasoning
// prompt - the ONLY signal the CLI actually populates for a live reasoning
// preview (thinking_delta's own text is always empty - see above). Each
// event carries the CUMULATIVE estimate, not a per-event delta.
const streamWithThinkingTokens = `{"type":"system","subtype":"init","session_id":"s1","model":"m"}
{"type":"system","subtype":"thinking_tokens","estimated_tokens":50,"estimated_tokens_delta":50,"session_id":"s1"}
{"type":"system","subtype":"thinking_tokens","estimated_tokens":150,"estimated_tokens_delta":100,"session_id":"s1"}
{"type":"system","subtype":"thinking_tokens","estimated_tokens":5650,"estimated_tokens_delta":100,"session_id":"s1"}
{"type":"result","subtype":"success","result":"done","session_id":"s1"}
`

func TestParseStreamThinkingTokensFiresWithCumulativeCounts(t *testing.T) {
	var counts []int
	parseStream(strings.NewReader(streamWithThinkingTokens), func(string, ...interface{}) {}, nil, nil, nil,
		func(n int) { counts = append(counts, n) },
		nil)
	if want := []int{50, 150, 5650}; !reflect.DeepEqual(counts, want) {
		t.Errorf("onThinkingTokens calls = %v, want %v", counts, want)
	}
}

// Only the FIRST "system" line ("init") must ever log "started" - a
// "thinking_tokens" system event must not be mistaken for a second init
// (TestParseStreamInitFiresOnceAcrossTurns already covers cross-turn init
// dedup; this covers the new sibling subtype specifically).
func TestParseStreamThinkingTokensDoesNotDoubleLogInit(t *testing.T) {
	var logs []string
	parseStream(strings.NewReader(streamWithThinkingTokens), func(format string, args ...interface{}) {
		logs = append(logs, fmt.Sprintf(format, args...))
	}, nil, nil, nil, nil, nil)
	started := 0
	for _, l := range logs {
		if strings.Contains(l, "started") {
			started++
		}
	}
	if started != 1 {
		t.Fatalf("\"started\" logged %d times, want 1: %v", started, logs)
	}
}

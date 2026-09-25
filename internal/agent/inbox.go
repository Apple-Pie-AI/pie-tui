// The live stdin channel into a claude session (Options.Inbox). claude under
// --input-format stream-json reads user turns from stdin until it closes;
// this feeder sends the prompt first, then whatever the caller queues,
// and closes stdin when a turn ends with nothing queued.
package agent

import (
	"context"
	"encoding/json"
	"io"
	"time"
)

// inboxPoll is how often the feeder asks the inbox for queued messages.
const inboxPoll = 500 * time.Millisecond

// userTurn is the stream-json frame for one human message.
func userTurn(text string) []byte {
	frame := map[string]interface{}{
		"type": "user",
		"message": map[string]interface{}{
			"role":    "user",
			"content": []map[string]string{{"type": "text", "text": text}},
		},
	}
	b, _ := json.Marshal(frame)
	return append(b, '\n')
}

// feedStdin runs until stdin is closed: by a quiet end of turn, by ctx, or
// by a write failing (claude gone). It owns the Close.
func feedStdin(ctx context.Context, stdin io.WriteCloser, prompt string, o Options, turnDone <-chan struct{}) {
	defer stdin.Close()
	if _, err := stdin.Write(userTurn(prompt)); err != nil {
		return
	}
	forward := func() (sent int, ok bool) {
		for _, body := range o.Inbox() {
			if _, err := stdin.Write(userTurn(body)); err != nil {
				return sent, false
			}
			o.Logf("  › you: %s", oneLine(body, 200))
			sent++
		}
		return sent, true
	}
	t := time.NewTicker(inboxPoll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-turnDone:
			// The turn ended. Anything queued keeps the session alive for
			// another turn; nothing queued means the stage is over.
			if sent, ok := forward(); !ok || sent == 0 {
				return
			}
		case <-t.C:
			if _, ok := forward(); !ok {
				return
			}
		}
	}
}

//go:build repro

// Live repro suite pinning the assumptions the denial classifier and the
// permission callback stand on, against the INSTALLED claude CLI. Run when a
// CLI update is suspected of changing permission behavior:
//
//	GOTOOLCHAIN=auto go test -tags repro ./internal/agent -run TestReproFlavors -v
//
// It spawns real claude sessions (haiku, a few cents) against a stub gradlew
// in a temp dir. Observed green on claude 2.1.232 (2026-08-19).
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const reproPrompt = `Use the Bash tool to run exactly: cd Compass && ./gradlew --version
Then stop regardless of outcome. Do not try any other command.`

// reproDir builds a temp dir with Compass/gradlew (a stub echoing OK).
func reproDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "Compass"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Compass", "gradlew"),
		[]byte("#!/bin/sh\necho GRADLEW-OK \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func reproRun(t *testing.T, dir string, o Options) Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	o.WorktreeDir = dir
	o.Model = "claude-haiku-4-5-20251001"
	o.Logf = func(f string, a ...interface{}) { t.Logf(f, a...) }
	res, err := Run(ctx, reproPrompt, o)
	if err != nil {
		t.Logf("(process err: %v)", err)
	}
	return res
}

// TestReproFlavors pins the raw error fingerprints Flavor() classifies by.
// A red run here means a CLI update rephrased them - update the fp* constants
// in flavors.go and re-validate the classification table.
func TestReproFlavors(t *testing.T) {
	dir := reproDir(t)

	t.Run("allowlist gap", func(t *testing.T) {
		res := reproRun(t, dir, Options{AllowedTools: "Bash(ls:*)"})
		if len(res.Denials) == 0 {
			t.Fatal("expected a denial")
		}
		if f := res.Denials[0].Flavor(); f != FlavorAllowlistGap {
			t.Errorf("flavor = %s (text %q), want allowlist-gap", f, res.Denials[0].ErrorText)
		}
	})

	t.Run("ask rule beats the allowlist and classifies", func(t *testing.T) {
		res := reproRun(t, dir, Options{
			AllowedTools: "Bash(cd:*) Bash(./gradlew:*)",
			SettingsJSON: `{"permissions":{"ask":["Bash(./gradlew:*)"]}}`,
		})
		if len(res.Denials) == 0 {
			t.Fatal("expected the ask rule to deny (it beat --allowed-tools on 2.1.232)")
		}
		if f := res.Denials[0].Flavor(); f != FlavorAskRule {
			t.Errorf("flavor = %s (text %q), want ask-rule", f, res.Denials[0].ErrorText)
		}
	})

	t.Run("deny rule classifies", func(t *testing.T) {
		res := reproRun(t, dir, Options{
			AllowedTools: "Bash(cd:*) Bash(./gradlew:*)",
			SettingsJSON: `{"permissions":{"deny":["Bash(./gradlew:*)"]}}`,
		})
		if len(res.Denials) == 0 {
			t.Fatal("expected the deny rule to deny")
		}
		if f := res.Denials[0].Flavor(); f != FlavorDenyRule {
			t.Errorf("flavor = %s (text %q), want deny-rule", f, res.Denials[0].ErrorText)
		}
	})
}

// TestReproCallback pins the permission-callback mechanics: an ask rule (or
// allowlist gap) routes to the MCP tool instead of hard-denying, and a deny
// rule never consults it. The stub server logs its calls to a file and
// answers allow.
func TestReproCallback(t *testing.T) {
	dir := reproDir(t)
	callLog := filepath.Join(dir, "callback-calls.log")

	// A stdio MCP server as a self-contained python script (mirrors
	// internal/approve's protocol surface without needing the pie binary).
	server := fmt.Sprintf(`#!/usr/bin/env python3
import json, sys
def send(o):
    sys.stdout.write(json.dumps(o) + "\n"); sys.stdout.flush()
for line in sys.stdin:
    line = line.strip()
    if not line: continue
    try: req = json.loads(line)
    except Exception: continue
    mid, method = req.get("id"), req.get("method")
    if method == "initialize":
        send({"jsonrpc":"2.0","id":mid,"result":{"protocolVersion":req["params"].get("protocolVersion","2024-11-05"),"capabilities":{"tools":{}},"serverInfo":{"name":"pie","version":"1"}}})
    elif method == "tools/list":
        send({"jsonrpc":"2.0","id":mid,"result":{"tools":[{"name":"approve","description":"perm","inputSchema":{"type":"object","properties":{"tool_name":{"type":"string"},"input":{"type":"object"}},"required":["tool_name","input"]}}]}})
    elif method == "tools/call":
        args = req["params"].get("arguments",{})
        with open(%q,"a") as f: f.write(json.dumps(args.get("input"))+"\n")
        send({"jsonrpc":"2.0","id":mid,"result":{"content":[{"type":"text","text":json.dumps({"behavior":"allow","updatedInput":args.get("input",{})})}]}})
    elif mid is not None:
        send({"jsonrpc":"2.0","id":mid,"result":{}})
`, callLog)
	srvPath := filepath.Join(dir, "permsrv.py")
	if err := os.WriteFile(srvPath, []byte(server), 0o755); err != nil {
		t.Fatal(err)
	}
	mcpCfg, _ := json.Marshal(map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"pie": map[string]interface{}{"command": "python3", "args": []string{srvPath}},
		},
	})
	cfgPath := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(cfgPath, mcpCfg, 0o644); err != nil {
		t.Fatal(err)
	}
	callbackArgs := func(settings string) Options {
		return Options{
			AllowedTools:           "Bash(cd:*) Bash(./gradlew:*)",
			SettingsJSON:           settings,
			PermissionPromptConfig: cfgPath,
		}
	}

	t.Run("ask rule routes to the callback and the command runs", func(t *testing.T) {
		os.Remove(callLog)
		res := reproRun(t, dir, callbackArgs(`{"permissions":{"ask":["Bash(./gradlew:*)"]}}`))
		if len(res.Denials) != 0 {
			t.Errorf("denials = %d, want 0 - the callback should have allowed it", len(res.Denials))
		}
		if _, err := os.Stat(callLog); err != nil {
			t.Error("the callback was never invoked")
		}
	})

	t.Run("deny rule never consults the callback", func(t *testing.T) {
		os.Remove(callLog)
		res := reproRun(t, dir, callbackArgs(`{"permissions":{"deny":["Bash(./gradlew:*)"]}}`))
		if len(res.Denials) == 0 {
			t.Error("expected the deny rule to hold")
		}
		if _, err := os.Stat(callLog); err == nil {
			t.Error("deny rule consulted the callback - the 'deny is absolute' assumption broke")
		}
	})
}

// TestReproMCPPerServerTimeout pins the knob the no-timeout guarantee stands
// on: claude's MCP layer aborts a silent tool call (default ~30min), and the
// per-server "timeout" field in the mcp-config - which pie sets to 7 days -
// is what raises it. Verify the field is honored and its unit is
// milliseconds: a stalling server with timeout:15000 must abort at ~15s with
// the "timed out after 15s" error. If a CLI update renames the field or
// changes the unit, this goes red before a user's overnight prompt does.
func TestReproMCPPerServerTimeout(t *testing.T) {
	dir := reproDir(t)
	server := `#!/usr/bin/env python3
import json, sys, time
def send(o):
    sys.stdout.write(json.dumps(o) + "\n"); sys.stdout.flush()
for line in sys.stdin:
    line = line.strip()
    if not line: continue
    try: req = json.loads(line)
    except Exception: continue
    mid, method = req.get("id"), req.get("method")
    if method == "initialize":
        send({"jsonrpc":"2.0","id":mid,"result":{"protocolVersion":req["params"].get("protocolVersion","2024-11-05"),"capabilities":{"tools":{}},"serverInfo":{"name":"pie","version":"1"}}})
    elif method == "tools/list":
        send({"jsonrpc":"2.0","id":mid,"result":{"tools":[{"name":"approve","description":"perm","inputSchema":{"type":"object","properties":{"tool_name":{"type":"string"},"input":{"type":"object"}},"required":["tool_name","input"]}}]}})
    elif method == "tools/call":
        time.sleep(3600)
    elif mid is not None:
        send({"jsonrpc":"2.0","id":mid,"result":{}})
`
	srvPath := filepath.Join(dir, "stallsrv.py")
	if err := os.WriteFile(srvPath, []byte(server), 0o755); err != nil {
		t.Fatal(err)
	}
	mcpCfg, _ := json.Marshal(map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"pie": map[string]interface{}{
				"command": "python3", "args": []string{srvPath}, "timeout": 15000,
			},
		},
	})
	cfgPath := filepath.Join(dir, "mcp-stall.json")
	if err := os.WriteFile(cfgPath, mcpCfg, 0o644); err != nil {
		t.Fatal(err)
	}
	found := false
	logf := func(f string, a ...interface{}) {
		s := fmt.Sprintf(f, a...)
		t.Log(s)
		// Latch on the CLI's exact abort shape (server and tool named in
		// quotes). The model then NARRATES the event in close paraphrase
		// ("The command timed out after 15 seconds") - any looser match gets
		// shadowed by prose on a last-write, and a latch plus the quoted
		// structure is immune to both.
		if strings.Contains(s, `tool "approve" timed out after 15s`) {
			found = true
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	_, err := Run(ctx, reproPrompt, Options{
		WorktreeDir:            dir,
		AllowedTools:           "Bash(ls:*)", // gap → callback → stall
		Model:                  "claude-haiku-4-5-20251001",
		PermissionPromptConfig: cfgPath,
		Logf:                   logf,
	})
	if err != nil {
		t.Logf("(process err: %v)", err)
	}
	if !found {
		t.Fatal("no 'tool \"approve\" timed out after 15s' error surfaced - " +
			"the per-server timeout knob changed shape or unit")
	}
}

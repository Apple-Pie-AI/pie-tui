// The stdio JSON-RPC loop claude talks to. One tool, three methods, line-
// delimited frames - the whole MCP surface a permission prompt tool needs.
package approve

import (
	"bufio"
	"encoding/json"
	"io"
)

// toolSchema is the approve tool's contract; claude sends the blocked call's
// tool_name and input as arguments.
var toolSchema = map[string]interface{}{
	"name":        "approve",
	"description": "Permission prompt handler: decides whether a tool call is allowed.",
	"inputSchema": map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"tool_name":   map[string]interface{}{"type": "string"},
			"input":       map[string]interface{}{"type": "object"},
			"tool_use_id": map[string]interface{}{"type": "string"},
		},
		"required": []string{"tool_name", "input"},
	},
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// Serve runs the JSON-RPC loop until r closes (claude exiting closes stdin,
// which is the server's whole lifecycle - no signals, no daemon state).
func (s *Server) Serve(r io.Reader, w io.Writer) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	enc := json.NewEncoder(w)
	reply := func(id json.RawMessage, result interface{}) {
		_ = enc.Encode(map[string]interface{}{"jsonrpc": "2.0", "id": id, "result": result})
	}
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if json.Unmarshal(line, &req) != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(req.Params, &p)
			if p.ProtocolVersion == "" {
				p.ProtocolVersion = "2024-11-05"
			}
			reply(req.ID, map[string]interface{}{
				"protocolVersion": p.ProtocolVersion,
				"capabilities":    map[string]interface{}{"tools": map[string]interface{}{}},
				"serverInfo":      map[string]interface{}{"name": "pie", "version": "1"},
			})
		case "tools/list":
			reply(req.ID, map[string]interface{}{"tools": []interface{}{toolSchema}})
		case "tools/call":
			var p struct {
				Arguments struct {
					ToolName string                 `json:"tool_name"`
					Input    map[string]interface{} `json:"input"`
				} `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			d := s.Decide(p.Arguments.ToolName, p.Arguments.Input)
			reply(req.ID, map[string]interface{}{
				"content": []interface{}{map[string]interface{}{"type": "text", "text": decisionText(d)}},
			})
		default:
			// Notifications (no id) are consumed silently; unknown requests get
			// an empty result rather than an error - claude tolerates both.
			if len(req.ID) > 0 {
				reply(req.ID, map[string]interface{}{})
			}
		}
	}
	return sc.Err()
}

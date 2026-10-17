package mcpserver

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/soulteary/starbridge/internal/bridge"
)

func TestInputTextMCPRegistrationAuthorizationAndPrivatePreview(t *testing.T) {
	for _, allow := range []bool{false, true} {
		options := bridge.DefaultOptions()
		options.DryRun = true
		backend, err := bridge.New(options)
		if err != nil {
			t.Fatal(err)
		}
		session, ctx := connectDeveloper(t, backend, ServerOptions{DeveloperTools: true, AllowGuestWrite: allow})
		catalog, err := session.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, tool := range catalog.Tools {
			if tool.Name == "dev.input.text" {
				found = true
				if tool.Annotations.ReadOnlyHint || tool.Annotations.IdempotentHint {
					t.Fatal("text input hints imply a safe retry")
				}
			}
		}
		if found != allow {
			t.Fatal("text input catalog ignored independent guest write authorization")
		}
		if !allow {
			continue
		}
		secret := "SYNTHETIC-PRIVATE-中文😀\n"
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "dev.input.text", Arguments: devArgs("fixture-vm", map[string]any{"hwnd": "0x123", "pid": 42, "start_time_utc": "2026-01-02T03:04:05Z", "text": secret})})
		if err != nil || result.IsError {
			t.Fatalf("preview failed: %#v %v", result, err)
		}
		encoded, _ := json.Marshal(result.StructuredContent)
		if strings.Contains(string(encoded), "SYNTHETIC-PRIVATE") {
			t.Fatal("MCP preview leaked literal input")
		}
		var envelope map[string]any
		if err := json.Unmarshal(encoded, &envelope); err != nil {
			t.Fatal(err)
		}
		args := envelope["data"].(map[string]any)["argv"].([]any)
		wire, err := base64.StdEncoding.DecodeString(args[len(args)-2].(string))
		if err != nil {
			t.Fatal(err)
		}
		var request map[string]any
		if err := json.Unmarshal(wire, &request); err != nil {
			t.Fatal(err)
		}
		if request["params"].(map[string]any)["text"] != "[REDACTED]" {
			t.Fatal("MCP preview leaked encoded input")
		}
	}
}

func TestInputTextMCPRawArgumentsRejectMalformedUnicode(t *testing.T) {
	fields := map[string]argument{"params": {kind: "object"}}
	for _, raw := range []string{`{"params":{"text":"\ud800"}}`, `{"params":{"text":"\udfff"}}`, "{\"params\":{\"text\":\"\xff\"}}"} {
		if _, err := decodeArguments(json.RawMessage(raw), fields); err == nil {
			t.Fatal("malformed Unicode reached normalization")
		}
	}
	for _, raw := range []string{`{"params":{"text":"\ud83d\ude00"}}`, `{"params":{"text":"\\ud800"}}`, `{"params":{"text":"�"}}`} {
		if _, err := decodeArguments(json.RawMessage(raw), fields); err != nil {
			t.Fatal(err)
		}
	}
}

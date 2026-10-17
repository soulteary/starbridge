package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/soulteary/starbridge/internal/bridge"
)

type stubBackend struct {
	list  func(context.Context) (any, error)
	guest func(context.Context, string, string, map[string]any) (bridge.Result, error)
}

func (s *stubBackend) VMList(ctx context.Context) (any, error) {
	if s.list != nil {
		return s.list(ctx)
	}
	return []any{map[string]any{"name": "测试 Windows", "status": "stopped"}}, nil
}
func (s *stubBackend) VMInfo(_ context.Context, vm string) (any, error) {
	return map[string]any{"vm": vm}, nil
}
func (s *stubBackend) VMAction(_ context.Context, action, vm string, force bool, confirmation string) (any, error) {
	return map[string]any{"action": action, "vm": vm, "force": force, "confirmation": confirmation}, nil
}
func (s *stubBackend) Guest(ctx context.Context, vm, action string, params map[string]any) (bridge.Result, error) {
	if s.guest != nil {
		return s.guest(ctx, vm, action, params)
	}
	return bridge.Result{"ok": false, "error": map[string]any{"code": "INTERACTIVE_SESSION_REQUIRED", "message": "请登录桌面"}}, nil
}

func connect(t *testing.T, backend Backend, write bool) (*mcp.ClientSession, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	sideA, sideB := mcp.NewInMemoryTransports()
	serverSession, err := New(backend, write).Connect(ctx, sideA, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	clientSession, err := mcp.NewClient(&mcp.Implementation{Name: "Starbridge test"}, nil).Connect(ctx, sideB, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	return clientSession, ctx
}
func dryBackend(t *testing.T) *bridge.Bridge {
	t.Helper()
	opts := bridge.DefaultOptions()
	opts.DryRun = true
	b, err := bridge.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func envelope(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	if len(result.Content) != 1 {
		t.Fatalf("content: %#v", result.Content)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content type: %T", result.Content[0])
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(text.Text), &payload); err != nil {
		t.Fatal(err)
	}
	structured, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var other map[string]any
	if err := json.Unmarshal(structured, &other); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(payload, other) {
		t.Fatalf("text/structured disagree: %s vs %s", text.Text, structured)
	}
	if result.IsError != (payload["ok"] != true) {
		t.Fatalf("isError does not match envelope: %#v", result)
	}
	return payload
}
func call(t *testing.T, session *mcp.ClientSession, ctx context.Context, name string, args any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func requireCode(t *testing.T, payload map[string]any, code string) {
	t.Helper()
	errorObject, ok := payload["error"].(map[string]any)
	if !ok || errorObject["code"] != code {
		t.Fatalf("want %s, got %#v", code, payload)
	}
}

func TestToolCatalog(t *testing.T) {
	for _, allowWrite := range []bool{false, true} {
		t.Run(fmt.Sprint(allowWrite), func(t *testing.T) {
			session, ctx := connect(t, &stubBackend{}, allowWrite)
			result, err := session.ListTools(ctx, &mcp.ListToolsParams{})
			if err != nil {
				t.Fatal(err)
			}
			expected := map[string]bool{"vm_list": true, "vm_info": true, "guest_processes": true, "guest_windows": true, "guest_ui_tree": true}
			if allowWrite {
				for _, action := range []string{"start", "stop", "suspend", "resume"} {
					expected["vm_"+action] = false
				}
			}
			if len(result.Tools) != len(expected) {
				t.Fatalf("unexpected tools: %#v", result.Tools)
			}
			for _, tool := range result.Tools {
				readOnly, exists := expected[tool.Name]
				if !exists {
					t.Fatalf("unexpected tool: %s", tool.Name)
				}
				if tool.Annotations == nil || tool.Annotations.ReadOnlyHint != readOnly || tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint == readOnly || tool.Annotations.IdempotentHint != readOnly || tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint {
					t.Fatalf("annotations: %#v", tool)
				}
				schema, ok := tool.InputSchema.(map[string]any)
				if !ok || schema["type"] != "object" || schema["additionalProperties"] != false {
					t.Fatalf("schema: %#v", tool.InputSchema)
				}
			}
		})
	}
}

func TestReadToolsAndValidation(t *testing.T) {
	session, ctx := connect(t, dryBackend(t), false)
	for _, test := range []struct{ name, args string }{
		{"vm_list", `{}`}, {"vm_info", `{"vm":"测试 Windows"}`},
		{"guest_processes", `{"vm":"Win","name":"记事本 😀","pid":12,"limit":1}`},
		{"guest_processes", `{"vm":"Win","name":null,"pid":null}`},
		{"guest_windows", `{"vm":"Win","pid":12}`},
		{"guest_processes", `{"vm":"Win","pid":12.0,"limit":1.2e1}`},
		{"guest_ui_tree", `{"vm":"Win","hwnd":"0x123","max_depth":0,"max_nodes":1}`},
	} {
		t.Run(test.name+test.args, func(t *testing.T) {
			payload := envelope(t, call(t, session, ctx, test.name, json.RawMessage(test.args)))
			if payload["ok"] != true {
				t.Fatalf("unexpected failure: %#v", payload)
			}
		})
	}
	for _, test := range []struct{ name, args string }{
		{"vm_list", `{"vm":"Win"}`}, {"vm_info", `{}`}, {"vm_info", `{"vm":null}`}, {"vm_info", `{"vm":"-bad"}`},
		{"guest_processes", `{"vm":"Win","pid":1.1}`}, {"guest_processes", `{"vm":"Win","pid":true}`},
		{"guest_processes", `{"vm":"Win","pid":"12"}`}, {"guest_processes", `{"vm":"Win","pid":2147483648}`},
		{"guest_processes", `{"vm":"Win","limit":null}`}, {"guest_processes", `{"vm":"Win","limit":0}`},
		{"guest_processes", `{"vm":"Win","include_path":true}`},
		{"guest_windows", `{"vm":"Win","include_hidden":true}`}, {"guest_ui_tree", `{"vm":"Win"}`},
		{"guest_ui_tree", `{"vm":"Win","hwnd":"0"}`}, {"guest_ui_tree", `{"vm":"Win","hwnd":"１２３"}`},
		{"guest_ui_tree", `{"vm":"Win","hwnd":"1","max_depth":13}`},
		{"guest_ui_tree", `{"vm":"Win","hwnd":"1","max_nodes":1001}`},
		{"guest_ui_tree", `{"vm":"Win","hwnd":"1","timeout_ms":1000}`},
	} {
		t.Run("invalid/"+test.name+test.args, func(t *testing.T) {
			requireCode(t, envelope(t, call(t, session, ctx, test.name, json.RawMessage(test.args))), "INVALID_ARGUMENT")
		})
	}
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "vm_delete", Arguments: map[string]any{"vm": "Win"}}); err == nil {
		t.Fatal("destructive tool was registered")
	}
}

func TestDecodeArgumentsMalformed(t *testing.T) {
	for _, input := range []string{"null", "[]", "true", `{"vm":`, `{} {}`, `{"vm":"` + strings.Repeat("x", 65536) + `"}`} {
		if _, err := decodeArguments(json.RawMessage(input), map[string]argument{}); err == nil {
			t.Fatalf("accepted %q", input[:min(len(input), 60)])
		}
	}
}

func TestGuestErrorAndMetadata(t *testing.T) {
	for _, returnedError := range []bool{false, true} {
		t.Run(fmt.Sprint(returnedError), func(t *testing.T) {
			backend := &stubBackend{}
			if returnedError {
				backend.guest = func(context.Context, string, string, map[string]any) (bridge.Result, error) {
					return nil, bridge.NewError("TIMEOUT", "Timed out", map[string]any{"outcome": "no_result"})
				}
			}
			session, ctx := connect(t, backend, false)
			payload := envelope(t, call(t, session, ctx, "guest_windows", map[string]any{"vm": "Win"}))
			code := "INTERACTIVE_SESSION_REQUIRED"
			if returnedError {
				code = "TIMEOUT"
			}
			requireCode(t, payload, code)
		})
	}
	backend := &stubBackend{guest: func(_ context.Context, vm, action string, params map[string]any) (bridge.Result, error) {
		if vm != "测试" || action != "ui.tree" || params["max_depth"] != 6 || params["max_nodes"] != 300 {
			t.Errorf("defaults: %s %s %#v", vm, action, params)
		}
		return bridge.Result{"ok": true, "data": []any{}, "meta": map[string]any{"protocol_version": "1", "session_id": 1}}, nil
	}}
	session, ctx := connect(t, backend, false)
	payload := envelope(t, call(t, session, ctx, "guest_ui_tree", map[string]any{"vm": "测试", "hwnd": "1"}))
	if payload["meta"].(map[string]any)["protocol_version"] != "1" {
		t.Fatal("metadata was lost")
	}
}

func TestWriteToolsAreBounded(t *testing.T) {
	session, ctx := connect(t, &stubBackend{}, true)
	for _, action := range []string{"start", "stop", "suspend", "resume"} {
		payload := envelope(t, call(t, session, ctx, "vm_"+action, map[string]any{"vm": "Win"}))
		data := payload["data"].(map[string]any)
		if data["action"] != action || data["vm"] != "Win" || data["force"] != false || data["confirmation"] != "" {
			t.Fatalf("unbounded mutation: %#v", data)
		}
	}
	requireCode(t, envelope(t, call(t, session, ctx, "vm_stop", map[string]any{"vm": "Win", "force": true})), "INVALID_ARGUMENT")
}

func TestConcurrentRequestsAndCancellation(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	backend := &stubBackend{list: func(ctx context.Context) (any, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return nil, ctx.Err()
	}}
	session, ctx := connect(t, backend, false)
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = session.CallTool(callCtx, &mcp.CallToolParams{Name: "vm_list", Arguments: map[string]any{}})
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("blocked request never started")
	}
	payload := envelope(t, call(t, session, ctx, "vm_info", map[string]any{"vm": "independent"}))
	if payload["ok"] != true {
		t.Fatal(payload)
	}
	cancel()
	for _, signal := range []chan struct{}{done, stopped} {
		select {
		case <-signal:
		case <-ctx.Done():
			t.Fatal("cancellation did not reach backend")
		}
	}
}

// This test binary is also an MCP subprocess fixture: SDK client initialization,
// framing, tool discovery and shutdown traverse real OS stdin/stdout pipes.
func TestMCPStdioHelper(t *testing.T) {
	mode := os.Getenv("STARBRIDGE_TEST_MCP_STDIO")
	if mode == "" {
		return
	}
	if err := New(&stubBackend{}, mode == "write").Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}
func TestStdioProtocol(t *testing.T) {
	for _, mode := range []string{"read", "write"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			command := exec.Command(os.Args[0], "-test.run=^TestMCPStdioHelper$")
			command.Env = append(os.Environ(), "STARBRIDGE_TEST_MCP_STDIO="+mode)
			command.Stderr = os.Stderr
			session, err := mcp.NewClient(&mcp.Implementation{Name: "stdio test"}, nil).Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			result, err := session.ListTools(ctx, &mcp.ListToolsParams{})
			if err != nil {
				t.Fatal(err)
			}
			count := 5
			if mode == "write" {
				count = 9
			}
			if len(result.Tools) != count {
				t.Fatalf("tools=%d", len(result.Tools))
			}
			payload := envelope(t, call(t, session, ctx, "vm_list", map[string]any{}))
			if !strings.Contains(fmt.Sprint(payload), "测试 Windows") {
				t.Fatal(payload)
			}
			requireCode(t, envelope(t, call(t, session, ctx, "guest_windows", map[string]any{"vm": "Win"})), "INTERACTIVE_SESSION_REQUIRED")
			if err := session.Ping(ctx, nil); err != nil {
				t.Fatal(err)
			}
		})
	}
}

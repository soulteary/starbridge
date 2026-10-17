package bridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

var developerLocalCalls atomic.Int64

func init() {
	identity := func(p map[string]any) (map[string]any, error) { return p, nil }
	valid := func(p map[string]any) bool { return p["value"] == true }
	registerDeveloperAction(DeveloperAction{Name: "test.echo", Normalize: identity, ValidateResult: valid})
	registerDeveloperAction(DeveloperAction{Name: "test.mutate", Mutation: true, Normalize: identity, ValidateResult: valid})
	registerDeveloperAction(DeveloperAction{Name: "test.interactive", Interactive: true, Normalize: identity, ValidateResult: valid})
	registerDeveloperAction(DeveloperAction{Name: "test.preview", Normalize: identity, ValidateResult: valid,
		RedactPreview: func(p map[string]any) map[string]any { p["value"] = "[redacted]"; return p }})
	registerDeveloperAction(DeveloperAction{Name: "test.budget", Normalize: identity, ValidateResult: valid,
		ValidateWithinRequest: func(data, params map[string]any) bool { return data["budget"] == params["budget"] }})
	registerDeveloperAction(DeveloperAction{Name: "test.local", Mutation: true, Normalize: identity,
		Execute: func(_ context.Context, _ *Bridge, _ string, p map[string]any, allowWrite bool) (Result, error) {
			developerLocalCalls.Add(1)
			return Success(map[string]any{"value": p["value"], "authorized": allowWrite}), nil
		}})
}

func developerResponse(action string, mutation bool, data map[string]any) string {
	response := Success(data)
	response["meta"] = map[string]any{"protocol_version": DeveloperProtocol, "action": action, "read_only": !mutation, "session_id": 1, "user_interactive": true}
	encoded, _ := json.Marshal(response)
	return string(encoded)
}

func systemInfoData() map[string]any {
	return map[string]any{
		"os":                 map[string]any{"name": "Windows", "version": "10.0.26100", "build": "26100"},
		"architecture":       map[string]any{"os": "arm64", "process": "amd64"},
		"powershell_version": "5.1", "dotnet_version": "4.0",
		"session":      map[string]any{"id": 1, "user_interactive": true},
		"helper":       map[string]any{"protocol_version": DeveloperProtocol, "version": "dev-1", "sha256": strings.Repeat("a", 64)},
		"capabilities": []any{map[string]any{"name": "system.info", "mutation": false, "interactive": false, "description": "Environment information"}},
	}
}

func TestDeveloperSystemInfoTransportAndIdentity(t *testing.T) {
	f := newFixture(t)
	f.bridge.options.DeveloperScript = `D:\Bridge folder\developer's bridge.ps1`
	f.response.stdout = developerResponse("system.info", false, systemInfoData())
	vm := "Windows 中文 ; $(touch never)"
	result, err := f.bridge.Developer(context.Background(), vm, "system.info", nil, false)
	if err != nil || result["ok"] != true || f.calls != 1 {
		t.Fatalf("result=%#v err=%v calls=%d", result, err, f.calls)
	}
	want := []string{fakePRLCTL, "exec", vm, "--current-user", "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", f.bridge.options.DeveloperScript, "-RequestBase64"}
	if !reflect.DeepEqual(f.argv[:len(f.argv)-1], want) {
		t.Fatalf("argument vector=%#v", f.argv)
	}
	request, err := base64.StdEncoding.DecodeString(f.argv[len(f.argv)-1])
	if err != nil || string(request) != `{"action":"system.info","params":{}}` {
		t.Fatalf("request=%s err=%v", request, err)
	}
}

func TestDeveloperRejectsBeforeExecution(t *testing.T) {
	for _, test := range []struct {
		name, vm, action string
		params           map[string]any
		write            bool
		code             string
	}{
		{"VM option", "--all", "system.info", nil, false, "INVALID_ARGUMENT"},
		{"unknown", "vm", "arbitrary.shell", nil, true, "UNKNOWN_ACTION"},
		{"wrong case", "vm", "System.Info", nil, false, "UNKNOWN_ACTION"},
		{"unknown parameter", "vm", "system.info", map[string]any{"path": true}, false, "INVALID_ARGUMENT"},
		{"write denied", "vm", "test.mutate", nil, false, "WRITE_NOT_ALLOWED"},
		{"oversize", "vm", "test.echo", map[string]any{"text": strings.Repeat("界", 6000)}, false, "INVALID_ARGUMENT"},
		{"invalid JSON value", "vm", "test.echo", map[string]any{"function": func() {}}, false, "INVALID_ARGUMENT"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			_, err := f.bridge.Developer(context.Background(), test.vm, test.action, test.params, test.write)
			checkCode(t, err, test.code)
			if f.calls != 0 {
				t.Fatal("transport ran for rejected request")
			}
		})
	}
}

func TestDeveloperMutationAuthorizationAndUnknownOutcome(t *testing.T) {
	f := newFixture(t)
	f.response.stdout = developerResponse("test.mutate", true, map[string]any{"value": true})
	if _, err := f.bridge.Developer(context.Background(), "vm", "test.mutate", nil, true); err != nil {
		t.Fatal(err)
	}
	if f.argv[len(f.argv)-1] != "-AllowWrite" {
		t.Fatal(f.argv)
	}
	for _, runErr := range []error{context.Canceled, context.DeadlineExceeded} {
		f.err = runErr
		_, err := f.bridge.Developer(context.Background(), "vm", "test.mutate", nil, true)
		code := "INTERRUPTED"
		if runErr == context.DeadlineExceeded {
			code = "TIMEOUT"
		}
		if checkCode(t, err, code).Details["outcome"] != "unknown" {
			t.Fatal(err)
		}
	}
	f.err, f.response.outputExceeded = nil, true
	_, err := f.bridge.Developer(context.Background(), "vm", "test.mutate", nil, true)
	if checkCode(t, err, "OUTPUT_LIMIT_EXCEEDED").Details["outcome"] != "unknown" {
		t.Fatal(err)
	}
}

func TestDeveloperMetadataAndActionContracts(t *testing.T) {
	for _, test := range []struct {
		field string
		value any
	}{
		{"protocol_version", "1"}, {"action", "test.mutate"}, {"read_only", false},
		{"session_id", -1}, {"session_id", "1"}, {"session_id", nil}, {"user_interactive", "true"},
	} {
		t.Run(fmt.Sprint(test.field, test.value), func(t *testing.T) {
			payload, _ := decodeJSON(developerResponse("test.echo", false, map[string]any{"value": true}))
			response, _ := asObject(payload)
			meta, _ := asObject(response["meta"])
			meta[test.field] = test.value
			encoded, _ := json.Marshal(response)
			f := newFixture(t)
			f.response.stdout = string(encoded)
			_, err := f.bridge.Developer(context.Background(), "vm", "test.echo", nil, false)
			checkCode(t, err, "INVALID_GUEST_RESPONSE")
		})
	}
	for _, session := range []string{`0`, `0.0`} {
		f := newFixture(t)
		f.response.stdout = strings.Replace(developerResponse("test.interactive", false, map[string]any{"value": true}), `"session_id":1`, `"session_id":`+session, 1)
		_, err := f.bridge.Developer(context.Background(), "vm", "test.interactive", nil, false)
		checkCode(t, err, "INVALID_GUEST_RESPONSE")
	}
	f := newFixture(t)
	f.response.stdout = developerResponse("test.echo", false, map[string]any{"value": "wrong type"})
	_, err := f.bridge.Developer(context.Background(), "vm", "test.echo", nil, false)
	checkCode(t, err, "INVALID_GUEST_RESPONSE")
	f.response.stdout = developerResponse("test.budget", false, map[string]any{"value": true, "budget": "greater"})
	_, err = f.bridge.Developer(context.Background(), "vm", "test.budget", map[string]any{"budget": "requested"}, false)
	checkCode(t, err, "INVALID_GUEST_RESPONSE")
}

func TestDeveloperErrorEnvelopeAndExitCode(t *testing.T) {
	f := newFixture(t)
	f.response = commandResult{stdout: `{"ok":false,"error":{"code":"NOT_READY","message":"Unavailable"},"meta":{"protocol_version":"dev-1","action":"test.mutate","read_only":false,"session_id":0,"user_interactive":false}}`, exitCode: 1}
	result, err := f.bridge.Developer(context.Background(), "vm", "test.mutate", nil, true)
	if err != nil || result["ok"] != false {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	f.response = commandResult{stdout: `{"ok":false,"error":{"code":"NOT_READY","message":"Unavailable"}}`, exitCode: 1}
	_, err = f.bridge.Developer(context.Background(), "vm", "test.mutate", nil, true)
	checkCode(t, err, "INVALID_GUEST_RESPONSE")
	f.response = commandResult{stdout: developerResponse("test.echo", false, map[string]any{"value": true}), exitCode: 5}
	_, err = f.bridge.Developer(context.Background(), "vm", "test.echo", nil, false)
	checkCode(t, err, "GUEST_EXIT_MISMATCH")
}

func TestDeveloperSystemInfoRejectsMalformedAndMismatchedSession(t *testing.T) {
	for _, field := range []string{"os", "architecture", "session", "helper", "capabilities", "powershell_version"} {
		data := systemInfoData()
		data[field] = nil
		f := newFixture(t)
		f.response.stdout = developerResponse("system.info", false, data)
		_, err := f.bridge.Developer(context.Background(), "vm", "system.info", nil, false)
		checkCode(t, err, "INVALID_GUEST_RESPONSE")
	}
	data := systemInfoData()
	data["session"].(map[string]any)["id"] = 2
	f := newFixture(t)
	f.response.stdout = developerResponse("system.info", false, data)
	_, err := f.bridge.Developer(context.Background(), "vm", "system.info", nil, false)
	checkCode(t, err, "INVALID_GUEST_RESPONSE")
}

func TestDeveloperDryRunAndLocalActionStayOffline(t *testing.T) {
	f := newFixture(t)
	f.bridge.options.DryRun = true
	f.bridge.resolve = func(string) (string, error) { t.Fatal("dry run resolved CLI"); return "", nil }
	before := developerLocalCalls.Load()
	for _, action := range []string{"system.info", "test.local"} {
		result, err := f.bridge.Developer(context.Background(), "vm", action, nil, true)
		if err != nil || mapValue(t, result["data"])["dry_run"] != true {
			t.Fatalf("%s: %#v %v", action, result, err)
		}
	}
	if f.calls != 0 || developerLocalCalls.Load() != before {
		t.Fatal("dry run executed work")
	}
	_, err := f.bridge.Developer(context.Background(), "vm", "test.local", nil, false)
	checkCode(t, err, "WRITE_NOT_ALLOWED")
	f.bridge.options.DryRun = false
	result, err := f.bridge.Developer(context.Background(), "vm", "test.local", map[string]any{"value": "local"}, true)
	if err != nil || mapValue(t, result["data"])["value"] != "local" || developerLocalCalls.Load() != before+1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestDeveloperRegistryAndPath(t *testing.T) {
	actions := DeveloperActions()
	for i := 1; i < len(actions); i++ {
		if actions[i-1].Name >= actions[i].Name {
			t.Fatal("registry is not sorted")
		}
	}
	actions[0].Name = "replaced.action"
	if DeveloperActions()[0].Name == "replaced.action" {
		t.Fatal("registry was modified through its public copy")
	}
	opts := DefaultOptions()
	opts.DeveloperScript = ""
	b, err := New(opts)
	if err != nil || b.options.DeveloperScript != DefaultDeveloperScript {
		t.Fatal("missing default")
	}
	for _, path := range []string{`relative.ps1`, "C:\\a\nfile.ps1", `C:\a"b.ps1`} {
		opts.DeveloperScript = path
		_, err := New(opts)
		checkCode(t, err, "INVALID_ARGUMENT")
	}
}

func TestDeveloperPreviewRedactionDoesNotChangeWireOrCaller(t *testing.T) {
	params := map[string]any{"value": "synthetic-sensitive-fixture"}
	f := newFixture(t)
	f.bridge.options.DryRun = true
	result, err := f.bridge.Developer(context.Background(), "vm", "test.preview", params, false)
	if err != nil {
		t.Fatal(err)
	}
	argv := mapValue(t, result["data"])["argv"].([]string)
	request, err := base64.StdEncoding.DecodeString(argv[len(argv)-1])
	if err != nil || strings.Contains(string(request), "synthetic-sensitive-fixture") || !strings.Contains(string(request), "[redacted]") {
		t.Fatal("preview did not redact parameters")
	}
	if params["value"] != "synthetic-sensitive-fixture" {
		t.Fatal("preview modified caller's parameters")
	}
	f.bridge.options.DryRun = false
	f.response.stdout = developerResponse("test.preview", false, map[string]any{"value": true})
	if _, err := f.bridge.Developer(context.Background(), "vm", "test.preview", params, false); err != nil {
		t.Fatal(err)
	}
	request, err = base64.StdEncoding.DecodeString(f.argv[len(f.argv)-1])
	if err != nil || !strings.Contains(string(request), "synthetic-sensitive-fixture") {
		t.Fatal("preview changed the real request")
	}
}

func TestDeveloperRequestByteBoundary(t *testing.T) {
	empty, _ := json.Marshal(map[string]any{"action": "test.echo", "params": map[string]any{"text": ""}})
	params := map[string]any{"text": strings.Repeat("x", MaxDeveloperRequestBytes-len(empty))}
	f := newFixture(t)
	f.bridge.options.DryRun = true
	if _, err := f.bridge.Developer(context.Background(), "vm", "test.echo", params, false); err != nil {
		t.Fatal(err)
	}
	params["text"] = params["text"].(string) + "x"
	_, err := f.bridge.Developer(context.Background(), "vm", "test.echo", params, false)
	checkCode(t, err, "INVALID_ARGUMENT")
}

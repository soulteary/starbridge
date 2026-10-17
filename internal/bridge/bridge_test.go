package bridge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const fakePRLCTL = "/fake path/Parallels Desktop/prlctl"

type fixture struct {
	bridge   *Bridge
	response commandResult
	err      error
	calls    int
	argv     []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	b, err := New(DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{bridge: b}
	b.resolve = func(string) (string, error) { return fakePRLCTL, nil }
	b.runner = func(_ context.Context, executable string, args []string) (commandResult, error) {
		f.calls++
		f.argv = append([]string{executable}, args...)
		return f.response, f.err
	}
	return f
}

func checkCode(t *testing.T, err error, code string) *Error {
	t.Helper()
	var got *Error
	if !errors.As(err, &got) || got.Code != code {
		t.Fatalf("error = %#v; want %s", err, code)
	}
	return got
}

func mapValue(t *testing.T, value any) map[string]any {
	t.Helper()
	result, ok := asObject(value)
	if !ok {
		t.Fatalf("expected object, got %T", value)
	}
	return result
}

func TestTransportPreservesArgumentsAndUnicode(t *testing.T) {
	f := newFixture(t)
	f.response.stdout = "started\n"
	vm := "Windows 中文 ; $(touch never)"
	value, err := f.bridge.VMAction(context.Background(), "start", vm, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(value, map[string]any{"command_completed": true, "stdout": "started"}) {
		t.Fatalf("result: %#v", value)
	}
	if !reflect.DeepEqual(f.argv, []string{fakePRLCTL, "start", vm}) || f.calls != 1 {
		t.Fatalf("transport: %#v calls=%d", f.argv, f.calls)
	}
}

func TestStructuredTransportAndErrors(t *testing.T) {
	f := newFixture(t)
	f.response.stdout = `[{"uuid":"vm-1","name":"测试 Windows","status":"running"}]`
	value, err := f.bridge.VMList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.argv, []string{fakePRLCTL, "list", "-a", "-j"}) {
		t.Fatal(f.argv)
	}
	if value.([]any)[0].(map[string]any)["name"] != "测试 Windows" {
		t.Fatal(value)
	}
	f.response = commandResult{stdout: "unexpected banner", stderr: "Tools unavailable"}
	_, err = f.bridge.VMList(context.Background())
	got := checkCode(t, err, "INVALID_JSON")
	if got.Details["stdout"] != "unexpected banner" {
		t.Fatal(got.Details)
	}
	f.response = commandResult{stdout: strings.Repeat("o", 5000), stderr: strings.Repeat("e", 6000), exitCode: 7}
	_, err = f.bridge.VMAction(context.Background(), "start", "vm", false, "")
	got = checkCode(t, err, "PRLCTL_FAILED")
	if got.Details["exit_code"] != 7 || len(got.Details["stdout"].(string)) != 4096 || len(got.Details["stderr"].(string)) != 4096 {
		t.Fatal(got.Details)
	}
	for _, payload := range []string{`{} {}`, `[] trailing`} {
		f.response = commandResult{stdout: payload}
		_, err := f.bridge.VMList(context.Background())
		checkCode(t, err, "INVALID_JSON")
	}
}

func TestTimeoutCancellationAndOutputLimitDoNotRetry(t *testing.T) {
	for _, mutation := range []bool{false, true} {
		for _, test := range []struct {
			name, code string
			runError   error
			exceeded   bool
		}{
			{"deadline", "TIMEOUT", context.DeadlineExceeded, false},
			{"canceled", "INTERRUPTED", context.Canceled, false},
			{"limit", "OUTPUT_LIMIT_EXCEEDED", nil, true},
			{"spawn", "EXECUTION_FAILED", errors.New("permission denied"), false},
		} {
			t.Run(fmt.Sprintf("%s-mutation-%t", test.name, mutation), func(t *testing.T) {
				f := newFixture(t)
				f.err, f.response.outputExceeded = test.runError, test.exceeded
				var err error
				if mutation {
					_, err = f.bridge.VMClone(context.Background(), "vm", "clone")
				} else {
					_, err = f.bridge.VMList(context.Background())
				}
				got := checkCode(t, err, test.code)
				if f.calls != 1 {
					t.Fatalf("retried %d times", f.calls)
				}
				if test.code != "EXECUTION_FAILED" {
					outcome := "no_result"
					if mutation {
						outcome = "unknown"
					}
					if got.Details["outcome"] != outcome {
						t.Fatal(got.Details)
					}
				}
			})
		}
	}
}

func TestDryRunDoesNotResolveOrExecute(t *testing.T) {
	f := newFixture(t)
	f.bridge.options.PRLCTL, f.bridge.options.DryRun = fakePRLCTL, true
	f.bridge.resolve = func(string) (string, error) { t.Fatal("dry run resolved executable"); return "", nil }
	value, err := f.bridge.VMAction(context.Background(), "delete", "vm", false, "")
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]any{"dry_run": true, "argv": []string{fakePRLCTL, "delete", "vm"}, "mutation": true}
	if !reflect.DeepEqual(value, expected) || f.calls != 0 {
		t.Fatalf("result=%#v calls=%d", value, f.calls)
	}
}

func TestAllCommandArguments(t *testing.T) {
	f := newFixture(t)
	f.response.stdout = "{}"
	ctx := context.Background()
	for _, test := range []struct {
		name string
		call func() (any, error)
		args []string
	}{
		{"info", func() (any, error) { return f.bridge.VMInfo(ctx, "vm") }, []string{"list", "-i", "-j", "vm"}},
		{"stop", func() (any, error) { return f.bridge.VMAction(ctx, "stop", "vm", false, "") }, []string{"stop", "vm"}},
		{"force", func() (any, error) { return f.bridge.VMAction(ctx, "stop", "vm", true, "vm") }, []string{"stop", "vm", "--kill"}},
		{"delete", func() (any, error) { return f.bridge.VMAction(ctx, "delete", "vm", false, "vm") }, []string{"delete", "vm"}},
		{"suspend", func() (any, error) { return f.bridge.VMAction(ctx, "suspend", "vm", false, "") }, []string{"suspend", "vm"}},
		{"resume", func() (any, error) { return f.bridge.VMAction(ctx, "resume", "vm", false, "") }, []string{"resume", "vm"}},
		{"clone", func() (any, error) { return f.bridge.VMClone(ctx, "vm", "new 中文") }, []string{"clone", "vm", "--name", "new 中文"}},
		{"snapshots", func() (any, error) { return f.bridge.SnapshotList(ctx, "vm") }, []string{"snapshot-list", "vm", "-j"}},
		{"snapshot-create", func() (any, error) { return f.bridge.SnapshotCreate(ctx, "vm", "clean") }, []string{"snapshot", "vm", "--name", "clean"}},
		{"snapshot-restore", func() (any, error) { return f.bridge.SnapshotRestore(ctx, "vm", "clean", "vm") }, []string{"snapshot-switch", "vm", "--id", "clean", "--skip-resume"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.call(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(f.argv[1:], test.args) {
				t.Fatalf("got %v want %v", f.argv[1:], test.args)
			}
		})
	}
	value, err := f.bridge.Doctor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if mapValue(t, value)["guest_verified"] != false || f.argv[1] != "--version" {
		t.Fatal(value)
	}
}

func TestConfirmationAndActionValidationBeforeTransport(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for _, confirmation := range []string{"", "VM", "vm "} {
		for _, call := range []func() (any, error){
			func() (any, error) { return f.bridge.VMAction(ctx, "delete", "vm", false, confirmation) },
			func() (any, error) { return f.bridge.VMAction(ctx, "stop", "vm", true, confirmation) },
			func() (any, error) { return f.bridge.SnapshotRestore(ctx, "vm", "clean", confirmation) },
		} {
			_, err := call()
			checkCode(t, err, "CONFIRMATION_REQUIRED")
		}
	}
	for _, action := range []string{"start", "unsupported"} {
		_, err := f.bridge.VMAction(ctx, action, "vm", true, "")
		checkCode(t, err, "INVALID_ARGUMENT")
	}
	if f.calls != 0 {
		t.Fatal("invalid command reached transport")
	}
}

func TestValidation(t *testing.T) {
	for _, value := range []string{"", " ", "-vm", "vm\nother", "vm\x00", strings.Repeat("x", 513)} {
		if identifier(value, "VM") == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	if identifier("Windows 中文", "VM") != nil {
		t.Fatal("rejected unicode")
	}
	for _, value := range []any{true, false, float64(1), "1", 0, 1001, json.Number("1.1")} {
		if boundedInt(value, 1, 1000, "limit") == nil {
			t.Fatalf("accepted %#v", value)
		}
	}
	for _, value := range []any{1, int64(1000), json.Number("1000"), json.Number("12.0"), json.Number("1.2e1")} {
		if err := boundedInt(value, 1, 1000, "limit"); err != nil {
			t.Fatal(err)
		}
	}
	for _, timeout := range []time.Duration{0, time.Second - 1, 601 * time.Second} {
		opts := DefaultOptions()
		opts.Timeout = timeout
		_, err := New(opts)
		checkCode(t, err, "INVALID_ARGUMENT")
	}
	for _, path := range []string{"helper.ps1", `C:helper.ps1`, `\helper.ps1`, `C:\bad"path\helper.ps1`, "C:\\bad\nhelper.ps1", `\\server`} {
		opts := DefaultOptions()
		opts.GuestScript = path
		_, err := New(opts)
		checkCode(t, err, "INVALID_ARGUMENT")
	}
	for _, path := range []string{`C:\Program Files\中文\helper.ps1`, `\\server\share\helper.ps1`, `C:/foo/helper.ps1`} {
		opts := DefaultOptions()
		opts.GuestScript = path
		if _, err := New(opts); err != nil {
			t.Fatal(err)
		}
	}
}

func TestGuestRequestEncodingAndPath(t *testing.T) {
	f := newFixture(t)
	f.bridge.options.GuestScript = `C:\Program Files\桌面助手\desktop bridge.ps1`
	f.response.stdout = `{"ok":true,"data":{"processes":[{"name":"程序","pid":42}],"limit":17,"truncated":false},"meta":{"protocol_version":"1","action":"processes.list","session_id":1,"user_interactive":true}}`
	params := map[string]any{"name": "程序 名称 ; $(x)", "limit": 17, "include_path": true}
	result, err := f.bridge.Guest(context.Background(), "Windows 空格", "processes.list", params)
	if err != nil {
		t.Fatal(err)
	}
	if result["ok"] != true || !reflect.DeepEqual(f.argv[:5], []string{fakePRLCTL, "exec", "Windows 空格", "--current-user", "powershell.exe"}) {
		t.Fatal(f.argv, result)
	}
	if f.argv[11] != f.bridge.options.GuestScript {
		t.Fatal(f.argv)
	}
	decoded, err := base64.StdEncoding.DecodeString(f.argv[len(f.argv)-1])
	if err != nil {
		t.Fatal(err)
	}
	request, err := decodeJSON(string(decoded))
	if err != nil {
		t.Fatal(err)
	}
	if mapValue(t, mapValue(t, request)["params"])["name"] != params["name"] || params["limit"] != 17 {
		t.Fatal(request, params)
	}
	if strings.Contains(strings.Join(f.argv, "|"), "-Command") {
		t.Fatal("used arbitrary PowerShell code")
	}
}

func TestGuestEnvelopesAndTransportFailures(t *testing.T) {
	for _, test := range []struct {
		payload string
		exit    int
		code    string
	}{
		{`{"ok":false,"error":{"code":"UI_TIMEOUT","message":"窗口不可用"}}`, 1, ""},
		{`{"ok":true,"data":{}}`, 1, "GUEST_EXIT_MISMATCH"},
		{`[]`, 0, "INVALID_GUEST_RESPONSE"},
		{`{"ok":1,"data":[]}`, 0, "INVALID_GUEST_RESPONSE"},
		{`{"ok":true}`, 0, "INVALID_GUEST_RESPONSE"},
		{`{"ok":false,"error":"bad"}`, 0, "INVALID_GUEST_RESPONSE"},
		{"Tools missing", 9, "PRLCTL_FAILED"},
		{"Not JSON", 0, "INVALID_JSON"},
	} {
		f := newFixture(t)
		f.response = commandResult{stdout: test.payload, exitCode: test.exit}
		result, err := f.bridge.Guest(context.Background(), "vm", "windows.list", nil)
		if test.code != "" {
			checkCode(t, err, test.code)
		} else if err != nil || result["ok"] != false {
			t.Fatal(result, err)
		}
	}
}

func TestGuestProtocolAndSessionMetadata(t *testing.T) {
	valid := map[string]any{"protocol_version": "1", "action": "processes.list", "session_id": 1, "user_interactive": true}
	invalid := []map[string]any{nil}
	for field := range valid {
		metadata := map[string]any{}
		for key, value := range valid {
			if key != field {
				metadata[key] = value
			}
		}
		invalid = append(invalid, metadata)
	}
	for _, change := range []struct {
		field string
		value any
	}{{"protocol_version", "2"}, {"protocol_version", 1}, {"action", "windows.list"}, {"session_id", -1}, {"session_id", true}, {"session_id", "1"}, {"session_id", json.Number("1.1")}, {"user_interactive", 1}, {"user_interactive", "true"}, {"user_interactive", nil}} {
		metadata := map[string]any{}
		for key, value := range valid {
			metadata[key] = value
		}
		metadata[change.field] = change.value
		invalid = append(invalid, metadata)
	}
	for _, metadata := range invalid {
		f := newFixture(t)
		payload, _ := json.Marshal(map[string]any{"ok": true, "data": validGuestData("processes.list"), "meta": metadata})
		f.response.stdout = string(payload)
		_, err := f.bridge.Guest(context.Background(), "vm", "processes.list", nil)
		checkCode(t, err, "GUEST_PROTOCOL_MISMATCH")
	}
	for _, action := range []string{"processes.list", "windows.list", "ui.tree"} {
		for _, session := range []int{0, 1} {
			for _, interactive := range []bool{false, true} {
				f := newFixture(t)
				payload, _ := json.Marshal(map[string]any{"ok": true, "data": validGuestData(action), "meta": map[string]any{"protocol_version": "1", "action": action, "session_id": session, "user_interactive": interactive}})
				f.response.stdout = string(payload)
				params := map[string]any{}
				if action == "ui.tree" {
					params["hwnd"] = "1"
				}
				_, err := f.bridge.Guest(context.Background(), "vm", action, params)
				if action != "processes.list" && (session == 0 || !interactive) {
					checkCode(t, err, "INTERACTIVE_SESSION_REQUIRED")
				} else if err != nil {
					t.Fatal(action, err)
				}
			}
		}
	}
}

func TestGuestParameterValidationBeforeExecution(t *testing.T) {
	f := newFixture(t)
	for _, test := range []struct {
		action string
		params map[string]any
	}{
		{"processes.list", map[string]any{"pid": 0}}, {"processes.list", map[string]any{"pid": int64(2147483648)}},
		{"windows.list", map[string]any{"pid": true}}, {"windows.list", map[string]any{"limit": 1001}},
		{"processes.list", map[string]any{"limit": 0}}, {"processes.list", map[string]any{"name": strings.Repeat("x", 257)}},
		{"processes.list", map[string]any{"include_path": "yes"}}, {"windows.list", map[string]any{"include_hidden": 1}},
		{"windows.list", map[string]any{"extra": true}}, {"unsupported", nil}, {"ui.tree", nil},
		{"ui.tree", map[string]any{"hwnd": 123}}, {"ui.tree", map[string]any{"hwnd": "0"}}, {"ui.tree", map[string]any{"hwnd": "-1"}},
		{"ui.tree", map[string]any{"hwnd": "+12"}}, {"ui.tree", map[string]any{"hwnd": "1_0"}}, {"ui.tree", map[string]any{"hwnd": "１２"}},
		{"ui.tree", map[string]any{"hwnd": " 1"}}, {"ui.tree", map[string]any{"hwnd": "0x8000000000000000"}},
		{"ui.tree", map[string]any{"hwnd": "1", "max_depth": 13}}, {"ui.tree", map[string]any{"hwnd": "1", "max_nodes": 1001}},
		{"ui.tree", map[string]any{"hwnd": "1", "timeout_ms": 999}}, {"ui.tree", map[string]any{"hwnd": "1", "timeout_ms": 20001}},
	} {
		_, err := f.bridge.Guest(context.Background(), "vm", test.action, test.params)
		checkCode(t, err, "INVALID_ARGUMENT")
	}
	if f.calls != 0 {
		t.Fatal("invalid request reached transport")
	}
	f.bridge.options.DryRun = true
	for _, test := range []struct {
		action string
		params map[string]any
	}{
		{"processes.list", map[string]any{"pid": 2147483647, "limit": 1000, "include_path": false}},
		{"windows.list", map[string]any{"pid": 1, "limit": 1, "include_hidden": false}},
		{"ui.tree", map[string]any{"hwnd": "0x7fffffffffffffff", "max_depth": 12, "max_nodes": 1000, "timeout_ms": 20000}},
		{"ui.tree", map[string]any{"hwnd": "1", "max_depth": 0, "max_nodes": 1, "timeout_ms": 1000}},
	} {
		result, err := f.bridge.Guest(context.Background(), "vm", test.action, test.params)
		if err != nil || result["ok"] != true {
			t.Fatal(result, err)
		}
	}
}

func policyFile(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRecyclePoliciesAndPrecedence(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	value, err := f.bridge.Recycle(ctx, "vm", RecycleOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if mapValue(t, value)["policy_source"] != "built-in default" || !reflect.DeepEqual(f.argv[1:], []string{"stop", "vm"}) {
		t.Fatal(value, f.argv)
	}
	path := policyFile(t, `{"default":{"mode":"stop"},"vms":{"disposable":{"mode":"delete"},"test-win":{"mode":"restore","snapshot":"clean-snapshot"}}}`)
	for _, test := range []struct {
		vm, source string
		opts       RecycleOptions
		args       []string
	}{
		{"other", "policy default", RecycleOptions{PolicyFile: path}, []string{"stop", "other"}},
		{"disposable", "per-VM policy", RecycleOptions{PolicyFile: path, Confirmation: "disposable"}, []string{"delete", "disposable"}},
		{"test-win", "per-VM policy", RecycleOptions{PolicyFile: path, Confirmation: "test-win"}, []string{"snapshot-switch", "test-win", "--id", "clean-snapshot", "--skip-resume"}},
		{"test-win", "command override", RecycleOptions{PolicyFile: path, Mode: "stop"}, []string{"stop", "test-win"}},
		{"test-win", "per-VM policy", RecycleOptions{PolicyFile: path, Snapshot: "new", Confirmation: "test-win"}, []string{"snapshot-switch", "test-win", "--id", "new", "--skip-resume"}},
	} {
		value, err := f.bridge.Recycle(ctx, test.vm, test.opts)
		if err != nil {
			t.Fatal(err)
		}
		if mapValue(t, value)["policy_source"] != test.source || !reflect.DeepEqual(f.argv[1:], test.args) {
			t.Fatal(value, f.argv)
		}
		if test.opts.Mode == "stop" {
			if _, exists := mapValue(t, mapValue(t, value)["policy"])["snapshot"]; exists {
				t.Fatal("old snapshot retained")
			}
		}
	}
	f.response = commandResult{exitCode: 1, stdout: "restore failed"}
	before := f.calls
	_, err = f.bridge.Recycle(ctx, "vm", RecycleOptions{Mode: "restore", Snapshot: "clean", Confirmation: "vm"})
	checkCode(t, err, "PRLCTL_FAILED")
	if f.calls != before+1 {
		t.Fatal("failure caused fallback")
	}
}

func TestInvalidPoliciesFailWithoutExecution(t *testing.T) {
	f := newFixture(t)
	for _, payload := range []string{`[]`, `{"unknown":{}}`, `{"default":[]}`, `{"vms":[]}`, `{"default":{"mode":"pause"}}`, `{"default":{"mode":[]}}`, `{"default":{"mode":{}}}`, `{"default":{"mode":"restore"}}`, `{"default":{"mode":"stop","snapshot":"s"}}`, `{"vms":{"unused-vm":{"mode":"restore","snapshot":""}}}`, `null`, `{`} {
		_, err := f.bridge.Recycle(context.Background(), "vm", RecycleOptions{PolicyFile: policyFile(t, payload)})
		checkCode(t, err, "INVALID_POLICY")
	}
	for _, opts := range []RecycleOptions{{Mode: "restore"}, {Snapshot: "snap"}, {Mode: "pause"}, {PolicyFile: filepath.Join(t.TempDir(), "missing")}} {
		_, err := f.bridge.Recycle(context.Background(), "vm", opts)
		if err == nil {
			t.Fatalf("accepted %#v", opts)
		}
	}
	if f.calls != 0 {
		t.Fatal("invalid policy reached transport")
	}
}

func TestEnvironmentPrecedence(t *testing.T) {
	t.Setenv("STARBRIDGE_PRLCTL", "new-env")
	t.Setenv("PRLBRIDGE_PRLCTL", "old-env")
	if configuredPRLCTL("explicit") != "explicit" || configuredPRLCTL("") != "new-env" {
		t.Fatal("precedence")
	}
	t.Setenv("STARBRIDGE_PRLCTL", "")
	if configuredPRLCTL("") != "old-env" {
		t.Fatal("legacy precedence")
	}
	f := newFixture(t)
	f.bridge.options.DryRun = true
	value, err := f.bridge.VMList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if mapValue(t, value)["argv"].([]string)[0] != "old-env" {
		t.Fatal(value)
	}
	t.Setenv("PRLBRIDGE_PRLCTL", "")
	value, err = f.bridge.VMList(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if mapValue(t, value)["argv"].([]string)[0] != AppPRLCTL {
		t.Fatal(value)
	}
}

func TestFailureMarshalsAsOriginalEnvelope(t *testing.T) {
	data, err := json.Marshal(Failure(NewError("FAIL", "message", map[string]any{"vm": "中文"})))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"error":{"code":"FAIL","message":"message","details":{"vm":"中文"}},"ok":false}` {
		t.Fatal(string(data))
	}
}

// The test binary is a controlled real child process: no shell or external runtime.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("STARBRIDGE_GO_TEST_CHILD") != "1" {
		return
	}
	separator := 0
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	args := os.Args[separator+1:]
	switch args[0] {
	case "args":
		data, _ := json.Marshal(args[1:])
		_, _ = os.Stdout.Write(append([]byte{0xef, 0xbb, 0xbf}, data...))
	case "exit":
		_, _ = fmt.Fprint(os.Stdout, "out")
		_, _ = fmt.Fprint(os.Stderr, strings.Repeat("e", 10000))
		os.Exit(7)
	case "large":
		_, _ = os.Stdout.Write(bytes.Repeat([]byte("x"), MaxOutputBytes+65536))
	case "sleep":
		time.Sleep(10 * time.Second)
	}
	os.Exit(0)
}

func TestNativeRunnerBOMArgumentsExitAndBounds(t *testing.T) {
	t.Setenv("STARBRIDGE_GO_TEST_CHILD", "1")
	command := os.Args[0]
	args := []string{"-test.run=^TestHelperProcess$", "--", "args", "Windows 中文 ; $(touch never)", "space arg"}
	result, err := runCommand(context.Background(), command, args)
	if err != nil {
		t.Fatal(err)
	}
	if result.stdout != `["Windows 中文 ; $(touch never)","space arg"]` {
		t.Fatal(result.stdout)
	}
	result, err = runCommand(context.Background(), command, []string{"-test.run=^TestHelperProcess$", "--", "exit"})
	if err != nil || result.exitCode != 7 || len(result.stderr) != 4096 || result.stdout != "out" {
		t.Fatal(result, err)
	}
	result, err = runCommand(context.Background(), command, []string{"-test.run=^TestHelperProcess$", "--", "large"})
	if err != nil || !result.outputExceeded || len(result.stdout) != MaxOutputBytes {
		t.Fatal(len(result.stdout), result.outputExceeded, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = runCommand(ctx, command, []string{"-test.run=^TestHelperProcess$", "--", "sleep"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	canceled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	_, err = runCommand(canceled, command, []string{"-test.run=^TestHelperProcess$", "--", "sleep"})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

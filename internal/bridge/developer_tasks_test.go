package bridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const fixtureTaskID = "0123456789abcdef0123456789abcdef"

func taskData() map[string]any {
	return map[string]any{"task_id": fixtureTaskID, "profile": "build", "status": "running", "created_at": "2026-10-18T00:00:00Z", "pid": 42, "start_time_utc": "2026-10-18T00:00:01Z", "exit_code": nil, "stdout_bytes": 0, "stderr_bytes": 0, "stdout_truncated": false, "stderr_truncated": false}
}
func taskEnvelope(action string, data map[string]any) string {
	mutation := action == "task.start" || action == "task.cancel" || action == "task.close"
	encoded, _ := json.Marshal(Result{"ok": true, "data": data, "meta": map[string]any{"protocol_version": DeveloperProtocol, "action": action, "read_only": !mutation, "session_id": 1, "user_interactive": true}})
	return string(encoded)
}
func TestDeveloperTasksRequireAuthorizationAndKeepArgumentBoundaries(t *testing.T) {
	f := newFixture(t)
	params := map[string]any{"profile": "build", "args": []string{"中文 空格", `a"b`, `C:\end\`, "", `$(never); & still-data`}, "env": map[string]string{"BUILD_LABEL": "placeholder-only"}}
	before, _ := json.Marshal(params)
	_, err := f.bridge.Developer(context.Background(), "vm", "task.start", params, false)
	checkCode(t, err, "WRITE_NOT_ALLOWED")
	if f.calls != 0 {
		t.Fatal("denied mutation ran")
	}
	f.response.stdout = taskEnvelope("task.start", taskData())
	result, err := f.bridge.Developer(context.Background(), "vm", "task.start", params, true)
	if err != nil || result["ok"] != true {
		t.Fatal(result, err)
	}
	if f.calls != 1 || f.argv[len(f.argv)-1] != "-AllowWrite" {
		t.Fatal(f.argv)
	}
	encoded := f.argv[len(f.argv)-2]
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	request, err := decodeJSON(string(decoded))
	if err != nil {
		t.Fatal(err)
	}
	actual := mapValue(t, mapValue(t, request)["params"])
	if !reflect.DeepEqual(actual["args"], []any{"中文 空格", `a"b`, `C:\end\`, "", `$(never); & still-data`}) {
		t.Fatal(actual)
	}
	after, _ := json.Marshal(params)
	if string(before) != string(after) {
		t.Fatal("input mutation")
	}
}
func TestDeveloperTaskInvalidInputsDoNotRun(t *testing.T) {
	f := newFixture(t)
	for _, test := range []struct {
		action string
		params map[string]any
	}{
		{"task.start", map[string]any{"profile": "../escape"}}, {"task.start", map[string]any{"profile": "build", "executable": "cmd.exe"}},
		{"task.start", map[string]any{"profile": "build", "args": "command"}}, {"task.start", map[string]any{"profile": "build", "args": []any{true}}},
		{"task.start", map[string]any{"profile": "build", "args": []string{strings.Repeat("x", 1025)}}},
		{"task.start", map[string]any{"profile": "build", "env": map[string]any{"A": 1}}}, {"task.start", map[string]any{"profile": "build", "env": map[string]string{"BAD=KEY": "x"}}},
		{"task.start", map[string]any{"profile": "build", "env": map[string]string{"A": "x\x00y"}}}, {"task.start", map[string]any{"profile": "build", "timeout_ms": true}},
		{"task.start", map[string]any{"profile": "build", "timeout_ms": 3600001}}, {"task.status", map[string]any{"task_id": "../path"}},
		{"task.cancel", map[string]any{"task_id": fixtureTaskID, "pid": 42}}, {"task.output", map[string]any{"task_id": fixtureTaskID, "stream": "../stderr"}},
		{"task.output", map[string]any{"task_id": fixtureTaskID, "offset": -1}}, {"task.output", map[string]any{"task_id": fixtureTaskID, "limit": 65537}},
	} {
		_, err := f.bridge.Developer(context.Background(), "vm", test.action, test.params, true)
		checkCode(t, err, "INVALID_ARGUMENT")
	}
	if f.calls != 0 {
		t.Fatal("invalid input reached runner")
	}
}
func TestDeveloperTaskOutputBudgetsAndIdentity(t *testing.T) {
	for _, change := range []string{"none", "identity", "offset", "stream", "length", "encoding"} {
		t.Run(change, func(t *testing.T) {
			f := newFixture(t)
			data := map[string]any{"task_id": fixtureTaskID, "status": "exited", "stream": "stdout", "offset": 2, "next_offset": 5, "data_base64": base64.StdEncoding.EncodeToString([]byte{0xe4, 0xb8, 0xad}), "eof": true, "truncated": false}
			switch change {
			case "identity":
				data["task_id"] = strings.Repeat("a", 32)
			case "offset":
				data["offset"] = 3
				data["next_offset"] = 6
			case "stream":
				data["stream"] = "stderr"
			case "length":
				data["data_base64"] = base64.StdEncoding.EncodeToString([]byte("more"))
				data["next_offset"] = 6
			case "encoding":
				data["data_base64"] = "@@@"
			}
			f.response.stdout = taskEnvelope("task.output", data)
			result, err := f.bridge.Developer(context.Background(), "vm", "task.output", map[string]any{"task_id": fixtureTaskID, "offset": 2, "limit": 3}, false)
			if change == "none" {
				if err != nil || result["ok"] != true {
					t.Fatal(result, err)
				}
			} else {
				checkCode(t, err, "INVALID_GUEST_RESPONSE")
			}
		})
	}
}
func TestDeveloperTaskControlAndUnknownOutcome(t *testing.T) {
	for _, action := range []string{"task.cancel", "task.close"} {
		f := newFixture(t)
		data := taskData()
		data["accepted"], data["control_status"], data["reason"] = false, "blocked", "NO_MAIN_WINDOW_OR_CLOSE_BLOCKED"
		f.response.stdout = taskEnvelope(action, data)
		result, err := f.bridge.Developer(context.Background(), "vm", action, map[string]any{"task_id": fixtureTaskID}, true)
		if err != nil || result["ok"] != true {
			t.Fatal(action, result, err)
		}
	}
	f := newFixture(t)
	f.err = context.DeadlineExceeded
	_, err := f.bridge.Developer(context.Background(), "vm", "task.start", map[string]any{"profile": "build"}, true)
	failure := checkCode(t, err, "TIMEOUT")
	if failure.Details["outcome"] != "unknown" || f.calls != 1 {
		t.Fatal(failure, f.calls)
	}
}
func TestDeveloperTaskOutputDefaultsAndPrivateCopies(t *testing.T) {
	input := map[string]any{"task_id": fixtureTaskID}
	params, err := normalizeTaskParameters("task.output", input)
	if err != nil || params["limit"] != int64(16384) || params["offset"] != int64(0) || params["stream"] != "stdout" {
		t.Fatal(params, err)
	}
	if len(input) != 1 {
		t.Fatal("input changed")
	}
	args := []string{"value"}
	env := map[string]string{"LABEL": "value"}
	params, err = normalizeTaskParameters("task.start", map[string]any{"profile": "build", "args": args, "env": env})
	if err != nil {
		t.Fatal(err)
	}
	params["args"].([]string)[0] = "changed"
	params["env"].(map[string]string)["LABEL"] = "changed"
	if args[0] != "value" || env["LABEL"] != "value" {
		t.Fatal("nested input aliased")
	}
}

func TestDeveloperTaskDryRunRedactsSecretsAndRunsNothing(t *testing.T) {
	f := newFixture(t)
	f.bridge.options.DryRun = true
	params := map[string]any{"profile": "build", "args": []string{"private-argument"}, "env": map[string]string{"PRIVATE_LABEL": "private-environment"}}
	result, err := f.bridge.Developer(context.Background(), "vm", "task.start", params, true)
	if err != nil || result["ok"] != true {
		t.Fatal(result, err)
	}
	argv := mapValue(t, result["data"])["argv"].([]string)
	request, err := base64.StdEncoding.DecodeString(argv[len(argv)-2])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(request), "private-argument") || strings.Contains(string(request), "private-environment") || !strings.Contains(string(request), "[redacted]") {
		t.Fatal("preview leaked private values")
	}
	if params["args"].([]string)[0] != "private-argument" || params["env"].(map[string]string)["PRIVATE_LABEL"] != "private-environment" || f.calls != 0 {
		t.Fatal("preview mutated input or executed")
	}
}

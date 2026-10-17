package bridge

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func waitValidationRequest(action string, params map[string]any) map[string]any {
	return map[string]any{"action": action, "params": params, "predicate": map[string]any{"path": "data", "op": "exists"}, "timeout_ms": 20, "interval_ms": 10}
}

func TestWaitAndAssertionValidateObservationBeforePreviewOrExecution(t *testing.T) {
	for _, test := range []struct {
		name, observation string
		params            map[string]any
	}{
		{"VM unknown field", "vm.info", map[string]any{"unexpected": true}},
		{"process unknown field", "processes.list", map[string]any{"unexpected": true}},
		{"process zero limit", "processes.list", map[string]any{"limit": 0}},
		{"process wrong name type", "processes.list", map[string]any{"name": []any{}}},
		{"window zero limit", "windows.list", map[string]any{"limit": 0}},
		{"window invalid flag", "windows.list", map[string]any{"include_hidden": "true"}},
		{"UI tree missing identity", "ui.tree", map[string]any{}},
		{"UI tree zero budget", "ui.tree", map[string]any{"hwnd": "0x123", "max_nodes": 0}},
		{"environment unknown field", "system.info", map[string]any{"unexpected": true}},
		{"task missing identity", "task.status", map[string]any{}},
		{"task invalid identity", "task.status", map[string]any{"task_id": "invalid"}},
		{"task unknown field", "task.status", map[string]any{"task_id": strings.Repeat("a", 32), "limit": 1}},
		{"UI query missing identity", "ui.query", map[string]any{}},
		{"UI query zero budget", "ui.query", map[string]any{"hwnd": "0x123", "pid": 42, "start_time_utc": "2026-01-02T03:04:05Z", "limit": 0}},
	} {
		for _, action := range []string{"wait.until", "assert.matches"} {
			for _, dryRun := range []bool{false, true} {
				name := test.name + "/" + action + "/runtime"
				if dryRun {
					name = test.name + "/" + action + "/preview"
				}
				t.Run(name, func(t *testing.T) {
					f := newFixture(t)
					f.bridge.options.DryRun = dryRun
					resolutions := 0
					f.bridge.resolve = func(string) (string, error) { resolutions++; return fakePRLCTL, nil }
					_, err := f.bridge.Developer(context.Background(), "synthetic-vm", action, waitValidationRequest(test.observation, test.params), false)
					checkCode(t, err, "INVALID_ARGUMENT")
					if f.calls != 0 || resolutions != 0 {
						t.Fatal("invalid observation reached the backend")
					}
				})
			}
		}
	}
}

func TestWaitNormalizesObservationDefaultsWithoutChangingCaller(t *testing.T) {
	for _, test := range []struct {
		action           string
		params, expected map[string]any
	}{
		{"vm.info", map[string]any{}, map[string]any{}},
		{"processes.list", map[string]any{"limit": json.Number("1e0")}, map[string]any{"limit": int64(1), "include_path": false}},
		{"windows.list", map[string]any{}, map[string]any{"limit": int64(500), "include_hidden": false}},
		{"ui.tree", map[string]any{"hwnd": "0X000123"}, map[string]any{"hwnd": "0x123", "max_depth": int64(6), "max_nodes": int64(300), "timeout_ms": int64(10000)}},
		{"system.info", map[string]any{}, map[string]any{}},
		{"task.status", map[string]any{"task_id": strings.Repeat("a", 32)}, map[string]any{"task_id": strings.Repeat("a", 32)}},
		{"ui.query", map[string]any{"hwnd": "0X000123", "pid": 42, "start_time_utc": "2026-01-02T03:04:05Z"}, map[string]any{"hwnd": "0x123", "pid": int64(42), "limit": int64(50), "include_value": false}},
	} {
		t.Run(test.action, func(t *testing.T) {
			request := waitValidationRequest(test.action, test.params)
			before, _ := json.Marshal(request)
			normalized, err := normalizeWait(request)
			if err != nil {
				t.Fatal(err)
			}
			params := mapValue(t, normalized["params"])
			for key, expected := range test.expected {
				if !reflect.DeepEqual(params[key], expected) {
					t.Fatalf("%s = %#v; want %#v", key, params[key], expected)
				}
			}
			repeated, err := normalizeWait(normalized)
			if err != nil || !reflect.DeepEqual(normalized, repeated) {
				t.Fatalf("normalization is not idempotent: %v", err)
			}
			for _, action := range []string{"wait.until", "assert.matches"} {
				f := newFixture(t)
				f.bridge.options.DryRun = true
				f.bridge.resolve = func(string) (string, error) { t.Fatal("preview resolved the backend"); return "", nil }
				result, err := f.bridge.Developer(context.Background(), "synthetic-vm", action, request, false)
				if err != nil || result["ok"] != true {
					t.Fatalf("preview failed: %v", err)
				}
				preview := mapValue(t, mapValue(t, result["data"])["params"])
				if !reflect.DeepEqual(preview["params"], params) || f.calls != 0 {
					t.Fatal("preview did not use the canonical observation parameters")
				}
			}
			after, _ := json.Marshal(request)
			if string(before) != string(after) {
				t.Fatal("observation normalization modified the caller")
			}
		})
	}
}

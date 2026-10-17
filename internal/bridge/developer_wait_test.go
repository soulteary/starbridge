package bridge

import (
	"context"
	"encoding/json"
	"testing"
)

func waitRequest(action string) map[string]any {
	return map[string]any{"action": action, "params": map[string]any{"limit": 1}, "predicate": map[string]any{"path": "data.processes", "op": "nonempty"}, "timeout_ms": 100, "interval_ms": 10}
}

func TestDeveloperWaitObservesWithoutRepeatingWrites(t *testing.T) {
	f := newFixture(t)
	calls := 0
	f.bridge.runner = func(context.Context, string, []string) (commandResult, error) {
		calls++
		items := []any{}
		if calls > 1 {
			items = append(items, map[string]any{"pid": 42, "name": "SyntheticFixture"})
		}
		response := map[string]any{"ok": true, "data": map[string]any{"processes": items, "limit": 1, "truncated": false}, "meta": map[string]any{"protocol_version": "1", "action": "processes.list", "session_id": 1, "user_interactive": true}}
		encoded, _ := json.Marshal(response)
		return commandResult{stdout: string(encoded)}, nil
	}
	p, err := normalizeWait(waitRequest("processes.list"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.bridge.waitUntil(context.Background(), "synthetic-vm", p)
	if err != nil || result["ok"] != true || calls != 2 {
		t.Fatalf("result=%v err=%v calls=%d", result, err, calls)
	}
}

func TestDeveloperWaitTimeoutAndCancellation(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "cancelled"}[cancelled], func(t *testing.T) {
			f := newFixture(t)
			f.response.stdout = `{"ok":true,"data":{"processes":[],"limit":1,"truncated":false},"meta":{"protocol_version":"1","action":"processes.list","session_id":1,"user_interactive":true}}`
			p := waitRequest("processes.list")
			p["timeout_ms"] = 20
			p, err := normalizeWait(p)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if cancelled {
				cancel()
			}
			_, err = f.bridge.waitUntil(ctx, "synthetic-vm", p)
			code := "WAIT_TIMEOUT"
			if cancelled {
				code = "INTERRUPTED"
				if f.calls != 0 {
					t.Fatal("cancelled wait ran a command")
				}
			}
			checkCode(t, err, code)
		})
	}
}

func TestDeveloperWaitRejectsMutationAndInvalidPredicates(t *testing.T) {
	for _, action := range []string{"task.start", "vm.start", "ui.invoke", "wait.until", "unknown"} {
		if _, err := normalizeWait(waitRequest(action)); err == nil {
			t.Errorf("accepted %s", action)
		}
	}
	for _, predicate := range []map[string]any{
		{"path": "data..value", "op": "eq", "value": 1},
		{"path": "data.value", "op": "execute", "value": "synthetic"},
		{"path": "data.value", "op": "eq"},
		{"path": "data.value", "op": "eq", "value": map[string]any{}},
		{"path": "data.value", "op": "empty", "value": "unexpected"},
	} {
		p := waitRequest("processes.list")
		p["predicate"] = predicate
		if _, err := normalizeWait(p); err == nil {
			t.Errorf("accepted predicate: %v", predicate)
		}
	}
}

func TestDeveloperWaitPredicatePathsAndScalarComparison(t *testing.T) {
	result := Success(map[string]any{"nodes": []any{map[string]any{"is_enabled": true, "name": "Synthetic button", "count": json.Number("12.0")}}})
	for _, p := range []map[string]any{
		{"path": "data.nodes.0.is_enabled", "op": "eq", "value": true},
		{"path": "data.nodes.0.name", "op": "contains", "value": "button"},
		{"path": "data.nodes.0.count", "op": "eq", "value": 12},
		{"path": "data.nodes", "op": "nonempty"},
	} {
		if !matchesWait(result, p) {
			t.Errorf("did not match %v", p)
		}
	}
	if matchesWait(result, map[string]any{"path": "data.nodes.1.name", "op": "exists"}) {
		t.Fatal("matched missing node")
	}
}

func TestDeveloperWaitDryRunNeverObserves(t *testing.T) {
	f := newFixture(t)
	f.bridge.options.DryRun = true
	p, err := normalizeWait(waitRequest("processes.list"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.bridge.waitUntil(context.Background(), "synthetic-vm", p)
	if err != nil || result["ok"] != true || f.calls != 0 {
		t.Fatal("dry run executed backend")
	}
}

func TestDeveloperAssertionDoesNotPoll(t *testing.T) {
	f := newFixture(t)
	f.response.stdout = `{"ok":true,"data":{"processes":[],"limit":1,"truncated":false},"meta":{"protocol_version":"1","action":"processes.list","session_id":1,"user_interactive":true}}`
	_, err := f.bridge.Developer(context.Background(), "synthetic-vm", "assert.matches", waitRequest("processes.list"), false)
	checkCode(t, err, "ASSERTION_FAILED")
	if f.calls != 1 {
		t.Fatal("assertion polled backend")
	}
}

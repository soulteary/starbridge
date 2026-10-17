package bridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func uiActionFixtureParams(name string) map[string]any {
	p := uiFixtureParams()
	switch name {
	case "ui.act":
		p["operation"] = "invoke"
		p["selector"] = map[string]any{"name": "Fixture button", "control_type": "Button"}
	case "window.resize":
		p["width"], p["height"] = 600, 400
	case "input.key":
		p["key"] = "A"
		p["modifiers"] = []any{"Control"}
	case "input.mouse":
		p["x"], p["y"] = 20, 30
	}
	return p
}

func uiActionFixtureData(name string, p map[string]any) map[string]any {
	data := map[string]any{"action": name, "window": map[string]any{"hwnd": p["hwnd"], "pid": p["pid"], "start_time_utc": p["start_time_utc"]},
		"timestamp": "2026-01-02T03:05:00Z", "outcome": "dispatched", "coordination": "windows-session-mutex"}
	switch name {
	case "ui.act":
		data["operation"] = p["operation"]
		data["element"] = map[string]any{"runtime_id": []any{1, 2}, "control_type": "Button", "is_password": false}
	case "window.resize":
		data["rect"] = map[string]any{"x": -20, "y": 40, "width": 600, "height": 400}
		data["coordinate_space"] = "guest-screen-physical-pixels"
	case "input.key", "input.mouse":
		data["input_count"] = 2
		data["foreground_after"] = "0x123"
	}
	return data
}

func uiActionFixtureResponse(t *testing.T, name string, data map[string]any) string {
	t.Helper()
	encoded, err := json.Marshal(Result{"ok": true, "data": data, "meta": map[string]any{"protocol_version": DeveloperProtocol, "action": name, "read_only": false, "session_id": 1, "user_interactive": true}})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestDeveloperUIActionsRequireExplicitAuthorization(t *testing.T) {
	for _, name := range []string{"ui.act", "window.focus", "window.resize", "input.key", "input.mouse"} {
		for _, dry := range []bool{false, true} {
			t.Run(name+"/dry="+stringValueForTest(dry), func(t *testing.T) {
				f := newFixture(t)
				f.bridge.options.DryRun = dry
				_, err := f.bridge.Developer(context.Background(), "fixture-vm", name, uiActionFixtureParams(name), false)
				checkCode(t, err, "WRITE_NOT_ALLOWED")
				if f.calls != 0 {
					t.Fatal("unauthorized action reached the guest")
				}
			})
		}
	}
}

func TestDeveloperUIActionPreviewRedactsAllTextAndKeyInputs(t *testing.T) {
	for _, name := range []string{"ui.act", "input.key"} {
		p := uiActionFixtureParams(name)
		secret := "SYNTHETIC-SECRET-NOT-FOR-OUTPUT"
		if name == "ui.act" {
			p["operation"] = "set_value"
			p["value"] = secret
		}
		f := newFixture(t)
		f.bridge.options.DryRun = true
		result, err := f.bridge.Developer(context.Background(), "fixture-vm", name, p, true)
		if err != nil {
			t.Fatal(err)
		}
		argv := mapValue(t, result["data"])["argv"].([]string)
		if argv[len(argv)-1] != "-AllowWrite" {
			t.Fatal("write authorization was not forwarded")
		}
		decoded, err := base64.StdEncoding.DecodeString(argv[len(argv)-2])
		if err != nil {
			t.Fatal(err)
		}
		value, err := decodeJSON(string(decoded))
		if err != nil {
			t.Fatal(err)
		}
		wire := mapValue(t, mapValue(t, value)["params"])
		if name == "ui.act" {
			if wire["value"] != "[REDACTED]" || strings.Contains(string(decoded), secret) || p["value"] != secret {
				t.Fatal("set_value preview leaked or changed its input")
			}
		} else if wire["key"] != "[REDACTED]" || wire["input_redacted"] != true || p["key"] != "A" {
			t.Fatal("key preview was not privately redacted")
		}
		if f.calls != 0 {
			t.Fatal("preview executed a UI action")
		}
	}
}

func TestDeveloperUIActionValidationAndResultContract(t *testing.T) {
	for _, name := range []string{"ui.act", "window.focus", "window.resize", "input.key", "input.mouse"} {
		t.Run(name, func(t *testing.T) {
			p := uiActionFixtureParams(name)
			normalized, err := normalizeDeveloperUIAction(name, p)
			if err != nil {
				t.Fatal(err)
			}
			data := uiActionFixtureData(name, normalized)
			f := newFixture(t)
			f.response.stdout = uiActionFixtureResponse(t, name, data)
			result, err := f.bridge.Developer(context.Background(), "fixture-vm", name, p, true)
			if err != nil || result["ok"] != true || f.calls != 1 {
				t.Fatalf("action result=%#v error=%v calls=%d", result, err, f.calls)
			}
			for _, field := range []string{"value", "key", "request"} {
				data[field] = "synthetic-input"
				if validDeveloperUIActionResult(name, data) {
					t.Fatal("a result echoed a prohibited request field")
				}
				delete(data, field)
			}
			data["window"].(map[string]any)["pid"] = 43
			if developerUIActionWithinRequest(name, data, normalized) {
				t.Fatal("result accepted a different window owner")
			}
		})
	}
	for _, test := range []struct {
		name, key string
		value     any
	}{
		{"ui.act", "selector", map[string]any{}}, {"ui.act", "operation", "execute"}, {"ui.act", "value", "not-valid-for-invoke"},
		{"ui.act", "expected_runtime_id", []any{}}, {"ui.act", "expected_runtime_id", []any{true}},
		{"ui.act", "node_id", "n000001"}, {"ui.act", "horizontal", "small_increment"},
		{"window.resize", "width", 0}, {"window.resize", "height", 8193}, {"window.resize", "x", -65536},
		{"window.focus", "force", true}, {"window.focus", "lock_timeout_ms", 10000},
		{"input.key", "key", "Win"}, {"input.key", "key", "text injection"}, {"input.key", "modifiers", []any{"Control", "Control"}},
		{"input.key", "modifiers", []any{"Win"}}, {"input.mouse", "x", nil}, {"input.mouse", "event", "drag"},
		{"input.mouse", "wheel_delta", 120}, {"input.mouse", "button", "fourth"},
	} {
		t.Run(test.name+"/"+test.key+"/"+stringValueForTest(test.value), func(t *testing.T) {
			p := uiActionFixtureParams(test.name)
			p[test.key] = test.value
			f := newFixture(t)
			_, err := f.bridge.Developer(context.Background(), "fixture-vm", test.name, p, true)
			checkCode(t, err, "INVALID_ARGUMENT")
			if f.calls != 0 {
				t.Fatal("invalid action reached transport")
			}
		})
	}
}

func TestDeveloperUIActionSpecificOperationBudgets(t *testing.T) {
	for _, operation := range []string{"invoke", "set_value", "toggle", "select", "expand", "collapse", "scroll", "scroll_into_view", "focus"} {
		p := uiActionFixtureParams("ui.act")
		p["operation"] = operation
		if operation == "set_value" {
			p["value"] = ""
		}
		if operation == "scroll" {
			p["vertical"] = "small_increment"
		}
		p["expected_runtime_id"] = []any{json.Number("1.0"), 2}
		if _, err := normalizeDeveloperUIAction("ui.act", p); err != nil {
			t.Fatalf("operation %s: %v", operation, err)
		}
	}
	p := uiActionFixtureParams("input.mouse")
	p["event"] = "wheel"
	p["wheel_delta"] = -1200
	if _, err := normalizeDeveloperUIAction("input.mouse", p); err != nil {
		t.Fatal(err)
	}
	p["wheel_delta"] = -121
	_, err := normalizeDeveloperUIAction("input.mouse", p)
	checkCode(t, err, "INVALID_ARGUMENT")
	p = uiActionFixtureParams("window.focus")
	p["timeout_ms"] = 1000
	p["lock_timeout_ms"] = 1000
	_, err = normalizeDeveloperUIAction("window.focus", p)
	checkCode(t, err, "INVALID_ARGUMENT")
	p = uiActionFixtureParams("ui.act")
	p["expected_runtime_id"] = []any{1, 3}
	normalized, err := normalizeDeveloperUIAction("ui.act", p)
	if err != nil {
		t.Fatal(err)
	}
	if developerUIActionWithinRequest("ui.act", uiActionFixtureData("ui.act", normalized), normalized) {
		t.Fatal("the response ignored the caller's runtime freshness guard")
	}
}

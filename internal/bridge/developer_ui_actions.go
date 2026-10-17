package bridge

import (
	"strings"
	"time"
)

var uiSemanticOperations = map[string]bool{"invoke": true, "set_value": true, "toggle": true, "select": true, "expand": true, "collapse": true, "scroll": true, "scroll_into_view": true, "focus": true}

func init() {
	for _, name := range []string{"ui.act", "window.focus", "window.resize", "input.key", "input.mouse"} {
		actionName := name
		registerDeveloperAction(DeveloperAction{
			Name: name, Description: "Perform an explicitly authorized UI operation in an identified active Windows session; inspect state after dispatch.", Mutation: true, Interactive: true,
			Normalize: func(params map[string]any) (map[string]any, error) {
				return normalizeDeveloperUIAction(actionName, params)
			},
			ValidateResult: func(data map[string]any) bool { return validDeveloperUIActionResult(actionName, data) },
			ValidateWithinRequest: func(data, params map[string]any) bool {
				return developerUIActionWithinRequest(actionName, data, params)
			},
			RedactPreview: redactDeveloperUIAction,
		})
	}
}

func uiActionEnum(input map[string]any, key, fallback string, allowed ...string) (string, error) {
	value, err := uiQueryString(input, key, fallback, 32)
	if err != nil {
		return "", err
	}
	for _, item := range allowed {
		if value == item {
			return value, nil
		}
	}
	return "", uiQueryArgument("Invalid UI operation field: " + key)
}

func normalizeDeveloperUIAction(name string, input map[string]any) (map[string]any, error) {
	allowed := []string{"hwnd", "pid", "start_time_utc", "timeout_ms", "lock_timeout_ms"}
	switch name {
	case "ui.act":
		allowed = append(allowed, "operation", "selector", "ancestor", "max_depth", "max_scan_nodes", "expected_runtime_id", "value", "horizontal", "vertical")
	case "window.resize":
		allowed = append(allowed, "width", "height", "x", "y")
	case "input.key":
		allowed = append(allowed, "key", "modifiers")
	case "input.mouse":
		allowed = append(allowed, "event", "button", "x", "y", "wheel_delta")
	}
	if err := uiQueryFields(input, allowed...); err != nil {
		return nil, err
	}
	result, err := normalizeDeveloperUIWindow(input)
	if err != nil {
		return nil, err
	}
	for _, bound := range []struct {
		key                string
		fallback, min, max int64
	}{{"timeout_ms", 10000, 1000, 20000}, {"lock_timeout_ms", 0, 0, 5000}} {
		result[bound.key], err = uiQueryInteger(input, bound.key, bound.fallback, bound.min, bound.max)
		if err != nil {
			return nil, err
		}
	}
	if result["lock_timeout_ms"].(int64) >= result["timeout_ms"].(int64) {
		return nil, uiQueryArgument("lock_timeout_ms must be smaller than timeout_ms.")
	}
	switch name {
	case "ui.act":
		operation, err := uiQueryString(input, "operation", "", 32)
		if err != nil || !uiSemanticOperations[operation] {
			return nil, uiQueryArgument("Unsupported semantic UI operation.")
		}
		result["operation"] = operation
		queryInput := map[string]any{"hwnd": result["hwnd"], "pid": result["pid"], "start_time_utc": result["start_time_utc"], "mode": "one"}
		for _, key := range []string{"selector", "ancestor", "max_depth", "max_scan_nodes"} {
			if value, present := input[key]; present {
				queryInput[key] = value
			}
		}
		query, err := normalizeDeveloperUIQuery(queryInput)
		if err != nil {
			return nil, err
		}
		if len(query["selector"].(map[string]any)) == 0 {
			return nil, uiQueryArgument("ui.act requires a nonempty selector.")
		}
		for _, key := range []string{"selector", "ancestor", "max_depth", "max_scan_nodes"} {
			result[key] = query[key]
		}
		if runtime, present := input["expected_runtime_id"]; present {
			values, ok := runtime.([]any)
			if !ok || len(values) < 1 || len(values) > 64 {
				return nil, uiQueryArgument("expected_runtime_id must be an integer array of 1..64 items.")
			}
			copy := make([]any, len(values))
			for i, value := range values {
				n, valid := integral(value)
				if !valid || n < -2147483648 || n > 2147483647 {
					return nil, uiQueryArgument("Invalid expected_runtime_id item.")
				}
				copy[i] = n
			}
			result["expected_runtime_id"] = copy
		}
		value, hasValue := input["value"]
		if operation == "set_value" {
			if !hasValue {
				return nil, uiQueryArgument("set_value requires value.")
			}
			text, err := uiQueryString(map[string]any{"value": value}, "value", "", 4096)
			if err != nil {
				return nil, err
			}
			result["value"] = text
		} else if hasValue {
			return nil, uiQueryArgument("value is only valid for set_value.")
		}
		for _, key := range []string{"horizontal", "vertical"} {
			if operation == "scroll" {
				result[key], err = uiActionEnum(input, key, "none", "none", "small_decrement", "small_increment", "large_decrement", "large_increment")
				if err != nil {
					return nil, err
				}
			} else if _, present := input[key]; present {
				return nil, uiQueryArgument("Scroll amounts are only valid for scroll.")
			}
		}
		if operation == "scroll" && result["horizontal"] == "none" && result["vertical"] == "none" {
			return nil, uiQueryArgument("scroll requires a nonzero direction.")
		}
	case "window.resize":
		for _, key := range []string{"width", "height"} {
			result[key], err = uiQueryInteger(input, key, 0, 64, 8192)
			if err != nil || result[key] == int64(0) {
				return nil, uiQueryArgument("resize requires width and height in [64,8192].")
			}
		}
		for _, key := range []string{"x", "y"} {
			if _, present := input[key]; present {
				result[key], err = uiQueryInteger(input, key, 0, -65535, 65535)
				if err != nil {
					return nil, err
				}
			}
		}
	case "input.key":
		key, err := uiQueryString(input, "key", "", 16)
		if err != nil || !validDeveloperInputKey(key) {
			return nil, uiQueryArgument("key must be a supported named keyboard key.")
		}
		result["key"] = key
		modifiers := []any{}
		if value, present := input["modifiers"]; present {
			var valid bool
			modifiers, valid = value.([]any)
			if !valid || len(modifiers) > 3 {
				return nil, uiQueryArgument("modifiers must contain at most Control, Alt and Shift.")
			}
		}
		copy := make([]any, 0, len(modifiers))
		seen := map[string]bool{}
		for _, value := range modifiers {
			modifier, ok := value.(string)
			if !ok || seen[modifier] || (modifier != "Control" && modifier != "Alt" && modifier != "Shift") {
				return nil, uiQueryArgument("Invalid or repeated keyboard modifier.")
			}
			seen[modifier] = true
			copy = append(copy, modifier)
		}
		result["modifiers"] = copy
	case "input.mouse":
		result["event"], err = uiActionEnum(input, "event", "click", "click", "move", "wheel")
		if err != nil {
			return nil, err
		}
		for _, key := range []string{"x", "y"} {
			if _, present := input[key]; !present {
				return nil, uiQueryArgument("Mouse input requires physical guest-screen x and y.")
			}
			result[key], err = uiQueryInteger(input, key, 0, -65535, 65535)
			if err != nil {
				return nil, err
			}
		}
		if result["event"] == "click" {
			result["button"], err = uiActionEnum(input, "button", "left", "left", "right", "middle")
			if err != nil {
				return nil, err
			}
		} else if _, present := input["button"]; present {
			return nil, uiQueryArgument("button is only valid for click.")
		}
		if result["event"] == "wheel" {
			delta, err := uiQueryInteger(input, "wheel_delta", 0, -1200, 1200)
			if err != nil || delta == 0 || delta%120 != 0 {
				return nil, uiQueryArgument("wheel_delta must be a nonzero multiple of 120 in [-1200,1200].")
			}
			result["wheel_delta"] = delta
		} else if _, present := input["wheel_delta"]; present {
			return nil, uiQueryArgument("wheel_delta is only valid for wheel.")
		}
	}
	return result, nil
}

func validDeveloperInputKey(key string) bool {
	if len(key) == 1 && strings.Contains("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", key) {
		return true
	}
	for _, named := range []string{"Enter", "Tab", "Escape", "Space", "Backspace", "Delete", "Home", "End", "PageUp", "PageDown", "Left", "Right", "Up", "Down", "Insert", "F1", "F2", "F3", "F4", "F5", "F6", "F7", "F8", "F9", "F10", "F11", "F12"} {
		if key == named {
			return true
		}
	}
	return false
}

func redactDeveloperUIAction(params map[string]any) map[string]any {
	if _, present := params["value"]; present {
		params["value"] = "[REDACTED]"
	}
	if _, present := params["key"]; present {
		params["key"] = "[REDACTED]"
		params["modifiers"] = []any{}
		params["input_redacted"] = true
	}
	return params
}

func validDeveloperUIActionResult(name string, data map[string]any) bool {
	allowed := []string{"action", "window", "timestamp", "coordination", "outcome"}
	switch name {
	case "ui.act":
		allowed = append(allowed, "operation", "element")
	case "window.resize":
		allowed = append(allowed, "rect", "coordinate_space")
	case "input.key", "input.mouse":
		allowed = append(allowed, "input_count", "foreground_after")
	}
	if uiQueryFields(data, allowed...) != nil {
		return false
	}
	window, ok := asObject(data["window"])
	if !ok || window == nil {
		return false
	}
	if _, err := normalizeDeveloperUIWindow(window); err != nil {
		return false
	}
	if uiQueryFields(window, "hwnd", "pid", "start_time_utc") != nil {
		return false
	}
	stamp, typed := data["timestamp"].(string)
	_, stampErr := time.Parse(time.RFC3339Nano, stamp)
	if data["action"] != name || !typed || stampErr != nil || data["coordination"] != "windows-session-mutex" ||
		(data["outcome"] != "dispatched" && data["outcome"] != "already_satisfied") {
		return false
	}
	if name != "window.focus" && data["outcome"] != "dispatched" {
		return false
	}
	// Results and logs never echo inputs, including ordinary (non-password) text.
	for _, key := range []string{"value", "key", "modifiers", "text", "request"} {
		if _, present := data[key]; present {
			return false
		}
	}
	if name == "ui.act" {
		operation, _ := data["operation"].(string)
		element, ok := asObject(data["element"])
		if !uiSemanticOperations[operation] || !ok || !nonemptyText(element["control_type"]) || !boolValue(element["is_password"]) {
			return false
		}
		runtime, ok := element["runtime_id"].([]any)
		if !ok || len(runtime) < 1 || len(runtime) > 64 {
			return false
		}
		for _, item := range runtime {
			if !responseInteger(item, -2147483648, 2147483647) {
				return false
			}
		}
		for key := range element {
			if key != "runtime_id" && key != "control_type" && key != "is_password" {
				return false
			}
		}
	}
	if name == "window.resize" {
		rect, ok := asObject(data["rect"])
		if !ok || uiQueryFields(rect, "x", "y", "width", "height") != nil || !responseRect(rect, false) || data["coordinate_space"] != "guest-screen-physical-pixels" ||
			!responseInteger(rect["width"], 1, 2147483647) || !responseInteger(rect["height"], 1, 2147483647) {
			return false
		}
	}
	if name == "input.key" || name == "input.mouse" {
		if !responseInteger(data["input_count"], 1, 12) || !textValue(data["foreground_after"]) {
			return false
		}
	}
	return true
}

func developerUIActionWithinRequest(name string, data, params map[string]any) bool {
	if !validDeveloperUIActionResult(name, data) {
		return false
	}
	window, _ := asObject(data["window"])
	if window["hwnd"] != params["hwnd"] || window["start_time_utc"] != params["start_time_utc"] {
		return false
	}
	pid, _ := integral(window["pid"])
	expected, _ := integral(params["pid"])
	if pid != expected {
		return false
	}
	if name == "ui.act" {
		if expectedRuntime, present := params["expected_runtime_id"]; present {
			element, _ := asObject(data["element"])
			actual := element["runtime_id"].([]any)
			expected := expectedRuntime.([]any)
			if len(actual) != len(expected) {
				return false
			}
			for i, value := range actual {
				a, validA := integral(value)
				b, validB := integral(expected[i])
				if !validA || !validB || a != b {
					return false
				}
			}
		}
	}
	return name != "ui.act" || data["operation"] == params["operation"]
}

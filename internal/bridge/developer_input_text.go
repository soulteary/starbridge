package bridge

import (
	"errors"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

const maxDeveloperTextCodePoints = 2048
const maxDeveloperTextEvents = 8192

func init() {
	registerDeveloperAction(DeveloperAction{
		Name: "input.text", Description: "Insert literal Unicode text into an identified foreground Windows window; requires explicit write authorization and postcondition inspection.",
		Mutation: true, Interactive: true, Normalize: normalizeDeveloperInputText,
		ValidateResult:        func(data map[string]any) bool { return validDeveloperUIActionResult("input.text", data) },
		ValidateWithinRequest: developerInputTextWithinRequest,
		RedactPreview: func(params map[string]any) map[string]any {
			params["text"] = "[REDACTED]"
			params["input_redacted"] = true
			return params
		},
	})
}

func normalizeDeveloperInputText(input map[string]any) (map[string]any, error) {
	if err := uiQueryFields(input, "hwnd", "pid", "start_time_utc", "timeout_ms", "lock_timeout_ms", "text"); err != nil {
		// Unknown field names can themselves contain private text.
		return nil, uiQueryArgument("Unsupported input.text field.")
	}
	text, ok := input["text"].(string)
	if !ok || !utf8.ValidString(text) || len(text) == 0 || strings.ContainsRune(text, 0) || utf8.RuneCountInString(text) > maxDeveloperTextCodePoints {
		return nil, uiQueryArgument("text must contain 1..2048 valid Unicode code points without NUL.")
	}
	base := make(map[string]any, len(input)-1)
	for key, value := range input {
		if key != "text" {
			base[key] = value
		}
	}
	result, err := normalizeDeveloperUIAction("window.focus", base)
	if err != nil {
		return nil, err
	}
	result["text"] = text
	return result, nil
}

func developerInputTextWithinRequest(data, params map[string]any) bool {
	if !developerUIActionWithinRequest("input.text", data, params) {
		return false
	}
	text, ok := params["text"].(string)
	if !ok {
		return false
	}
	units := len(utf16.Encode([]rune(text)))
	count, _ := integral(data["input_count"])
	points, _ := integral(data["code_point_count"])
	actualUnits, _ := integral(data["utf16_unit_count"])
	return count == int64(units*2) && points == int64(utf8.RuneCountInString(text)) && actualUnits == int64(units)
}

// Text must never appear in transport diagnostics or provider exceptions. Only
// fixed error identifiers and the mutation outcome cross this boundary.
func privateDeveloperInputTextError(err error) error {
	code := "INPUT_FAILED"
	var failure *Error
	if errors.As(err, &failure) {
		for _, known := range []string{"PRLCTL_NOT_FOUND", "EXECUTION_FAILED", "TIMEOUT", "INTERRUPTED", "OUTPUT_LIMIT_EXCEEDED", "PRLCTL_FAILED", "INVALID_JSON", "INVALID_GUEST_RESPONSE", "GUEST_EXIT_MISMATCH"} {
			if failure.Code == known {
				code = known
			}
		}
	}
	return NewError(code, "Text input could not be confirmed; inspect application state before another write.", map[string]any{"outcome": "unknown"})
}

func privateDeveloperInputTextResult(response map[string]any) Result {
	meta, _ := asObject(response["meta"])
	safeMeta := map[string]any{}
	for _, key := range []string{"protocol_version", "action", "read_only", "session_id", "user_interactive"} {
		safeMeta[key] = meta[key]
	}
	if timestamp, ok := meta["timestamp"].(string); ok {
		if _, err := time.Parse(time.RFC3339Nano, timestamp); err == nil {
			safeMeta["timestamp"] = timestamp
		}
	}
	if responseInteger(meta["duration_ms"], 0, 2147483647) {
		safeMeta["duration_ms"] = meta["duration_ms"]
	}
	if response["ok"] == true {
		return Result{"ok": true, "data": response["data"], "meta": safeMeta}
	}
	failure, _ := asObject(response["error"])
	code := "INPUT_FAILED"
	for _, known := range []string{"INVALID_ARGUMENT", "WRITE_NOT_ALLOWED", "INTERACTIVE_SESSION_REQUIRED", "WINDOW_IDENTITY_MISMATCH", "INPUT_DESKTOP_UNAVAILABLE", "WINDOW_NOT_INTERACTABLE", "WINDOW_MINIMIZED", "WINDOW_NOT_FOUND", "TARGET_NOT_FOREGROUND", "INPUT_BUSY", "SESSION_BUSY", "SESSION_LOCK_UNAVAILABLE", "SESSION_RECOVERY_REQUIRED", "UIA_WORKER_UNAVAILABLE", "UIA_WORKER_FAILED", "ACTION_TIMEOUT", "UI_OPERATION_FAILED", "INTERNAL_ERROR", "OUTPUT_LIMIT_EXCEEDED", "INPUT_FAILED"} {
		if failure["code"] == known {
			code = known
		}
	}
	outcome := "unknown"
	if details, ok := asObject(failure["details"]); ok && details["outcome"] == "not_started" {
		outcome = "not_started"
	}
	return Result{"ok": false, "meta": safeMeta, "error": map[string]any{
		"code": code, "message": "Text input did not complete; inspect the reported outcome and application state before another write.",
		"details": map[string]any{"outcome": outcome},
	}}
}

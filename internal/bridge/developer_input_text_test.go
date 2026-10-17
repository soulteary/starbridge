package bridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

func inputTextFixtureParams(text string) map[string]any {
	params := uiFixtureParams()
	params["text"] = text
	return params
}

func inputTextFixtureData(params map[string]any) map[string]any {
	data := uiActionFixtureData("input.text", params)
	text := params["text"].(string)
	units := len(utf16.Encode([]rune(text)))
	data["input_count"], data["utf16_unit_count"], data["code_point_count"] = units*2, units, utf8.RuneCountInString(text)
	data["newline_policy"], data["foreground_after"] = "literal", "0x123"
	return data
}

func TestInputTextAuthorizationAndPrivatePreview(t *testing.T) {
	secret := "SYNTHETIC-PRIVATE-中文😀\r\n\t"
	for _, dry := range []bool{false, true} {
		f := newFixture(t)
		f.bridge.options.DryRun = dry
		_, err := f.bridge.Developer(context.Background(), "fixture-vm", "input.text", inputTextFixtureParams(secret), false)
		checkCode(t, err, "WRITE_NOT_ALLOWED")
		if f.calls != 0 {
			t.Fatal("unauthorized input reached transport")
		}
	}
	f := newFixture(t)
	f.bridge.options.DryRun = true
	params := inputTextFixtureParams(secret)
	result, err := f.bridge.Developer(context.Background(), "fixture-vm", "input.text", params, true)
	if err != nil {
		t.Fatal(err)
	}
	argv := mapValue(t, result["data"])["argv"].([]string)
	if argv[len(argv)-1] != "-AllowWrite" {
		t.Fatal("write authorization missing")
	}
	decoded, err := base64.StdEncoding.DecodeString(argv[len(argv)-2])
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if err := json.Unmarshal(decoded, &request); err != nil {
		t.Fatal(err)
	}
	p := request["params"].(map[string]any)
	if p["text"] != "[REDACTED]" || p["input_redacted"] != true || params["text"] != secret || f.calls != 0 {
		t.Fatal("preview leaked/modified text or executed transport")
	}
}

func TestInputTextBudgetsAndExactLiteralWire(t *testing.T) {
	for _, text := range []string{"中文😀\r\n\r\n\t", "�", strings.Repeat("😀", 2048), strings.Repeat("\n", 2048)} {
		params, err := normalizeDeveloperInputText(inputTextFixtureParams(text))
		if err != nil || params["text"] != text {
			t.Fatalf("literal input changed or rejected: %v", err)
		}
		f := newFixture(t)
		f.response.stdout = uiActionFixtureResponse(t, "input.text", inputTextFixtureData(params))
		result, err := f.bridge.Developer(context.Background(), "fixture-vm", "input.text", inputTextFixtureParams(text), true)
		if err != nil || result["ok"] != true || f.calls != 1 {
			t.Fatalf("input result=%#v err=%v calls=%d", result, err, f.calls)
		}
		encoded := f.argv[len(f.argv)-2]
		wire, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		var request map[string]any
		if err := json.Unmarshal(wire, &request); err != nil || request["params"].(map[string]any)["text"] != text {
			t.Fatal("literal Unicode or newline text changed on wire")
		}
	}
	for _, value := range []any{nil, true, 42, "", "a\x00b", string([]byte{0xff}), strings.Repeat("a", 2049), strings.Repeat("😀", 2049)} {
		f := newFixture(t)
		params := inputTextFixtureParams("valid")
		params["text"] = value
		_, err := f.bridge.Developer(context.Background(), "fixture-vm", "input.text", params, true)
		checkCode(t, err, "INVALID_ARGUMENT")
		if f.calls != 0 {
			t.Fatal("invalid text executed transport")
		}
	}
	// Escaping in JSON counts against the common 16000-byte wire budget even
	// when the code-point/event budget alone permits these control characters.
	for _, text := range []string{strings.Repeat("\x01", 2048), strings.Repeat("😀", 2048)} {
		f := newFixture(t)
		f.bridge.options.DryRun = true
		if _, err := f.bridge.Developer(context.Background(), "fixture-vm", "input.text", inputTextFixtureParams(text), true); err != nil {
			t.Fatal(err)
		}
	}
	params := inputTextFixtureParams("safe")
	params["SYNTHETIC-PRIVATE-FIELD"] = "bad"
	_, err := normalizeDeveloperInputText(params)
	if err == nil || strings.Contains(err.Error(), "SYNTHETIC-PRIVATE") {
		t.Fatal("unknown field was echoed")
	}
	params = inputTextFixtureParams("safe")
	params["timeout_ms"], params["lock_timeout_ms"] = 1000, 1000
	f := newFixture(t)
	_, err = f.bridge.Developer(context.Background(), "fixture-vm", "input.text", params, true)
	checkCode(t, err, "INVALID_ARGUMENT")
	if f.calls != 0 {
		t.Fatal("invalid worker/lease budget reached transport")
	}
}

func TestInputTextResultBudgetIdentityAndPrivacy(t *testing.T) {
	params, _ := normalizeDeveloperInputText(inputTextFixtureParams("a😀\r\n"))
	for _, bad := range []struct {
		key   string
		value any
	}{
		{"input_count", 7}, {"utf16_unit_count", 3}, {"code_point_count", 2}, {"newline_policy", "normalize"},
		{"foreground_after", "private-value"}, {"text", "SYNTHETIC-PRIVATE"}, {"request", map[string]any{}},
	} {
		f := newFixture(t)
		data := inputTextFixtureData(params)
		data[bad.key] = bad.value
		f.response.stdout = uiActionFixtureResponse(t, "input.text", data)
		_, err := f.bridge.Developer(context.Background(), "fixture-vm", "input.text", params, true)
		if failure := checkCode(t, err, "INVALID_GUEST_RESPONSE"); failure.Details["outcome"] != "unknown" {
			t.Fatal("malformed success lost unknown mutation outcome")
		}
	}
	data := inputTextFixtureData(params)
	data["window"].(map[string]any)["pid"] = 99
	if developerInputTextWithinRequest(data, params) {
		t.Fatal("different process identity accepted")
	}
}

func TestInputTextFailuresNeverEchoTransportOrProviderText(t *testing.T) {
	secret := "SYNTHETIC-PRIVATE-INPUT"
	for _, mode := range []string{"exit", "malformed", "spawn", "timeout", "canceled", "limit", "guest"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.response.stdout, f.response.stderr = secret, base64.StdEncoding.EncodeToString([]byte(secret))
			switch mode {
			case "exit":
				f.response.exitCode = 2
			case "spawn":
				f.err = errors.New(secret)
			case "timeout":
				f.err = context.DeadlineExceeded
			case "canceled":
				f.err = context.Canceled
			case "limit":
				f.response.outputExceeded = true
			case "guest":
				wire, _ := json.Marshal(map[string]any{"ok": false, "extra": secret, "error": map[string]any{"code": secret, "message": secret, "details": map[string]any{"outcome": "not_started", "private": secret}}, "meta": map[string]any{
					"protocol_version": DeveloperProtocol, "action": "input.text", "read_only": false, "session_id": 1, "user_interactive": true, "extra": secret, "timestamp": secret}})
				f.response.stdout = string(wire)
			}
			result, err := f.bridge.Developer(context.Background(), "fixture-vm", "input.text", inputTextFixtureParams(secret), true)
			if err != nil {
				result = Failure(err)
			}
			wire, _ := json.Marshal(result)
			if strings.Contains(string(wire), secret) || strings.Contains(string(wire), base64.StdEncoding.EncodeToString([]byte(secret))) || f.calls != 1 {
				t.Fatalf("failure leaked input or retried: %s calls=%d", wire, f.calls)
			}
			var failure map[string]any
			if typed, ok := result["error"].(*Error); ok {
				failure = map[string]any{"details": typed.Details}
			} else {
				failure = mapValue(t, result["error"])
			}
			outcome := "unknown"
			if mode == "guest" {
				outcome = "not_started"
			}
			if mapValue(t, failure["details"])["outcome"] != outcome {
				t.Fatalf("unsafe outcome: %s", wire)
			}
		})
	}
}

func TestInputTextSuccessfulEnvelopeDropsUnvalidatedMetadata(t *testing.T) {
	params, _ := normalizeDeveloperInputText(inputTextFixtureParams("SYNTHETIC-PRIVATE-INPUT"))
	var response map[string]any
	if err := json.Unmarshal([]byte(uiActionFixtureResponse(t, "input.text", inputTextFixtureData(params))), &response); err != nil {
		t.Fatal(err)
	}
	response["request"] = params
	meta := response["meta"].(map[string]any)
	meta["raw_input"] = params["text"]
	meta["timestamp"], meta["duration_ms"] = "2026-01-02T03:05:00Z", 5
	wire, _ := json.Marshal(response)
	f := newFixture(t)
	f.response.stdout = string(wire)
	result, err := f.bridge.Developer(context.Background(), "fixture-vm", "input.text", params, true)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "SYNTHETIC-PRIVATE") || mapValue(t, result["meta"])["timestamp"] != meta["timestamp"] {
		t.Fatal("success response leaked unvalidated envelope fields or lost timing")
	}
}

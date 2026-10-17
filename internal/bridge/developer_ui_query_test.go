package bridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const uiFixtureStart = "2026-01-02T03:04:05.1234567Z"

func uiFixtureParams() map[string]any {
	return map[string]any{"hwnd": "0X000123", "pid": 42, "start_time_utc": uiFixtureStart}
}

func uiFixtureItem() map[string]any {
	return map[string]any{
		"node_id": "n000002", "parent_id": "n000001", "runtime_id": []any{1, 2}, "depth": 1, "pid": 42,
		"name": "Fixture input", "control_type": "Edit", "automation_id": "FixtureInput", "class_name": "Edit", "framework_id": "Win32",
		"rect": map[string]any{"x": -20, "y": 40, "width": 100, "height": 30}, "is_enabled": true, "is_offscreen": false, "is_password": false,
		"patterns": []any{"Value", "Text"}, "value": nil, "value_status": "not_requested", "value_truncated": false,
		"state": map[string]any{"toggle": nil, "is_selected": nil, "expand_collapse": nil, "value_read_only": false, "range_value": nil, "range_minimum": nil, "range_maximum": nil},
	}
}

func uiFixtureData(params map[string]any) map[string]any {
	return map[string]any{
		"window":      map[string]any{"hwnd": params["hwnd"], "pid": params["pid"], "start_time_utc": params["start_time_utc"]},
		"snapshot_id": "fixture-snapshot", "timestamp": "2026-01-02T03:05:00Z", "coordinate_space": "guest-screen-physical-pixels", "pagination": "live",
		"mode": params["mode"], "offset": params["offset"], "limit": params["limit"], "max_depth": params["max_depth"], "max_scan_nodes": params["max_scan_nodes"], "include_value": params["include_value"],
		"scanned_nodes": 2, "matched_count": 1, "returned_count": 1, "items": []any{uiFixtureItem()}, "has_more": false,
		"complete": true, "node_limit_reached": false, "depth_limit_reached": false, "warnings": []any{},
	}
}

func uiFixtureResponse(t *testing.T, data map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(Result{"ok": true, "data": data, "meta": map[string]any{
		"protocol_version": DeveloperProtocol, "action": "ui.query", "session_id": 1, "user_interactive": true, "read_only": true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

func TestDeveloperUIQueryNormalizesIdentityAndPrivateSelectors(t *testing.T) {
	params := uiFixtureParams()
	params["pid"] = json.Number("4.2e1")
	params["start_time_utc"] = "2026-01-02T04:04:05.1234567+01:00"
	params["selector"] = map[string]any{"name_contains": "Fixture", "control_type": "Edit"}
	params["ancestor"] = map[string]any{"automation_id": "FixturePanel"}
	before, _ := json.Marshal(params)
	got, err := normalizeDeveloperUIQuery(params)
	if err != nil {
		t.Fatal(err)
	}
	if got["hwnd"] != "0x123" || got["pid"] != int64(42) || got["start_time_utc"] != uiFixtureStart ||
		got["limit"] != int64(50) || got["max_scan_nodes"] != int64(2000) || got["include_value"] != false {
		t.Fatal(got)
	}
	got["selector"].(map[string]any)["name_contains"] = "changed"
	got["ancestor"].(map[string]any)["automation_id"] = "changed"
	after, _ := json.Marshal(params)
	if string(before) != string(after) {
		t.Fatal("normalization changed the caller's input")
	}
	params = uiFixtureParams()
	params["mode"] = "one"
	one, err := normalizeDeveloperUIQuery(params)
	if err != nil || one["limit"] != int64(1) || one["offset"] != int64(0) {
		t.Fatalf("%#v %v", one, err)
	}
}

func TestDeveloperUIQueryRejectsUnsafeArgumentsBeforeExecution(t *testing.T) {
	cases := []struct {
		key   string
		value any
	}{
		{"unknown", true}, {"hwnd", "-1"}, {"hwnd", "0"}, {"hwnd", "0x8000000000000000"}, {"hwnd", 123},
		{"pid", nil}, {"pid", 0}, {"pid", true}, {"pid", json.Number("42.1")},
		{"start_time_utc", ""}, {"start_time_utc", "2026-01-02"}, {"start_time_utc", "2026-01-02T03:04:05.123456789Z"},
		{"selector", nil}, {"selector", []any{}}, {"selector", map[string]any{"invoke": true}},
		{"selector", map[string]any{"name": strings.Repeat("x", 257)}}, {"selector", map[string]any{"name": "bad\x00text"}},
		{"ancestor", map[string]any{"automation_id": true}}, {"mode", "first"},
		{"offset", -1}, {"offset", 5000}, {"limit", 201}, {"max_depth", 13}, {"max_scan_nodes", 5001}, {"timeout_ms", 20001}, {"include_value", "true"},
	}
	for _, test := range cases {
		t.Run(test.key+"/"+stringValueForTest(test.value), func(t *testing.T) {
			params := uiFixtureParams()
			params[test.key] = test.value
			f := newFixture(t)
			_, err := f.bridge.Developer(context.Background(), "fixture-vm", "ui.query", params, false)
			checkCode(t, err, "INVALID_ARGUMENT")
			if f.calls != 0 {
				t.Fatal("invalid request reached transport")
			}
		})
	}
	for _, key := range []string{"hwnd", "pid", "start_time_utc"} {
		params := uiFixtureParams()
		delete(params, key)
		_, err := normalizeDeveloperUIQuery(params)
		checkCode(t, err, "INVALID_ARGUMENT")
	}
	for _, override := range []map[string]any{{"offset": 1}, {"limit": 2}} {
		params := uiFixtureParams()
		params["mode"] = "one"
		for key, value := range override {
			params[key] = value
		}
		_, err := normalizeDeveloperUIQuery(params)
		checkCode(t, err, "INVALID_ARGUMENT")
	}
}

func stringValueForTest(value any) string { data, _ := json.Marshal(value); return string(data) }

func TestDeveloperUIQueryDryRunEncodesOnlyQueryWithoutWriteAuthorization(t *testing.T) {
	f := newFixture(t)
	f.bridge.options.DryRun = true
	params := uiFixtureParams()
	params["selector"] = map[string]any{"name": "Fixture ; $(never)"}
	result, err := f.bridge.Developer(context.Background(), "fixture-vm", "ui.query", params, false)
	if err != nil {
		t.Fatal(err)
	}
	args := mapValue(t, result["data"])["argv"].([]string)
	if f.calls != 0 || args[len(args)-2] != "-RequestBase64" {
		t.Fatal(args)
	}
	for _, arg := range args {
		if arg == "-AllowWrite" {
			t.Fatal("query requested write authorization")
		}
	}
	decoded, err := base64.StdEncoding.DecodeString(args[len(args)-1])
	if err != nil {
		t.Fatal(err)
	}
	value, err := decodeJSON(string(decoded))
	if err != nil {
		t.Fatal(err)
	}
	request := mapValue(t, value)
	if request["action"] != "ui.query" || !reflect.DeepEqual(mapValue(t, mapValue(t, request["params"])["selector"]), map[string]any{"name": "Fixture ; $(never)"}) {
		t.Fatal(request)
	}
}

func TestDeveloperUIQueryAcceptsValuesAndRejectsForgedOrLeakingResults(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(map[string]any)
		valid  bool
	}{
		{"plain snapshot", func(map[string]any) {}, true},
		{"value read", func(data map[string]any) {
			item := data["items"].([]any)[0].(map[string]any)
			item["value"] = "fixture-value"
			item["value_status"] = "available"
		}, true},
		{"password skipped", func(data map[string]any) {
			item := data["items"].([]any)[0].(map[string]any)
			item["is_password"] = true
			item["value_status"] = "password_skipped"
			item["state"].(map[string]any)["value_read_only"] = nil
		}, true},
		{"password value leak", func(data map[string]any) {
			item := data["items"].([]any)[0].(map[string]any)
			item["is_password"] = true
			item["value"] = "must-not-leak"
			item["value_status"] = "available"
		}, false},
		{"password numeric state leak", func(data map[string]any) {
			item := data["items"].([]any)[0].(map[string]any)
			item["is_password"] = true
			item["value_status"] = "password_skipped"
			item["state"].(map[string]any)["range_value"] = 12
		}, false},
		{"missing nullable field", func(data map[string]any) { delete(data["items"].([]any)[0].(map[string]any), "value") }, false},
		{"wrong window", func(data map[string]any) { data["window"].(map[string]any)["pid"] = 43 }, false},
		{"larger request budget", func(data map[string]any) { data["max_scan_nodes"] = 5000 }, false},
		{"forged count", func(data map[string]any) { data["returned_count"] = 0 }, false},
		{"invalid item", func(data map[string]any) { data["items"] = []any{"invalid"} }, false},
		{"oversized value", func(data map[string]any) {
			item := data["items"].([]any)[0].(map[string]any)
			item["value"] = strings.Repeat("x", 2049)
			item["value_status"] = "available"
		}, false},
		{"depth exceeds query", func(data map[string]any) { data["items"].([]any)[0].(map[string]any)["depth"] = 7 }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			params := uiFixtureParams()
			params["include_value"] = true
			normalized, err := normalizeDeveloperUIQuery(params)
			if err != nil {
				t.Fatal(err)
			}
			data := uiFixtureData(normalized)
			test.change(data)
			f := newFixture(t)
			f.response.stdout = uiFixtureResponse(t, data)
			result, err := f.bridge.Developer(context.Background(), "fixture-vm", "ui.query", params, false)
			if test.valid {
				if err != nil || result["ok"] != true {
					t.Fatalf("%#v %v", result, err)
				}
			} else {
				checkCode(t, err, "INVALID_GUEST_RESPONSE")
			}
			if f.calls != 1 {
				t.Fatal("query was retried")
			}
		})
	}
}

func TestDeveloperUIQueryPaginationAndUniqueResultContract(t *testing.T) {
	params, err := normalizeDeveloperUIQuery(uiFixtureParams())
	if err != nil {
		t.Fatal(err)
	}
	data := uiFixtureData(params)
	data["offset"], data["returned_count"], data["items"] = 2, 0, []any{}
	if !validDeveloperUIQueryResult(data) {
		t.Fatal("an offset after the final match must return an empty page")
	}
	data["has_more"] = true
	if validDeveloperUIQueryResult(data) {
		t.Fatal("empty final page claimed more matches")
	}
	params["mode"], params["limit"] = "one", int64(1)
	data = uiFixtureData(params)
	if !validDeveloperUIQueryResult(data) {
		t.Fatal("valid unique snapshot rejected")
	}
	data["complete"], data["node_limit_reached"] = false, true
	if validDeveloperUIQueryResult(data) {
		t.Fatal("incomplete scope reported a unique result")
	}
	f := newFixture(t)
	f.response.exitCode = 1
	f.response.stdout = `{"ok":false,"error":{"code":"AMBIGUOUS_MATCH","message":"More than one fixture control matched."},"meta":{"protocol_version":"dev-1","action":"ui.query","read_only":true,"session_id":1,"user_interactive":true}}`
	request := uiFixtureParams()
	request["mode"] = "one"
	result, err := f.bridge.Developer(context.Background(), "fixture-vm", "ui.query", request, false)
	if err != nil || result["ok"] != false || mapValue(t, result["error"])["code"] != "AMBIGUOUS_MATCH" {
		t.Fatalf("%#v %v", result, err)
	}
}

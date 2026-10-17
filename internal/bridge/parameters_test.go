package bridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

type parameterFixtures struct {
	Defaults []struct {
		Action           string
		Input, Canonical map[string]any
	} `json:"defaults"`
	Integers []struct {
		Name, Input string
		Valid       bool
		Canonical   int64
	} `json:"public_integer_cases"`
	Text []struct {
		Name, Unit string
		Repeat     int
		Valid      bool
	} `json:"text_cases"`
	HWND []struct {
		Name, Canonical string
		Input           any
		Valid           bool
	} `json:"hwnd_cases"`
}

func TestSharedGuestParameterContract(t *testing.T) {
	data, err := os.ReadFile("../../tests/fixtures/parameters.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures parameterFixtures
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&fixtures); err != nil {
		t.Fatal(err)
	}
	for _, test := range fixtures.Defaults {
		t.Run("defaults/"+test.Action, func(t *testing.T) {
			before, _ := json.Marshal(test.Input)
			f := newFixture(t)
			f.bridge.options.DryRun = true
			result, err := f.bridge.Guest(context.Background(), "vm", test.Action, test.Input)
			if err != nil {
				t.Fatal(err)
			}
			argv := mapValue(t, result["data"])["argv"].([]string)
			decoded, err := base64.StdEncoding.DecodeString(argv[len(argv)-1])
			if err != nil {
				t.Fatal(err)
			}
			request, err := decodeJSON(string(decoded))
			if err != nil {
				t.Fatal(err)
			}
			actual := mapValue(t, mapValue(t, request)["params"])
			if !reflect.DeepEqual(actual, test.Canonical) {
				t.Fatalf("params=%#v, want %#v", actual, test.Canonical)
			}
			after, _ := json.Marshal(test.Input)
			if string(before) != string(after) {
				t.Fatal("caller input was mutated")
			}
		})
	}
	for _, test := range fixtures.Integers {
		t.Run("integer/"+test.Name, func(t *testing.T) {
			value, err := decodeJSON(test.Input)
			if err != nil {
				t.Fatal(err)
			}
			params, err := normalizeGuestParameters("processes.list", map[string]any{"limit": value})
			if (err == nil) != test.Valid {
				t.Fatalf("valid=%t, error=%v", test.Valid, err)
			}
			if test.Valid && params["limit"] != test.Canonical {
				t.Fatalf("not canonical: %#v", params)
			}
		})
	}
	for _, test := range fixtures.Text {
		t.Run("text/"+test.Name, func(t *testing.T) {
			_, err := normalizeGuestParameters("processes.list", map[string]any{"name": strings.Repeat(test.Unit, test.Repeat)})
			if (err == nil) != test.Valid {
				t.Fatalf("valid=%t, error=%v", test.Valid, err)
			}
		})
	}
	for _, test := range fixtures.HWND {
		t.Run("hwnd/"+test.Name, func(t *testing.T) {
			params := map[string]any{"hwnd": test.Input, "max_depth": 0, "max_nodes": 1}
			before, _ := json.Marshal(params)
			f := newFixture(t)
			f.bridge.options.DryRun = true
			result, err := f.bridge.Guest(context.Background(), "vm", "ui.tree", params)
			if (err == nil) != test.Valid {
				t.Fatalf("valid=%t, error=%v", test.Valid, err)
			}
			if test.Valid {
				argv := mapValue(t, result["data"])["argv"].([]string)
				decoded, err := base64.StdEncoding.DecodeString(argv[len(argv)-1])
				if err != nil {
					t.Fatal(err)
				}
				request, err := decodeJSON(string(decoded))
				if err != nil {
					t.Fatal(err)
				}
				actual := mapValue(t, mapValue(t, request)["params"])
				if actual["hwnd"] != test.Canonical || len(test.Canonical) > 18 {
					t.Fatalf("transport params=%#v, want hwnd=%q within helper width", actual, test.Canonical)
				}
				if actual["max_depth"] != json.Number("0") || actual["max_nodes"] != json.Number("1") || actual["timeout_ms"] != json.Number("10000") {
					t.Fatalf("HWND normalization changed request budgets: %#v", actual)
				}
			} else {
				checkCode(t, err, "INVALID_ARGUMENT")
			}
			after, _ := json.Marshal(params)
			if string(before) != string(after) {
				t.Fatal("caller input was mutated")
			}
			if f.calls != 0 {
				t.Fatal("dry run or invalid request reached transport")
			}
		})
	}
}

func TestGuestCanonicalHWNDKeepsResponseBudgets(t *testing.T) {
	for _, inflated := range []bool{false, true} {
		t.Run(map[bool]string{false: "accepted", true: "inflated response rejected"}[inflated], func(t *testing.T) {
			f := newFixture(t)
			data := boundedGuestData("ui.tree", 1, 0)
			data["hwnd"], data["max_depth"], data["max_nodes"] = "0x1", 0, 1
			if inflated {
				data["max_depth"] = 1
			}
			payload, err := json.Marshal(Result{"ok": true, "data": data, "meta": map[string]any{
				"protocol_version": "1", "action": "ui.tree", "session_id": 1, "user_interactive": true}})
			if err != nil {
				t.Fatal(err)
			}
			f.response.stdout = string(payload)
			result, err := f.bridge.Guest(context.Background(), "vm", "ui.tree", map[string]any{
				"hwnd": "000000000000000000001", "max_depth": 0, "max_nodes": 1})
			if inflated {
				checkCode(t, err, "INVALID_GUEST_RESPONSE")
			} else if err != nil || result["ok"] != true {
				t.Fatalf("result=%#v error=%v", result, err)
			}
			if f.calls != 1 {
				t.Fatalf("helper calls=%d, want one", f.calls)
			}
		})
	}
}

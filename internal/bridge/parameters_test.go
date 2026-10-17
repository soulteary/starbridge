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
}

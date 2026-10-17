package mcpserver

import (
	"encoding/json"
	"os"
	"testing"
)

func TestSharedMCPIntegerContract(t *testing.T) {
	data, err := os.ReadFile("../../tests/fixtures/parameters.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Cases []struct {
			Name, Input string
			Valid       bool
			Canonical   int64
		} `json:"public_integer_cases"`
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, test := range fixtures.Cases {
		t.Run(test.Name, func(t *testing.T) {
			args, err := decodeArguments(json.RawMessage(`{"limit":`+test.Input+`}`), map[string]argument{"limit": {kind: "integer", min: 1, max: 1000}})
			if (err == nil) != test.Valid {
				t.Fatalf("valid=%t, error=%v", test.Valid, err)
			}
			if test.Valid && args["limit"] != test.Canonical {
				t.Fatalf("not canonical: %#v", args)
			}
		})
	}
}

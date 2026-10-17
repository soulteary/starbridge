package contract

import "testing"

func TestJSONUnicodePreservesScalarsAndEscapes(t *testing.T) {
	for _, test := range []struct {
		name, raw string
		valid     bool
	}{
		{"literal multilingual", `{"text":"中文😀�"}`, true},
		{"valid pair", `{"text":"\ud83d\ude00"}`, true},
		{"escaped replacement", `{"text":"\ufffd"}`, true},
		{"escaped backslash", `{"text":"\\ud800"}`, true},
		{"escaped quote", `{"text":"\"\\udc00"}`, true},
		{"escaped backslash before actual pair", `{"text":"\\\uD83D\uDE00"}`, true},
		{"high", `{"text":"\ud800"}`, false},
		{"low", `{"text":"\udfff"}`, false},
		{"high then plain", `{"text":"\ud800x"}`, false},
		{"high then non-low", `{"text":"\ud800\u0061"}`, false},
		{"high then escaped backslash", `{"text":"\ud800\\udc00"}`, false},
		{"unpaired property", `{"\ud800":"value"}`, false},
		{"invalid UTF8", "{\"text\":\"\xff\"}", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := ValidJSONUnicode([]byte(test.raw)); got != test.valid {
				t.Fatalf("valid=%t want=%t", got, test.valid)
			}
		})
	}
}

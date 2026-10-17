package contract

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestExactIntegerBoundaries(t *testing.T) {
	for _, test := range []struct {
		input string
		want  int64
		valid bool
	}{
		{"9223372036854775807", math.MaxInt64, true}, {"-9223372036854775808", math.MinInt64, true},
		{"9223372036854775808", 0, false}, {"-9223372036854775809", 0, false},
		{"0e9999999999999999999999", 0, true}, {"-0.000e-99999999999999", 0, true},
		{"1" + strings.Repeat("0", 10000) + "e-10000", 1, true},
		{"1e" + strings.Repeat("9", 10000), 0, false},
		{"1000.000000000000000000000000001", 0, false},
		{"01", 0, false}, {"+1", 0, false}, {".1", 0, false}, {"1.", 0, false}, {"1e", 0, false},
	} {
		name := test.input
		if len(name) > 50 {
			name = name[:50] + "..."
		}
		t.Run(name, func(t *testing.T) {
			got, valid := JSONInteger(json.Number(test.input))
			if valid != test.valid || (valid && got != test.want) {
				t.Fatalf("got %d/%t, want %d/%t", got, valid, test.want, test.valid)
			}
		})
	}
}

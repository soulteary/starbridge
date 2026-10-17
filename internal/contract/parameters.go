// Package contract shares host-side guest parameter defaults and exact JSON numbers.
package contract

import (
	"encoding/json"
	"strconv"
	"strings"
)

const (
	DefaultListLimit = 500
	DefaultMaxDepth  = 6
	DefaultMaxNodes  = 300
	DefaultTimeoutMS = 10000
)

// JSONInteger accepts every JSON number with an exact int64 value, including
// 12.0 and 1.2e1. It never rounds through float64 or allocates an exponent-sized
// integer. Scanning and storage are linear in the already bounded input length.
func JSONInteger(number json.Number) (int64, bool) {
	text := string(number)
	if text == "" {
		return 0, false
	}
	i := 0
	negative := text[0] == '-'
	if negative {
		i++
	}
	if i == len(text) {
		return 0, false
	}
	start := i
	if text[i] == '0' {
		i++
	} else {
		if text[i] < '1' || text[i] > '9' {
			return 0, false
		}
		for i < len(text) && text[i] >= '0' && text[i] <= '9' {
			i++
		}
	}
	digits := text[start:i]
	fraction := 0
	if i < len(text) && text[i] == '.' {
		i++
		start = i
		for i < len(text) && text[i] >= '0' && text[i] <= '9' {
			i++
		}
		if start == i {
			return 0, false
		}
		fraction = i - start
		digits += text[start:i]
	}
	exponent := 0
	if i < len(text) && (text[i] == 'e' || text[i] == 'E') {
		i++
		exponentNegative := false
		if i < len(text) && (text[i] == '+' || text[i] == '-') {
			exponentNegative = text[i] == '-'
			i++
		}
		start = i
		// Larger exponents cannot be canceled by the significand's digit count.
		cap := len(text) + 20
		for i < len(text) && text[i] >= '0' && text[i] <= '9' {
			if exponent < cap {
				exponent = min(cap, exponent*10+int(text[i]-'0'))
			}
			i++
		}
		if start == i {
			return 0, false
		}
		if exponentNegative {
			exponent = -exponent
		}
	}
	if i != len(text) {
		return 0, false
	}
	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		return 0, true
	}
	trimmed := strings.TrimRight(digits, "0")
	scale := exponent - fraction + len(digits) - len(trimmed)
	if scale < 0 || len(trimmed)+scale > 19 {
		return 0, false
	}
	canonical := trimmed + strings.Repeat("0", scale)
	if negative {
		canonical = "-" + canonical
	}
	value, err := strconv.ParseInt(canonical, 10, 64)
	return value, err == nil
}

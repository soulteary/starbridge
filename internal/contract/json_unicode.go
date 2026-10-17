package contract

import "unicode/utf8"

// ValidJSONUnicode rejects invalid UTF-8 and unpaired JSON UTF-16 escapes before
// encoding/json can silently replace them with U+FFFD. It is a Unicode check;
// callers must still use a JSON decoder to validate syntax and structure.
func ValidJSONUnicode(raw []byte) bool {
	if !utf8.Valid(raw) {
		return false
	}
	hexUnit := func(offset int) (uint16, bool) {
		if offset+4 > len(raw) {
			return 0, false
		}
		var value uint16
		for _, digit := range raw[offset : offset+4] {
			value <<= 4
			switch {
			case digit >= '0' && digit <= '9':
				value |= uint16(digit - '0')
			case digit >= 'a' && digit <= 'f':
				value |= uint16(digit-'a') + 10
			case digit >= 'A' && digit <= 'F':
				value |= uint16(digit-'A') + 10
			default:
				return 0, false
			}
		}
		return value, true
	}
	inside := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			inside = !inside
			continue
		}
		if !inside || raw[i] != '\\' {
			continue
		}
		i++ // Escaped backslashes/quotes are consumed rather than rescanned.
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		unit, valid := hexUnit(i + 1)
		if !valid || unit >= 0xdc00 && unit <= 0xdfff {
			return false
		}
		i += 4
		if unit >= 0xd800 && unit <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, valid := hexUnit(i + 3)
			if !valid || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return !inside
}

package bridge

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/soulteary/starbridge/internal/contract"
)

var hwndPattern = regexp.MustCompile(`^(?:[0-9]+|0[xX][0-9a-fA-F]+)$`)

func parseHWND(value any) (uint64, bool) {
	hwnd, ok := value.(string)
	if !ok || !hwndPattern.MatchString(hwnd) {
		return 0, false
	}
	base, text := 10, hwnd
	if strings.HasPrefix(strings.ToLower(hwnd), "0x") {
		base, text = 16, hwnd[2:]
	}
	number, err := strconv.ParseUint(text, base, 63)
	return number, err == nil && number > 0
}

// Normalize a private copy so every entry point sends the same explicit defaults,
// exact integer values, and canonical HWNDs, including direct Bridge callers.
func normalizeGuestParameters(action string, params map[string]any) (map[string]any, error) {
	copy := make(map[string]any, len(params)+3)
	for key, value := range params {
		copy[key] = value
	}
	if action == "ui.tree" {
		for key, value := range map[string]int{"max_depth": contract.DefaultMaxDepth, "max_nodes": contract.DefaultMaxNodes, "timeout_ms": contract.DefaultTimeoutMS} {
			if _, present := copy[key]; !present {
				copy[key] = value
			}
		}
	} else if action == "processes.list" || action == "windows.list" {
		if _, present := copy["limit"]; !present {
			copy["limit"] = contract.DefaultListLimit
		}
		flag := "include_hidden"
		if action == "processes.list" {
			flag = "include_path"
		}
		if _, present := copy[flag]; !present {
			copy[flag] = false
		}
	}
	if err := validateGuest(action, copy); err != nil {
		return nil, err
	}
	for _, key := range []string{"pid", "limit", "max_depth", "max_nodes", "timeout_ms"} {
		if value, present := copy[key]; present {
			integer, _ := integral(value) // validateGuest has checked exactness and bounds.
			copy[key] = integer
		}
	}
	if action == "ui.tree" {
		// The guest bounds textual handle lengths. Canonical hex fits those
		// bounds even when a valid public input has many leading zeroes.
		number, _ := parseHWND(copy["hwnd"]) // validateGuest checked syntax and range.
		copy["hwnd"] = "0x" + strconv.FormatUint(number, 16)
	}
	return copy, nil
}

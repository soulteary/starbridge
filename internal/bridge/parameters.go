package bridge

import "github.com/soulteary/starbridge/internal/contract"

// Normalize a private copy so every entry point sends the same explicit defaults
// and exact integer values to helpers, including direct Bridge callers.
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
	return copy, nil
}

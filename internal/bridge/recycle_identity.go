package bridge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var recycleUUIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Check before decoding into maps: map decoding otherwise silently keeps the
// last repeated member and loses evidence of conflicting recycle decisions.
func uniqueRecyclePolicyKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var visit func(int) error
	visit = func(depth int) error {
		if depth > 32 {
			return fmt.Errorf("policy nesting exceeds the limit")
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch token {
		case json.Delim('{'):
			seen := map[string]bool{}
			for decoder.More() {
				token, err := decoder.Token()
				key, ok := token.(string)
				if err != nil || !ok {
					return fmt.Errorf("invalid policy object key")
				}
				if seen[key] {
					return fmt.Errorf("duplicate policy object key")
				}
				seen[key] = true
				if err := visit(depth + 1); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case json.Delim('['):
			for decoder.More() {
				if err := visit(depth + 1); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		}
		return nil
	}
	return visit(0)
}

func canonicalRecycleUUID(value string) (string, bool) {
	if strings.HasPrefix(value, "{") && strings.HasSuffix(value, "}") {
		value = value[1 : len(value)-1]
	}
	return strings.ToLower(value), recycleUUIDPattern.MatchString(value)
}

func normalizeRecycleOverrides(overrides map[string]any) (map[string]any, error) {
	normalized := make(map[string]any, len(overrides))
	for key, policy := range overrides {
		if !validPolicy(policy) {
			return nil, NewError("INVALID_POLICY", "Each policy needs mode stop/restore/delete; restore also needs snapshot.", nil)
		}
		uuid, valid := canonicalRecycleUUID(key)
		if !valid {
			return nil, NewError("INVALID_POLICY", "Each vms key must be a VM UUID, not a display name.", nil)
		}
		if _, exists := normalized[uuid]; exists {
			return nil, NewError("INVALID_POLICY", "The same VM UUID appears more than once in vms.", nil)
		}
		normalized[uuid] = policy
	}
	return normalized, nil
}

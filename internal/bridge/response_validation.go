package bridge

import (
	"encoding/json"
	"math"
	"strings"
)

func invalidGuestResponse() error {
	return NewError("INVALID_GUEST_RESPONSE", "Guest response does not match the requested action's response contract.", nil)
}

// Validate the envelope before trusting a helper error or interpreting its data.
// Extra fields are allowed so compatible helper additions do not break clients.
func guestEnvelope(value any) (Result, error) {
	object, objectOK := asObject(value)
	if !objectOK {
		return nil, invalidGuestResponse()
	}
	ok, typed := object["ok"].(bool)
	_, hasData := object["data"]
	_, hasError := object["error"]
	if !typed || (ok && (!hasData || hasError)) || (!ok && (!hasError || hasData)) {
		return nil, invalidGuestResponse()
	}
	if ok {
		if data, valid := asObject(object["data"]); !valid || data == nil {
			return nil, invalidGuestResponse()
		}
	} else {
		failure, valid := asObject(object["error"])
		if !valid || !nonemptyText(failure["code"]) || !nonemptyText(failure["message"]) {
			return nil, invalidGuestResponse()
		}
	}
	return Result(object), nil
}

func nonemptyText(value any) bool {
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) != ""
}

func textValue(value any) bool { _, ok := value.(string); return ok }
func boolValue(value any) bool { _, ok := value.(bool); return ok }

func responseInteger(value any, min, max int64) bool {
	n, ok := integral(value)
	return ok && n >= min && n <= max
}

func numberValue(value any) bool {
	switch n := value.(type) {
	case json.Number:
		f, err := n.Float64()
		return err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	case float64:
		return !math.IsNaN(n) && !math.IsInf(n, 0)
	default:
		_, ok := integral(value)
		return ok
	}
}

func responseRect(value any, nullable bool) bool {
	if value == nil {
		return nullable
	}
	object, ok := asObject(value)
	if !ok {
		return false
	}
	for _, key := range []string{"x", "y", "width", "height"} {
		if !numberValue(object[key]) {
			return false
		}
	}
	return true
}

func responseItems(value any, limit int64, validate func(map[string]any) bool) bool {
	items, ok := value.([]any)
	if !ok || items == nil || int64(len(items)) > limit {
		return false
	}
	for _, value := range items {
		item, ok := asObject(value)
		if !ok || !validate(item) {
			return false
		}
	}
	return true
}

func guestData(action string, value any) error {
	data, ok := asObject(value)
	if !ok || data == nil {
		return invalidGuestResponse()
	}
	valid := false
	switch action {
	case "processes.list":
		limit, _ := integral(data["limit"])
		valid = boolValue(data["truncated"]) && responseInteger(data["limit"], 1, 1000) &&
			responseItems(data["processes"], limit, func(item map[string]any) bool {
				return responseInteger(item["pid"], 0, 2147483647) && nonemptyText(item["name"])
			})
	case "windows.list":
		limit, _ := integral(data["limit"])
		valid = boolValue(data["truncated"]) && responseInteger(data["limit"], 1, 1000) && nonemptyText(data["coordinate_space"]) &&
			responseItems(data["windows"], limit, func(item map[string]any) bool {
				return nonemptyText(item["hwnd"]) && responseInteger(item["pid"], 0, 2147483647) &&
					textValue(item["title"]) && textValue(item["class_name"]) && boolValue(item["is_visible"]) && responseRect(item["rect"], false)
			})
	case "ui.tree":
		maxNodes, _ := integral(data["max_nodes"])
		maxDepth, _ := integral(data["max_depth"])
		warnings, warningsOK := data["warnings"].([]any)
		valid = warningsOK && warnings != nil
		for _, warning := range warnings {
			valid = valid && nonemptyText(warning)
		}
		valid = valid && nonemptyText(data["hwnd"]) && nonemptyText(data["snapshot_id"]) && nonemptyText(data["view"]) &&
			responseInteger(data["max_depth"], 0, 12) && responseInteger(data["max_nodes"], 1, 1000) &&
			boolValue(data["node_limit_reached"]) && boolValue(data["depth_limit_reached"]) && nonemptyText(data["coordinate_space"]) &&
			responseItems(data["nodes"], maxNodes, func(item map[string]any) bool {
				parent, hasParent := item["parent_id"]
				name, hasName := item["name"]
				rect, hasRect := item["rect"]
				return nonemptyText(item["node_id"]) && hasParent && (parent == nil || nonemptyText(parent)) &&
					responseInteger(item["depth"], 0, maxDepth) && hasName && (name == nil || textValue(name)) && nonemptyText(item["control_type"]) &&
					responseInteger(item["pid"], 0, 2147483647) && hasRect && responseRect(rect, true) &&
					boolValue(item["is_enabled"]) && boolValue(item["is_offscreen"]) && boolValue(item["is_password"])
			})
	}
	if !valid {
		return invalidGuestResponse()
	}
	return nil
}

// A helper may return a stricter bound, but cannot increase the caller's budget
// by declaring a larger limit in its response. Params are already normalized.
func guestDataWithinRequest(action string, value any, params map[string]any) error {
	if err := guestData(action, value); err != nil {
		return err
	}
	data, _ := asObject(value)
	fields := []string{"limit"}
	if action == "ui.tree" {
		fields = []string{"max_nodes", "max_depth"}
	}
	for _, field := range fields {
		declared, validDeclared := integral(data[field])
		requested, validRequested := integral(params[field])
		if !validDeclared || !validRequested || declared > requested {
			return invalidGuestResponse()
		}
	}
	return nil
}

package bridge

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

func init() {
	registerDeveloperAction(DeveloperAction{
		Name: "ui.query", Description: "Find controls inside an identified Windows window, with bounded live pagination and optional non-password values.",
		Interactive: true, Normalize: normalizeDeveloperUIQuery,
		ValidateResult: validDeveloperUIQueryResult, ValidateWithinRequest: developerUIQueryWithinRequest,
	})
}

func uiQueryArgument(message string) error { return NewError("INVALID_ARGUMENT", message, nil) }

func uiQueryFields(input map[string]any, allowed ...string) error {
	keys := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		keys[key] = true
	}
	for key := range input {
		if !keys[key] {
			return uiQueryArgument("Unknown UI query field: " + key)
		}
	}
	return nil
}

func uiQueryString(input map[string]any, key, fallback string, max int) (string, error) {
	value, exists := input[key]
	if !exists {
		return fallback, nil
	}
	text, ok := value.(string)
	if !ok || utf8.RuneCountInString(text) > max || strings.ContainsRune(text, 0) {
		return "", uiQueryArgument(fmt.Sprintf("%s must be a string of at most %d code points without NUL.", key, max))
	}
	return text, nil
}

func uiQueryInteger(input map[string]any, key string, fallback, min, max int64) (int64, error) {
	value, exists := input[key]
	if !exists {
		return fallback, nil
	}
	number, ok := integral(value)
	if !ok || number < min || number > max {
		return 0, uiQueryArgument(fmt.Sprintf("%s must be an integer in [%d, %d].", key, min, max))
	}
	return number, nil
}

func normalizeDeveloperUISelector(value any) (map[string]any, error) {
	selector, ok := asObject(value)
	if !ok || selector == nil {
		return nil, uiQueryArgument("selector and ancestor must be JSON objects.")
	}
	if err := uiQueryFields(selector, "name", "name_contains", "automation_id", "control_type", "class_name"); err != nil {
		return nil, err
	}
	result := make(map[string]any, len(selector))
	for key := range selector {
		max := 256
		if key == "automation_id" {
			max = 512
		}
		if key == "control_type" {
			max = 64
		}
		text, err := uiQueryString(selector, key, "", max)
		if err != nil {
			return nil, err
		}
		result[key] = text
	}
	return result, nil
}

// Window identity is mandatory even for reads: an HWND or PID can be recycled.
func normalizeDeveloperUIWindow(input map[string]any) (map[string]any, error) {
	hwnd, valid := parseHWND(input["hwnd"])
	if !valid {
		return nil, uiQueryArgument("hwnd must be a positive decimal or hexadecimal string in the 63-bit range.")
	}
	pid, err := uiQueryInteger(input, "pid", 0, 1, 2147483647)
	if err != nil || pid == 0 {
		return nil, uiQueryArgument("pid is required and must be a positive Windows process ID.")
	}
	start, err := uiQueryString(input, "start_time_utc", "", 40)
	if err != nil {
		return nil, err
	}
	instant, err := time.Parse(time.RFC3339Nano, start)
	if err != nil || instant.IsZero() || instant.Nanosecond()%100 != 0 {
		return nil, uiQueryArgument("start_time_utc is required and must be an RFC3339 process start time with 100 ns precision or coarser.")
	}
	return map[string]any{"hwnd": fmt.Sprintf("0x%x", hwnd), "pid": pid, "start_time_utc": instant.UTC().Format(time.RFC3339Nano)}, nil
}

func normalizeDeveloperUIQuery(input map[string]any) (map[string]any, error) {
	if err := uiQueryFields(input, "hwnd", "pid", "start_time_utc", "selector", "ancestor", "mode", "offset", "limit", "max_depth", "max_scan_nodes", "timeout_ms", "include_value"); err != nil {
		return nil, err
	}
	result, err := normalizeDeveloperUIWindow(input)
	if err != nil {
		return nil, err
	}
	for _, key := range []string{"selector", "ancestor"} {
		value, present := input[key]
		if !present {
			value = map[string]any{}
		}
		selector, err := normalizeDeveloperUISelector(value)
		if err != nil {
			return nil, err
		}
		result[key] = selector
	}
	mode, err := uiQueryString(input, "mode", "list", 8)
	if err != nil || (mode != "list" && mode != "one") {
		return nil, uiQueryArgument("mode must be list or one.")
	}
	result["mode"] = mode
	for _, bound := range []struct {
		key                string
		fallback, min, max int64
	}{
		{"offset", 0, 0, 4999}, {"limit", 50, 1, 200}, {"max_depth", 6, 0, 12},
		{"max_scan_nodes", 2000, 1, 5000}, {"timeout_ms", 10000, 1000, 20000},
	} {
		fallback := bound.fallback
		if bound.key == "limit" && mode == "one" {
			fallback = 1
		}
		value, err := uiQueryInteger(input, bound.key, fallback, bound.min, bound.max)
		if err != nil {
			return nil, err
		}
		result[bound.key] = value
	}
	if mode == "one" && (result["offset"] != int64(0) || result["limit"] != int64(1)) {
		return nil, uiQueryArgument("mode one requires offset 0 and limit 1.")
	}
	includeValue := false
	if value, present := input["include_value"]; present {
		var valid bool
		includeValue, valid = value.(bool)
		if !valid {
			return nil, uiQueryArgument("include_value must be a boolean.")
		}
	}
	result["include_value"] = includeValue
	return result, nil
}

func uiQueryBoundedText(value any, max int, nullable bool) bool {
	if value == nil {
		return nullable
	}
	text, ok := value.(string)
	return ok && utf8.RuneCountInString(text) <= max
}

func uiQueryNullableNumber(value any) bool { return value == nil || numberValue(value) }
func uiQueryNullableBool(value any) bool   { return value == nil || boolValue(value) }

func validDeveloperUIQueryItem(item map[string]any) bool {
	for _, key := range []string{"node_id", "parent_id", "runtime_id", "depth", "pid", "name", "control_type", "automation_id", "class_name", "framework_id", "rect", "is_enabled", "is_offscreen", "is_password", "patterns", "state", "value", "value_status", "value_truncated"} {
		if _, present := item[key]; !present {
			return false
		}
	}
	if !uiQueryBoundedText(item["node_id"], 64, false) {
		return false
	}
	if !nonemptyText(item["node_id"]) || !responseInteger(item["depth"], 0, 12) || !responseInteger(item["pid"], 0, 2147483647) ||
		!uiQueryBoundedText(item["name"], 512, true) || !uiQueryBoundedText(item["automation_id"], 512, true) ||
		!uiQueryBoundedText(item["control_type"], 64, false) || !nonemptyText(item["control_type"]) ||
		!uiQueryBoundedText(item["class_name"], 256, true) || !uiQueryBoundedText(item["framework_id"], 128, true) ||
		!responseRect(item["rect"], true) || !boolValue(item["is_enabled"]) || !boolValue(item["is_offscreen"]) || !boolValue(item["is_password"]) ||
		!uiQueryBoundedText(item["value"], 2048, true) || !boolValue(item["value_truncated"]) {
		return false
	}
	if parent := item["parent_id"]; parent != nil && !nonemptyText(parent) {
		return false
	}
	if runtime := item["runtime_id"]; runtime != nil {
		values, ok := runtime.([]any)
		if !ok || len(values) > 64 {
			return false
		}
		for _, number := range values {
			if !responseInteger(number, -2147483648, 2147483647) {
				return false
			}
		}
	}
	patterns, ok := item["patterns"].([]any)
	if !ok || patterns == nil || len(patterns) > 32 {
		return false
	}
	seen := map[string]bool{}
	for _, value := range patterns {
		pattern, ok := value.(string)
		if !ok || pattern == "" || len(pattern) > 64 || seen[pattern] {
			return false
		}
		seen[pattern] = true
	}
	status, ok := item["value_status"].(string)
	if !ok || (status != "not_requested" && status != "password_skipped" && status != "available" && status != "unsupported" && status != "unavailable") {
		return false
	}
	if item["is_password"] == true && (item["value"] != nil || status != "password_skipped" || item["value_truncated"] != false) {
		return false
	}
	if status != "available" && item["value"] != nil {
		return false
	}
	if status == "available" && !textValue(item["value"]) {
		return false
	}
	state, ok := asObject(item["state"])
	if !ok || state == nil {
		return false
	}
	for _, key := range []string{"toggle", "is_selected", "expand_collapse", "value_read_only", "range_value", "range_minimum", "range_maximum"} {
		value, present := state[key]
		if !present || (item["is_password"] == true && value != nil) {
			return false
		}
	}
	for _, key := range []string{"is_selected", "value_read_only"} {
		if !uiQueryNullableBool(state[key]) {
			return false
		}
	}
	for _, key := range []string{"range_value", "range_minimum", "range_maximum"} {
		if !uiQueryNullableNumber(state[key]) {
			return false
		}
	}
	if toggle := state["toggle"]; toggle != nil && toggle != "Off" && toggle != "On" && toggle != "Indeterminate" {
		return false
	}
	if expanded := state["expand_collapse"]; expanded != nil && expanded != "Collapsed" && expanded != "Expanded" && expanded != "PartiallyExpanded" && expanded != "LeafNode" {
		return false
	}
	return true
}

func validDeveloperUIQueryResult(data map[string]any) bool {
	window, ok := asObject(data["window"])
	if !ok || window == nil {
		return false
	}
	if _, err := normalizeDeveloperUIWindow(window); err != nil {
		return false
	}
	stamp, stampOK := data["timestamp"].(string)
	_, stampError := time.Parse(time.RFC3339Nano, stamp)
	if !nonemptyText(data["snapshot_id"]) || !uiQueryBoundedText(data["snapshot_id"], 64, false) || !stampOK || stampError != nil || data["coordinate_space"] != "guest-screen-physical-pixels" || data["pagination"] != "live" {
		return false
	}
	for _, bound := range []struct {
		key      string
		min, max int64
	}{
		{"offset", 0, 4999}, {"limit", 1, 200}, {"max_depth", 0, 12}, {"max_scan_nodes", 1, 5000},
		{"scanned_nodes", 1, 5000}, {"matched_count", 0, 5000}, {"returned_count", 0, 200},
	} {
		if !responseInteger(data[bound.key], bound.min, bound.max) {
			return false
		}
	}
	for _, key := range []string{"has_more", "complete", "node_limit_reached", "depth_limit_reached", "include_value"} {
		if !boolValue(data[key]) {
			return false
		}
	}
	mode, _ := data["mode"].(string)
	if mode != "list" && mode != "one" {
		return false
	}
	items, ok := data["items"].([]any)
	if !ok || items == nil {
		return false
	}
	limit, _ := integral(data["limit"])
	returned, _ := integral(data["returned_count"])
	matched, _ := integral(data["matched_count"])
	scanned, _ := integral(data["scanned_nodes"])
	maxScan, _ := integral(data["max_scan_nodes"])
	depth, _ := integral(data["max_depth"])
	offset, _ := integral(data["offset"])
	if int64(len(items)) != returned || returned > limit || matched > scanned || scanned > maxScan || returned > matched || data["has_more"] != (matched > offset+returned) {
		return false
	}
	expectedCount := matched - offset
	if expectedCount < 0 {
		expectedCount = 0
	}
	if expectedCount > limit {
		expectedCount = limit
	}
	if returned != expectedCount {
		return false
	}
	if mode == "one" && (returned != 1 || matched != 1 || data["complete"] != true || offset != 0 || limit != 1) {
		return false
	}
	if data["node_limit_reached"] == true && data["complete"] != false {
		return false
	}
	for _, value := range items {
		item, ok := asObject(value)
		itemDepth, validDepth := integral(item["depth"])
		if !ok || !validDepth || itemDepth > depth || !validDeveloperUIQueryItem(item) {
			return false
		}
		if data["include_value"] == false && item["value"] != nil {
			return false
		}
	}
	warnings, ok := data["warnings"].([]any)
	if !ok || warnings == nil || len(warnings) > 16 {
		return false
	}
	for _, warning := range warnings {
		if !uiQueryBoundedText(warning, 80, false) || !nonemptyText(warning) {
			return false
		}
	}
	return true
}

func developerUIQueryWithinRequest(data, params map[string]any) bool {
	if !validDeveloperUIQueryResult(data) {
		return false
	}
	window, _ := asObject(data["window"])
	for _, key := range []string{"hwnd", "start_time_utc"} {
		if window[key] != params[key] {
			return false
		}
	}
	pid, _ := integral(window["pid"])
	expectedPID, _ := integral(params["pid"])
	if pid != expectedPID {
		return false
	}
	for _, key := range []string{"offset", "limit", "max_depth", "max_scan_nodes"} {
		actual, _ := integral(data[key])
		requested, _ := integral(params[key])
		if actual != requested {
			return false
		}
	}
	return data["mode"] == params["mode"] && data["include_value"] == params["include_value"]
}

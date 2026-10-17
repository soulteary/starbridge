package bridge

import (
	"context"
	"fmt"
	"math/big"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var waitPathPart = regexp.MustCompile(`^(?:[A-Za-z_][A-Za-z0-9_]*|[0-9]{1,4})$`)

func init() {
	registerDeveloperAction(DeveloperAction{
		Name: "assert.matches", Description: "Assert one read-only observation without polling or retrying a mutation.", Normalize: normalizeWait,
		ValidateResult: func(data map[string]any) bool { return data["matched"] == true },
		Execute: func(ctx context.Context, b *Bridge, vm string, p map[string]any, _ bool) (Result, error) {
			params, _ := asObject(p["params"])
			result, err := b.waitObservation(ctx, vm, p["action"].(string), params)
			if err != nil || result["ok"] == false {
				return result, err
			}
			predicate, _ := asObject(p["predicate"])
			if !matchesWait(result, predicate) {
				return nil, NewError("ASSERTION_FAILED", "The read-only observation did not match the required condition.", nil)
			}
			return Success(map[string]any{"matched": true, "observation": result}), nil
		},
	})
	registerDeveloperAction(DeveloperAction{
		Name: "wait.until", Description: "Wait for an explicit read-only observation predicate; never retry a mutation.",
		Normalize: normalizeWait, ValidateResult: func(data map[string]any) bool { return data["matched"] == true },
		Execute: func(ctx context.Context, b *Bridge, vm string, p map[string]any, _ bool) (Result, error) {
			return b.waitUntil(ctx, vm, p)
		},
	})
}

func waitInvalid(message string) error { return NewError("INVALID_ARGUMENT", message, nil) }

func normalizeWait(p map[string]any) (map[string]any, error) {
	for k := range p {
		if k != "action" && k != "params" && k != "predicate" && k != "timeout_ms" && k != "interval_ms" {
			return nil, waitInvalid("Unknown wait field: " + k)
		}
	}
	action, _ := p["action"].(string)
	switch action {
	case "vm.info", "processes.list", "windows.list", "ui.tree", "system.info", "task.status", "ui.query":
	default:
		return nil, waitInvalid("Wait requires a supported read-only observation action.")
	}
	params := map[string]any{}
	if value, exists := p["params"]; exists {
		var valid bool
		params, valid = asObject(value)
		if !valid || params == nil {
			return nil, waitInvalid("params must be an object.")
		}
	}
	if action == "vm.info" && len(params) != 0 {
		return nil, waitInvalid("vm.info takes no observation params.")
	}
	predicate, ok := asObject(p["predicate"])
	if !ok || predicate == nil {
		return nil, waitInvalid("predicate must be an object.")
	}
	for key := range predicate {
		if key != "path" && key != "op" && key != "value" {
			return nil, waitInvalid("Unknown predicate field.")
		}
	}
	path, ok := predicate["path"].(string)
	if !ok || len(path) > 256 || path == "" {
		return nil, waitInvalid("A bounded predicate path is required.")
	}
	for _, part := range strings.Split(path, ".") {
		if !waitPathPart.MatchString(part) {
			return nil, waitInvalid("Predicate paths use dot-separated field names and array indices.")
		}
	}
	op, _ := predicate["op"].(string)
	_, hasValue := predicate["value"]
	switch op {
	case "exists", "empty", "nonempty":
		if hasValue {
			return nil, waitInvalid("This predicate takes no value.")
		}
	case "eq":
		if !hasValue {
			return nil, waitInvalid("eq requires a value.")
		}
		if predicate["value"] != nil && !boolValue(predicate["value"]) && !textValue(predicate["value"]) && !numberValue(predicate["value"]) {
			return nil, waitInvalid("eq values must be JSON scalars.")
		}
	case "contains":
		value, valid := predicate["value"].(string)
		if !valid || len(value) > 4096 {
			return nil, waitInvalid("contains requires bounded text.")
		}
	default:
		return nil, waitInvalid("Supported predicates: exists, empty, nonempty, eq, contains.")
	}
	result := map[string]any{"action": action, "params": params, "predicate": predicate}
	for key, spec := range map[string][3]int64{"timeout_ms": {30000, 1, 600000}, "interval_ms": {250, 10, 10000}} {
		value, present := p[key]
		if !present {
			value = spec[0]
		}
		n, exact := integral(value)
		if !exact || n < spec[1] || n > spec[2] {
			return nil, waitInvalid(fmt.Sprintf("%s is outside its integer range.", key))
		}
		result[key] = n
	}
	return result, nil
}

func waitValue(root any, path string) (any, bool) {
	current := root
	for _, part := range strings.Split(path, ".") {
		if object, ok := asObject(current); ok {
			var found bool
			current, found = object[part]
			if !found {
				return nil, false
			}
			continue
		}
		items, ok := current.([]any)
		index, err := strconv.Atoi(part)
		if !ok || err != nil || index < 0 || index >= len(items) {
			return nil, false
		}
		current = items[index]
	}
	return current, true
}

func matchesWait(result Result, predicate map[string]any) bool {
	actual, exists := waitValue(map[string]any(result), predicate["path"].(string))
	if !exists {
		return false
	}
	switch predicate["op"] {
	case "exists":
		return actual != nil
	case "eq":
		if numberValue(actual) && numberValue(predicate["value"]) {
			left, leftOK := new(big.Rat).SetString(fmt.Sprint(actual))
			right, rightOK := new(big.Rat).SetString(fmt.Sprint(predicate["value"]))
			return leftOK && rightOK && left.Cmp(right) == 0
		}
		if n, ok := integral(actual); ok {
			if expected, valid := integral(predicate["value"]); valid {
				return n == expected
			}
		}
		return reflect.DeepEqual(actual, predicate["value"])
	case "contains":
		value, ok := actual.(string)
		return ok && strings.Contains(value, predicate["value"].(string))
	case "empty", "nonempty":
		length := -1
		switch value := actual.(type) {
		case []any:
			length = len(value)
		case string:
			length = len(value)
		case map[string]any:
			length = len(value)
		}
		return length >= 0 && ((predicate["op"] == "empty" && length == 0) || (predicate["op"] == "nonempty" && length > 0))
	}
	return false
}

func (b *Bridge) waitUntil(ctx context.Context, vm string, p map[string]any) (Result, error) {
	if b.options.DryRun {
		return Success(map[string]any{"dry_run": true, "action": "wait.until", "plan": p}), nil
	}
	started := time.Now()
	deadline, cancel := context.WithTimeout(ctx, time.Duration(p["timeout_ms"].(int64))*time.Millisecond)
	defer cancel()
	action := p["action"].(string)
	params, _ := asObject(p["params"])
	predicate, _ := asObject(p["predicate"])
	attempts := 0
	lastCode := "CONDITION_NOT_MATCHED"
	for {
		if deadline.Err() != nil {
			break
		}
		attempts++
		result, err := b.waitObservation(deadline, vm, action, params)
		if err != nil {
			failure := Failure(err)["error"].(*Error)
			lastCode = failure.Code
			switch lastCode {
			case "PRLCTL_FAILED", "TIMEOUT", "INTERRUPTED", "WINDOW_NOT_FOUND", "INTERACTIVE_SESSION_REQUIRED":
			default:
				return nil, err
			}
		} else if result["ok"] == true && matchesWait(result, predicate) {
			return Success(map[string]any{"matched": true, "attempts": attempts, "elapsed_ms": time.Since(started).Milliseconds(), "observation": result}), nil
		} else if result["ok"] == false {
			failure, _ := asObject(result["error"])
			lastCode, _ = failure["code"].(string)
			switch lastCode {
			case "WINDOW_NOT_FOUND", "INTERACTIVE_SESSION_REQUIRED", "UIA_UNAVAILABLE", "UIA_TIMEOUT":
			default:
				return result, nil
			}
		}
		timer := time.NewTimer(time.Duration(p["interval_ms"].(int64)) * time.Millisecond)
		select {
		case <-deadline.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
		}
	}
	code := "WAIT_TIMEOUT"
	if ctx.Err() == context.Canceled {
		code = "INTERRUPTED"
	}
	return nil, NewError(code, "The observation condition was not satisfied before waiting ended.", map[string]any{"attempts": attempts, "last_error_code": lastCode, "elapsed_ms": time.Since(started).Milliseconds(), "outcome": "no_result"})
}

func (b *Bridge) waitObservation(ctx context.Context, vm, action string, params map[string]any) (Result, error) {
	switch action {
	case "vm.info":
		data, err := b.VMInfo(ctx, vm)
		if err != nil {
			return nil, err
		}
		return Success(data), nil
	case "processes.list", "windows.list", "ui.tree":
		return b.Guest(ctx, vm, action, params)
	default:
		return b.Developer(ctx, vm, action, params, false)
	}
}

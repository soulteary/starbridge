package bridge

import (
	"encoding/base64"
	"encoding/json"
	"regexp"
	"unicode/utf8"
)

var taskIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var taskProfilePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
var taskEnvPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

func init() {
	for _, action := range []struct {
		name                  string
		mutation, interactive bool
	}{
		{"task.start", true, false}, {"task.status", false, false},
		{"task.output", false, false}, {"task.cancel", true, false}, {"task.close", true, true},
	} {
		name := action.name
		registerDeveloperAction(DeveloperAction{Name: name, Description: "Run and inspect an explicitly configured Windows development task.", Mutation: action.mutation, Interactive: action.interactive,
			Normalize:             func(params map[string]any) (map[string]any, error) { return normalizeTaskParameters(name, params) },
			ValidateResult:        func(data map[string]any) bool { return validTaskResult(name, data) },
			ValidateWithinRequest: func(data, params map[string]any) bool { return taskResultWithinRequest(name, data, params) },
			RedactPreview: func(params map[string]any) map[string]any {
				if name == "task.start" {
					if args, ok := params["args"].([]string); ok {
						for i := range args {
							args[i] = "[redacted]"
						}
						params["args"] = args
					}
					if args, ok := params["args"].([]any); ok {
						for i := range args {
							args[i] = "[redacted]"
						}
						params["args"] = args
					}
					if environment, ok := asObject(params["env"]); ok {
						for key := range environment {
							environment[key] = "[redacted]"
						}
						params["env"] = environment
					}
				}
				return params
			},
		})
	}
}

func taskArgumentError(message string) error { return NewError("INVALID_ARGUMENT", message, nil) }

func normalizeTaskParameters(action string, params map[string]any) (map[string]any, error) {
	allowed := map[string]bool{"task_id": true}
	if action == "task.start" {
		allowed = map[string]bool{"profile": true, "args": true, "env": true, "timeout_ms": true}
	}
	if action == "task.output" {
		allowed["stream"] = true
		allowed["offset"] = true
		allowed["limit"] = true
	}
	result := make(map[string]any, len(params)+3)
	for key, value := range params {
		if !allowed[key] {
			return nil, taskArgumentError("Unsupported task parameter: " + key)
		}
		result[key] = value
	}
	if action == "task.start" {
		profile, ok := params["profile"].(string)
		if !ok || !taskProfilePattern.MatchString(profile) {
			return nil, taskArgumentError("profile must be a configured task name of at most 64 ASCII characters.")
		}
		args := []string{}
		if value, present := params["args"]; present {
			switch values := value.(type) {
			case []string:
				args = append(args, values...)
			case []any:
				for _, value := range values {
					text, ok := value.(string)
					if !ok {
						return nil, taskArgumentError("args must be an array of strings.")
					}
					args = append(args, text)
				}
			default:
				return nil, taskArgumentError("args must be an array of strings.")
			}
		}
		if len(args) > 64 {
			return nil, taskArgumentError("At most 64 task arguments are allowed.")
		}
		for _, arg := range args {
			if !taskText(arg, 1024) {
				return nil, taskArgumentError("Task arguments must be valid text of at most 1024 characters.")
			}
		}
		environment := map[string]string{}
		if value, present := params["env"]; present {
			switch values := value.(type) {
			case map[string]string:
				for key, value := range values {
					environment[key] = value
				}
			case map[string]any:
				for key, value := range values {
					text, ok := value.(string)
					if !ok {
						return nil, taskArgumentError("env values must be strings.")
					}
					environment[key] = text
				}
			default:
				return nil, taskArgumentError("env must be a string mapping.")
			}
		}
		if len(environment) > 32 {
			return nil, taskArgumentError("At most 32 environment overrides are allowed.")
		}
		for key, value := range environment {
			if !taskEnvPattern.MatchString(key) || !taskText(value, 2048) {
				return nil, taskArgumentError("Invalid environment override name or value.")
			}
		}
		result["args"], result["env"] = args, environment
		if value, present := params["timeout_ms"]; present {
			if err := boundedInt(value, 1000, 3600000, "timeout_ms"); err != nil {
				return nil, err
			}
			result["timeout_ms"], _ = integral(value)
		}
	} else {
		id, ok := params["task_id"].(string)
		if !ok || !taskIDPattern.MatchString(id) {
			return nil, taskArgumentError("task_id must be the 32-character task identifier returned by task.start.")
		}
	}
	if action == "task.output" {
		stream := "stdout"
		if value, present := params["stream"]; present {
			var ok bool
			stream, ok = value.(string)
			if !ok {
				return nil, taskArgumentError("stream must be stdout or stderr.")
			}
		}
		if stream != "stdout" && stream != "stderr" {
			return nil, taskArgumentError("stream must be stdout or stderr.")
		}
		result["stream"] = stream
		for _, field := range []struct {
			name               string
			fallback, min, max int64
		}{{"offset", 0, 0, 16777216}, {"limit", 16384, 1, 65536}} {
			value, present := params[field.name]
			if !present {
				value = field.fallback
			}
			if err := boundedInt(value, field.min, field.max, field.name); err != nil {
				return nil, err
			}
			result[field.name], _ = integral(value)
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > 12000 {
		return nil, taskArgumentError("Task request exceeds its encoded size budget.")
	}
	return result, nil
}

func taskText(text string, limit int) bool {
	if !utf8.ValidString(text) || utf8.RuneCountInString(text) > limit {
		return false
	}
	for _, r := range text {
		if r == 0 {
			return false
		}
	}
	return true
}
func taskState(value any) bool {
	text, ok := value.(string)
	return ok && map[string]bool{"starting": true, "running": true, "closing": true, "cancelling": true, "exited": true, "cancelled": true, "timed_out": true, "failed": true, "orphaned": true}[text]
}
func validTaskResult(action string, data map[string]any) bool {
	id, ok := data["task_id"].(string)
	if !ok || !taskIDPattern.MatchString(id) || !taskState(data["status"]) {
		return false
	}
	if action == "task.output" {
		stream, _ := data["stream"].(string)
		encoded, ok := data["data_base64"].(string)
		if !ok || (stream != "stdout" && stream != "stderr") {
			return false
		}
		chunk, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil || len(chunk) > 65536 {
			return false
		}
		offset, offsetOK := integral(data["offset"])
		next, nextOK := integral(data["next_offset"])
		return offsetOK && nextOK && offset >= 0 && next <= 16777216 && next-offset == int64(len(chunk)) && boolValue(data["eof"]) && boolValue(data["truncated"])
	}
	profile, ok := data["profile"].(string)
	if !ok || !taskProfilePattern.MatchString(profile) {
		return false
	}
	for _, field := range []string{"stdout_bytes", "stderr_bytes"} {
		if !responseInteger(data[field], 0, 16777216) {
			return false
		}
	}
	for _, field := range []string{"stdout_truncated", "stderr_truncated"} {
		if !boolValue(data[field]) {
			return false
		}
	}
	if value := data["pid"]; value != nil && !responseInteger(value, 1, 2147483647) {
		return false
	}
	if value := data["start_time_utc"]; value != nil && !nonemptyText(value) {
		return false
	}
	if value := data["exit_code"]; value != nil && !responseInteger(value, -2147483648, 2147483647) {
		return false
	}
	if !nonemptyText(data["created_at"]) {
		return false
	}
	if action == "task.cancel" || action == "task.close" {
		status, _ := data["control_status"].(string)
		return boolValue(data["accepted"]) && (status == "accepted" || status == "blocked" || status == "pending" || status == "already_finished")
	}
	return true
}
func taskResultWithinRequest(action string, data, params map[string]any) bool {
	if action == "task.start" {
		return data["profile"] == params["profile"]
	}
	if data["task_id"] != params["task_id"] {
		return false
	}
	if action == "task.output" {
		limit, ok := integral(params["limit"])
		next, _ := integral(data["next_offset"])
		offset, _ := integral(data["offset"])
		requestedOffset, offsetOK := integral(params["offset"])
		return ok && offsetOK && data["stream"] == params["stream"] && offset == requestedOffset && next-offset <= limit
	}
	return true
}

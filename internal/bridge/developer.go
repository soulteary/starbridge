package bridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"regexp"
	"sort"
)

const DeveloperProtocol = "dev-1"
const MaxDeveloperRequestBytes = 16000

// DeveloperAction is registered only during package initialization. Execute is
// optional: local composite actions use it instead of the Windows transport.
type DeveloperAction struct {
	Name                  string
	Description           string
	Mutation, Interactive bool
	Normalize             func(map[string]any) (map[string]any, error)
	ValidateResult        func(map[string]any) bool
	ValidateWithinRequest func(data map[string]any, params map[string]any) bool
	// RedactPreview removes secrets from dry-run output without changing the wire.
	RedactPreview func(map[string]any) map[string]any
	Execute       func(context.Context, *Bridge, string, map[string]any, bool) (Result, error)
}

var developerRegistry = map[string]DeveloperAction{}
var developerName = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

func registerDeveloperAction(action DeveloperAction) {
	if !developerName.MatchString(action.Name) || action.Normalize == nil || (action.ValidateResult == nil && action.Execute == nil) {
		panic("invalid developer action registration: " + action.Name)
	}
	if _, exists := developerRegistry[action.Name]; exists {
		panic("duplicate developer action registration: " + action.Name)
	}
	developerRegistry[action.Name] = action
}

// DeveloperActions returns a sorted copy; callers cannot replace registrations.
func DeveloperActions() []DeveloperAction {
	actions := make([]DeveloperAction, 0, len(developerRegistry))
	for _, action := range developerRegistry {
		actions = append(actions, action)
	}
	sort.Slice(actions, func(i, j int) bool { return actions[i].Name < actions[j].Name })
	return actions
}

// Developer runs a registered development action. A mutation requires explicit
// authorization even for direct Go callers and dry runs. Transport cancellation
// does not promise that a guest mutation was rolled back or stopped.
func (b *Bridge) Developer(ctx context.Context, vm, name string, params map[string]any, allowWrite bool) (Result, error) {
	if err := identifier(vm, "VM"); err != nil {
		return nil, err
	}
	action, exists := developerRegistry[name]
	if !exists {
		return nil, NewError("UNKNOWN_ACTION", "Unknown developer action.", nil)
	}
	if params == nil {
		params = map[string]any{}
	}
	params, err := action.Normalize(params)
	if err != nil {
		return nil, err
	}
	if params == nil {
		return nil, NewError("INVALID_ARGUMENT", "Developer parameters must be an object.", nil)
	}
	if action.Mutation && !allowWrite {
		return nil, NewError("WRITE_NOT_ALLOWED", "This developer action requires --allow-write.", nil)
	}
	request, err := json.Marshal(map[string]any{"action": name, "params": params})
	if err != nil || len(request) > MaxDeveloperRequestBytes {
		return nil, NewError("INVALID_ARGUMENT", "Developer request must be JSON of at most 16000 UTF-8 bytes.", nil)
	}
	previewParams := params
	if b.options.DryRun && action.RedactPreview != nil {
		// Give preview code a detached object; it cannot mutate caller parameters.
		copyValue, _ := decodeJSON(string(request))
		copyObject, _ := asObject(copyValue)
		copyParams, _ := asObject(copyObject["params"])
		previewParams = action.RedactPreview(copyParams)
		if previewParams == nil {
			return nil, NewError("INVALID_ARGUMENT", "Developer preview parameters must be an object.", nil)
		}
		request, err = json.Marshal(map[string]any{"action": name, "params": previewParams})
		if err != nil {
			return nil, NewError("INVALID_ARGUMENT", "Developer preview parameters must be JSON.", nil)
		}
	}
	if action.Execute != nil {
		if b.options.DryRun {
			return Success(map[string]any{"dry_run": true, "vm": vm, "action": name, "params": previewParams, "mutation": action.Mutation}), nil
		}
		return action.Execute(ctx, b, vm, params, allowWrite)
	}
	args := []string{"exec", vm, "--current-user", "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", b.options.DeveloperScript, "-RequestBase64", base64.StdEncoding.EncodeToString(request)}
	if action.Mutation {
		args = append(args, "-AllowWrite")
	}
	payload, err := b.run(ctx, args, false, action.Mutation, true)
	if err != nil {
		return nil, err
	}
	if b.options.DryRun {
		return Success(payload), nil
	}
	response, _ := asObject(payload) // run has validated the generic envelope.
	meta, valid := asObject(response["meta"])
	if !valid || meta["protocol_version"] != DeveloperProtocol || meta["action"] != name || meta["read_only"] != !action.Mutation ||
		!responseInteger(meta["session_id"], 0, 2147483647) || !boolValue(meta["user_interactive"]) {
		return nil, invalidGuestResponse()
	}
	if response["ok"] == true {
		sessionID, _ := integral(meta["session_id"])
		if action.Interactive && (sessionID == 0 || meta["user_interactive"] != true) {
			return nil, invalidGuestResponse()
		}
		data, _ := asObject(response["data"])
		if !action.ValidateResult(data) || (action.ValidateWithinRequest != nil && !action.ValidateWithinRequest(data, params)) {
			return nil, invalidGuestResponse()
		}
		if name == "system.info" {
			session, _ := asObject(data["session"])
			dataSessionID, _ := integral(session["id"])
			if dataSessionID != sessionID || session["user_interactive"] != meta["user_interactive"] {
				return nil, invalidGuestResponse()
			}
		}
		if name == "ui.observe" || name == "ui.click" {
			observation, _ := asObject(data["observation"])
			observedSession, _ := integral(observation["session_id"])
			if observedSession != sessionID {
				return nil, invalidGuestResponse()
			}
		}
	}
	return Result(response), nil
}

func init() {
	registerDeveloperAction(DeveloperAction{
		Name: "system.info", Description: "Inspect the Windows environment, session and installed developer actions.",
		Normalize: func(params map[string]any) (map[string]any, error) {
			if len(params) != 0 {
				return nil, NewError("INVALID_ARGUMENT", "system.info accepts no parameters.", nil)
			}
			return map[string]any{}, nil
		},
		ValidateResult: developerSystemInfo,
	})
}

var developerHash = regexp.MustCompile(`^[0-9a-f]{64}$`)

func developerSystemInfo(data map[string]any) bool {
	os, osOK := asObject(data["os"])
	arch, archOK := asObject(data["architecture"])
	session, sessionOK := asObject(data["session"])
	helper, helperOK := asObject(data["helper"])
	hash, hashOK := helper["sha256"].(string)
	if !osOK || !archOK || !sessionOK || !helperOK || !hashOK || !developerHash.MatchString(hash) ||
		os["name"] != "Windows" || !nonemptyText(os["version"]) || !nonemptyText(os["build"]) ||
		!nonemptyText(arch["os"]) || !nonemptyText(arch["process"]) || !nonemptyText(data["powershell_version"]) || !nonemptyText(data["dotnet_version"]) ||
		!responseInteger(session["id"], 0, 2147483647) || !boolValue(session["user_interactive"]) || helper["protocol_version"] != DeveloperProtocol || !nonemptyText(helper["version"]) {
		return false
	}
	capabilities, ok := data["capabilities"].([]any)
	if !ok || capabilities == nil || len(capabilities) == 0 || len(capabilities) > 256 {
		return false
	}
	seen := map[string]bool{}
	for _, capability := range capabilities {
		item, ok := asObject(capability)
		name, typed := item["name"].(string)
		if !ok || !typed || !developerName.MatchString(name) || seen[name] || !textValue(item["description"]) || !boolValue(item["mutation"]) || !boolValue(item["interactive"]) {
			return false
		}
		seen[name] = true
	}
	return seen["system.info"]
}

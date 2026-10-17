package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/soulteary/starbridge/internal/bridge"
)

const maxAccessPolicyBytes = 64 * 1024

var accessUUIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// AccessPolicy is an immutable, explicit set of tool grants per VM UUID.
// A nil policy preserves the unrestricted local behavior of New.
type AccessPolicy struct {
	grants map[string]map[string]bool
}

func canonicalAccessUUID(value string) (string, bool) {
	if strings.HasPrefix(value, "{") && strings.HasSuffix(value, "}") {
		value = value[1 : len(value)-1]
	}
	return strings.ToLower(value), accessUUIDPattern.MatchString(value)
}

func invalidAccessPolicy(message string) error {
	return bridge.NewError("INVALID_ACCESS_POLICY", message, nil)
}

// uniqueAccessObject rejects duplicate keys rather than silently taking the last
// value. Configuration mistakes must not silently replace an intended scope.
func uniqueAccessObject(raw []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return nil, fmt.Errorf("expected an object")
	}
	object := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return nil, fmt.Errorf("invalid object key")
		}
		if _, exists := object[key]; exists {
			return nil, fmt.Errorf("duplicate object key")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		object[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("expected exactly one object")
	}
	return object, nil
}

func parseAccessPolicy(data []byte) (*AccessPolicy, error) {
	if len(data) > maxAccessPolicyBytes {
		return nil, invalidAccessPolicy("Access policy exceeds 64 KiB.")
	}
	config, err := uniqueAccessObject(data)
	if err != nil || len(config) != 1 || config["vms"] == nil {
		return nil, invalidAccessPolicy("Access policy must contain exactly one vms object, without duplicate keys.")
	}
	vms, err := uniqueAccessObject(config["vms"])
	if err != nil {
		return nil, invalidAccessPolicy("vms must be a JSON object without duplicate UUIDs.")
	}
	known := map[string]bool{}
	for _, definition := range tools(true) {
		if definition.name != "vm_list" {
			known[definition.name] = true
		}
	}
	policy := &AccessPolicy{grants: map[string]map[string]bool{}}
	for identifier, raw := range vms {
		uuid, valid := canonicalAccessUUID(identifier)
		if !valid {
			return nil, invalidAccessPolicy("Each vms key must be a VM UUID, not a display name.")
		}
		if _, exists := policy.grants[uuid]; exists {
			return nil, invalidAccessPolicy("The same UUID appears more than once.")
		}
		var actions []string
		if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '[' || json.Unmarshal(raw, &actions) != nil {
			return nil, invalidAccessPolicy("Each VM grant must be an array of tool names.")
		}
		grants := map[string]bool{}
		for _, action := range actions {
			if !known[action] || grants[action] {
				return nil, invalidAccessPolicy("VM grants contain an unknown or duplicate tool name.")
			}
			grants[action] = true
		}
		policy.grants[uuid] = grants
	}
	return policy, nil
}

// LoadAccessPolicy reads a bounded, fail-closed JSON configuration before serving.
func LoadAccessPolicy(path string) (*AccessPolicy, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, invalidAccessPolicy("Could not read access policy JSON.")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxAccessPolicyBytes+1))
	if err != nil {
		return nil, invalidAccessPolicy("Could not read access policy JSON.")
	}
	return parseAccessPolicy(data)
}

type scopedBackend struct {
	backend    Backend
	policy     *AccessPolicy
	allowWrite bool
}

type accessVM struct {
	uuid, name string
	value      map[string]any
}

func invalidInventory() error {
	return bridge.NewError("INVALID_VM_INVENTORY", "VM inventory has missing, ambiguous or unsupported identity fields; access was denied.", nil)
}

// inventoryString accepts prlctl's uuid / ID / id spellings, rejecting any
// conflicting identity fields. Unknown inventory shapes never grant access.
func inventoryString(value map[string]any, identity bool) (string, error) {
	text, found := "", false
	for key, raw := range value {
		matches := strings.EqualFold(key, "name")
		if identity {
			matches = strings.EqualFold(key, "uuid") || strings.EqualFold(key, "id")
		}
		if !matches {
			continue
		}
		candidate, ok := raw.(string)
		if !ok {
			return "", invalidInventory()
		}
		if identity {
			candidate, ok = canonicalAccessUUID(candidate)
			if !ok {
				return "", invalidInventory()
			}
		}
		if found && text != candidate {
			return "", invalidInventory()
		}
		text, found = candidate, true
	}
	if identity && !found {
		return "", invalidInventory()
	}
	return text, nil
}

func (b *scopedBackend) inventory(ctx context.Context) ([]accessVM, error) {
	data, err := b.backend.VMList(ctx)
	if err != nil {
		// Raw stdout/stderr can include VM names outside the configured scope.
		code := "VM_INVENTORY_UNAVAILABLE"
		var failure *bridge.Error
		if errors.As(err, &failure) {
			code = failure.Code
		}
		return nil, bridge.NewError(code, "Could not read VM inventory for the MCP access policy.", nil)
	}
	rows, ok := data.([]any)
	if !ok {
		return nil, invalidInventory()
	}
	seen := map[string]bool{}
	vms := make([]accessVM, 0, len(rows))
	for _, row := range rows {
		value, ok := row.(map[string]any)
		if !ok {
			return nil, invalidInventory()
		}
		uuid, err := inventoryString(value, true)
		if err != nil || seen[uuid] {
			return nil, invalidInventory()
		}
		name, err := inventoryString(value, false)
		if err != nil {
			return nil, err
		}
		seen[uuid] = true
		vms = append(vms, accessVM{uuid: uuid, name: name, value: value})
	}
	return vms, nil
}

func accessDenied() error {
	return bridge.NewError("ACCESS_DENIED", "This VM or action is not granted by the MCP access policy.", nil)
}

// resolve checks the full inventory, before filtering, so duplicate names cannot
// select an allowed VM accidentally. The eventual operation always uses its UUID
// rather than the mutable name that was checked.
func (b *scopedBackend) resolve(ctx context.Context, vm, action string) (string, error) {
	vms, err := b.inventory(ctx)
	if err != nil {
		return "", err
	}
	uuid, byUUID := canonicalAccessUUID(vm)
	match := ""
	for _, candidate := range vms {
		if (byUUID && candidate.uuid == uuid) || (!byUUID && candidate.name == vm && vm != "") {
			if match != "" {
				return "", accessDenied()
			}
			match = candidate.uuid
		}
	}
	if match == "" || !b.policy.grants[match][action] {
		return "", accessDenied()
	}
	return match, nil
}

func (b *scopedBackend) VMList(ctx context.Context) (any, error) {
	vms, err := b.inventory(ctx)
	if err != nil {
		return nil, err
	}
	visible := make([]any, 0, len(vms))
	for _, vm := range vms {
		if _, allowed := b.policy.grants[vm.uuid]; allowed {
			visible = append(visible, vm.value)
		}
	}
	return visible, nil
}

func (b *scopedBackend) VMInfo(ctx context.Context, vm string) (any, error) {
	uuid, err := b.resolve(ctx, vm, "vm_info")
	if err != nil {
		return nil, err
	}
	return b.backend.VMInfo(ctx, uuid)
}

func (b *scopedBackend) Guest(ctx context.Context, vm, action string, params map[string]any) (bridge.Result, error) {
	toolName := map[string]string{"processes.list": "guest_processes", "windows.list": "guest_windows", "ui.tree": "guest_ui_tree"}[action]
	if toolName == "" {
		return nil, accessDenied()
	}
	uuid, err := b.resolve(ctx, vm, toolName)
	if err != nil {
		return nil, err
	}
	return b.backend.Guest(ctx, uuid, action, params)
}

func (b *scopedBackend) VMAction(ctx context.Context, action, vm string, force bool, confirmation string) (any, error) {
	if !b.allowWrite || force || confirmation != "" || (action != "start" && action != "stop" && action != "suspend" && action != "resume") {
		return nil, accessDenied()
	}
	uuid, err := b.resolve(ctx, vm, "vm_"+action)
	if err != nil {
		return nil, err
	}
	return b.backend.VMAction(ctx, action, uuid, false, "")
}

// NewWithAccess adds an optional UUID/action policy without changing New's API.
// VM write tools still require allowWrite, even if granted by the policy.
func NewWithAccess(backend Backend, allowWrite bool, policy *AccessPolicy) *mcp.Server {
	if policy == nil {
		return New(backend, allowWrite)
	}
	server := New(&scopedBackend{backend: backend, policy: policy, allowWrite: allowWrite}, allowWrite)
	for _, definition := range tools(allowWrite) {
		if definition.name == "vm_list" {
			continue
		}
		granted := false
		for _, actions := range policy.grants {
			granted = granted || actions[definition.name]
		}
		if !granted {
			server.RemoveTools(definition.name)
		}
	}
	return server
}

// RunWithAccess serves stdio using the same optional policy as NewWithAccess.
func RunWithAccess(ctx context.Context, backend Backend, allowWrite bool, policy *AccessPolicy) error {
	return NewWithAccess(backend, allowWrite, policy).Run(ctx, &mcp.StdioTransport{})
}

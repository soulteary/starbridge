package mcpserver

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/soulteary/starbridge/internal/bridge"
	"github.com/soulteary/starbridge/internal/contract"
)

// Optional interfaces keep existing Backend implementations source-compatible.
type DeveloperBackend interface {
	Developer(context.Context, string, string, map[string]any, bool) (bridge.Result, error)
}
type DoctorBackend interface {
	DoctorVM(context.Context, string) (any, error)
}
type SnapshotBackend interface {
	SnapshotList(context.Context, string) (any, error)
}

type ServerOptions struct {
	AllowVMWrite       bool
	DeveloperTools     bool
	AllowGuestWrite    bool
	DeveloperWorkspace string
	AccessPolicy       *AccessPolicy
	// Tests supply synthetic action contracts without importing optional modules.
	developerActions []bridge.DeveloperAction
}

func developerToolName(action string) string { return "dev." + action }

func invalidServer(message string) error { return bridge.NewError("INVALID_ARGUMENT", message, nil) }

func (options ServerOptions) actions() []bridge.DeveloperAction {
	if options.developerActions != nil {
		return options.developerActions
	}
	return bridge.DeveloperActions()
}

// NewWithOptions opts into development tools and confines host file access.
// No flags implicitly grant another flag's VM/guest mutation authority.
func NewWithOptions(backend Backend, options ServerOptions) (*mcp.Server, error) {
	if !options.DeveloperTools && (options.AllowGuestWrite || options.DeveloperWorkspace != "") {
		return nil, invalidServer("--allow-guest-write and --developer-workspace require --developer-tools.")
	}
	workspace, err := newDeveloperWorkspace(options.DeveloperWorkspace)
	if err != nil {
		return nil, err
	}
	_, developerOK := backend.(DeveloperBackend)
	_, doctorOK := backend.(DoctorBackend)
	_, snapshotOK := backend.(SnapshotBackend)
	if options.AccessPolicy != nil {
		backend = &scopedBackend{backend: backend, policy: options.AccessPolicy, allowWrite: options.AllowVMWrite, allowGuestWrite: options.AllowGuestWrite}
	}
	backend = newCoordinatedBackend(backend)
	definitions := tools(options.AllowVMWrite)
	if options.DeveloperTools {
		for i := range definitions {
			switch definitions[i].name {
			case "guest_processes":
				definitions[i].arguments["include_path"] = argument{kind: "boolean", fallback: false}
			case "guest_windows":
				definitions[i].arguments["include_hidden"] = argument{kind: "boolean", fallback: false}
			case "guest_ui_tree":
				definitions[i].arguments["timeout_ms"] = argument{kind: "integer", min: 1000, max: 20000, fallback: contract.DefaultTimeoutMS}
			}
		}
		vmArguments := map[string]argument{"vm": {kind: "string", required: true, maxLength: 512}}
		if doctorOK {
			definitions = append(definitions, tool{name: "doctor_vm", description: "Read VM, guest session and desktop readiness without starting the VM.", arguments: vmArguments,
				invoke: func(ctx context.Context, args map[string]any) (bridge.Result, error) {
					if err := validDeveloperVM(args["vm"].(string)); err != nil {
						return nil, err
					}
					value, err := backend.(DoctorBackend).DoctorVM(ctx, args["vm"].(string))
					if err != nil {
						return nil, err
					}
					return bridge.Success(value), nil
				}})
		}
		if snapshotOK {
			definitions = append(definitions, tool{name: "vm_snapshot_list", description: "List existing VM snapshots without changing VM state.", arguments: vmArguments,
				invoke: func(ctx context.Context, args map[string]any) (bridge.Result, error) {
					if err := validDeveloperVM(args["vm"].(string)); err != nil {
						return nil, err
					}
					value, err := backend.(SnapshotBackend).SnapshotList(ctx, args["vm"].(string))
					if err != nil {
						return nil, err
					}
					return bridge.Success(value), nil
				}})
		}
		if developerOK {
			for _, action := range options.actions() {
				if action.Mutation && !options.AllowGuestWrite {
					continue
				}
				if action.Execute != nil && !supportedLocalAction(action.Name) {
					continue
				}
				definition := action
				description := action.Description
				if action.Interactive {
					description += " Requires an interactive Windows session."
				}
				definitions = append(definitions, tool{name: developerToolName(action.Name), description: description, write: action.Mutation, interactive: action.Interactive,
					arguments: map[string]argument{"vm": {kind: "string", required: true, maxLength: 512}, "params": {kind: "object", fallback: map[string]any{}}},
					invoke: func(ctx context.Context, args map[string]any) (bridge.Result, error) {
						vm := args["vm"].(string)
						if err := validDeveloperVM(vm); err != nil {
							return nil, err
						}
						params := args["params"].(map[string]any)
						params, err := definition.Normalize(params)
						if err != nil {
							return nil, err
						}
						if params == nil {
							return nil, invalidServer("Developer parameters must be an object.")
						}
						request, err := json.Marshal(map[string]any{"action": definition.Name, "params": params})
						if err != nil || len(request) > bridge.MaxDeveloperRequestBytes {
							return nil, invalidServer("Developer request exceeds 16000 UTF-8 bytes.")
						}
						// Local file operations share this gate through validation and execution,
						// preventing this MCP instance from replacing a checked profile mid-run.
						if definition.Execute != nil && hostFileAction(definition.Name) {
							release, err := workspace.acquire(ctx)
							if err != nil {
								return nil, err
							}
							defer release()
						}
						permissions, err := workspace.validate(options, definition.Name, params, vm, 0)
						if err != nil {
							return nil, err
						}
						ctx = context.WithValue(ctx, developerPermissionsKey{}, permissions)
						ctx = context.WithValue(ctx, developerMutationKey{}, definition.Mutation)
						return backend.(DeveloperBackend).Developer(ctx, vm, definition.Name, params, options.AllowGuestWrite)
					}})
			}
		}
	}
	if options.AccessPolicy != nil {
		filtered := definitions[:0]
		for _, definition := range definitions {
			granted := definition.name == "vm_list"
			for _, grants := range options.AccessPolicy.grants {
				granted = granted || grants[definition.name]
			}
			if granted {
				filtered = append(filtered, definition)
			}
		}
		definitions = filtered
	}
	return newServer(backend, definitions), nil
}

func RunWithOptions(ctx context.Context, backend Backend, options ServerOptions) error {
	server, err := NewWithOptions(backend, options)
	if err != nil {
		return err
	}
	return server.Run(ctx, &mcp.StdioTransport{})
}

func validDeveloperVM(vm string) error {
	if strings.TrimSpace(vm) == "" || strings.HasPrefix(vm, "-") {
		return invalidServer("VM must be a nonempty identifier without a leading '-'.")
	}
	for _, r := range vm {
		if r < 32 {
			return invalidServer("VM contains a control character.")
		}
	}
	return nil
}

type developerPermissionsKey struct{}

func optionalBackendError() error {
	return bridge.NewError("UNSUPPORTED_BACKEND", "The backend does not implement this development capability.", nil)
}

func (b *coordinatedBackend) DoctorVM(ctx context.Context, vm string) (any, error) {
	impl, ok := b.backend.(DoctorBackend)
	if !ok {
		return nil, optionalBackendError()
	}
	release, err := b.acquireSlot(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return impl.DoctorVM(ctx, vm)
}
func (b *coordinatedBackend) SnapshotList(ctx context.Context, vm string) (any, error) {
	impl, ok := b.backend.(SnapshotBackend)
	if !ok {
		return nil, optionalBackendError()
	}
	release, err := b.acquireSlot(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return impl.SnapshotList(ctx, vm)
}
func (b *coordinatedBackend) Developer(ctx context.Context, vm, action string, params map[string]any, allowWrite bool) (bridge.Result, error) {
	impl, ok := b.backend.(DeveloperBackend)
	if !ok {
		return nil, optionalBackendError()
	}
	mutation := false
	for _, spec := range bridge.DeveloperActions() {
		if spec.Name == action {
			mutation = spec.Mutation
			break
		}
	}
	// The handler passes the registry decision, including synthetic test contracts.
	if value, ok := ctx.Value(developerMutationKey{}).(bool); ok {
		mutation = value
	}
	if mutation {
		unlock, err := b.acquireWrite(ctx, vm)
		if err != nil {
			return nil, err
		}
		defer unlock()
	}
	release, err := b.acquireSlot(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return impl.Developer(ctx, vm, action, params, allowWrite)
}

type developerMutationKey struct{}

func (b *scopedBackend) DoctorVM(ctx context.Context, vm string) (any, error) {
	impl, ok := b.backend.(DoctorBackend)
	if !ok {
		return nil, optionalBackendError()
	}
	if uuid, ok := canonicalAccessUUID(vm); ok && !b.policy.grants[uuid]["doctor_vm"] {
		return nil, accessDenied()
	}
	uuid, err := b.resolve(ctx, vm, "doctor_vm")
	if err != nil {
		return nil, err
	}
	return impl.DoctorVM(ctx, uuid)
}
func (b *scopedBackend) SnapshotList(ctx context.Context, vm string) (any, error) {
	impl, ok := b.backend.(SnapshotBackend)
	if !ok {
		return nil, optionalBackendError()
	}
	if uuid, ok := canonicalAccessUUID(vm); ok && !b.policy.grants[uuid]["vm_snapshot_list"] {
		return nil, accessDenied()
	}
	uuid, err := b.resolve(ctx, vm, "vm_snapshot_list")
	if err != nil {
		return nil, err
	}
	return impl.SnapshotList(ctx, uuid)
}
func (b *scopedBackend) Developer(ctx context.Context, vm, action string, params map[string]any, allowWrite bool) (bridge.Result, error) {
	impl, ok := b.backend.(DeveloperBackend)
	if !ok {
		return nil, optionalBackendError()
	}
	if mutation, _ := ctx.Value(developerMutationKey{}).(bool); mutation && (!allowWrite || !b.allowGuestWrite) {
		return nil, accessDenied()
	}
	permissions, _ := ctx.Value(developerPermissionsKey{}).([]string)
	permissions = append(append([]string{}, permissions...), developerToolName(action))
	if uuid, ok := canonicalAccessUUID(vm); ok {
		for _, name := range permissions {
			if !b.policy.grants[uuid][name] {
				return nil, accessDenied()
			}
		}
	}
	uuid, err := b.resolve(ctx, vm, developerToolName(action))
	if err != nil {
		return nil, err
	}
	for _, name := range permissions {
		if !b.policy.grants[uuid][name] {
			return nil, accessDenied()
		}
	}
	return impl.Developer(ctx, uuid, action, params, allowWrite && b.allowGuestWrite)
}

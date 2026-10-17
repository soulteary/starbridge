package mcpserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image/png"
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
				if workspace.root == "" && hostFileAction(action.Name) {
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
	return newDeveloperServer(backend, definitions), nil
}

// The image action shares the ordinary invocation closure (including workspace,
// access-policy and coordination checks), but returns native MCP image content.
// Keeping it out of newServer avoids registering a second generic JSON handler.
func newDeveloperServer(backend Backend, definitions []tool) *mcp.Server {
	ordinary := make([]tool, 0, len(definitions))
	images := []tool{}
	for _, definition := range definitions {
		if (definition.name == developerToolName("screenshot.capture") || definition.name == developerToolName("ui.observe") || definition.name == developerToolName("ui.click")) && definition.invoke != nil {
			images = append(images, definition)
		} else {
			ordinary = append(ordinary, definition)
		}
	}
	server := newServer(backend, ordinary)
	for _, definition := range images {
		open, destructive := definition.interactive, definition.write
		server.AddTool(&mcp.Tool{Name: definition.name, Description: definition.description, InputSchema: inputSchema(definition.arguments), OutputSchema: outputSchema(),
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: !definition.write, DestructiveHint: &destructive, IdempotentHint: !definition.write, OpenWorldHint: &open}},
			func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				args, err := decodeArguments(request.Params.Arguments, definition.arguments)
				if err != nil {
					return toolResult(bridge.Failure(err)), nil
				}
				payload, err := definition.invoke(ctx, args)
				if err != nil {
					return toolResult(bridge.Failure(err)), nil
				}
				if definition.name != developerToolName("screenshot.capture") {
					return developerVisualImageResult(payload, args["params"].(map[string]any), strings.TrimPrefix(definition.name, "dev.")), nil
				}
				return developerImageResult(payload, args["params"].(map[string]any)), nil
			})
	}
	return server
}

// Visual actions carry exactly one image inside data.screenshot. Validate their
// complete contract even for optional backends, then remove bytes from both
// metadata representations without mutating the backend result.
func developerVisualImageResult(payload bridge.Result, params map[string]any, action string) *mcp.CallToolResult {
	if payload["ok"] != true {
		return toolResult(payload)
	}
	data, ok := payload["data"].(map[string]any)
	if !ok {
		return toolResult(bridge.Failure(bridge.NewError("INVALID_GUEST_RESPONSE", "Invalid visual observation.", nil)))
	}
	_, hasScreenshot := data["screenshot"]
	_, hasPixels := data["png_base64"]
	_, hasObservation := data["observation"]
	if data["dry_run"] == true && !hasScreenshot && !hasPixels && !hasObservation {
		return toolResult(payload)
	}
	for _, spec := range bridge.DeveloperActions() {
		if spec.Name != action {
			continue
		}
		normalized, err := spec.Normalize(params)
		if err != nil || !spec.ValidateResult(data) || (spec.ValidateWithinRequest != nil && !spec.ValidateWithinRequest(data, normalized)) {
			return toolResult(bridge.Failure(bridge.NewError("INVALID_GUEST_RESPONSE", "Invalid visual observation.", nil)))
		}
		imageResult := developerImageResult(bridge.Success(data["screenshot"]), normalized)
		if imageResult.IsError {
			return imageResult
		}
		metadataData := make(map[string]any, len(data))
		for k, v := range data {
			metadataData[k] = v
		}
		screenshot := data["screenshot"].(map[string]any)
		metadataImage := make(map[string]any, len(screenshot)-1)
		for k, v := range screenshot {
			if k != "png_base64" {
				metadataImage[k] = v
			}
		}
		metadataData["screenshot"] = metadataImage
		metadata := make(bridge.Result, len(payload))
		for k, v := range payload {
			metadata[k] = v
		}
		metadata["data"] = metadataData
		result := toolResult(metadata)
		result.Content = append(result.Content, imageResult.Content[len(imageResult.Content)-1])
		return result
	}
	return toolResult(bridge.Failure(bridge.NewError("INVALID_GUEST_RESPONSE", "Unknown visual response action.", nil)))
}

const maxMCPImageBytes = 4 * 1024 * 1024
const maxMCPImagePixels = 16 * 1024 * 1024

func imageInteger(value any) (int64, bool) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return 0, false
	}
	return contract.JSONInteger(json.Number(encoded))
}

func developerImageResult(payload bridge.Result, params map[string]any) *mcp.CallToolResult {
	if payload["ok"] != true {
		return toolResult(payload)
	}
	invalid := func() *mcp.CallToolResult {
		return toolResult(bridge.Failure(bridge.NewError("INVALID_GUEST_RESPONSE", "Screenshot response is not a complete PNG within its declared and requested image budgets.", nil)))
	}
	data, ok := payload["data"].(map[string]any)
	if !ok {
		return invalid()
	}
	if _, hasImage := data["png_base64"]; !hasImage && data["dry_run"] == true {
		return toolResult(payload)
	}
	encoded, ok := data["png_base64"].(string)
	if !ok || len(encoded) > base64.StdEncoding.EncodedLen(maxMCPImageBytes) {
		return invalid()
	}
	imageBytes, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(imageBytes) == 0 || len(imageBytes) > maxMCPImageBytes || base64.StdEncoding.EncodeToString(imageBytes) != encoded {
		return invalid()
	}
	maxBytes, maxPixels := int64(maxMCPImageBytes), int64(8*1024*1024)
	for _, limit := range []struct {
		name        string
		min, max    int64
		destination *int64
	}{{"max_bytes", 1024, maxMCPImageBytes, &maxBytes}, {"max_pixels", 1, maxMCPImagePixels, &maxPixels}} {
		if value, present := params[limit.name]; present {
			n, valid := imageInteger(value)
			if !valid || n < limit.min || n > limit.max {
				return invalid()
			}
			*limit.destination = n
		}
	}
	width, widthOK := imageInteger(data["width"])
	height, heightOK := imageInteger(data["height"])
	length, lengthOK := imageInteger(data["byte_length"])
	if !widthOK || !heightOK || width < 1 || height < 1 || width > 16384 || height > 16384 || width*height > maxPixels ||
		!lengthOK || length != int64(len(imageBytes)) || length > maxBytes {
		return invalid()
	}
	hash, hashOK := data["sha256"].(string)
	digest := sha256.Sum256(imageBytes)
	if !hashOK || hash != hex.EncodeToString(digest[:]) {
		return invalid()
	}
	config, err := png.DecodeConfig(bytes.NewReader(imageBytes))
	if err != nil || int64(config.Width) != width || int64(config.Height) != height {
		return invalid()
	}
	// Dimensions and compressed bytes are checked before allocating decoded
	// pixels. A valid PNG header alone is insufficient for an image response.
	if _, err := png.Decode(bytes.NewReader(imageBytes)); err != nil {
		return invalid()
	}
	metadata := make(bridge.Result, len(payload))
	for key, value := range payload {
		metadata[key] = value
	}
	metadataData := make(map[string]any, len(data)-1)
	for key, value := range data {
		if key != "png_base64" {
			metadataData[key] = value
		}
	}
	metadata["data"] = metadataData
	result := toolResult(metadata)
	// ImageContent.Data contains PNG bytes; the SDK encodes them as Base64 on
	// the wire and decodes them for clients. Never Base64-encode these twice.
	result.Content = append(result.Content, &mcp.ImageContent{Data: imageBytes, MIMEType: "image/png"})
	return result
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

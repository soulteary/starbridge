// Package bridge provides a bounded, argument-vector-only Parallels adapter.
package bridge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	DefaultGuestScript = `C:\ProgramData\Starbridge\desktop-bridge.ps1`
	AppPRLCTL          = "/Applications/Parallels Desktop.app/Contents/MacOS/prlctl"
	// MaxOutputBytes bounds the subprocess's stdout, including JSON responses.
	MaxOutputBytes = 8 * 1024 * 1024
)

type Result map[string]any

type Error struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *Error) Error() string { return e.Message }

func NewError(code, message string, details map[string]any) *Error {
	return &Error{Code: code, Message: message, Details: details}
}

func Success(data any) Result { return Result{"ok": true, "data": data} }

func Failure(err error) Result {
	var bridgeErr *Error
	if !errors.As(err, &bridgeErr) {
		message := "Unknown error."
		if err != nil {
			message = err.Error()
		}
		bridgeErr = NewError("EXECUTION_FAILED", message, nil)
	}
	return Result{"ok": false, "error": bridgeErr}
}

type Options struct {
	PRLCTL      string
	Timeout     time.Duration
	GuestScript string
	DryRun      bool
}

func DefaultOptions() Options {
	return Options{Timeout: 30 * time.Second, GuestScript: DefaultGuestScript}
}

type commandResult struct {
	stdout, stderr string
	exitCode       int
	outputExceeded bool
}

type commandRunner func(context.Context, string, []string) (commandResult, error)

type Bridge struct {
	options Options
	runner  commandRunner
	resolve func(string) (string, error)
}

var drivePath = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

func absoluteWindowsPath(path string) bool {
	if drivePath.MatchString(path) {
		return true
	}
	normalized := strings.ReplaceAll(path, "/", `\`)
	if !strings.HasPrefix(normalized, `\\`) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(normalized, `\\`), `\`)
	return len(parts) >= 2 && parts[0] != "" && parts[1] != ""
}

func New(options Options) (*Bridge, error) {
	if options.Timeout < time.Second || options.Timeout > 600*time.Second {
		return nil, NewError("INVALID_ARGUMENT", "timeout must be between 1 and 600 seconds.", nil)
	}
	if !absoluteWindowsPath(options.GuestScript) || strings.ContainsAny(options.GuestScript, "\r\n\x00\"") {
		return nil, NewError("INVALID_ARGUMENT", "guest-script must be an absolute Windows path without quotes.", nil)
	}
	return &Bridge{options: options, runner: runCommand, resolve: resolvePRLCTL}, nil
}

func configuredPRLCTL(explicit string) string {
	for _, candidate := range []string{explicit, os.Getenv("STARBRIDGE_PRLCTL"), os.Getenv("PRLBRIDGE_PRLCTL")} {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

func resolvePRLCTL(explicit string) (string, error) {
	if candidate := configuredPRLCTL(explicit); candidate != "" {
		found, err := exec.LookPath(candidate)
		if err != nil {
			return "", NewError("PRLCTL_NOT_FOUND", "Configured prlctl is not executable.", nil)
		}
		return found, nil
	}
	if info, err := os.Stat(AppPRLCTL); err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0 {
		return AppPRLCTL, nil
	}
	if found, err := exec.LookPath("prlctl"); err == nil {
		return found, nil
	}
	return "", NewError("PRLCTL_NOT_FOUND", "Install Parallels Desktop with CLI support or set STARBRIDGE_PRLCTL.", nil)
}

// cappedBuffer stops the child on overflow and never retains more than its cap.
type cappedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
	cancel   context.CancelFunc
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	count := len(p)
	remaining := b.limit - b.buffer.Len()
	if count > remaining {
		if remaining > 0 {
			_, _ = b.buffer.Write(p[:remaining])
		}
		b.exceeded = true
		b.cancel()
		return count, nil
	}
	_, _ = b.buffer.Write(p)
	return count, nil
}

type tailBuffer struct {
	data  []byte
	limit int
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	count := len(p)
	if count >= b.limit {
		b.data = append(b.data[:0], p[count-b.limit:]...)
		return count, nil
	}
	if len(b.data)+count > b.limit {
		b.data = b.data[len(b.data)+count-b.limit:]
	}
	b.data = append(b.data, p...)
	return count, nil
}

func runCommand(ctx context.Context, executable string, args []string) (commandResult, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stdout := &cappedBuffer{limit: MaxOutputBytes, cancel: cancel}
	stderr := &tailBuffer{limit: 4096}
	cmd := exec.CommandContext(runCtx, executable, args...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	// Bound waits on output pipes inherited by descendants of the child.
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	result := commandResult{stdout: decodeOutput(stdout.buffer.Bytes()), stderr: decodeOutput(stderr.data), outputExceeded: stdout.exceeded}
	if cmd.ProcessState != nil {
		result.exitCode = cmd.ProcessState.ExitCode()
	}
	if stdout.exceeded {
		return result, nil
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return result, nil
	}
	return result, err
}

func decodeOutput(data []byte) string {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	return strings.ToValidUTF8(string(data), "\ufffd")
}

func tail(text string, max int) string {
	runes := []rune(text)
	if len(runes) > max {
		return string(runes[len(runes)-max:])
	}
	return text
}

func decodeJSON(text string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var payload any
	if err := decoder.Decode(&payload); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values")
		}
		return nil, err
	}
	return payload, nil
}

func asObject(value any) (map[string]any, bool) {
	switch obj := value.(type) {
	case map[string]any:
		return obj, true
	case Result:
		return map[string]any(obj), true
	default:
		return nil, false
	}
}

func (b *Bridge) run(ctx context.Context, args []string, structured, mutation, guest bool) (any, error) {
	executable := configuredPRLCTL(b.options.PRLCTL)
	if b.options.DryRun {
		if executable == "" {
			executable = AppPRLCTL
		}
		argv := append([]string{executable}, args...)
		return map[string]any{"dry_run": true, "argv": argv, "mutation": mutation}, nil
	}
	var err error
	executable, err = b.resolve(b.options.PRLCTL)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, b.options.Timeout)
	defer cancel()
	result, err := b.runner(ctx, executable, args)
	outcome := "no_result"
	if mutation {
		outcome = "unknown"
	}
	if result.outputExceeded {
		return nil, NewError("OUTPUT_LIMIT_EXCEEDED", "Host command exceeded the output limit; inspect state before retrying.", map[string]any{"max_output_bytes": MaxOutputBytes, "outcome": outcome})
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, NewError("TIMEOUT", "Host command timed out; guest or VM work may still be running. Inspect state before retrying.", map[string]any{"timeout_seconds": b.options.Timeout.Seconds(), "outcome": outcome})
		}
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return nil, NewError("INTERRUPTED", "Interrupted; guest or VM work may still be running. Inspect state before retrying a mutation.", map[string]any{"outcome": outcome})
		}
		return nil, NewError("EXECUTION_FAILED", err.Error(), nil)
	}
	if guest {
		if payload, parseErr := decodeJSON(result.stdout); parseErr == nil {
			if object, objectOK := asObject(payload); objectOK {
				ok, typed := object["ok"].(bool)
				_, hasData := object["data"]
				_, hasError := asObject(object["error"])
				if typed && ((ok && hasData) || (!ok && hasError)) {
					if result.exitCode != 0 && ok {
						return nil, NewError("GUEST_EXIT_MISMATCH", "Guest reported success but exited with an error.", map[string]any{"exit_code": result.exitCode})
					}
					return Result(object), nil
				}
			}
		}
	}
	if result.exitCode != 0 {
		return nil, NewError("PRLCTL_FAILED", "Parallels command failed.", map[string]any{"exit_code": result.exitCode, "stderr": tail(result.stderr, 4096), "stdout": tail(result.stdout, 4096)})
	}
	if structured || guest {
		payload, parseErr := decodeJSON(result.stdout)
		if parseErr != nil {
			return nil, NewError("INVALID_JSON", "Expected JSON output. Check CLI version, Parallels Tools and guest helper installation.", map[string]any{"stdout": tail(result.stdout, 2048), "stderr": tail(result.stderr, 2048)})
		}
		if guest {
			return nil, NewError("INVALID_GUEST_RESPONSE", "Guest response must contain a boolean ok and data or error.", nil)
		}
		return payload, nil
	}
	return map[string]any{"command_completed": true, "stdout": strings.TrimSpace(result.stdout)}, nil
}

func identifier(value, label string) error {
	if strings.TrimSpace(value) == "" || strings.HasPrefix(value, "-") {
		return NewError("INVALID_ARGUMENT", label+" must be nonempty and cannot start with '-'.", nil)
	}
	if utf8.RuneCountInString(value) > 512 {
		return NewError("INVALID_ARGUMENT", "Invalid "+label+".", nil)
	}
	for _, r := range value {
		if r < 32 {
			return NewError("INVALID_ARGUMENT", "Invalid "+label+".", nil)
		}
	}
	return nil
}

// integral deliberately rejects floating-point types: protocol 1.0 is not an integer.
func integral(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int8:
		return int64(v), true
	case int16:
		return int64(v), true
	case int32:
		return int64(v), true
	case int64:
		return v, true
	case uint:
		if uint64(v) <= uint64(^uint64(0)>>1) {
			return int64(v), true
		}
	case uint8:
		return int64(v), true
	case uint16:
		return int64(v), true
	case uint32:
		return int64(v), true
	case uint64:
		if v <= uint64(^uint64(0)>>1) {
			return int64(v), true
		}
	case json.Number:
		n, err := strconv.ParseInt(string(v), 10, 64)
		return n, err == nil
	}
	return 0, false
}

func boundedInt(value any, min, max int64, label string) error {
	if n, ok := integral(value); !ok || n < min || n > max {
		return NewError("INVALID_ARGUMENT", fmt.Sprintf("%s must be an integer in [%d, %d].", label, min, max), nil)
	}
	return nil
}

func requireConfirmation(vm, confirmation string) error {
	if vm != confirmation {
		return NewError("CONFIRMATION_REQUIRED", "Repeat the exact VM identifier with --confirm.", map[string]any{"vm": vm})
	}
	return nil
}

func (b *Bridge) Doctor(ctx context.Context) (any, error) {
	result, err := b.run(ctx, []string{"--version"}, false, false, false)
	if err != nil {
		return nil, err
	}
	return map[string]any{"host": result, "guest_script": b.options.GuestScript, "requirements": []string{"Parallels CLI license support", "Parallels Tools in Windows", "Windows PowerShell 5.1", "logged-in interactive Windows user for UI inspection"}, "guest_verified": false}, nil
}

func (b *Bridge) VMList(ctx context.Context) (any, error) {
	return b.run(ctx, []string{"list", "-a", "-j"}, true, false, false)
}
func (b *Bridge) VMInfo(ctx context.Context, vm string) (any, error) {
	if err := identifier(vm, "VM"); err != nil {
		return nil, err
	}
	return b.run(ctx, []string{"list", "-i", "-j", vm}, true, false, false)
}

func (b *Bridge) VMAction(ctx context.Context, action, vm string, force bool, confirmation string) (any, error) {
	if err := identifier(vm, "VM"); err != nil {
		return nil, err
	}
	if action != "start" && action != "stop" && action != "suspend" && action != "resume" && action != "delete" {
		return nil, NewError("INVALID_ARGUMENT", "Unsupported VM action.", nil)
	}
	if force && action != "stop" {
		return nil, NewError("INVALID_ARGUMENT", "force is only valid for stop.", nil)
	}
	if (action == "delete" || force) && !b.options.DryRun {
		if err := requireConfirmation(vm, confirmation); err != nil {
			return nil, err
		}
	}
	args := []string{action, vm}
	if force {
		args = append(args, "--kill")
	}
	return b.run(ctx, args, false, true, false)
}

func (b *Bridge) VMClone(ctx context.Context, vm, name string) (any, error) {
	if err := identifier(vm, "VM"); err != nil {
		return nil, err
	}
	if err := identifier(name, "clone name"); err != nil {
		return nil, err
	}
	return b.run(ctx, []string{"clone", vm, "--name", name}, false, true, false)
}

func (b *Bridge) SnapshotList(ctx context.Context, vm string) (any, error) {
	if err := identifier(vm, "VM"); err != nil {
		return nil, err
	}
	return b.run(ctx, []string{"snapshot-list", vm, "-j"}, true, false, false)
}

func (b *Bridge) SnapshotCreate(ctx context.Context, vm, name string) (any, error) {
	if err := identifier(vm, "VM"); err != nil {
		return nil, err
	}
	if err := identifier(name, "snapshot name"); err != nil {
		return nil, err
	}
	return b.run(ctx, []string{"snapshot", vm, "--name", name}, false, true, false)
}

func (b *Bridge) SnapshotRestore(ctx context.Context, vm, snapshot, confirmation string) (any, error) {
	if err := identifier(vm, "VM"); err != nil {
		return nil, err
	}
	if !b.options.DryRun {
		if err := requireConfirmation(vm, confirmation); err != nil {
			return nil, err
		}
	}
	if err := identifier(snapshot, "snapshot ID"); err != nil {
		return nil, err
	}
	return b.run(ctx, []string{"snapshot-switch", vm, "--id", snapshot, "--skip-resume"}, false, true, false)
}

type RecycleOptions struct{ PolicyFile, Mode, Snapshot, Confirmation string }

func validPolicy(value any) bool {
	policy, ok := asObject(value)
	if !ok {
		return false
	}
	for key := range policy {
		if key != "mode" && key != "snapshot" {
			return false
		}
	}
	mode, _ := policy["mode"].(string)
	if mode != "stop" && mode != "restore" && mode != "delete" {
		return false
	}
	if snapshot, exists := policy["snapshot"]; exists {
		text, ok := snapshot.(string)
		if !ok || strings.TrimSpace(text) == "" || mode != "restore" {
			return false
		}
	}
	if mode == "restore" {
		_, hasSnapshot := policy["snapshot"]
		return hasSnapshot
	}
	return true
}

func (b *Bridge) Recycle(ctx context.Context, vm string, options RecycleOptions) (any, error) {
	if err := identifier(vm, "VM"); err != nil {
		return nil, err
	}
	policy := map[string]any{"mode": "stop"}
	source := "built-in default"
	if options.PolicyFile != "" {
		file, err := os.Open(options.PolicyFile)
		if err != nil {
			return nil, NewError("INVALID_POLICY", "Could not read recycle policy JSON.", nil)
		}
		data, readErr := io.ReadAll(io.LimitReader(file, MaxOutputBytes+1))
		_ = file.Close()
		if readErr != nil || len(data) > MaxOutputBytes {
			return nil, NewError("INVALID_POLICY", "Could not read recycle policy JSON.", nil)
		}
		payload, err := decodeJSON(string(data))
		if err != nil {
			return nil, NewError("INVALID_POLICY", "Could not read recycle policy JSON.", nil)
		}
		config, ok := asObject(payload)
		if !ok {
			return nil, NewError("INVALID_POLICY", "Policy must contain only default and vms objects.", nil)
		}
		for key := range config {
			if key != "default" && key != "vms" {
				return nil, NewError("INVALID_POLICY", "Policy must contain only default and vms objects.", nil)
			}
		}
		defaults := map[string]any{"mode": "stop"}
		if value, exists := config["default"]; exists {
			defaults, ok = asObject(value)
			if !ok {
				return nil, NewError("INVALID_POLICY", "default and vms must be JSON objects.", nil)
			}
		}
		overrides := map[string]any{}
		if value, exists := config["vms"]; exists {
			overrides, ok = asObject(value)
			if !ok {
				return nil, NewError("INVALID_POLICY", "default and vms must be JSON objects.", nil)
			}
		}
		if !validPolicy(defaults) {
			return nil, NewError("INVALID_POLICY", "Each policy needs mode stop/restore/delete; restore also needs snapshot.", nil)
		}
		for _, entry := range overrides {
			if !validPolicy(entry) {
				return nil, NewError("INVALID_POLICY", "Each policy needs mode stop/restore/delete; restore also needs snapshot.", nil)
			}
		}
		selected := defaults
		source = "policy default"
		if entry, exists := overrides[vm]; exists {
			selected, _ = asObject(entry)
			source = "per-VM policy"
		}
		policy = make(map[string]any, len(selected))
		for key, value := range selected {
			policy[key] = value
		}
	}
	if options.Mode != "" {
		if options.Mode != "stop" && options.Mode != "restore" && options.Mode != "delete" {
			return nil, NewError("INVALID_ARGUMENT", "mode must be stop, restore or delete.", nil)
		}
		if policy["mode"] != options.Mode {
			delete(policy, "snapshot")
		}
		policy["mode"] = options.Mode
		source = "command override"
	}
	if options.Snapshot != "" {
		if err := identifier(options.Snapshot, "snapshot ID"); err != nil {
			return nil, err
		}
		policy["snapshot"] = options.Snapshot
	}
	mode := policy["mode"].(string)
	if _, exists := policy["snapshot"]; exists && mode != "restore" {
		return nil, NewError("INVALID_ARGUMENT", "snapshot is only valid for recycle mode restore.", nil)
	}
	var result any
	var err error
	if mode == "restore" {
		snapshot, _ := policy["snapshot"].(string)
		if snapshot == "" {
			return nil, NewError("INVALID_ARGUMENT", "restore requires a snapshot ID in policy or --snapshot.", nil)
		}
		result, err = b.SnapshotRestore(ctx, vm, snapshot, options.Confirmation)
	} else {
		result, err = b.VMAction(ctx, mode, vm, false, options.Confirmation)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"vm": vm, "policy": policy, "policy_source": source, "result": result}, nil
}

var hwndPattern = regexp.MustCompile(`^(?:[0-9]+|0[xX][0-9a-fA-F]+)$`)

func validateGuest(action string, params map[string]any) error {
	allowed := map[string]map[string]bool{
		"processes.list": {"pid": true, "name": true, "limit": true, "include_path": true},
		"windows.list":   {"pid": true, "include_hidden": true, "limit": true},
		"ui.tree":        {"hwnd": true, "max_depth": true, "max_nodes": true, "timeout_ms": true},
	}
	keys, known := allowed[action]
	if !known {
		return NewError("INVALID_ARGUMENT", "Unsupported guest action or parameters.", nil)
	}
	for key := range params {
		if !keys[key] {
			return NewError("INVALID_ARGUMENT", "Unsupported guest action or parameters.", nil)
		}
	}
	for _, bound := range []struct {
		key      string
		min, max int64
	}{{"pid", 1, 2147483647}, {"limit", 1, 1000}} {
		if value, exists := params[bound.key]; exists {
			if err := boundedInt(value, bound.min, bound.max, bound.key); err != nil {
				return err
			}
		}
	}
	if value, exists := params["name"]; exists {
		name, ok := value.(string)
		if !ok || utf8.RuneCountInString(name) > 256 {
			return NewError("INVALID_ARGUMENT", "name must be a string of at most 256 characters.", nil)
		}
	}
	for _, key := range []string{"include_hidden", "include_path"} {
		if value, exists := params[key]; exists {
			if _, ok := value.(bool); !ok {
				return NewError("INVALID_ARGUMENT", key+" must be a boolean.", nil)
			}
		}
	}
	if action == "ui.tree" {
		hwnd, ok := params["hwnd"].(string)
		valid := ok && hwndPattern.MatchString(hwnd)
		if valid {
			base, text := 10, hwnd
			if strings.HasPrefix(strings.ToLower(hwnd), "0x") {
				base, text = 16, hwnd[2:]
			}
			number, err := strconv.ParseUint(text, base, 63)
			valid = err == nil && number > 0
		}
		if !valid {
			return NewError("INVALID_ARGUMENT", "hwnd must be a positive decimal or hexadecimal string.", nil)
		}
		for _, bound := range []struct {
			key                    string
			defaultValue, min, max int64
		}{{"max_depth", 6, 0, 12}, {"max_nodes", 300, 1, 1000}, {"timeout_ms", 10000, 1000, 20000}} {
			value, exists := params[bound.key]
			if !exists {
				value = bound.defaultValue
			}
			if err := boundedInt(value, bound.min, bound.max, bound.key); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *Bridge) Guest(ctx context.Context, vm, action string, params map[string]any) (Result, error) {
	if err := identifier(vm, "VM"); err != nil {
		return nil, err
	}
	copied := make(map[string]any, len(params))
	for key, value := range params {
		copied[key] = value
	}
	if err := validateGuest(action, copied); err != nil {
		return nil, err
	}
	request, err := json.Marshal(map[string]any{"action": action, "params": copied})
	if err != nil {
		return nil, NewError("INVALID_ARGUMENT", "Could not encode guest request.", nil)
	}
	encoded := base64.StdEncoding.EncodeToString(request)
	value, err := b.run(ctx, []string{"exec", vm, "--current-user", "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", b.options.GuestScript, "-RequestBase64", encoded}, false, false, true)
	if err != nil {
		return nil, err
	}
	if b.options.DryRun {
		return Success(value), nil
	}
	result := value.(Result)
	if result["ok"] == true {
		meta, ok := asObject(result["meta"])
		session, validSession := integral(meta["session_id"])
		interactive, validInteractive := meta["user_interactive"].(bool)
		if !ok || meta["protocol_version"] != "1" || meta["action"] != action || !validSession || session < 0 || !validInteractive {
			return nil, NewError("GUEST_PROTOCOL_MISMATCH", "Install the matching guest helper; response metadata is missing or incompatible.", nil)
		}
		if (action == "windows.list" || action == "ui.tree") && (session == 0 || !interactive) {
			return nil, NewError("INTERACTIVE_SESSION_REQUIRED", "Windows UI queries require the logged-in user's interactive session.", nil)
		}
	}
	return result, nil
}

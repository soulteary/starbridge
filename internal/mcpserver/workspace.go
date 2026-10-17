package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/soulteary/starbridge/internal/bridge"
)

type developerWorkspace struct {
	root, configured string
	gate             chan struct{}
}

func workspaceDenied() error {
	return bridge.NewError("WORKSPACE_ACCESS_DENIED", "Host paths must stay inside --developer-workspace without parent traversal or symbolic links; local composite actions require an explicit supported policy.", nil)
}

func newDeveloperWorkspace(path string) (*developerWorkspace, error) {
	w := &developerWorkspace{gate: make(chan struct{}, 1)}
	if path == "" {
		return w, nil
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, workspaceDenied()
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, workspaceDenied()
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, workspaceDenied()
	}
	w.root, w.configured = resolved, absolute
	return w, nil
}

func (w *developerWorkspace) acquire(ctx context.Context) (func(), error) {
	if ctx.Err() != nil {
		return nil, queuedError(ctx)
	}
	select {
	case w.gate <- struct{}{}:
		if ctx.Err() != nil {
			<-w.gate
			return nil, queuedError(ctx)
		}
		return func() { <-w.gate }, nil
	case <-ctx.Done():
		return nil, queuedError(ctx)
	}
}

func pathInside(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	return rel, err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// Resolve only the configured root's OS ancestors (e.g. macOS /var). Every
// component below that root, including existing output leaves, rejects symlinks.
func (w *developerWorkspace) path(value any, kind string, missing bool) (string, error) {
	path, ok := value.(string)
	if !ok || w.root == "" || !filepath.IsAbs(path) || len(path) > 4096 || strings.ContainsAny(path, "\x00\r\n") {
		return "", workspaceDenied()
	}
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return "", workspaceDenied()
		}
	}
	clean := filepath.Clean(path)
	if clean != path {
		return "", workspaceDenied()
	}
	rel, inside := pathInside(w.configured, clean)
	if !inside {
		rel, inside = pathInside(w.root, clean)
	}
	if !inside {
		return "", workspaceDenied()
	}
	current := w.root
	parts := []string{}
	if rel != "." {
		parts = strings.Split(rel, string(filepath.Separator))
	}
	// The root itself may have been replaced since server startup.
	info, err := os.Lstat(current)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", workspaceDenied()
	}
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err = os.Lstat(current)
		if os.IsNotExist(err) && missing {
			return filepath.Join(w.root, rel), nil
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return "", workspaceDenied()
		}
		if i < len(parts)-1 && !info.IsDir() {
			return "", workspaceDenied()
		}
	}
	if (kind == "file" && !info.Mode().IsRegular()) || (kind == "directory" && !info.IsDir()) {
		return "", workspaceDenied()
	}
	return current, nil
}

func (w *developerWorkspace) field(params map[string]any, key, kind string, missing bool) error {
	path, err := w.path(params[key], kind, missing)
	if err != nil {
		return err
	}
	params[key] = path
	return nil
}

func (w *developerWorkspace) tree(path string) error {
	count := 0
	return filepath.WalkDir(path, func(current string, entry os.DirEntry, err error) error {
		count++
		if err != nil || count > 10000 || entry.Type()&os.ModeSymlink != 0 {
			return workspaceDenied()
		}
		if !entry.IsDir() {
			info, e := entry.Info()
			if e != nil || !info.Mode().IsRegular() {
				return workspaceDenied()
			}
		}
		return nil
	})
}

func supportedLocalAction(action string) bool {
	switch action {
	case "wait.until", "assert.matches", "files.upload", "files.download", "files.sync", "bootstrap.install", "bootstrap.rollback", "evidence.capture", "workflow.run", "workflow.cleanup":
		return true
	}
	return false
}

func hostFileAction(action string) bool {
	switch action {
	case "files.upload", "files.download", "files.sync", "bootstrap.install", "evidence.capture", "workflow.run", "workflow.cleanup":
		return true
	}
	return false
}

func actionSpec(options ServerOptions, name string) (bridge.DeveloperAction, bool) {
	for _, spec := range options.actions() {
		if spec.Name == name {
			return spec, true
		}
	}
	return bridge.DeveloperAction{}, false
}

// validate returns every nested tool grant a composite action will need. It
// deliberately uses generic JSON rather than types from optional feature PRs.
func (w *developerWorkspace) validate(options ServerOptions, action string, p map[string]any, vm string, depth int) ([]string, error) {
	if depth > 8 {
		return nil, workspaceDenied()
	}
	legacy := map[string]string{"vm.info": "vm_info", "vm.snapshot-list": "vm_snapshot_list", "processes.list": "guest_processes", "windows.list": "guest_windows", "ui.tree": "guest_ui_tree"}
	if name, ok := legacy[action]; ok {
		if strings.HasPrefix(action, "vm.") && len(p) != 0 {
			return nil, accessDenied()
		}
		return []string{name}, nil
	}
	switch action {
	case "vm.start", "vm.stop", "vm.suspend", "vm.resume":
		if !options.AllowVMWrite || len(p) != 0 {
			return nil, accessDenied()
		}
		return []string{"vm_" + strings.TrimPrefix(action, "vm.")}, nil
	case "vm.recycle":
		mode, present := p["mode"]
		if !options.AllowVMWrite || !onlyFields(p, "mode") || (present && mode != "stop") {
			return nil, accessDenied()
		}
		// Restore/delete and all other destructive VM actions stay outside MCP.
		return []string{"vm_stop"}, nil
	}
	spec, known := actionSpec(options, action)
	if !known || (spec.Mutation && !options.AllowGuestWrite) || (spec.Execute != nil && !supportedLocalAction(action)) {
		return nil, accessDenied()
	}
	permissions := []string{developerToolName(action)}
	if spec.Execute == nil {
		return permissions, nil
	}
	var err error
	switch action {
	case "files.upload", "files.download":
		err = w.field(p, "local_path", "file", action == "files.download")
	case "bootstrap.install":
		if err = w.field(p, "source_dir", "directory", false); err == nil {
			err = w.tree(p["source_dir"].(string))
		}
	case "bootstrap.rollback": // target_dir is a Windows path, not a host path.
	case "files.sync":
		if err = w.field(p, "profile", "file", false); err != nil {
			break
		}
		var profile map[string]any
		profile, err = readWorkspaceJSON(p["profile"].(string))
		if err != nil {
			break
		}
		if !onlyFields(profile, "source", "root", "path", "exclude", "overwrite") {
			err = workspaceDenied()
			break
		}
		source, ok := profile["source"].(string)
		if !ok || source == "" || filepath.IsAbs(source) || strings.Contains(source, `\`) {
			err = workspaceDenied()
			break
		}
		for _, part := range strings.Split(source, "/") {
			if part == ".." || part == "" {
				err = workspaceDenied()
				break
			}
		}
		if err != nil {
			break
		}
		var scoped string
		scoped, err = w.path(filepath.Join(filepath.Dir(p["profile"].(string)), filepath.FromSlash(source)), "directory", false)
		if err == nil {
			err = w.tree(scoped)
		}
	case "evidence.capture":
		if err = w.field(p, "output_directory", "directory", true); err != nil {
			break
		}
		observations, ok := p["observations"].([]any)
		if !ok || len(observations) < 1 || len(observations) > 16 {
			err = workspaceDenied()
			break
		}
		for _, value := range observations {
			item, ok := value.(map[string]any)
			if !ok || !onlyFields(item, "name", "action", "params") {
				err = workspaceDenied()
				break
			}
			child, ok := item["action"].(string)
			if !ok || !readObservation(options, child) {
				err = accessDenied()
				break
			}
			params, ok := optionalParams(item)
			if !ok {
				err = workspaceDenied()
				break
			}
			var grants []string
			grants, err = w.validate(options, child, params, vm, depth+1)
			if err != nil {
				break
			}
			permissions = append(permissions, grants...)
		}
	case "wait.until", "assert.matches":
		child, ok := p["action"].(string)
		if !ok || !readObservation(options, child) {
			err = accessDenied()
			break
		}
		params, ok := optionalParams(p)
		if !ok {
			err = workspaceDenied()
			break
		}
		var grants []string
		grants, err = w.validate(options, child, params, vm, depth+1)
		permissions = append(permissions, grants...)
	case "workflow.run", "workflow.cleanup":
		if err = w.field(p, "profile_file", "file", false); err != nil {
			break
		}
		if err = w.field(p, "output_directory", "directory", true); err != nil {
			break
		}
		var grants []string
		grants, err = w.workflow(options, p["profile_file"].(string), vm, depth+1, action == "workflow.cleanup")
		permissions = append(permissions, grants...)
	default:
		err = workspaceDenied()
	}
	return permissions, err
}

func readObservation(options ServerOptions, action string) bool {
	switch action {
	case "vm.info", "processes.list", "windows.list", "ui.tree":
		return true
	}
	spec, ok := actionSpec(options, action)
	return ok && !spec.Mutation && spec.Execute == nil
}

func optionalParams(object map[string]any) (map[string]any, bool) {
	value, present := object["params"]
	if !present {
		return map[string]any{}, true
	}
	params, ok := value.(map[string]any)
	if !ok || params == nil || params["$ref"] != nil {
		return nil, false
	}
	return params, true
}

func onlyFields(object map[string]any, fields ...string) bool {
	allowed := map[string]bool{}
	for _, field := range fields {
		allowed[field] = true
	}
	for field := range object {
		if !allowed[field] {
			return false
		}
	}
	return true
}

func (w *developerWorkspace) workflow(options ServerOptions, path, vm string, depth int, cleanupOnly bool) ([]string, error) {
	profile, err := readWorkspaceJSON(path)
	if err != nil {
		return nil, err
	}
	if !onlyFields(profile, "version", "vm", "developer_script", "timeout_seconds", "steps", "cleanup") || profile["version"] != json.Number("1") {
		return nil, workspaceDenied()
	}
	if script, present := profile["developer_script"]; present && script != "" {
		return nil, accessDenied()
	}
	if timeout, present := profile["timeout_seconds"]; present {
		number, ok := timeout.(json.Number)
		if !ok {
			return nil, workspaceDenied()
		}
		n, err := number.Int64()
		if err != nil || n < 0 || n > 600 {
			return nil, workspaceDenied()
		}
	}
	target, ok := profile["vm"].(string)
	expected, valid := canonicalAccessUUID(vm)
	actual, targetValid := canonicalAccessUUID(target)
	if !ok || !valid || !targetValid || expected != actual {
		return nil, accessDenied()
	}
	steps, ok := profile["steps"].([]any)
	if !ok || len(steps) < 1 || len(steps) > 32 {
		return nil, workspaceDenied()
	}
	cleanup, hasCleanup := profile["cleanup"]
	if cleanupOnly && (!hasCleanup || cleanup == nil) {
		return nil, workspaceDenied()
	}
	if hasCleanup && cleanup != nil {
		object, ok := cleanup.(map[string]any)
		if !ok || object["action"] != "vm.recycle" {
			return nil, accessDenied()
		}
		steps = append(append([]any{}, steps...), cleanup)
	}
	permissions := []string{}
	seen := map[string]bool{}
	for index, value := range steps {
		step, ok := value.(map[string]any)
		if !ok || !onlyFields(step, "name", "action", "params") {
			return nil, workspaceDenied()
		}
		// Cleanup has no required stage name; ordinary stages must be unique.
		if index < len(profile["steps"].([]any)) {
			name, ok := step["name"].(string)
			if !ok || !workspaceStepName.MatchString(name) || seen[name] {
				return nil, workspaceDenied()
			}
			seen[name] = true
		}
		action, ok := step["action"].(string)
		if !ok || strings.HasPrefix(action, "workflow.") {
			return nil, accessDenied()
		}
		if cleanupOnly && index < len(profile["steps"].([]any)) {
			// Retain structural checks, but these stages are not executed during
			// cleanup and must not require their grants or filesystem access.
			if raw, present := step["params"]; present && raw != nil {
				if _, ok := raw.(map[string]any); !ok {
					return nil, workspaceDenied()
				}
			}
			continue
		}
		params, ok := optionalParams(step)
		if !ok {
			return nil, workspaceDenied()
		}
		grants, err := w.validate(options, action, params, vm, depth+1)
		if err != nil {
			return nil, err
		}
		permissions = append(permissions, grants...)
	}
	return permissions, nil
}

var workspaceStepName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// Recursive token decoding rejects duplicate (including escaped) object keys.
// Params remain generic so optional feature packages are not compile dependencies.
func readWorkspaceJSON(path string) (map[string]any, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 || before.Size() > 65536 {
		return nil, workspaceDenied()
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, workspaceDenied()
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 65536 || !os.SameFile(before, info) {
		return nil, workspaceDenied()
	}
	data, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(data) > 65536 || !utf8.Valid(data) {
		return nil, workspaceDenied()
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := uniqueWorkspaceValue(decoder, 0)
	if err != nil {
		return nil, workspaceDenied()
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, workspaceDenied()
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, workspaceDenied()
	}
	return object, nil
}

func uniqueWorkspaceValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > 32 {
		return nil, fmt.Errorf("depth")
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		object := map[string]any{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			key, ok := keyToken.(string)
			if err != nil || !ok {
				return nil, fmt.Errorf("key")
			}
			if _, exists := object[key]; exists {
				return nil, fmt.Errorf("duplicate")
			}
			value, err := uniqueWorkspaceValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		_, err = decoder.Token()
		return object, err
	case json.Delim('['):
		array := []any{}
		for decoder.More() {
			value, err := uniqueWorkspaceValue(decoder, depth+1)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		_, err = decoder.Token()
		return array, err
	default:
		return token, nil
	}
}

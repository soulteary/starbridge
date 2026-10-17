package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/soulteary/starbridge/internal/bridge"
)

type developerFixture struct {
	stubBackend
	mu                 sync.Mutex
	calls, inventories int
	vm, action         string
	params             map[string]any
	write              bool
	invoke             func(context.Context, string, string, map[string]any, bool) (bridge.Result, error)
}

func (f *developerFixture) VMList(context.Context) (any, error) {
	f.mu.Lock()
	f.inventories++
	f.mu.Unlock()
	return []any{map[string]any{"uuid": accessUUIDA, "name": "sandbox"}, map[string]any{"uuid": accessUUIDB, "name": "other"}}, nil
}
func (f *developerFixture) Developer(ctx context.Context, vm, action string, p map[string]any, write bool) (bridge.Result, error) {
	f.mu.Lock()
	f.calls++
	f.vm, f.action, f.params, f.write = vm, action, p, write
	f.mu.Unlock()
	if f.invoke != nil {
		return f.invoke(ctx, vm, action, p, write)
	}
	return bridge.Success(map[string]any{"action": action, "vm": vm}), nil
}
func (f *developerFixture) DoctorVM(_ context.Context, vm string) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return map[string]any{"vm": vm, "ready": false}, nil
}
func (f *developerFixture) SnapshotList(_ context.Context, vm string) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return []any{map[string]any{"vm": vm, "name": "synthetic"}}, nil
}

func syntheticDeveloperActions() []bridge.DeveloperAction {
	identity := func(p map[string]any) (map[string]any, error) { return p, nil }
	validate := func(map[string]any) bool { return true }
	local := func(context.Context, *bridge.Bridge, string, map[string]any, bool) (bridge.Result, error) {
		return nil, nil
	}
	actions := bridge.DeveloperActions()
	for _, name := range []string{"ui.query", "ui.act", "task.start", "task.status", "task.output", "screenshot.capture", "file.read"} {
		actions = append(actions, bridge.DeveloperAction{Name: name, Normalize: identity, ValidateResult: validate, Mutation: name == "ui.act" || name == "task.start", Interactive: strings.HasPrefix(name, "ui.")})
	}
	for _, name := range []string{"files.upload", "files.download", "files.sync", "bootstrap.install", "bootstrap.rollback", "evidence.capture", "workflow.run", "workflow.cleanup", "wait.until", "assert.matches", "future.host"} {
		actions = append(actions, bridge.DeveloperAction{Name: name, Normalize: identity, Execute: local, Mutation: name != "wait.until" && name != "assert.matches"})
	}
	return actions
}

func connectDeveloper(t *testing.T, backend Backend, options ServerOptions) (*mcp.ClientSession, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	server, err := NewWithOptions(backend, options)
	if err != nil {
		t.Fatal(err)
	}
	a, b := mcp.NewInMemoryTransports()
	session, err := server.Connect(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	client, err := mcp.NewClient(&mcp.Implementation{Name: "synthetic developer client"}, nil).Connect(ctx, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client, ctx
}
func devOptions(root string) ServerOptions {
	return ServerOptions{DeveloperTools: true, AllowGuestWrite: true, DeveloperWorkspace: root, developerActions: syntheticDeveloperActions()}
}
func writeSyntheticFile(t *testing.T, root, name, content string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func devArgs(vm string, p map[string]any) map[string]any {
	return map[string]any{"vm": vm, "params": p}
}

func TestDevelopmentOptInCatalogAndOptionalBackends(t *testing.T) {
	f := &developerFixture{}
	for _, write := range []bool{false, true} {
		session, ctx := connectDeveloper(t, f, ServerOptions{AllowVMWrite: write})
		catalog, err := session.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := 5
		if write {
			want = 9
		}
		if len(catalog.Tools) != want {
			t.Fatalf("default count=%d", len(catalog.Tools))
		}
	}
	session, ctx := connectDeveloper(t, &stubBackend{}, ServerOptions{DeveloperTools: true})
	catalog, err := session.ListTools(ctx, nil)
	if err != nil || len(catalog.Tools) != 5 {
		t.Fatalf("old backend compatibility: %v %v", catalog, err)
	}
	options := devOptions("")
	options.AllowGuestWrite = false
	session, ctx = connectDeveloper(t, f, options)
	catalog, err = session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range catalog.Tools {
		names[tool.Name] = true
		if tool.Name == "dev.ui.query" && (!tool.Annotations.ReadOnlyHint || !*tool.Annotations.OpenWorldHint) {
			t.Fatal("UI query hints do not match registration")
		}
	}
	for _, name := range []string{"doctor_vm", "vm_snapshot_list", "dev.system.info", "dev.ui.query", "dev.wait.until"} {
		if !names[name] {
			t.Fatal("missing tool " + name)
		}
	}
	for _, name := range []string{"dev.ui.act", "dev.task.start", "dev.future.host", "vm_start"} {
		if names[name] {
			t.Fatal("unexpected tool " + name)
		}
	}
	options.AllowGuestWrite = true
	session, ctx = connectDeveloper(t, f, options)
	catalog, err = session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range catalog.Tools {
		if tool.Name == "dev.ui.act" && (tool.Annotations.ReadOnlyHint || !*tool.Annotations.DestructiveHint || tool.Annotations.IdempotentHint) {
			t.Fatal("mutation hints are wrong")
		}
	}
}

func TestDevelopmentCallsOptionalMethodsAndExtendedGuestParams(t *testing.T) {
	f := &developerFixture{}
	var observed map[string]any
	var action string
	f.guest = func(_ context.Context, _ string, a string, p map[string]any) (bridge.Result, error) {
		action, observed = a, p
		return bridge.Success(map[string]any{}), nil
	}
	session, ctx := connectDeveloper(t, f, devOptions(""))
	for _, name := range []string{"doctor_vm", "vm_snapshot_list"} {
		if payload := envelope(t, call(t, session, ctx, name, map[string]any{"vm": "sandbox"})); payload["ok"] != true {
			t.Fatal(payload)
		}
	}
	for _, test := range []struct {
		name, field, action string
		value               any
	}{
		{"guest_processes", "include_path", "processes.list", true}, {"guest_windows", "include_hidden", "windows.list", true}, {"guest_ui_tree", "timeout_ms", "ui.tree", json.Number("1000")},
	} {
		args := map[string]any{"vm": "sandbox", test.field: test.value}
		if test.name == "guest_ui_tree" {
			args["hwnd"] = "0x1"
		}
		if payload := envelope(t, call(t, session, ctx, test.name, args)); payload["ok"] != true || action != test.action || observed[test.field] == nil {
			t.Fatal(payload, observed)
		}
	}
	if payload := envelope(t, call(t, session, ctx, "dev.system.info", map[string]any{"vm": "sandbox"})); payload["ok"] != true || f.action != "system.info" {
		t.Fatal(payload)
	}
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "vm_delete", Arguments: map[string]any{"vm": "sandbox"}}); err == nil {
		t.Fatal("destructive VM tool exposed")
	}
}

func TestDeveloperNormalizeAndInputValidationBeforeBackend(t *testing.T) {
	f := &developerFixture{}
	session, ctx := connectDeveloper(t, f, devOptions(""))
	for _, args := range []any{
		map[string]any{"vm": "sandbox", "params": nil}, map[string]any{"vm": "sandbox", "params": []any{}}, map[string]any{"vm": "sandbox", "extra": true},
		devArgs("-bad", nil), devArgs("", map[string]any{}), devArgs("sandbox", map[string]any{"unexpected": true}),
	} {
		requireCode(t, envelope(t, call(t, session, ctx, "dev.system.info", args)), "INVALID_ARGUMENT")
	}
	if f.calls != 0 || f.inventories != 0 {
		t.Fatal("invalid input invoked backend")
	}
}

func TestWorkspacePathsRejectBeforeBackend(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	source := writeSyntheticFile(t, root, "source.txt", "synthetic")
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, filepath.Join(root, "leaf-link")); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		params map[string]any
	}{
		{"files.upload", map[string]any{"local_path": filepath.Join(outside, "source.txt"), "path": `C:\not-a-host-path`}},
		{"files.download", map[string]any{"local_path": filepath.Join(root, "link", "escaped.txt")}},
		{"files.download", map[string]any{"local_path": root + string(filepath.Separator) + ".." + string(filepath.Separator) + "escaped.txt"}},
		{"files.upload", map[string]any{"local_path": filepath.Join(root, "leaf-link")}},
		{"evidence.capture", map[string]any{"output_directory": filepath.Join(root, "link"), "observations": []any{map[string]any{"name": "info", "action": "system.info"}}}},
		{"bootstrap.install", map[string]any{"source_dir": root, "target_dir": `C:\Starbridge`}}, // nested symlinks are rejected
	} {
		f := &developerFixture{}
		session, ctx := connectDeveloper(t, f, devOptions(root))
		requireCode(t, envelope(t, call(t, session, ctx, developerToolName(test.name), devArgs(accessUUIDA, test.params))), "WORKSPACE_ACCESS_DENIED")
		if f.calls != 0 || f.inventories != 0 {
			t.Fatal("rejected path invoked backend")
		}
	}
	f := &developerFixture{}
	session, ctx := connectDeveloper(t, f, devOptions(""))
	requireCode(t, envelope(t, call(t, session, ctx, "dev.files.upload", devArgs(accessUUIDA, map[string]any{"local_path": source}))), "WORKSPACE_ACCESS_DENIED")
	if f.calls != 0 {
		t.Fatal("missing workspace invoked backend")
	}
}

func TestWorkspaceValidHostPathsLeaveWindowsPathsUntouched(t *testing.T) {
	root := t.TempDir()
	source := writeSyntheticFile(t, root, "source.txt", "synthetic")
	f := &developerFixture{}
	session, ctx := connectDeveloper(t, f, devOptions(root))
	for _, test := range []struct {
		action string
		params map[string]any
	}{
		{"files.upload", map[string]any{"local_path": source, "root": "project", "path": "output/file.txt"}},
		{"files.download", map[string]any{"local_path": filepath.Join(root, "new", "download.txt"), "root": "project", "path": "data.txt"}},
		{"bootstrap.rollback", map[string]any{"target_dir": `D:\Program Files\Starbridge`, "installation_id": strings.Repeat("a", 32)}},
		{"task.start", map[string]any{"profile": "synthetic-tests", "args": []any{`C:\project\data.txt`}}},
	} {
		payload := envelope(t, call(t, session, ctx, developerToolName(test.action), devArgs(accessUUIDA, test.params)))
		if payload["ok"] != true {
			t.Fatal(payload)
		}
		if value, ok := test.params["target_dir"]; ok && f.params["target_dir"] != value {
			t.Fatal("guest Windows path was treated as host path")
		}
	}
}

func workflowFixture(t *testing.T, root string, step map[string]any, extra map[string]any) string {
	t.Helper()
	profile := map[string]any{"version": 1, "vm": accessUUIDA, "steps": []any{step}}
	for key, value := range extra {
		profile[key] = value
	}
	encoded, _ := json.Marshal(profile)
	return writeSyntheticFile(t, root, "workflow.json", string(encoded))
}
func workflowRequest(profile, output string) map[string]any {
	return map[string]any{"profile_file": profile, "output_directory": output}
}

func TestWorkflowAndSyncProfilesRejectNestedEscapeAndDynamicPaths(t *testing.T) {
	for _, kind := range []string{"outside", "reference", "container-reference", "unknown-local", "destructive", "vm-no-flag", "helper-override", "nested-sync", "sync-traversal", "sync-symlink", "duplicate-json"} {
		t.Run(kind, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			options := devOptions(root)
			step := map[string]any{"name": "stage", "action": "files.upload", "params": map[string]any{"local_path": filepath.Join(outside, "secret")}}
			extra := map[string]any{}
			switch kind {
			case "reference":
				step["params"] = map[string]any{"local_path": map[string]any{"$ref": "earlier.data.path"}}
			case "container-reference":
				step["params"] = map[string]any{"$ref": "earlier.data.params"}
			case "unknown-local":
				step["action"] = "future.host"
				step["params"] = map[string]any{}
			case "destructive":
				step["action"] = "vm.recycle"
				step["params"] = map[string]any{"mode": "delete"}
				options.AllowVMWrite = true
			case "vm-no-flag":
				step["action"] = "vm.start"
				step["params"] = map[string]any{}
			case "helper-override":
				step["action"] = "system.info"
				step["params"] = map[string]any{}
				extra["developer_script"] = `C:\Temp\unapproved.ps1`
			case "nested-sync", "sync-traversal", "sync-symlink":
				if err := os.Mkdir(filepath.Join(root, "source"), 0700); err != nil {
					t.Fatal(err)
				}
				source := "source"
				if kind == "sync-traversal" {
					source = "../outside"
				}
				if kind == "sync-symlink" {
					if err := os.Symlink(outside, filepath.Join(root, "source", "link")); err != nil {
						t.Fatal(err)
					}
				}
				encoded, _ := json.Marshal(map[string]any{"source": source, "root": "project", "path": ""})
				syncPath := writeSyntheticFile(t, root, "sync.json", string(encoded))
				if kind == "nested-sync" {
					syncPath = filepath.Join(outside, "sync.json")
				}
				step["action"] = "files.sync"
				step["params"] = map[string]any{"profile": syncPath}
			}
			profile := workflowFixture(t, root, step, extra)
			if kind == "duplicate-json" {
				profile = writeSyntheticFile(t, root, "workflow.json", `{"version":1,"vm":"`+accessUUIDA+`","steps":[],"steps":[]}`)
			}
			f := &developerFixture{}
			session, ctx := connectDeveloper(t, f, options)
			payload := envelope(t, call(t, session, ctx, "dev.workflow.run", devArgs(accessUUIDA, workflowRequest(profile, filepath.Join(root, "runs")))))
			if payload["ok"] != false {
				t.Fatal("unsafe workflow accepted", payload)
			}
			if f.calls != 0 || f.inventories != 0 {
				t.Fatal("unsafe profile invoked backend")
			}
		})
	}
}

func TestWorkflowNestedGrantsAndVMWriteFlag(t *testing.T) {
	root := t.TempDir()
	profile := workflowFixture(t, root, map[string]any{"name": "inspect", "action": "task.status", "params": map[string]any{"task_id": map[string]any{"$ref": "start.data.task_id"}}}, nil)
	options := devOptions(root)
	options.AccessPolicy = &AccessPolicy{grants: map[string]map[string]bool{accessUUIDA: {"dev.workflow.run": true}, accessUUIDB: {"dev.task.status": true}}}
	f := &developerFixture{}
	session, ctx := connectDeveloper(t, f, options)
	requireCode(t, envelope(t, call(t, session, ctx, "dev.workflow.run", devArgs(accessUUIDA, workflowRequest(profile, filepath.Join(root, "runs"))))), "ACCESS_DENIED")
	if f.calls != 0 || f.inventories != 0 {
		t.Fatal("unauthorized nested action reached backend")
	}
	options.AccessPolicy = &AccessPolicy{grants: map[string]map[string]bool{accessUUIDA: {"dev.workflow.run": true, "dev.task.status": true}}}
	f = &developerFixture{}
	session, ctx = connectDeveloper(t, f, options)
	if payload := envelope(t, call(t, session, ctx, "dev.workflow.run", devArgs(accessUUIDA, workflowRequest(profile, filepath.Join(root, "runs"))))); payload["ok"] != true || f.calls != 1 {
		t.Fatal(payload)
	}
	profile = workflowFixture(t, root, map[string]any{"name": "start", "action": "vm.start", "params": map[string]any{}}, nil)
	options.AllowVMWrite = true
	options.AccessPolicy = &AccessPolicy{grants: map[string]map[string]bool{accessUUIDA: {"dev.workflow.run": true, "vm_start": true}}}
	f = &developerFixture{}
	session, ctx = connectDeveloper(t, f, options)
	if payload := envelope(t, call(t, session, ctx, "dev.workflow.run", devArgs(accessUUIDA, workflowRequest(profile, filepath.Join(root, "runs"))))); payload["ok"] != true {
		t.Fatal(payload)
	}
}

func TestDeveloperPolicyPreservesUUIDAndOptionalInterfaces(t *testing.T) {
	policy := accessPolicy(t, `{"vms":{"`+accessUUIDA+`":["doctor_vm","vm_snapshot_list","dev.system.info"]}}`)
	f := &developerFixture{}
	options := ServerOptions{DeveloperTools: true, AccessPolicy: policy}
	session, ctx := connectDeveloper(t, f, options)
	for _, tool := range []string{"doctor_vm", "vm_snapshot_list", "dev.system.info"} {
		if payload := envelope(t, call(t, session, ctx, tool, map[string]any{"vm": "sandbox"})); payload["ok"] != true {
			t.Fatal(payload)
		}
	}
	if f.vm != accessUUIDA {
		t.Fatal("policy wrapper dropped optional interface/UUID resolution")
	}
	f.mu.Lock()
	before := f.calls
	inventoryBefore := f.inventories
	f.mu.Unlock()
	for _, name := range []string{"dev.system.info", "doctor_vm", "vm_snapshot_list"} {
		requireCode(t, envelope(t, call(t, session, ctx, name, map[string]any{"vm": accessUUIDB})), "ACCESS_DENIED")
	}
	if f.calls != before || f.inventories != inventoryBefore {
		t.Fatal("unauthorized UUID invoked backend")
	}
}

func TestWorkflowCleanupRequiresOnlyItsExecutedActions(t *testing.T) {
	root := t.TempDir()
	profile := workflowFixture(t, root, map[string]any{"name": "upload", "action": "files.upload", "params": map[string]any{"local_path": filepath.Join(t.TempDir(), "unexecuted.bin")}}, map[string]any{"cleanup": map[string]any{"action": "vm.recycle", "params": map[string]any{"mode": "stop"}}})
	for _, grantStop := range []bool{false, true} {
		options := devOptions(root)
		options.AllowVMWrite = true
		grants := map[string]bool{"dev.workflow.cleanup": true}
		if grantStop {
			grants["vm_stop"] = true
		}
		options.AccessPolicy = &AccessPolicy{grants: map[string]map[string]bool{accessUUIDA: grants}}
		f := &developerFixture{}
		session, ctx := connectDeveloper(t, f, options)
		payload := envelope(t, call(t, session, ctx, "dev.workflow.cleanup", devArgs(accessUUIDA, workflowRequest(profile, filepath.Join(root, "runs")))))
		if grantStop {
			if payload["ok"] != true || f.calls != 1 || f.action != "workflow.cleanup" {
				t.Fatal("authorized cleanup required an unexecuted stage", payload)
			}
		} else {
			requireCode(t, payload, "ACCESS_DENIED")
			if f.calls != 0 {
				t.Fatal("cleanup bypassed its own stop grant")
			}
		}
	}
}

func TestDeveloperMutationsSerializeWithVMLifecycleAndQueuedCancel(t *testing.T) {
	f := &developerFixture{}
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	f.invoke = func(ctx context.Context, _ string, _ string, _ map[string]any, _ bool) (bridge.Result, error) {
		entered <- struct{}{}
		select {
		case <-release:
			return bridge.Success(map[string]any{}), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	b := newCoordinatedBackend(f)
	ctx := context.WithValue(context.Background(), developerMutationKey{}, true)
	completed := make(chan error, 1)
	go func() { _, err := b.Developer(ctx, accessUUIDA, "ui.act", map[string]any{}, true); completed <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("mutation did not start")
	}
	waitCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	_, err := b.VMAction(waitCtx, "stop", "{"+strings.ToUpper(accessUUIDA)+"}", false, "")
	assertAccessCode(t, err, "TIMEOUT")
	var failure *bridge.Error
	failure, _ = err.(*bridge.Error)
	if failure.Details["phase"] != "queued" {
		t.Fatal(err)
	}
	close(release)
	if err := <-completed; err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceConfigurationAndProfileDecoder(t *testing.T) {
	for _, options := range []ServerOptions{{AllowGuestWrite: true}, {DeveloperWorkspace: t.TempDir()}, {DeveloperTools: true, DeveloperWorkspace: filepath.Join(t.TempDir(), "missing")}} {
		if _, err := NewWithOptions(&developerFixture{}, options); err == nil {
			t.Fatal("unsafe server options accepted")
		}
	}
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if _, err := newDeveloperWorkspace(link); err == nil {
		t.Fatal("workspace symlink accepted")
	}
	for _, text := range []string{`[]`, `null`, `{} {}`, `{"key":1,"k\u0065y":2}`, `{"nested":{"x":1,"x":2}}`, strings.Repeat(`{"x":`, 34) + `{}` + strings.Repeat(`}`, 34)} {
		path := writeSyntheticFile(t, root, "profile.json", text)
		if _, err := readWorkspaceJSON(path); err == nil {
			t.Fatal("invalid profile accepted")
		}
	}
	path := writeSyntheticFile(t, root, "profile.json", `{"array":[{},1],"number":1}`)
	value, err := readWorkspaceJSON(path)
	if err != nil || !reflect.DeepEqual(value["number"], json.Number("1")) {
		t.Fatal(value, err)
	}
}

func TestWorkspaceProfilesRejectSpecialFilesBeforeOpening(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix named pipe fixture")
	}
	root := t.TempDir()
	fifo := filepath.Join(root, "profile.json")
	if err := exec.Command("mkfifo", fifo).Run(); err != nil {
		t.Skip("mkfifo fixture unavailable")
	}
	for name, reader := range map[string]func() error{
		"workspace-profile": func() error { _, err := readWorkspaceJSON(fifo); return err },
		"access-policy":     func() error { _, err := LoadAccessPolicy(fifo); return err },
	} {
		t.Run(name, func(t *testing.T) {
			done := make(chan error, 1)
			go func() { done <- reader() }()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("FIFO profile accepted")
				}
			case <-time.After(time.Second):
				t.Fatal("profile reader blocked while opening FIFO")
			}
		})
	}
}

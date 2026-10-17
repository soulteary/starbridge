package bridge

import "context"

// DoctorVM performs bounded read-only probes. A successful diagnostic response
// does not imply readiness: callers must inspect ready and the individual checks.
func (b *Bridge) DoctorVM(ctx context.Context, vm string) (any, error) {
	if err := identifier(vm, "VM"); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, b.options.Timeout)
	defer cancel()
	checks := []any{}
	report := map[string]any{"vm": vm, "ready": false, "guest_verified": false, "desktop_verified": false, "ui_verified": false, "checks": checks}
	add := func(name string, data any, err error) bool {
		check := map[string]any{"name": name, "status": "passed", "data": data}
		if err != nil {
			check = map[string]any{"name": name, "status": "failed", "error": Failure(err)["error"]}
		}
		checks = append(checks, check)
		report["checks"] = checks
		return err == nil
	}
	host, err := b.Doctor(ctx)
	if !add("host_cli", host, err) {
		return report, nil
	}
	info, err := b.VMInfo(ctx, vm)
	if !add("vm_info", info, err) {
		return report, nil
	}
	if b.options.DryRun {
		report["dry_run"] = true
		processes, err := b.Guest(ctx, vm, "processes.list", map[string]any{"limit": 1})
		add("guest_processes_plan", processes, err)
		windows, err := b.Guest(ctx, vm, "windows.list", map[string]any{"limit": 1})
		add("guest_windows_plan", windows, err)
		return report, nil
	}
	probe := func(name, action string, params map[string]any) (Result, bool) {
		result, err := b.Guest(ctx, vm, action, params)
		if err == nil && result["ok"] != true {
			checks = append(checks, map[string]any{"name": name, "status": "failed", "error": result["error"]})
			report["checks"] = checks
			return result, false
		}
		return result, add(name, result, err)
	}
	processes, ok := probe("guest_processes", "processes.list", map[string]any{"limit": 1})
	if !ok {
		return report, nil
	}
	report["guest_verified"] = true
	report["session"] = processes["meta"]
	windows, ok := probe("guest_windows", "windows.list", map[string]any{"limit": 1})
	if !ok {
		return report, nil
	}
	data, _ := asObject(windows["data"])
	items, _ := data["windows"].([]any)
	if len(items) == 0 {
		report["desktop_status"] = "no_windows_observed"
		return report, nil
	}
	window, _ := asObject(items[0])
	hwnd, _ := window["hwnd"].(string)
	if hwnd == "" {
		add("window_identity", nil, NewError("INVALID_GUEST_RESPONSE", "Window response has no valid HWND.", nil))
		return report, nil
	}
	report["desktop_verified"] = true
	ui, ok := probe("guest_ui", "ui.tree", map[string]any{"hwnd": hwnd, "max_depth": 0, "max_nodes": 1, "timeout_ms": 5000})
	if !ok {
		return report, nil
	}
	uiData, _ := asObject(ui["data"])
	nodes, _ := uiData["nodes"].([]any)
	if len(nodes) == 0 {
		add("ui_root", nil, NewError("INVALID_GUEST_RESPONSE", "No UI Automation root was observed.", nil))
		return report, nil
	}
	root, object := asObject(nodes[0])
	depth, integralDepth := integral(root["depth"])
	nodeID, _ := root["node_id"].(string)
	if !object || !integralDepth || depth != 0 || nodeID == "" {
		add("ui_root", nil, NewError("INVALID_GUEST_RESPONSE", "The UI Automation root has no valid identity.", nil))
		return report, nil
	}
	report["ui_verified"], report["ready"] = true, true
	report["readiness_scope"] = "one observed window root; not a guarantee about every application or the visible input desktop"
	return report, nil
}

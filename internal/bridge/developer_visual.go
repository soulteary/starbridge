package bridge

import (
	"encoding/base64"
	"regexp"
	"time"
)

var visualErrorCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// Visual targets are image pixels, never UIA node IDs. The guest authenticates
// observation tokens with DPAPI and rechecks geometry under its session lease.
func init() {
	for _, name := range []string{"ui.observe", "ui.click"} {
		action := name
		registerDeveloperAction(DeveloperAction{
			Name: name, Description: "Observe an identified window with UIA and an image, or dispatch one guarded visual click and return a fresh observation; business verification remains pending.",
			Mutation: name == "ui.click", Interactive: true,
			Normalize:             func(p map[string]any) (map[string]any, error) { return normalizeDeveloperVisual(action, p) },
			ValidateResult:        func(d map[string]any) bool { return validDeveloperVisual(action, d) },
			ValidateWithinRequest: func(d, p map[string]any) bool { return visualWithinRequest(action, d, p) },
		})
	}
}

func visualBoolean(p map[string]any, key string, fallback bool) (bool, error) {
	if value, present := p[key]; present {
		b, ok := value.(bool)
		if !ok {
			return false, uiQueryArgument(key + " must be a boolean.")
		}
		return b, nil
	}
	return fallback, nil
}

func normalizeVisualObservation(p map[string]any) (map[string]any, error) {
	if uiQueryFields(p, "token", "hwnd", "pid", "start_time_utc", "timestamp", "expires_at", "session_id", "dpi", "rect", "window_rect", "width", "height", "sha256", "coordinate_space", "capture_method") != nil {
		return nil, uiQueryArgument("Unknown observation field.")
	}
	result, err := normalizeDeveloperUIWindow(p)
	if err != nil {
		return nil, err
	}
	for _, key := range []string{"token", "sha256", "coordinate_space", "capture_method", "timestamp", "expires_at"} {
		value, ok := p[key].(string)
		if !ok || value == "" {
			return nil, uiQueryArgument("Observation metadata is incomplete.")
		}
		result[key] = value
	}
	token, err := base64.StdEncoding.Strict().DecodeString(result["token"].(string))
	if err != nil || len(token) < 32 || len(token) > 4096 || base64.StdEncoding.EncodeToString(token) != result["token"] {
		return nil, uiQueryArgument("Invalid observation token.")
	}
	stamp, e1 := time.Parse(time.RFC3339Nano, result["timestamp"].(string))
	expires, e2 := time.Parse(time.RFC3339Nano, result["expires_at"].(string))
	if e1 != nil || e2 != nil || expires.Sub(stamp) != 30*time.Second || !developerHash.MatchString(result["sha256"].(string)) || result["coordinate_space"] != "guest-screen-physical-pixels" ||
		(result["capture_method"] != "print_window" && result["capture_method"] != "windows_graphics_capture") {
		return nil, uiQueryArgument("Invalid observation freshness or coordinate metadata.")
	}
	for _, item := range []struct {
		key      string
		min, max int64
	}{{"session_id", 1, 2147483647}, {"dpi", 48, 960}, {"width", 1, 16384}, {"height", 1, 16384}} {
		if _, present := p[item.key]; !present {
			return nil, uiQueryArgument("Observation metadata is incomplete.")
		}
		result[item.key], err = uiQueryInteger(p, item.key, 0, item.min, item.max)
		if err != nil {
			return nil, err
		}
	}
	if result["width"].(int64)*result["height"].(int64) > developerScreenshotMaxPixels {
		return nil, uiQueryArgument("Observation dimensions exceed the image budget.")
	}
	for _, key := range []string{"rect", "window_rect"} {
		r, ok := asObject(p[key])
		if !ok || r == nil || uiQueryFields(r, "x", "y", "width", "height") != nil {
			return nil, uiQueryArgument("Invalid observation geometry.")
		}
		copy := map[string]any{}
		for _, field := range []string{"x", "y", "width", "height"} {
			min, max := int64(-2147483648), int64(2147483647)
			if field == "width" || field == "height" {
				min, max = 1, 16384
			}
			if _, present := r[field]; !present {
				return nil, uiQueryArgument("Incomplete observation geometry.")
			}
			copy[field], err = uiQueryInteger(r, field, 0, min, max)
			if err != nil {
				return nil, err
			}
		}
		result[key] = copy
	}
	r := result["rect"].(map[string]any)
	if r["width"] != result["width"] || r["height"] != result["height"] {
		return nil, uiQueryArgument("Capture geometry and image dimensions differ.")
	}
	return result, nil
}

func normalizeVisualTarget(p, observation map[string]any) (map[string]any, error) {
	if err := uiQueryFields(p, "x", "y", "width", "height", "image_width", "image_height"); err != nil {
		return nil, err
	}
	result := map[string]any{}
	for _, key := range []string{"image_width", "image_height"} {
		fallback, _ := integral(observation[map[string]string{"image_width": "width", "image_height": "height"}[key]])
		n, err := uiQueryInteger(p, key, fallback, 1, 16384)
		if err != nil {
			return nil, err
		}
		result[key] = n
	}
	_, hasW := p["width"]
	_, hasH := p["height"]
	if hasW != hasH {
		return nil, uiQueryArgument("A target rectangle requires both width and height.")
	}
	for _, axis := range []struct{ pos, size, image string }{{"x", "width", "image_width"}, {"y", "height", "image_height"}} {
		if _, present := p[axis.pos]; !present {
			return nil, uiQueryArgument("Target x and y are required.")
		}
		limit := result[axis.image].(int64)
		n, err := uiQueryInteger(p, axis.pos, 0, 0, limit-1)
		if err != nil {
			return nil, err
		}
		result[axis.pos] = n
		if hasW {
			size, err := uiQueryInteger(p, axis.size, 0, 1, limit-n)
			if err != nil {
				return nil, err
			}
			result[axis.size] = size
		}
	}
	return result, nil
}

func normalizeDeveloperVisual(action string, p map[string]any) (map[string]any, error) {
	allowed := []string{"include_uia", "uia_timeout_ms", "timeout_ms", "max_bytes", "max_pixels", "capture_method"}
	var identity map[string]any
	var observation map[string]any
	var err error
	if action == "ui.observe" {
		allowed = append(allowed, "hwnd", "pid", "start_time_utc")
		identity = p
	} else {
		allowed = append(allowed, "observation", "target", "focus", "button", "max_age_ms", "strict_image_match", "wait_ms", "lock_timeout_ms")
		object, ok := asObject(p["observation"])
		if !ok || object == nil {
			return nil, uiQueryArgument("An observation object from ui.observe is required.")
		}
		observation, err = normalizeVisualObservation(object)
		if err != nil {
			return nil, err
		}
		identity = observation
	}
	if err = uiQueryFields(p, allowed...); err != nil {
		return nil, err
	}
	capture := map[string]any{"hwnd": identity["hwnd"], "pid": identity["pid"], "start_time_utc": identity["start_time_utc"]}
	fallbackMethod := "print_window"
	if action == "ui.click" {
		fallbackMethod = observation["capture_method"].(string)
	}
	method, err := uiActionEnum(p, "capture_method", fallbackMethod, "print_window", "windows_graphics_capture")
	if err != nil {
		return nil, err
	}
	// print_window exists in the baseline screenshot contract, where the method
	// is implicit. Consume that selector here instead of forwarding an unknown
	// field; additional backends must be accepted by the installed normalizer.
	if method != "print_window" {
		capture["capture_method"] = method
	}
	for _, key := range []string{"max_bytes", "max_pixels"} {
		if value, exists := p[key]; exists {
			capture[key] = value
		}
	}
	timeout, err := uiQueryInteger(p, "timeout_ms", 15000, 2000, 20000)
	if err != nil {
		return nil, err
	}
	capture["timeout_ms"] = timeout
	result, err := normalizeDeveloperScreenshot(capture)
	if err != nil {
		return nil, err
	}
	result["capture_method"] = method
	result["include_uia"], err = visualBoolean(p, "include_uia", true)
	if err != nil {
		return nil, err
	}
	result["uia_timeout_ms"], err = uiQueryInteger(p, "uia_timeout_ms", 1500, 1000, 5000)
	if err != nil {
		return nil, err
	}
	if result["uia_timeout_ms"].(int64) >= timeout {
		return nil, uiQueryArgument("uia_timeout_ms must be smaller than timeout_ms.")
	}
	if action == "ui.click" {
		// Identity is carried only in the authenticated observation on the wire.
		delete(result, "hwnd")
		delete(result, "pid")
		delete(result, "start_time_utc")
		result["observation"] = observation
		target, ok := asObject(p["target"])
		if !ok || target == nil {
			return nil, uiQueryArgument("One target point or rectangle is required.")
		}
		result["target"], err = normalizeVisualTarget(target, observation)
		if err != nil {
			return nil, err
		}
		if _, valid := visualPhysicalPoint(observation, result["target"].(map[string]any)); !valid {
			return nil, uiQueryArgument("The target maps outside the physical input coordinate budget.")
		}
		for _, key := range []string{"focus", "strict_image_match"} {
			result[key], err = visualBoolean(p, key, false)
			if err != nil {
				return nil, err
			}
		}
		result["button"], err = uiActionEnum(p, "button", "left", "left", "right", "middle")
		if err != nil {
			return nil, err
		}
		for _, bound := range []struct {
			key                string
			fallback, min, max int64
		}{{"max_age_ms", 15000, 100, 30000}, {"wait_ms", 250, 0, 2000}, {"lock_timeout_ms", 0, 0, 5000}} {
			result[bound.key], err = uiQueryInteger(p, bound.key, bound.fallback, bound.min, bound.max)
			if err != nil {
				return nil, err
			}
		}
		if result["lock_timeout_ms"].(int64)+result["wait_ms"].(int64) >= timeout {
			return nil, uiQueryArgument("Lock and wait budgets must be smaller than timeout_ms.")
		}
	}
	return result, nil
}

func validDeveloperVisual(action string, d map[string]any) bool {
	fields := []string{"action", "observation", "screenshot", "uia", "source"}
	if action == "ui.click" {
		fields = append(fields, "outcome", "verification", "input", "coordination")
	}
	if uiQueryFields(d, fields...) != nil || d["action"] != action {
		return false
	}
	o, ok := asObject(d["observation"])
	if !ok {
		return false
	}
	o, err := normalizeVisualObservation(o)
	if err != nil {
		return false
	}
	s, ok := asObject(d["screenshot"])
	if !ok || !validDeveloperScreenshot(s) {
		return false
	}
	for _, key := range []string{"hwnd", "start_time_utc", "timestamp", "sha256", "coordinate_space", "capture_method"} {
		if o[key] != s[key] {
			return false
		}
	}
	for _, key := range []string{"pid", "width", "height"} {
		a, _ := integral(o[key])
		b, _ := integral(s[key])
		if a != b {
			return false
		}
	}
	sr, _ := asObject(s["rect"])
	or := o["rect"].(map[string]any)
	for _, k := range []string{"x", "y", "width", "height"} {
		a, _ := integral(sr[k])
		b, _ := integral(or[k])
		if a != b {
			return false
		}
	}
	u, ok := asObject(d["uia"])
	if !ok || uiQueryFields(u, "status", "complete", "data", "error_code") != nil {
		return false
	}
	switch u["status"] {
	case "available":
		q, ok := asObject(u["data"])
		if !ok || !validDeveloperUIQueryResult(q) || u["complete"] != q["complete"] || d["source"] != "uia-and-visual" {
			return false
		}
		window, _ := asObject(q["window"])
		if window["hwnd"] != o["hwnd"] || window["start_time_utc"] != o["start_time_utc"] {
			return false
		}
		a, _ := integral(window["pid"])
		b, _ := integral(o["pid"])
		if a != b {
			return false
		}
	case "unavailable":
		code, ok := u["error_code"].(string)
		if !ok || !visualErrorCode.MatchString(code) || u["complete"] != false || d["source"] != "visual" || u["data"] != nil {
			return false
		}
	case "not_requested":
		if u["complete"] != false || d["source"] != "visual" || u["data"] != nil || u["error_code"] != nil {
			return false
		}
	default:
		return false
	}
	if action == "ui.click" {
		v, ok := asObject(d["verification"])
		if !ok || len(v) != 1 || v["status"] != "pending" || d["outcome"] != "dispatched" || d["coordination"] != "windows-session-mutex" {
			return false
		}
		i, ok := asObject(d["input"])
		if !ok || uiQueryFields(i, "x", "y", "coordinate_space") != nil || !responseInteger(i["x"], -65535, 65535) || !responseInteger(i["y"], -65535, 65535) || i["coordinate_space"] != "guest-screen-physical-pixels" {
			return false
		}
	}
	return true
}

func visualWithinRequest(action string, d, p map[string]any) bool {
	s, _ := asObject(d["screenshot"])
	identity := p
	if action == "ui.click" {
		identity, _ = asObject(p["observation"])
	}
	want := map[string]any{"hwnd": identity["hwnd"], "pid": identity["pid"], "start_time_utc": identity["start_time_utc"], "max_bytes": p["max_bytes"], "max_pixels": p["max_pixels"], "timeout_ms": p["timeout_ms"]}
	if method, present := p["capture_method"]; present {
		want["capture_method"] = method
	}
	if !developerScreenshotWithinRequest(s, want) {
		return false
	}
	if action == "ui.click" {
		observation, _ := asObject(d["observation"])
		a, _ := integral(observation["session_id"])
		b, _ := integral(identity["session_id"])
		before, _ := time.Parse(time.RFC3339Nano, identity["timestamp"].(string))
		after, _ := time.Parse(time.RFC3339Nano, observation["timestamp"].(string))
		if a != b || after.Before(before) {
			return false
		}
		target, _ := asObject(p["target"])
		point, valid := visualPhysicalPoint(identity, target)
		input, _ := asObject(d["input"])
		x, _ := integral(input["x"])
		y, _ := integral(input["y"])
		if !valid || point[0] != x || point[1] != y {
			return false
		}
	}
	u, _ := asObject(d["uia"])
	if p["include_uia"] == false {
		return u["status"] == "not_requested"
	}
	if u["status"] == "available" {
		queryParams := map[string]any{"hwnd": identity["hwnd"], "pid": identity["pid"], "start_time_utc": identity["start_time_utc"], "limit": int64(64), "max_depth": int64(6), "max_scan_nodes": int64(500), "include_value": false}
		normalized, err := normalizeDeveloperUIQuery(queryParams)
		if err != nil {
			return false
		}
		data, _ := asObject(u["data"])
		if !developerUIQueryWithinRequest(data, normalized) {
			return false
		}
	}
	return u["status"] != "not_requested"
}

func visualPhysicalPoint(observation, target map[string]any) ([2]int64, bool) {
	var point [2]int64
	rect, ok := asObject(observation["rect"])
	if !ok {
		return point, false
	}
	for i, axis := range [][3]string{{"x", "width", "image_width"}, {"y", "height", "image_height"}} {
		position, pok := integral(target[axis[0]])
		imageSize, iok := integral(target[axis[2]])
		size, sok := integral(observation[axis[1]])
		origin, ook := integral(rect[axis[0]])
		extent := int64(1)
		if value, present := target[axis[1]]; present {
			var valid bool
			extent, valid = integral(value)
			if !valid {
				return point, false
			}
		}
		if !pok || !iok || !sok || !ook || imageSize < 1 || imageSize > 16384 || position < 0 || extent < 1 || position+extent > imageSize || size < 1 || size > 16384 {
			return point, false
		}
		point[i] = origin + (2*position+extent)*size/(2*imageSize)
		if point[i] < -65535 || point[i] > 65535 {
			return point, false
		}
	}
	return point, true
}

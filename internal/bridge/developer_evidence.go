package bridge

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/soulteary/starbridge/internal/version"
)

var evidenceName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

func init() {
	registerDeveloperAction(DeveloperAction{Name: "evidence.capture", Description: "Save explicitly selected application observations, images and task output with a hashed manifest.", Mutation: true,
		Normalize: normalizeEvidence, ValidateResult: func(data map[string]any) bool { return nonemptyText(data["run_id"]) && boolValue(data["complete"]) },
		Execute: func(ctx context.Context, b *Bridge, vm string, p map[string]any, _ bool) (Result, error) {
			return b.captureEvidence(ctx, vm, p)
		},
	})
}

func normalizeEvidence(p map[string]any) (map[string]any, error) {
	bad := func() (map[string]any, error) {
		return nil, NewError("INVALID_ARGUMENT", "Evidence requires an output directory and 1..16 named, scoped read-only observations.", nil)
	}
	for key := range p {
		if key != "output_directory" && key != "observations" {
			return bad()
		}
	}
	directory, ok := p["output_directory"].(string)
	if !ok || strings.TrimSpace(directory) == "" || len(directory) > 4096 || strings.ContainsRune(directory, 0) {
		return bad()
	}
	items, ok := p["observations"].([]any)
	if !ok || len(items) < 1 || len(items) > 16 {
		return bad()
	}
	copy := make([]any, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		observation, valid := asObject(item)
		if !valid {
			return bad()
		}
		for key := range observation {
			if key != "name" && key != "action" && key != "params" {
				return bad()
			}
		}
		name, valid := observation["name"].(string)
		if !valid || !evidenceName.MatchString(name) || seen[name] {
			return bad()
		}
		seen[name] = true
		action, _ := observation["action"].(string)
		switch action {
		case "system.info", "processes.list", "windows.list", "ui.tree", "ui.query", "task.status", "task.output", "screenshot.capture":
		default:
			return bad()
		}
		params := map[string]any{}
		if value, exists := observation["params"]; exists {
			params, valid = asObject(value)
			if !valid || params == nil {
				return bad()
			}
		}
		// Evidence collection never enumerates unrelated applications by default.
		if action == "processes.list" || action == "windows.list" {
			if !responseInteger(params["pid"], 1, 2147483647) {
				return bad()
			}
		}
		copy = append(copy, map[string]any{"name": name, "action": action, "params": params})
	}
	return map[string]any{"output_directory": directory, "observations": copy}, nil
}

func saveEvidenceFile(directory, name string, content []byte) (map[string]any, error) {
	if len(content) > MaxOutputBytes {
		return nil, NewError("OUTPUT_LIMIT_EXCEEDED", "Evidence file exceeds its byte budget.", nil)
	}
	file, err := os.OpenFile(filepath.Join(directory, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	_, err = file.Write(content)
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(content)
	return map[string]any{"name": name, "bytes": len(content), "sha256": hex.EncodeToString(hash[:])}, nil
}

func evidencePayload(directory, name, action string, result Result) ([]any, error) {
	files := []any{}
	// Work on JSON-decoded private storage, never remove data from caller maps.
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	var private Result
	if json.Unmarshal(encoded, &private) != nil {
		return nil, invalidGuestResponse()
	}
	data, _ := asObject(private["data"])
	if result["ok"] == true && action == "screenshot.capture" {
		text, valid := data["png_base64"].(string)
		if !valid || len(text) > 6*1024*1024 {
			return nil, invalidGuestResponse()
		}
		image, decodeErr := base64.StdEncoding.Strict().DecodeString(text)
		if decodeErr != nil || len(image) > 4*1024*1024 {
			return nil, invalidGuestResponse()
		}
		config, decodeErr := png.DecodeConfig(bytes.NewReader(image))
		if decodeErr != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 64000000 {
			return nil, invalidGuestResponse()
		}
		file, saveErr := saveEvidenceFile(directory, name+".png", image)
		if saveErr != nil {
			return nil, saveErr
		}
		files = append(files, file)
		delete(data, "png_base64")
		data["image_file"] = name + ".png"
	}
	if result["ok"] == true && action == "task.output" {
		text, valid := data["data_base64"].(string)
		if !valid || len(text) > 100000 {
			return nil, invalidGuestResponse()
		}
		output, decodeErr := base64.StdEncoding.Strict().DecodeString(text)
		if decodeErr != nil || len(output) > 65536 {
			return nil, invalidGuestResponse()
		}
		file, saveErr := saveEvidenceFile(directory, name+".log", output)
		if saveErr != nil {
			return nil, saveErr
		}
		files = append(files, file)
		delete(data, "data_base64")
		data["output_file"] = name + ".log"
	}
	encoded, err = json.MarshalIndent(private, "", "  ")
	if err != nil {
		return nil, err
	}
	file, err := saveEvidenceFile(directory, name+".json", append(encoded, '\n'))
	if err != nil {
		return nil, err
	}
	return append(files, file), nil
}

func (b *Bridge) captureEvidence(ctx context.Context, vm string, p map[string]any) (Result, error) {
	uuid, valid := canonicalRecycleUUID(vm)
	if !valid {
		return nil, NewError("INVALID_ARGUMENT", "Evidence target must be a VM UUID.", nil)
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, NewError("IO_ERROR", "Cannot create evidence identity.", nil)
	}
	runID := hex.EncodeToString(nonce[:])
	directory := filepath.Join(p["output_directory"].(string), runID)
	if os.MkdirAll(directory, 0700) != nil {
		return nil, NewError("IO_ERROR", "Cannot create evidence directory.", nil)
	}
	observations := []any{}
	complete := true
	totalBytes := int64(0)
	for _, item := range p["observations"].([]any) {
		if ctx.Err() != nil {
			complete = false
			break
		}
		observation, _ := asObject(item)
		params, _ := asObject(observation["params"])
		name := observation["name"].(string)
		action := observation["action"].(string)
		started := time.Now()
		var result Result
		var err error
		switch action {
		case "processes.list", "windows.list", "ui.tree":
			result, err = b.Guest(ctx, vm, action, params)
		default:
			result, err = b.Developer(ctx, vm, action, params, false)
		}
		if err != nil {
			result = Failure(err)
		}
		if result["ok"] != true {
			complete = false
		}
		estimate, encodeErr := json.Marshal(result)
		if encodeErr != nil || totalBytes+int64(len(estimate)) > 32*1024*1024 {
			complete = false
			break
		}
		files, err := evidencePayload(directory, name, action, result)
		if err != nil {
			complete = false
			result = Failure(NewError("EVIDENCE_CAPTURE_FAILED", "Observation could not be saved within the evidence contract.", nil))
			files, err = evidencePayload(directory, name, action, result)
		}
		if err != nil {
			return nil, NewError("IO_ERROR", "Cannot save evidence observation.", nil)
		}
		for _, file := range files {
			object, _ := asObject(file)
			totalBytes += int64(object["bytes"].(int))
		}
		observations = append(observations, map[string]any{"name": name, "action": action, "ok": result["ok"] == true, "started_at": started.UTC().Format(time.RFC3339Nano), "duration_ms": time.Since(started).Milliseconds(), "files": files})
		if totalBytes > 32*1024*1024 {
			complete = false
			break
		}
	}
	manifest := map[string]any{"run_id": runID, "vm": uuid, "starbridge_version": version.Version, "created_at": time.Now().UTC().Format(time.RFC3339Nano), "complete": complete, "bytes": totalBytes, "observations": observations}
	encoded, _ := json.MarshalIndent(manifest, "", "  ")
	if _, err := saveEvidenceFile(directory, "manifest.json", append(encoded, '\n')); err != nil {
		return nil, NewError("IO_ERROR", "Cannot save evidence manifest.", nil)
	}
	return Success(map[string]any{"run_id": runID, "complete": complete, "manifest_file": filepath.Join(directory, "manifest.json"), "observations": len(observations)}), nil
}

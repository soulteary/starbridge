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

const maxEvidenceRunBytes = 32 * 1024 * 1024
const evidenceManifestReserve = 64 * 1024

func init() {
	registerDeveloperAction(DeveloperAction{Name: "evidence.capture", Description: "Save explicitly selected application observations, images and task output with a hashed manifest.", Mutation: true,
		Normalize: normalizeEvidence, ValidateResult: func(data map[string]any) bool { return nonemptyText(data["run_id"]) && boolValue(data["complete"]) },
		RedactPreview: func(p map[string]any) map[string]any { p["output_directory"] = "[redacted]"; return p },
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
		if !valid || !evidenceName.MatchString(name) || name == "manifest" || seen[name] {
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
		// This file was exclusively created by this call. Do not leave a partial
		// artifact behind if writing or closing failed.
		_ = os.Remove(filepath.Join(directory, name))
		return nil, err
	}
	hash := sha256.Sum256(content)
	return map[string]any{"name": name, "bytes": len(content), "sha256": hex.EncodeToString(hash[:])}, nil
}

type evidenceArtifact struct {
	name    string
	content []byte
}

// Prepare the complete on-disk representation before creating any artifacts.
// Compact transport JSON is not a safe estimate of pretty-printed JSON size.
func prepareEvidencePayload(name, action string, result Result) ([]evidenceArtifact, error) {
	artifacts := []evidenceArtifact{}
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
		artifacts = append(artifacts, evidenceArtifact{name + ".png", image})
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
		artifacts = append(artifacts, evidenceArtifact{name + ".log", output})
		delete(data, "data_base64")
		data["output_file"] = name + ".log"
	}
	encoded, err = json.MarshalIndent(private, "", "  ")
	if err != nil {
		return nil, err
	}
	artifacts = append(artifacts, evidenceArtifact{name + ".json", append(encoded, '\n')})
	for _, artifact := range artifacts {
		if len(artifact.content) > MaxOutputBytes {
			return nil, NewError("OUTPUT_LIMIT_EXCEEDED", "Evidence file exceeds its byte budget.", nil)
		}
	}
	return artifacts, nil
}

func evidenceArtifactBytes(artifacts []evidenceArtifact) int64 {
	var bytes int64
	for _, artifact := range artifacts {
		bytes += int64(len(artifact.content))
	}
	return bytes
}

func saveEvidenceArtifacts(directory string, artifacts []evidenceArtifact, remaining int64) ([]any, error) {
	if evidenceArtifactBytes(artifacts) > remaining {
		return nil, NewError("EVIDENCE_BUDGET_EXCEEDED", "The observation does not fit the remaining evidence budget.", nil)
	}
	files := []any{}
	for _, artifact := range artifacts {
		file, err := saveEvidenceFile(directory, artifact.name, artifact.content)
		if err != nil {
			for _, saved := range files {
				entry, _ := asObject(saved)
				_ = os.Remove(filepath.Join(directory, entry["name"].(string)))
			}
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

func evidencePayload(directory, name, action string, result Result) ([]any, error) {
	artifacts, err := prepareEvidencePayload(name, action, result)
	if err != nil {
		return nil, err
	}
	return saveEvidenceArtifacts(directory, artifacts, maxEvidenceRunBytes-evidenceManifestReserve)
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
	stopReason := ""
	for _, item := range p["observations"].([]any) {
		if ctx.Err() != nil {
			complete = false
			stopReason = "interrupted"
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
		artifacts, err := prepareEvidencePayload(name, action, result)
		if err != nil {
			complete = false
			result = Failure(NewError("EVIDENCE_CAPTURE_FAILED", "Observation could not be saved within the evidence contract.", nil))
			artifacts, _ = prepareEvidencePayload(name, action, result)
		}
		remaining := int64(maxEvidenceRunBytes-evidenceManifestReserve) - totalBytes
		exhausted := evidenceArtifactBytes(artifacts) > remaining
		if exhausted {
			complete = false
			stopReason = "budget_exhausted"
			result = Failure(NewError("EVIDENCE_BUDGET_EXCEEDED", "The observation does not fit the remaining evidence budget.", nil))
			artifacts, _ = prepareEvidencePayload(name, action, result)
		}
		files := []any{}
		errorCode := ""
		if exhausted && evidenceArtifactBytes(artifacts) > remaining {
			// The reserved manifest can still record the omitted observation even
			// when no failure JSON fits. No part of its PNG/log is created.
			errorCode = "EVIDENCE_BUDGET_EXCEEDED"
		} else {
			files, err = saveEvidenceArtifacts(directory, artifacts, remaining)
			if err != nil {
				complete = false
				stopReason = "write_failed"
				errorCode = "EVIDENCE_WRITE_FAILED"
				files = []any{}
			}
		}
		for _, file := range files {
			object, _ := asObject(file)
			totalBytes += int64(object["bytes"].(int))
		}
		record := map[string]any{"name": name, "action": action, "ok": result["ok"] == true && errorCode == "", "started_at": started.UTC().Format(time.RFC3339Nano), "duration_ms": time.Since(started).Milliseconds(), "files": files}
		if errorCode != "" {
			record["error_code"] = errorCode
		}
		observations = append(observations, record)
		if exhausted || errorCode != "" {
			break
		}
	}
	manifest := map[string]any{"run_id": runID, "vm": uuid, "starbridge_version": version.Version, "created_at": time.Now().UTC().Format(time.RFC3339Nano), "complete": complete, "bytes": totalBytes,
		"observations": observations, "requested_observations": len(p["observations"].([]any)), "stop_reason": stopReason}
	var encoded []byte
	// Include the manifest itself in the reported total. Only the decimal byte
	// count can change its size; the small fixed point converges in a few passes.
	for {
		encoded, _ = json.MarshalIndent(manifest, "", "  ")
		encoded = append(encoded, '\n')
		withManifest := totalBytes + int64(len(encoded))
		if manifest["bytes"] == withManifest {
			break
		}
		manifest["bytes"] = withManifest
	}
	if len(encoded) > evidenceManifestReserve || totalBytes+int64(len(encoded)) > maxEvidenceRunBytes {
		return nil, NewError("OUTPUT_LIMIT_EXCEEDED", "Evidence manifest exceeded its reserved budget.", nil)
	}
	if _, err := saveEvidenceFile(directory, "manifest.json", encoded); err != nil {
		return nil, NewError("IO_ERROR", "Cannot save evidence manifest.", nil)
	}
	return Success(map[string]any{"run_id": runID, "complete": complete, "manifest_file": filepath.Join(directory, "manifest.json"), "observations": len(observations)}), nil
}

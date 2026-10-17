package bridge

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
)

func fileEnvelope(action string, data map[string]any) string {
	encoded, _ := json.Marshal(Result{"ok": true, "data": data, "meta": map[string]any{"protocol_version": DeveloperProtocol, "action": action, "read_only": !developerRegistry[action].Mutation, "session_id": 1, "user_interactive": true}})
	return string(encoded)
}
func fileTestDir(t *testing.T) string {
	t.Helper()
	d, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	return d
}
func fileWriteTest(t *testing.T, name string, data []byte) {
	t.Helper()
	if e := os.WriteFile(name, data, 0600); e != nil {
		t.Fatal(e)
	}
}
func requestFromArgs(t *testing.T, args []string) map[string]any {
	t.Helper()
	index := -1
	for i, v := range args {
		if v == "-RequestBase64" {
			index = i + 1
		}
	}
	if index < 0 || index >= len(args) {
		t.Fatal("missing fixed transport")
	}
	bytes, e := base64.StdEncoding.DecodeString(args[index])
	if e != nil {
		t.Fatal(e)
	}
	v, e := decodeJSON(string(bytes))
	if e != nil {
		t.Fatal(e)
	}
	return mapValue(t, v)
}
func TestFileParameterBoundariesAndNoInputMutation(t *testing.T) {
	f := newFixture(t)
	for _, p := range []string{"../escape", "a//b", "/absolute", `C:\file`, "a/./b", "a/../b", "a\x00b", "a:ads", "CON.txt", "foo. ", "x/.starbridge-transfers/y", "a/*"} {
		_, e := f.bridge.Developer(context.Background(), "vm", "file.hash", map[string]any{"root": "workspace", "path": p}, false)
		checkCode(t, e, "INVALID_ARGUMENT")
	}
	for _, p := range []map[string]any{{"root": "../escape", "path": "a"}, {"root": "workspace", "path": "a", "extra": true}, {"root": "workspace", "path": "a", "size_bytes": true, "sha256": strings.Repeat("a", 64)}, {"root": "workspace", "transfer_id": fixtureTaskID, "data_base64": "YQ==\n"}, {"root": "workspace", "transfer_id": fixtureTaskID, "data_base64": base64.StdEncoding.EncodeToString(make([]byte, 6145))}} {
		action := "file.hash"
		if _, ok := p["size_bytes"]; ok {
			action = "file.begin"
		}
		if _, ok := p["data_base64"]; ok {
			action = "file.write"
		}
		_, e := f.bridge.Developer(context.Background(), "vm", action, p, true)
		checkCode(t, e, "INVALID_ARGUMENT")
	}
	if f.calls != 0 {
		t.Fatal("invalid request ran")
	}
	p := map[string]any{"root": "workspace", "path": `nested\中文 file.bin`}
	before, _ := json.Marshal(p)
	normalized, e := normalizeFileParameters("file.hash", p)
	if e != nil || normalized["path"] != "nested/中文 file.bin" {
		t.Fatal(normalized, e)
	}
	after, _ := json.Marshal(p)
	if string(before) != string(after) {
		t.Fatal("caller map changed")
	}
}
func TestCompositeDryRunDoesNotReadFilesAndRedactsPaths(t *testing.T) {
	for _, action := range []string{"files.upload", "files.download", "files.sync", "bootstrap.install", "bootstrap.rollback"} {
		f := newFixture(t)
		f.bridge.options.DryRun = true
		p := map[string]any{"root": "workspace", "path": "fixture.txt", "local_path": "/synthetic/missing/file"}
		if action == "files.sync" {
			p = map[string]any{"profile": "/synthetic/missing/profile.json"}
		}
		if action == "bootstrap.install" {
			p = map[string]any{"source_dir": "/synthetic/missing/helper", "target_dir": `C:\Synthetic\Helper`}
		}
		if action == "bootstrap.rollback" {
			p = map[string]any{"target_dir": `C:\Synthetic\Helper`, "installation_id": fixtureTaskID}
		}
		_, e := f.bridge.Developer(context.Background(), "vm", action, p, false)
		checkCode(t, e, "WRITE_NOT_ALLOWED")
		r, e := f.bridge.Developer(context.Background(), "vm", action, p, true)
		if e != nil || r["ok"] != true || f.calls != 0 {
			t.Fatal(action, r, e)
		}
		bytes, _ := json.Marshal(r)
		if strings.Contains(string(bytes), "Synthetic") || strings.Contains(string(bytes), "synthetic") || strings.Contains(string(bytes), "fixture.txt") {
			t.Fatal("preview revealed path")
		}
	}
}
func TestUploadChunksAndNeverRetriesUnknownWrite(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "timeout"}[fail], func(t *testing.T) {
			dir := fileTestDir(t)
			content := []byte(strings.Repeat("中 x\x00", 3000))
			local := filepath.Join(dir, "input.bin")
			fileWriteTest(t, local, content)
			f := newFixture(t)
			calls := []string{}
			received := []byte{}
			f.bridge.runner = func(ctx context.Context, exe string, args []string) (commandResult, error) {
				request := requestFromArgs(t, args)
				action := request["action"].(string)
				p := mapValue(t, request["params"])
				calls = append(calls, action)
				d := map[string]any{"root": "workspace", "path": "nested/file.bin", "transfer_id": fixtureTaskID}
				switch action {
				case "file.begin":
					if p["overwrite"] != false {
						t.Fatal("overwrite default")
					}
					d["offset"] = 0
				case "file.write":
					if fail {
						return commandResult{}, context.DeadlineExceeded
					}
					chunk, _ := base64.StdEncoding.DecodeString(p["data_base64"].(string))
					off, _ := integral(p["offset"])
					if off != int64(len(received)) || len(chunk) > 6144 {
						t.Fatal("chunk boundary")
					}
					received = append(received, chunk...)
					delete(d, "path")
					d["next_offset"] = len(received)
				case "file.commit":
					hash := sha256.Sum256(content)
					d["size_bytes"] = len(content)
					d["sha256"] = hex.EncodeToString(hash[:])
				default:
					t.Fatal(action)
				}
				return commandResult{stdout: fileEnvelope(action, d)}, nil
			}
			r, e := f.bridge.Developer(context.Background(), "vm", "files.upload", map[string]any{"root": "workspace", "path": "nested/file.bin", "local_path": local}, true)
			if fail {
				checkCode(t, e, "TIMEOUT")
				if !reflect.DeepEqual(calls, []string{"file.begin", "file.write"}) {
					t.Fatal(calls)
				}
			} else {
				if e != nil || r["ok"] != true || string(received) != string(content) || calls[len(calls)-1] != "file.commit" {
					t.Fatal(r, e, calls)
				}
			}
		})
	}
}
func TestDownloadPreservesDestinationAndPartialOnFailure(t *testing.T) {
	for _, change := range []string{"success", "remote_change", "timeout", "existing"} {
		t.Run(change, func(t *testing.T) {
			dir := fileTestDir(t)
			target := filepath.Join(dir, "result.bin")
			content := []byte("中文\x00artifact")
			hash := sha256.Sum256(content)
			digest := hex.EncodeToString(hash[:])
			f := newFixture(t)
			hashCalls := 0
			f.bridge.runner = func(ctx context.Context, exe string, args []string) (commandResult, error) {
				request := requestFromArgs(t, args)
				action := request["action"].(string)
				d := map[string]any{"root": "artifacts", "path": "result.bin", "size_bytes": len(content), "sha256": digest, "version": "v1"}
				switch action {
				case "file.hash":
					hashCalls++
					if change == "remote_change" && hashCalls == 2 {
						d["version"] = "v2"
					}
				case "file.read":
					if change == "timeout" {
						return commandResult{}, context.DeadlineExceeded
					}
					delete(d, "sha256")
					d["offset"] = 0
					d["next_offset"] = len(content)
					d["data_base64"] = base64.StdEncoding.EncodeToString(content)
					d["eof"] = true
				default:
					t.Fatal(action)
				}
				return commandResult{stdout: fileEnvelope(action, d)}, nil
			}
			if change != "success" {
				fileWriteTest(t, target, []byte("previous"))
			}
			p := map[string]any{"root": "artifacts", "path": "result.bin", "local_path": target, "overwrite": change != "existing"}
			r, e := f.bridge.Developer(context.Background(), "vm", "files.download", p, true)
			if change == "success" {
				if e != nil || r["ok"] != true {
					t.Fatal(r, e)
				}
				bytes, _ := os.ReadFile(target)
				if string(bytes) != string(content) {
					t.Fatal("wrong artifact")
				}
			} else {
				if change == "existing" {
					checkCode(t, e, "FILE_EXISTS")
				} else if change == "timeout" {
					checkCode(t, e, "TIMEOUT")
				} else {
					checkCode(t, e, "FILE_CHANGED")
				}
				bytes, _ := os.ReadFile(target)
				if string(bytes) != "previous" {
					t.Fatal("existing artifact was replaced")
				}
				parts, _ := filepath.Glob(filepath.Join(dir, ".starbridge-part-*"))
				if change != "existing" && len(parts) != 1 {
					t.Fatal("partial not preserved")
				}
			}
		})
	}
}
func TestFileResponseIdentityBudgetAndBase64(t *testing.T) {
	for _, change := range []string{"none", "root", "path", "offset", "limit", "version", "size", "eof", "encoding"} {
		t.Run(change, func(t *testing.T) {
			f := newFixture(t)
			d := map[string]any{"root": "workspace", "path": "a.bin", "size_bytes": 3, "version": "v1", "offset": 0, "next_offset": 3, "data_base64": "YWJj", "eof": true}
			switch change {
			case "root":
				d["root"] = "other"
			case "path":
				d["path"] = "b.bin"
			case "offset":
				d["offset"] = 1
				d["next_offset"] = 4
			case "limit":
				d["data_base64"] = "YWJjZA=="
				d["next_offset"] = 4
				d["size_bytes"] = 4
			case "version":
				d["version"] = "v2"
			case "size":
				d["size_bytes"] = 4
				d["eof"] = false
			case "eof":
				d["eof"] = false
			case "encoding":
				d["data_base64"] = "YWJj\n"
			}
			f.response.stdout = fileEnvelope("file.read", d)
			_, e := f.bridge.Developer(context.Background(), "vm", "file.read", map[string]any{"root": "workspace", "path": "a.bin", "offset": 0, "limit": 3, "expected_size": 3, "expected_version": "v1"}, false)
			if change == "none" {
				if e != nil {
					t.Fatal(e)
				}
			} else {
				checkCode(t, e, "INVALID_GUEST_RESPONSE")
			}
		})
	}
}
func TestSyncIncrementalExclusionsAndProfileRelativeSource(t *testing.T) {
	dir := fileTestDir(t)
	if e := os.MkdirAll(filepath.Join(dir, "src", ".git"), 0700); e != nil {
		t.Fatal(e)
	}
	fileWriteTest(t, filepath.Join(dir, "src", "keep.txt"), []byte("unchanged"))
	fileWriteTest(t, filepath.Join(dir, "src", ".git", "private-fixture"), []byte("not sent"))
	profile := filepath.Join(dir, "sync.json")
	fileWriteTest(t, profile, []byte(`{"source":"src","root":"workspace","path":"project","exclude":["**/.git/**"],"overwrite":false}`))
	f := newFixture(t)
	calls := 0
	hash := sha256.Sum256([]byte("unchanged"))
	f.bridge.runner = func(ctx context.Context, exe string, args []string) (commandResult, error) {
		r := requestFromArgs(t, args)
		if r["action"] != "file.hash" {
			t.Fatal("unchanged file was transferred")
		}
		p := mapValue(t, r["params"])
		if p["path"] != "project/keep.txt" {
			t.Fatal(p)
		}
		calls++
		return commandResult{stdout: fileEnvelope("file.hash", map[string]any{"root": "workspace", "path": "project/keep.txt", "size_bytes": 9, "sha256": hex.EncodeToString(hash[:]), "version": "v1"})}, nil
	}
	r, e := f.bridge.Developer(context.Background(), "vm", "files.sync", map[string]any{"profile": profile}, true)
	if e != nil || r["ok"] != true || calls != 1 || mapValue(t, r["data"])["skipped"] != 1 {
		t.Fatal(r, e, calls)
	}
	for _, source := range []string{"../outside", "/absolute", `..\\outside`} {
		bytes, _ := json.Marshal(map[string]any{"source": source, "root": "workspace"})
		fileWriteTest(t, profile, bytes)
		_, e = f.bridge.Developer(context.Background(), "vm", "files.sync", map[string]any{"profile": profile}, true)
		checkCode(t, e, "INVALID_ARGUMENT")
	}
	if calls != 1 {
		t.Fatal("invalid profile ran")
	}
}
func TestTransferRejectsLocalSymbolicLink(t *testing.T) {
	dir := fileTestDir(t)
	source := filepath.Join(dir, "source.txt")
	fileWriteTest(t, source, []byte("fixture"))
	link := filepath.Join(dir, "link.txt")
	if e := os.Symlink(source, link); e != nil {
		t.Skip("symlink unavailable")
	}
	f := newFixture(t)
	_, e := f.bridge.Developer(context.Background(), "vm", "files.upload", map[string]any{"root": "workspace", "path": "file.txt", "local_path": link}, true)
	checkCode(t, e, "INVALID_ARGUMENT")
	if f.calls != 0 {
		t.Fatal("symlink transfer ran")
	}
}
func decodeBootstrap(t *testing.T, args []string) map[string]any {
	t.Helper()
	if args[len(args)-2] != "-EncodedCommand" {
		t.Fatal("fixed encoded transport missing")
	}
	encoded := args[len(args)-1]
	if len(encoded) > 30000 {
		t.Fatal("Windows command budget")
	}
	bytes, e := base64.StdEncoding.DecodeString(encoded)
	if e != nil || len(bytes)%2 != 0 {
		t.Fatal(e)
	}
	units := make([]uint16, len(bytes)/2)
	for i := range units {
		units[i] = uint16(bytes[i*2]) | uint16(bytes[i*2+1])<<8
	}
	script := string(utf16.Decode(units))
	start := strings.Index(script, "FromBase64String('") + len("FromBase64String('")
	end := strings.Index(script[start:], "')")
	if end < 0 {
		t.Fatal("request prefix missing")
	}
	request, e := base64.StdEncoding.DecodeString(script[start : start+end])
	if e != nil {
		t.Fatal(e)
	}
	v, e := decodeJSON(string(request))
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(script, "[IO.Compression.GZipStream]::new") || !strings.HasSuffix(script, "& ([ScriptBlock]::Create($l))") {
		t.Fatal("fixed compressed loader missing")
	}
	return mapValue(t, v)
}
func bootstrapEnvelope(d map[string]any) string {
	bytes, _ := json.Marshal(Success(d))
	return string(bytes)
}
func TestBootstrapCopiesCompleteTreeManifestAndNeverRetries(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "unknown"}[fail], func(t *testing.T) {
			dir := fileTestDir(t)
			if e := os.MkdirAll(filepath.Join(dir, "developer", "workers"), 0700); e != nil {
				t.Fatal(e)
			}
			for _, name := range []string{"desktop-bridge.ps1", "developer-bridge.ps1", "developer/system.ps1", "developer/tasks.ps1", "developer/workers/task-native.cs"} {
				fileWriteTest(t, filepath.Join(dir, filepath.FromSlash(name)), []byte("# synthetic fixture only\n"))
			}
			f := newFixture(t)
			received := map[string][]byte{}
			ops := []string{}
			f.bridge.runner = func(ctx context.Context, exe string, args []string) (commandResult, error) {
				p := decodeBootstrap(t, args)
				op := p["op"].(string)
				ops = append(ops, op)
				d := map[string]any{"installation_id": fixtureTaskID}
				switch op {
				case "prepare":
					if p["overwrite"] != false {
						t.Fatal("overwrite default")
					}
				case "put":
					if fail {
						return commandResult{}, context.DeadlineExceeded
					}
					name := p["path"].(string)
					off, _ := integral(p["offset"])
					chunk, _ := base64.StdEncoding.DecodeString(p["data_base64"].(string))
					if int(off) != len(received[name]) || len(chunk) > bootstrapChunkBytes {
						t.Fatal("bootstrap chunk")
					}
					received[name] = append(received[name], chunk...)
					d["next_offset"] = len(received[name])
				case "commit":
					manifest := received[".starbridge-manifest.json"]
					hash := sha256.Sum256(manifest)
					if p["manifest_sha256"] != hex.EncodeToString(hash[:]) {
						t.Fatal("manifest hash")
					}
					d["manifest_sha256"] = p["manifest_sha256"]
					d["file_count"] = 5
					d["backup_available"] = false
				default:
					t.Fatal(op)
				}
				return commandResult{stdout: bootstrapEnvelope(d)}, nil
			}
			r, e := f.bridge.Developer(context.Background(), "vm", "bootstrap.install", map[string]any{"source_dir": dir, "target_dir": `C:\Synthetic\Helper`}, true)
			if fail {
				checkCode(t, e, "TIMEOUT")
				if !reflect.DeepEqual(ops, []string{"prepare", "put"}) {
					t.Fatal(ops)
				}
			} else if e != nil || r["ok"] != true || len(received) != 6 {
				t.Fatal(r, e, len(received))
			}
		})
	}
}
func TestBootstrapRejectsIncompleteTreeBeforeMutation(t *testing.T) {
	dir := fileTestDir(t)
	fileWriteTest(t, filepath.Join(dir, "desktop-bridge.ps1"), []byte("fixture"))
	f := newFixture(t)
	_, e := f.bridge.Developer(context.Background(), "vm", "bootstrap.install", map[string]any{"source_dir": dir, "target_dir": `C:\Synthetic\Helper`}, true)
	checkCode(t, e, "INVALID_ARGUMENT")
	if f.calls != 0 {
		t.Fatal("incomplete install mutated")
	}
	for _, target := range []string{`C:\`, `C:\x\..\y`, `\\server\share\helper`, `C:\CON\helper`} {
		_, e = f.bridge.Developer(context.Background(), "vm", "bootstrap.rollback", map[string]any{"target_dir": target, "installation_id": fixtureTaskID}, true)
		checkCode(t, e, "INVALID_ARGUMENT")
	}
}
func TestSyncStopsOnReadFailureAndDoesNotAssumeMissing(t *testing.T) {
	dir := fileTestDir(t)
	if e := os.Mkdir(filepath.Join(dir, "src"), 0700); e != nil {
		t.Fatal(e)
	}
	fileWriteTest(t, filepath.Join(dir, "src", "a.txt"), []byte("fixture"))
	profile := filepath.Join(dir, "sync.json")
	fileWriteTest(t, profile, []byte(`{"source":"src","root":"workspace","path":"project"}`))
	f := newFixture(t)
	calls := 0
	f.bridge.runner = func(ctx context.Context, exe string, args []string) (commandResult, error) {
		calls++
		return commandResult{}, errors.New("synthetic runner failure")
	}
	_, e := f.bridge.Developer(context.Background(), "vm", "files.sync", map[string]any{"profile": profile}, true)
	checkCode(t, e, "EXECUTION_FAILED")
	if calls != 1 {
		t.Fatal("retried failed read")
	}
}

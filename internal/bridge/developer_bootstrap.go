package bridge

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"
)

//go:embed developer_bootstrap.ps1
var bootstrapScript string

const bootstrapChunkBytes = 1024

func normalizeBootstrap(action string, p map[string]any) (map[string]any, error) {
	fields := []string{"target_dir", "source_dir", "overwrite"}
	if action == "bootstrap.rollback" {
		fields = []string{"target_dir", "installation_id"}
	}
	out, e := fileFields(p, fields...)
	if e != nil {
		return nil, e
	}
	target, e := fileString(p, "target_dir")
	if e != nil {
		return nil, e
	}
	target = strings.ReplaceAll(target, "/", `\`)
	if !drivePath.MatchString(target) || len(target) < 4 || !taskText(target, 2048) {
		return nil, fileArgument("target_dir must be an explicit Windows drive path.")
	}
	suffix, e := filePath(target[3:], false)
	if e != nil {
		return nil, e
	}
	out["target_dir"] = target[:3] + strings.ReplaceAll(suffix, "/", `\`)
	if action == "bootstrap.install" {
		out["source_dir"], e = localAbsolute(p, "source_dir")
		if e != nil {
			return nil, e
		}
		out["overwrite"], e = fileBool(p, "overwrite", false)
	} else {
		id, ok := p["installation_id"].(string)
		if !ok || !taskIDPattern.MatchString(id) {
			return nil, fileArgument("Invalid installation_id.")
		}
		out["installation_id"] = id
	}
	return out, e
}
func init() {
	for _, name := range []string{"bootstrap.install", "bootstrap.rollback"} {
		action := name
		registerDeveloperAction(DeveloperAction{Name: action, Description: "Install a complete hash-verified helper directory or restore its previous installation.", Mutation: true, Normalize: func(p map[string]any) (map[string]any, error) { return normalizeBootstrap(action, p) }, RedactPreview: func(p map[string]any) map[string]any {
			for _, k := range []string{"source_dir", "target_dir"} {
				if _, ok := p[k]; ok {
					p[k] = "[redacted]"
				}
			}
			return p
		}, Execute: func(ctx context.Context, b *Bridge, vm string, p map[string]any, allow bool) (Result, error) {
			if action == "bootstrap.rollback" {
				return bootstrapRollback(ctx, b, vm, p)
			}
			return bootstrapInstall(ctx, b, vm, p)
		}})
	}
}
func bootstrapArguments(vm string, p map[string]any) ([]string, error) {
	request, e := json.Marshal(p)
	if e != nil {
		return nil, fileArgument("Invalid bootstrap request.")
	}

	var compressed bytes.Buffer
	zipper := gzip.NewWriter(&compressed)
	_, _ = zipper.Write([]byte(bootstrapScript))
	_ = zipper.Close()
	script := "$r=[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('" + base64.StdEncoding.EncodeToString(request) + "'))|ConvertFrom-Json;" +
		"$m=[IO.MemoryStream]::new([Convert]::FromBase64String('" + base64.StdEncoding.EncodeToString(compressed.Bytes()) + "'),$false);" +
		"$g=[IO.Compression.GZipStream]::new($m,[IO.Compression.CompressionMode]::Decompress);$s=[IO.StreamReader]::new($g,[Text.Encoding]::UTF8);try{$l=$s.ReadToEnd()}finally{$s.Dispose()}; & ([ScriptBlock]::Create($l))"

	units := utf16.Encode([]rune(script))
	bytes := make([]byte, len(units)*2)
	for i, u := range units {
		bytes[i*2] = byte(u)
		bytes[i*2+1] = byte(u >> 8)
	}
	encoded := base64.StdEncoding.EncodeToString(bytes)
	if len(encoded) > 30000 {
		return nil, fileArgument("Bootstrap transport exceeds the Windows command-line budget.")
	}
	return []string{"exec", vm, "--current-user", "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", encoded}, nil
}
func bootstrapCall(ctx context.Context, b *Bridge, vm string, p map[string]any) (map[string]any, error) {
	args, e := bootstrapArguments(vm, p)
	if e != nil {
		return nil, e
	}
	raw, e := b.run(ctx, args, false, true, true)
	if e != nil {
		return nil, e
	}
	response, _ := asObject(raw)
	if response["ok"] != true {
		data, _ := asObject(response["error"])
		code, _ := data["code"].(string)
		if code == "" {
			code = "BOOTSTRAP_IO"
		}
		return nil, NewError(code, "Helper installation operation failed; inspect state before retrying.", nil)
	}
	data, ok := asObject(response["data"])
	if !ok {
		return nil, invalidGuestResponse()
	}
	id, ok := data["installation_id"].(string)
	if !ok || !taskIDPattern.MatchString(id) {
		return nil, invalidGuestResponse()
	}
	if wanted, present := p["installation_id"]; present && wanted != id {
		return nil, invalidGuestResponse()
	}
	return data, nil
}

type bootstrapFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size_bytes"`
	SHA256 string `json:"sha256"`
	local  string
}

func bootstrapManifest(source string) ([]bootstrapFile, []byte, error) {
	if e := checkLocalPath(source, false); e != nil {
		return nil, nil, e
	}
	files := []bootstrapFile{}
	total := int64(0)
	e := filepath.WalkDir(source, func(current string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fileIO("Cannot enumerate helper source.")
		}
		if current == source {
			if !d.IsDir() {
				return fileArgument("source_dir must be a directory.")
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fileArgument("Helper source cannot contain symbolic links.")
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(source, current)
		rel = filepath.ToSlash(rel)
		if rel != "desktop-bridge.ps1" && rel != "developer-bridge.ps1" && !strings.HasPrefix(rel, "developer/") {
			return nil
		}
		if _, e := filePath(rel, false); e != nil {
			return e
		}
		ext := strings.ToLower(filepath.Ext(rel))
		if ext != ".ps1" && ext != ".cs" {
			return fileArgument("Helper tree may contain only PowerShell and C# source files.")
		}
		f, st, e := openSource(current)
		if e != nil {
			return e
		}
		defer f.Close()
		if st.Size() > 8<<20 {
			return fileArgument("Helper files are limited to 8 MiB each.")
		}
		hash, e := hashFile(f)
		if e != nil {
			return e
		}
		if !sameSource(f, st) {
			return NewError("FILE_CHANGED", "Helper source changed during hashing.", nil)
		}
		total += st.Size()
		files = append(files, bootstrapFile{rel, st.Size(), hash, current})
		if len(files) > 256 || total > 64<<20 {
			return fileArgument("Helper tree is limited to 256 files and 64 MiB.")
		}
		return nil
	})
	if e != nil {
		return nil, nil, e
	}
	seen := map[string]bool{}
	for _, f := range files {
		key := strings.ToLower(f.Path)
		if seen[key] {
			return nil, nil, fileArgument("Helper source has case-conflicting paths.")
		}
		seen[key] = true
	}
	for _, required := range []string{"desktop-bridge.ps1", "developer-bridge.ps1", "developer/system.ps1"} {
		if !seen[required] {
			return nil, nil, fileArgument("source_dir must contain the complete Windows helper tree.")
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	manifest, e := json.Marshal(map[string]any{"version": 1, "files": files})
	if e != nil || len(manifest) > 131072 {
		return nil, nil, fileArgument("Helper manifest is too large.")
	}
	return files, manifest, nil
}
func bootstrapPut(ctx context.Context, b *Bridge, vm, target, id, name string, data []byte) error {
	for offset := 0; offset < len(data) || offset == 0; {
		end := offset + bootstrapChunkBytes
		if end > len(data) {
			end = len(data)
		}
		d, e := bootstrapCall(ctx, b, vm, map[string]any{"op": "put", "target_dir": target, "installation_id": id, "path": name, "offset": offset, "data_base64": base64.StdEncoding.EncodeToString(data[offset:end])})
		if e != nil {
			return e
		}
		next, ok := integral(d["next_offset"])
		if !ok || next != int64(end) {
			return invalidGuestResponse()
		}
		if end == len(data) {
			return nil
		}
		offset = end
	}
	return nil
}
func bootstrapInstall(ctx context.Context, b *Bridge, vm string, p map[string]any) (Result, error) {
	files, manifest, e := bootstrapManifest(p["source_dir"].(string))
	if e != nil {
		return nil, e
	}
	target := p["target_dir"].(string)
	// Reject transport size failures before creating any guest staging directory.
	for _, file := range append(append([]bootstrapFile{}, files...), bootstrapFile{Path: ".starbridge-manifest.json"}) {
		_, err := bootstrapArguments(vm, map[string]any{"op": "put", "target_dir": target, "installation_id": strings.Repeat("0", 32), "path": file.Path, "offset": 8388608, "data_base64": base64.StdEncoding.EncodeToString(make([]byte, bootstrapChunkBytes))})
		if err != nil {
			return nil, err
		}
	}
	prepared, e := bootstrapCall(ctx, b, vm, map[string]any{"op": "prepare", "target_dir": target, "overwrite": p["overwrite"]})
	if e != nil {
		return nil, e
	}
	id := prepared["installation_id"].(string)
	for _, file := range files {
		if e = checkLocalPath(file.local, false); e != nil {
			return nil, e
		}
		source, stat, e := openSource(file.local)
		if e != nil {
			return nil, e
		}
		data, e := io.ReadAll(io.LimitReader(source, (8<<20)+1))
		stable := sameSource(source, stat)
		source.Close()
		if e != nil {
			return nil, fileIO("Cannot read helper source.")
		}
		if !stable || len(data) > 8<<20 {
			return nil, NewError("FILE_CHANGED", "Helper source changed before transfer.", map[string]any{"installation_id": id})
		}
		hash := sha256.Sum256(data)
		if int64(len(data)) != file.Size || hex.EncodeToString(hash[:]) != file.SHA256 {
			return nil, NewError("FILE_CHANGED", "Helper source changed before transfer.", map[string]any{"installation_id": id})
		}
		if e = bootstrapPut(ctx, b, vm, target, id, file.Path, data); e != nil {
			return nil, withFileIdentity(e, "installation_id", id)
		}
	}
	if e = bootstrapPut(ctx, b, vm, target, id, ".starbridge-manifest.json", manifest); e != nil {
		return nil, withFileIdentity(e, "installation_id", id)
	}
	hash := sha256.Sum256(manifest)
	digest := hex.EncodeToString(hash[:])
	d, e := bootstrapCall(ctx, b, vm, map[string]any{"op": "commit", "target_dir": target, "installation_id": id, "manifest_sha256": digest})
	if e != nil {
		return nil, withFileIdentity(e, "installation_id", id)
	}
	count, ok := integral(d["file_count"])
	if !ok || count != int64(len(files)) || d["manifest_sha256"] != digest || !boolValue(d["backup_available"]) {
		return nil, invalidGuestResponse()
	}
	return Success(d), nil
}
func bootstrapRollback(ctx context.Context, b *Bridge, vm string, p map[string]any) (Result, error) {
	d, e := bootstrapCall(ctx, b, vm, map[string]any{"op": "rollback", "target_dir": p["target_dir"], "installation_id": p["installation_id"]})
	if e != nil {
		return nil, e
	}
	if d["rolled_back"] != true {
		return nil, invalidGuestResponse()
	}
	return Success(d), nil
}

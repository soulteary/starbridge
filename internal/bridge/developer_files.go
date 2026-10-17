package bridge

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxTransferBytes int64 = 1 << 30
const uploadChunkBytes = 6144

var fileRootPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
var reservedWindowsName = regexp.MustCompile(`(?i)^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\.|$)`)

func fileArgument(message string) error { return NewError("INVALID_ARGUMENT", message, nil) }
func fileIO(message string) error       { return NewError("FILE_IO", message, nil) }
func filePath(value string, allowEmpty bool) (string, error) {
	value = strings.ReplaceAll(value, `\`, "/")
	if value == "" && allowEmpty {
		return "", nil
	}
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 2048 {
		return "", fileArgument("path must be a bounded relative Windows path.")
	}
	for _, r := range value {
		if unicode.IsControl(r) || strings.ContainsRune(`:<>"|?*`, r) {
			return "", fileArgument("path contains an unsupported character.")
		}
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." || strings.TrimRight(part, " .") != part || reservedWindowsName.MatchString(part) || strings.EqualFold(part, ".starbridge-transfers") {
			return "", fileArgument("path contains an unsupported segment.")
		}
	}
	return value, nil
}
func fileFields(p map[string]any, allowed ...string) (map[string]any, error) {
	out := make(map[string]any, len(p))
	keys := map[string]bool{}
	for _, k := range allowed {
		keys[k] = true
	}
	for k, v := range p {
		if !keys[k] {
			return nil, fileArgument("Unsupported file parameter.")
		}
		out[k] = v
	}
	return out, nil
}
func fileString(p map[string]any, k string) (string, error) {
	v, ok := p[k].(string)
	if !ok || v == "" || !taskText(v, 4096) {
		return "", fileArgument(k + " must be nonempty valid text.")
	}
	return v, nil
}
func fileBool(p map[string]any, k string, fallback bool) (bool, error) {
	v, present := p[k]
	if !present {
		return fallback, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, fileArgument(k + " must be boolean.")
	}
	return b, nil
}
func fileNumber(p map[string]any, k string, fallback, min, max int64) (int64, error) {
	v, present := p[k]
	if !present {
		return fallback, nil
	}
	if err := boundedInt(v, min, max, k); err != nil {
		return 0, err
	}
	n, _ := integral(v)
	return n, nil
}
func localAbsolute(p map[string]any, k string) (string, error) {
	s, err := fileString(p, k)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(s) || filepath.Clean(s) != s {
		return "", fileArgument(k + " must be an explicit clean absolute host path.")
	}
	return s, nil
}
func normalizeFileParameters(action string, p map[string]any) (map[string]any, error) {
	fields := []string{"root", "path"}
	switch action {
	case "file.read":
		fields = append(fields, "offset", "limit", "expected_size", "expected_version")
	case "file.begin":
		fields = append(fields, "size_bytes", "sha256", "overwrite")
	case "file.write":
		fields = []string{"root", "transfer_id", "offset", "data_base64"}
	case "file.commit", "file.abort":
		fields = []string{"root", "transfer_id"}
	case "files.upload", "files.download":
		fields = append(fields, "local_path", "overwrite")
	case "files.sync":
		fields = []string{"profile"}
	}
	out, err := fileFields(p, fields...)
	if err != nil {
		return nil, err
	}
	if action == "files.sync" {
		out["profile"], err = localAbsolute(p, "profile")
		return out, err
	}
	root, ok := p["root"].(string)
	if !ok || !fileRootPattern.MatchString(root) {
		return nil, fileArgument("root must name an explicitly configured guest root.")
	}
	out["root"] = root
	if action == "file.write" || action == "file.commit" || action == "file.abort" {
		id, ok := p["transfer_id"].(string)
		if !ok || !taskIDPattern.MatchString(id) {
			return nil, fileArgument("Invalid transfer_id.")
		}
		out["transfer_id"] = id
	} else {
		s, ok := p["path"].(string)
		if !ok {
			return nil, fileArgument("path must be a relative string.")
		}
		out["path"], err = filePath(s, false)
		if err != nil {
			return nil, err
		}
	}
	if action == "files.upload" || action == "files.download" {
		out["local_path"], err = localAbsolute(p, "local_path")
		if err != nil {
			return nil, err
		}
	}
	if action == "files.upload" || action == "files.download" || action == "file.begin" {
		out["overwrite"], err = fileBool(p, "overwrite", false)
		if err != nil {
			return nil, err
		}
	}
	if action == "file.begin" {
		if _, present := p["size_bytes"]; !present {
			return nil, fileArgument("size_bytes is required.")
		}
		out["size_bytes"], err = fileNumber(p, "size_bytes", 0, 0, maxTransferBytes)
		if err != nil {
			return nil, err
		}
		s, ok := p["sha256"].(string)
		if !ok || !developerHash.MatchString(s) {
			return nil, fileArgument("sha256 must be lowercase SHA-256.")
		}
		out["sha256"] = s
	}
	if action == "file.read" || action == "file.write" {
		out["offset"], err = fileNumber(p, "offset", 0, 0, maxTransferBytes)
		if err != nil {
			return nil, err
		}
	}
	if action == "file.read" {
		out["limit"], err = fileNumber(p, "limit", 65536, 1, 65536)
		if err != nil {
			return nil, err
		}
		if _, present := p["expected_size"]; !present {
			return nil, fileArgument("expected_size is required.")
		}
		out["expected_size"], err = fileNumber(p, "expected_size", 0, 0, maxTransferBytes)
		if err != nil {
			return nil, err
		}
		out["expected_version"], err = fileString(p, "expected_version")
		if err != nil {
			return nil, err
		}
		if !taskText(out["expected_version"].(string), 256) {
			return nil, fileArgument("expected_version must contain at most 256 characters.")
		}
	}
	if action == "file.write" {
		s, ok := p["data_base64"].(string)
		if !ok || len(s) > 8192 {
			return nil, fileArgument("data_base64 must contain at most 6144 bytes.")
		}
		data, e := base64.StdEncoding.Strict().DecodeString(s)
		if e != nil || len(data) == 0 || base64.StdEncoding.EncodeToString(data) != s {
			return nil, fileArgument("data_base64 must be nonempty canonical Base64.")
		}
		out["data_base64"] = s
	}
	return out, nil
}
func filePreview(p map[string]any) map[string]any {
	for _, k := range []string{"local_path", "profile", "path", "data_base64"} {
		if _, ok := p[k]; ok {
			p[k] = "[redacted]"
		}
	}
	return p
}
func validFileResult(action string, d map[string]any) bool {
	root, ok := d["root"].(string)
	if !ok || !fileRootPattern.MatchString(root) {
		return false
	}
	if action == "file.write" || action == "file.abort" || action == "file.begin" || action == "file.commit" {
		id, ok := d["transfer_id"].(string)
		if !ok || !taskIDPattern.MatchString(id) {
			return false
		}
	}
	switch action {
	case "file.write":
		return responseInteger(d["next_offset"], 0, maxTransferBytes)
	case "file.abort":
		return d["aborted"] == true
	}
	p, ok := d["path"].(string)
	if !ok {
		return false
	}
	normalized, e := filePath(p, false)
	if e != nil || normalized != p {
		return false
	}
	if action == "file.begin" {
		return responseInteger(d["offset"], 0, 0)
	}
	if !responseInteger(d["size_bytes"], 0, maxTransferBytes) {
		return false
	}
	if action == "file.read" {
		s, ok := d["data_base64"].(string)
		if !ok || len(s) > 87384 {
			return false
		}
		bytes, e := base64.StdEncoding.Strict().DecodeString(s)
		off, a := integral(d["offset"])
		next, b := integral(d["next_offset"])
		size, _ := integral(d["size_bytes"])
		return e == nil && base64.StdEncoding.EncodeToString(bytes) == s && a && b && off >= 0 && next == off+int64(len(bytes)) && next <= size && boolValue(d["eof"]) && d["eof"] == (next == size) && nonemptyText(d["version"])
	}
	hash, ok := d["sha256"].(string)
	return ok && developerHash.MatchString(hash) && (action != "file.hash" || nonemptyText(d["version"]))
}
func fileWithinRequest(action string, d, p map[string]any) bool {
	if d["root"] != p["root"] {
		return false
	}
	if v, present := p["path"]; present && d["path"] != v {
		return false
	}
	if v, present := p["transfer_id"]; present && d["transfer_id"] != v {
		return false
	}
	switch action {
	case "file.read":
		off, _ := integral(d["offset"])
		want, _ := integral(p["offset"])
		next, _ := integral(d["next_offset"])
		limit, _ := integral(p["limit"])
		size, _ := integral(d["size_bytes"])
		expected, _ := integral(p["expected_size"])
		return off == want && next-off <= limit && size == expected && d["version"] == p["expected_version"] && (next > off || d["eof"] == true)
	case "file.write":
		data, _ := base64.StdEncoding.DecodeString(p["data_base64"].(string))
		off, _ := integral(p["offset"])
		next, _ := integral(d["next_offset"])
		return next == off+int64(len(data))
	}
	return true
}
func init() {
	for _, name := range []string{"file.hash", "file.read", "file.begin", "file.write", "file.commit", "file.abort"} {
		action := name
		registerDeveloperAction(DeveloperAction{Name: action, Description: "Transfer a bounded file inside an explicitly configured Windows root.", Mutation: action != "file.hash" && action != "file.read", Normalize: func(p map[string]any) (map[string]any, error) { return normalizeFileParameters(action, p) }, ValidateResult: func(d map[string]any) bool { return validFileResult(action, d) }, ValidateWithinRequest: func(d, p map[string]any) bool { return fileWithinRequest(action, d, p) }, RedactPreview: filePreview})
	}
	for _, name := range []string{"files.upload", "files.download", "files.sync"} {
		action := name
		registerDeveloperAction(DeveloperAction{Name: action, Description: "Transfer or incrementally synchronize explicit host and guest paths.", Mutation: true, Normalize: func(p map[string]any) (map[string]any, error) { return normalizeFileParameters(action, p) }, RedactPreview: filePreview, Execute: func(ctx context.Context, b *Bridge, vm string, p map[string]any, allow bool) (Result, error) {
			switch action {
			case "files.upload":
				return uploadFile(ctx, b, vm, p)
			case "files.download":
				return downloadFile(ctx, b, vm, p)
			default:
				return syncFiles(ctx, b, vm, p)
			}
		}})
	}
}

// Keep a durable operation identity on errors without exposing local paths or bytes.
func withFileIdentity(err error, key string, value any) error {
	var original *Error
	if !errors.As(err, &original) {
		return err
	}
	details := make(map[string]any, len(original.Details)+1)
	for k, v := range original.Details {
		details[k] = v
	}
	details[key] = value
	return NewError(original.Code, original.Message, details)
}
func callFile(ctx context.Context, b *Bridge, vm, action string, p map[string]any) (map[string]any, error) {
	r, e := b.Developer(ctx, vm, action, p, true)
	if e != nil {
		return nil, e
	}
	if r["ok"] != true {
		err, _ := asObject(r["error"])
		code, _ := err["code"].(string)
		if code == "" {
			code = "FILE_IO"
		}
		return nil, NewError(code, "Guest file operation failed.", nil)
	}
	d, ok := asObject(r["data"])
	if !ok {
		return nil, invalidGuestResponse()
	}
	return d, nil
}
func checkLocalPath(p string, finalMayNotExist bool) error {
	clean := filepath.Clean(p)
	volume := filepath.VolumeName(clean)
	current := volume + string(filepath.Separator)
	parts := strings.Split(strings.TrimPrefix(clean, current), string(filepath.Separator))
	for i, part := range parts {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		st, e := os.Lstat(current)
		if e != nil {
			if finalMayNotExist && i == len(parts)-1 && errors.Is(e, os.ErrNotExist) {
				return nil
			}
			return fileIO("An explicit local path is unavailable.")
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return fileArgument("Symbolic links are not supported in transfer paths.")
		}
		if i < len(parts)-1 && !st.IsDir() {
			return fileIO("A local parent is not a directory.")
		}
	}
	return nil
}
func openSource(p string) (*os.File, os.FileInfo, error) {
	if e := checkLocalPath(p, false); e != nil {
		return nil, nil, e
	}
	f, e := os.Open(p)
	if e != nil {
		return nil, nil, fileIO("Cannot open local source.")
	}
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() > maxTransferBytes {
		f.Close()
		return nil, nil, fileArgument("Source must be a regular file of at most 1 GiB.")
	}
	return f, st, nil
}
func hashFile(f *os.File) (string, error) {
	if _, e := f.Seek(0, 0); e != nil {
		return "", fileIO("Cannot seek source.")
	}
	h := sha256.New()
	if _, e := io.Copy(h, io.LimitReader(f, maxTransferBytes+1)); e != nil {
		return "", fileIO("Cannot hash source.")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func sameSource(f *os.File, st os.FileInfo) bool {
	after, e := f.Stat()
	return e == nil && after.Size() == st.Size() && after.ModTime() == st.ModTime() && os.SameFile(st, after)
}
func uploadFile(ctx context.Context, b *Bridge, vm string, p map[string]any) (Result, error) {
	f, st, e := openSource(p["local_path"].(string))
	if e != nil {
		return nil, e
	}
	defer f.Close()
	hash, e := hashFile(f)
	if e != nil {
		return nil, e
	}
	if !sameSource(f, st) {
		return nil, NewError("FILE_CHANGED", "Local source changed during hashing.", nil)
	}
	begin, e := callFile(ctx, b, vm, "file.begin", map[string]any{"root": p["root"], "path": p["path"], "size_bytes": st.Size(), "sha256": hash, "overwrite": p["overwrite"]})
	if e != nil {
		return nil, e
	}
	id := begin["transfer_id"]
	if _, e = f.Seek(0, 0); e != nil {
		return nil, fileIO("Cannot rewind local source.")
	}
	buf := make([]byte, uploadChunkBytes)
	offset := int64(0)
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			_, e = callFile(ctx, b, vm, "file.write", map[string]any{"root": p["root"], "transfer_id": id, "offset": offset, "data_base64": base64.StdEncoding.EncodeToString(buf[:n])})
			if e != nil {
				return nil, withFileIdentity(e, "transfer_id", id)
			}
			offset += int64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, fileIO("Cannot read local source.")
		}
		if e = ctx.Err(); e != nil {
			return nil, NewError("INTERRUPTED", "Transfer interrupted; staged guest bytes are preserved.", map[string]any{"outcome": "unknown", "transfer_id": id})
		}
	}
	if offset != st.Size() || !sameSource(f, st) {
		return nil, NewError("FILE_CHANGED", "Local source changed during transfer; staged bytes are preserved.", map[string]any{"transfer_id": id})
	}
	result, e := callFile(ctx, b, vm, "file.commit", map[string]any{"root": p["root"], "transfer_id": id})
	if e != nil {
		return nil, withFileIdentity(e, "transfer_id", id)
	}
	size, _ := integral(result["size_bytes"])
	if result["path"] != p["path"] || size != st.Size() || result["sha256"] != hash {
		return nil, invalidGuestResponse()
	}
	return Success(result), nil
}
func downloadFile(ctx context.Context, b *Bridge, vm string, p map[string]any) (Result, error) {
	target := p["local_path"].(string)
	if e := checkLocalPath(target, true); e != nil {
		return nil, e
	}
	if st, e := os.Lstat(target); e == nil {
		if !p["overwrite"].(bool) {
			return nil, NewError("FILE_EXISTS", "Local destination exists; explicit overwrite is required.", nil)
		}
		if !st.Mode().IsRegular() {
			return nil, fileArgument("Local destination must be a regular file.")
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, fileIO("Cannot inspect local destination.")
	}
	identity := map[string]any{"root": p["root"], "path": p["path"]}
	remote, e := callFile(ctx, b, vm, "file.hash", identity)
	if e != nil {
		return nil, e
	}
	size, _ := integral(remote["size_bytes"])
	temp, e := os.CreateTemp(filepath.Dir(target), ".starbridge-part-*")
	if e != nil {
		return nil, fileIO("Cannot create partial download beside the destination.")
	}
	defer temp.Close()
	h := sha256.New()
	offset := int64(0)
	for offset < size {
		data, e := callFile(ctx, b, vm, "file.read", map[string]any{"root": p["root"], "path": p["path"], "offset": offset, "limit": 65536, "expected_size": size, "expected_version": remote["version"]})
		if e != nil {
			return nil, e
		}
		bytes, _ := base64.StdEncoding.DecodeString(data["data_base64"].(string))
		if _, e = temp.Write(bytes); e != nil {
			return nil, fileIO("Cannot write partial download; existing destination is preserved.")
		}
		h.Write(bytes)
		offset += int64(len(bytes))
	}
	final, e := callFile(ctx, b, vm, "file.hash", identity)
	if e != nil {
		return nil, e
	}
	if final["sha256"] != remote["sha256"] || final["version"] != remote["version"] || hex.EncodeToString(h.Sum(nil)) != remote["sha256"] {
		return nil, NewError("FILE_CHANGED", "Guest source changed; partial download is preserved.", nil)
	}
	if e = temp.Sync(); e != nil {
		return nil, fileIO("Cannot persist partial download.")
	}
	if e = temp.Close(); e != nil {
		return nil, fileIO("Cannot close partial download.")
	}
	if e = checkLocalPath(target, true); e != nil {
		return nil, e
	}
	if p["overwrite"].(bool) {
		e = os.Rename(temp.Name(), target)
	} else {
		e = os.Link(temp.Name(), target)
		if e == nil {
			_ = os.Remove(temp.Name())
		}
	}
	if e != nil {
		if errors.Is(e, os.ErrExist) {
			return nil, NewError("FILE_EXISTS", "Destination appeared during download; partial bytes are preserved.", nil)
		}
		return nil, fileIO("Cannot publish download; partial bytes are preserved.")
	}
	return Success(map[string]any{"root": p["root"], "path": p["path"], "size_bytes": size, "sha256": remote["sha256"], "downloaded": true}), nil
}

type syncProfile struct {
	Source    string   `json:"source"`
	Root      string   `json:"root"`
	Path      string   `json:"path"`
	Exclude   []string `json:"exclude"`
	Overwrite bool     `json:"overwrite"`
}

func readSyncProfile(filename string) (syncProfile, error) {
	var p syncProfile
	if e := checkLocalPath(filename, false); e != nil {
		return p, e
	}
	f, e := os.Open(filename)
	if e != nil {
		return p, fileIO("Cannot open sync profile.")
	}
	defer f.Close()
	st, statErr := f.Stat()
	if statErr != nil || !st.Mode().IsRegular() || st.Size() > 65536 {
		return p, fileArgument("Sync profile must be a regular JSON file of at most 65536 bytes.")
	}
	decoder := json.NewDecoder(io.LimitReader(f, 65537))
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(&p); e != nil {
		return p, fileArgument("Invalid sync profile.")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return p, fileArgument("Sync profile must contain one object.")
	}
	if p.Source == "" || filepath.IsAbs(p.Source) || strings.Contains(p.Source, `\`) {
		return p, fileArgument("Profile source must be relative to its directory.")
	}
	for _, s := range strings.Split(p.Source, "/") {
		if s == ".." || s == "" {
			return p, fileArgument("Profile source cannot traverse parent directories.")
		}
	}
	if !fileRootPattern.MatchString(p.Root) {
		return p, fileArgument("Invalid sync root.")
	}
	p.Path, e = filePath(p.Path, true)
	if e != nil {
		return p, e
	}
	if len(p.Exclude) > 64 {
		return p, fileArgument("At most 64 exclude globs are supported.")
	}
	for _, glob := range p.Exclude {
		if len(glob) > 256 || strings.HasPrefix(glob, "/") || strings.Contains(glob, `\`) {
			return p, fileArgument("Invalid exclude glob.")
		}
		for _, seg := range strings.Split(glob, "/") {
			if seg == ".." {
				return p, fileArgument("Invalid exclude glob.")
			}
			if seg != "**" {
				if _, e = path.Match(seg, ""); e != nil {
					return p, fileArgument("Invalid exclude glob.")
				}
			}
		}
	}
	return p, nil
}
func matchFileGlob(glob, name string) bool {
	g, n := strings.Split(glob, "/"), strings.Split(name, "/")
	type position struct{ i, j int }
	memo, visited := map[position]bool{}, map[position]bool{}
	var match func(int, int) bool
	match = func(i, j int) bool {
		at := position{i, j}
		if visited[at] {
			return memo[at]
		}
		visited[at] = true
		result := false
		if i == len(g) {
			result = j == len(n)
		} else if g[i] == "**" {
			result = match(i+1, j) || (j < len(n) && match(i, j+1))
		} else if j < len(n) {
			ok, _ := path.Match(g[i], n[j])
			result = ok && match(i+1, j+1)
		}
		memo[at] = result
		return result
	}
	return match(0, 0)
}

func syncFiles(ctx context.Context, b *Bridge, vm string, params map[string]any) (Result, error) {
	filename := params["profile"].(string)
	p, e := readSyncProfile(filename)
	if e != nil {
		return nil, e
	}
	source := filepath.Join(filepath.Dir(filename), filepath.FromSlash(p.Source))
	if e = checkLocalPath(source, false); e != nil {
		return nil, e
	}
	st, e := os.Stat(source)
	if e != nil || !st.IsDir() {
		return nil, fileArgument("Sync source must be a directory.")
	}
	files := []string{}
	var total int64
	entries := 0
	e = filepath.WalkDir(source, func(current string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fileIO("Cannot enumerate sync source.")
		}
		if current == source {
			return nil
		}
		entries++
		if entries > 10000 {
			return fileArgument("Sync is limited to 10000 directory entries.")
		}
		rel, _ := filepath.Rel(source, current)
		rel = filepath.ToSlash(rel)
		for _, glob := range p.Exclude {
			if matchFileGlob(glob, rel) {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fileArgument("Sync sources cannot include symbolic links.")
		}
		if d.IsDir() {
			return nil
		}
		info, e := d.Info()
		if e != nil || !info.Mode().IsRegular() || info.Size() > maxTransferBytes {
			return fileArgument("Sync entries must be regular files of at most 1 GiB.")
		}
		if _, e = filePath(rel, false); e != nil {
			return e
		}
		files = append(files, rel)
		total += info.Size()
		if len(files) > 2000 || total > 4*maxTransferBytes {
			return fileArgument("Sync is limited to 2000 files and 4 GiB.")
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	uploaded, skipped := 0, 0
	for _, rel := range files {
		local := filepath.Join(source, filepath.FromSlash(rel))
		f, st, e := openSource(local)
		if e != nil {
			return nil, e
		}
		hash, e := hashFile(f)
		stable := sameSource(f, st)
		f.Close()
		if e != nil {
			return nil, e
		}
		if !stable {
			return nil, NewError("FILE_CHANGED", "Sync source changed during hashing.", nil)
		}
		remotePath := path.Join(p.Path, rel)
		remote, e := callFile(ctx, b, vm, "file.hash", map[string]any{"root": p.Root, "path": remotePath})
		if e == nil && remote["sha256"] == hash {
			skipped++
			continue
		}
		if e != nil {
			var be *Error
			if !errors.As(e, &be) || be.Code != "FILE_NOT_FOUND" {
				return nil, e
			}
		}
		_, e = uploadFile(ctx, b, vm, map[string]any{"local_path": local, "root": p.Root, "path": remotePath, "overwrite": p.Overwrite})
		if e != nil {
			return nil, e
		}
		uploaded++
	}
	return Success(map[string]any{"root": p.Root, "file_count": len(files), "uploaded": uploaded, "skipped": skipped, "deleted": 0}), nil
}

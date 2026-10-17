// Package privacy checks committed test material, not the user's computer.
package privacy

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func sensitiveFixture(value []byte) bool {
	patterns := []string{
		`/` + `Users/[^/\s"']+`,
		`/` + `home/[^/\s"']+`,
		`(?i)[a-z]:[\\/]+` + `Users[\\/]+[^\\/\s"']+`,
		`-----BEGIN ` + `(?:RSA |EC |OPENSSH )?PRIVATE KEY-----`,
		`gh` + `[pousr]_[A-Za-z0-9]{30,}`,
		`github` + `_pat_[A-Za-z0-9_]{40,}`,
		`sk` + `-[A-Za-z0-9_-]{32,}`,
		`AKIA` + `[A-Z0-9]{16}`,
	}
	for _, pattern := range patterns {
		if regexp.MustCompile(pattern).Match(value) {
			return true
		}
	}
	for _, email := range regexp.MustCompile(`[A-Za-z0-9_.+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`).FindAllString(string(value), -1) {
		domain := strings.ToLower(strings.SplitN(email, "@", 2)[1])
		if domain != "example.com" && domain != "example.test" && domain != "example.invalid" && !strings.HasSuffix(domain, ".example") {
			return true
		}
	}
	return false
}

const maxFixtureBytes = 4 * 1024 * 1024

func fixtureCandidate(name string) bool {
	return (strings.HasPrefix(name, "tests/") || strings.HasPrefix(name, "examples/") ||
		(strings.HasPrefix(name, "internal/") && strings.HasSuffix(name, "_test.go"))) && !filepath.IsAbs(name) && !strings.Contains(name, "../")
}
func fixtureGit(root string, args ...string) ([]byte, error) {
	command := exec.Command("git", args...)
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("cannot inspect repository fixture metadata")
	}
	return output, nil
}
func scanFixtureMaterial(root string) error {
	// Inspect staged blobs as well as the working tree: cleaning a credential out
	// of a worktree file must not hide the same credential still in the index.
	indexed, err := fixtureGit(root, "ls-files", "--stage", "-z", "--", "tests", "examples", "internal")
	if err != nil {
		return err
	}
	for _, entry := range bytes.Split(indexed, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		parts := bytes.SplitN(entry, []byte{'\t'}, 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid fixture index metadata")
		}
		name := string(parts[1])
		if !fixtureCandidate(name) {
			continue
		}
		metadata := strings.Fields(string(parts[0]))
		if len(metadata) != 3 || metadata[2] != "0" || (metadata[0] != "100644" && metadata[0] != "100755") {
			return fmt.Errorf("fixture index entry must be a regular, merged file: %s", name)
		}
		sizeOutput, err := fixtureGit(root, "cat-file", "-s", metadata[1])
		if err != nil {
			return err
		}
		size, err := strconv.ParseInt(strings.TrimSpace(string(sizeOutput)), 10, 64)
		if err != nil || size < 0 || size > maxFixtureBytes {
			return fmt.Errorf("indexed fixture exceeds its byte budget: %s", name)
		}
		data, err := fixtureGit(root, "cat-file", "blob", metadata[1])
		if err != nil {
			return err
		}
		if int64(len(data)) != size {
			return fmt.Errorf("fixture index changed during inspection")
		}
		if sensitiveFixture(data) {
			return fmt.Errorf("indexed test material contains a personal path or credential pattern: %s (value withheld)", name)
		}
	}
	names, err := fixtureGit(root, "ls-files", "--cached", "--others", "--exclude-standard", "-z", "--", "tests", "examples", "internal")
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, raw := range bytes.Split(names, []byte{0}) {
		name := string(raw)
		if fixtureCandidate(name) {
			seen[name] = true
		}
	}
	ordered := make([]string, 0, len(seen))
	for name := range seen {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	for _, name := range ordered {
		filename := filepath.Join(root, filepath.FromSlash(name))
		// Reject linked parent directories before opening the final file.
		current := filename
		for current != root {
			info, err := os.Lstat(current)
			if os.IsNotExist(err) {
				break
			}
			if err != nil || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("fixture path must not contain symbolic links: %s", name)
			}
			current = filepath.Dir(current)
		}
		info, err := os.Lstat(filename)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("cannot inspect fixture %s", name)
		}
		if !info.Mode().IsRegular() || info.Size() > maxFixtureBytes {
			return fmt.Errorf("fixture must be a bounded regular file: %s", name)
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			return fmt.Errorf("cannot read fixture %s", name)
		}
		if sensitiveFixture(data) {
			return fmt.Errorf("working test material contains a personal path or credential pattern: %s (value withheld)", name)
		}
	}
	return nil
}
func TestRepositoryTestMaterialUsesSyntheticData(t *testing.T) {
	_, current, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(current), "..", ".."))
	if err := scanFixtureMaterial(root); err != nil {
		t.Fatal(err)
	}
}
func TestPrivacyScanIncludesItsOwnSource(t *testing.T) {
	_, current, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(current)
	if err != nil {
		t.Fatal("cannot read privacy test source")
	}
	if sensitiveFixture(data) {
		t.Fatal("privacy test source contains a sensitive pattern (value withheld)")
	}
}
func TestPrivacyScanChecksIndexAndUntrackedWorktree(t *testing.T) {
	root := t.TempDir()
	if _, err := fixtureGit(root, "init", "--quiet"); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "tests")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal("cannot create synthetic test repository")
	}
	filename := filepath.Join(directory, "synthetic.txt")
	sensitive := []byte("/" + "Users/" + "synthetic-account/fixture")
	write := func(data []byte) {
		t.Helper()
		if err := os.WriteFile(filename, data, 0600); err != nil {
			t.Fatal("cannot write synthetic fixture")
		}
	}
	write(sensitive)
	if err := scanFixtureMaterial(root); err == nil || !strings.Contains(err.Error(), "value withheld") {
		t.Fatal("untracked synthetic sensitive class was missed")
	}
	if _, err := fixtureGit(root, "add", "--", "tests/synthetic.txt"); err != nil {
		t.Fatal(err)
	}
	write([]byte("synthetic fixture only"))
	if err := scanFixtureMaterial(root); err == nil || !strings.HasPrefix(err.Error(), "indexed") {
		t.Fatal("staged synthetic sensitive class was missed")
	}
	if _, err := fixtureGit(root, "add", "--", "tests/synthetic.txt"); err != nil {
		t.Fatal(err)
	}
	if err := scanFixtureMaterial(root); err != nil {
		t.Fatal("clean synthetic repository was rejected")
	}
}

func TestSensitiveFixturePatternsWithoutEmbeddingSecrets(t *testing.T) {
	for _, text := range []string{
		"/" + "Users/" + "synthetic-account/fixture",
		"C:" + `\` + "Users" + `\` + "synthetic-account" + `\` + "fixture",
		"-----BEGIN " + "PRIVATE KEY-----",
		"gh" + "p_" + strings.Repeat("X", 36),
		"synthetic" + "@" + "private" + ".invalid",
	} {
		if !sensitiveFixture([]byte(text)) {
			t.Fatal("sensitive fixture class was missed")
		}
	}
	for _, text := range []string{"fixture@example.test", `C:\ProgramData\Starbridge`, "11111111-1111-4111-8111-111111111111"} {
		if sensitiveFixture([]byte(text)) {
			t.Fatal("synthetic fixture was rejected")
		}
	}
}

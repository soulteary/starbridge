package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The test binary doubles as an isolated prlctl fixture. It never invokes a VM,
// a shell, or the actual Parallels installation.
func init() {
	if os.Getenv("STARBRIDGE_CLI_TEST_HELPER") != "1" {
		return
	}
	if marker := os.Getenv("STARBRIDGE_CLI_TEST_MARKER"); marker != "" {
		_ = os.WriteFile(marker, []byte("invoked"), 0600)
	}
	if os.Getenv("STARBRIDGE_CLI_TEST_SLEEP") == "1" {
		time.Sleep(5 * time.Second)
	}
	_, _ = fmt.Fprint(os.Stdout, os.Getenv("STARBRIDGE_CLI_TEST_STDOUT"))
	_, _ = fmt.Fprint(os.Stderr, os.Getenv("STARBRIDGE_CLI_TEST_STDERR"))
	code, _ := strconv.Atoi(os.Getenv("STARBRIDGE_CLI_TEST_EXIT"))
	os.Exit(code)
}

func invoke(t *testing.T, args ...string) (int, map[string]any) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	status := Run(context.Background(), args, &stdout, &stderr)
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
	if strings.Count(stdout.String(), "\n") != 1 {
		t.Fatalf("want one JSON line, got %q", stdout.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON %q: %v", stdout.String(), err)
	}
	return status, result
}

func assertCode(t *testing.T, result map[string]any, code string) {
	t.Helper()
	if result["ok"] != false {
		t.Fatalf("want error, got %#v", result)
	}
	err, ok := result["error"].(map[string]any)
	if !ok || err["code"] != code {
		t.Fatalf("want %s, got %#v", code, result)
	}
}

func helper(t *testing.T, output string, status int) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("STARBRIDGE_CLI_TEST_HELPER", "1")
	t.Setenv("STARBRIDGE_CLI_TEST_STDOUT", output)
	t.Setenv("STARBRIDGE_CLI_TEST_EXIT", strconv.Itoa(status))
	return executable
}

func resultData(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	if result["ok"] != true {
		t.Fatalf("want success, got %#v", result)
	}
	data, ok := result["data"].(map[string]any)
	if !ok {
		t.Fatalf("want object data, got %#v", result)
	}
	return data
}

func argumentVector(t *testing.T, data map[string]any) []string {
	t.Helper()
	values, ok := data["argv"].([]any)
	if !ok {
		t.Fatalf("missing argument vector: %#v", data)
	}
	argv := make([]string, len(values))
	for i, value := range values {
		var valid bool
		argv[i], valid = value.(string)
		if !valid {
			t.Fatalf("non-string argument: %#v", value)
		}
	}
	return argv
}

func TestSuccessReturnsOneJSONEnvelope(t *testing.T) {
	exe := helper(t, `[{"name":"Windows 中文"}]`, 0)
	status, result := invoke(t, "--prlctl", exe, "vm", "list")
	if status != 0 || result["ok"] != true {
		t.Fatalf("status %d, result %#v", status, result)
	}
	data := result["data"].([]any)
	if data[0].(map[string]any)["name"] != "Windows 中文" {
		t.Fatal(result)
	}
}

func TestParserFailuresAreJSON(t *testing.T) {
	cases := [][]string{
		{}, {"unknown"}, {"vm"}, {"guest"}, {"vm", "unknown"}, {"guest", "unknown", "vm"},
		{"guest", "ui", "vm"}, {"vm", "clone", "vm"},
		{"guest", "windows", "vm", "--pid", "not-int"},
		{"doctor", "surplus"}, {"vm", "list", "surplus"},
		{"vm", "stop", "vm", "other"}, {"vm", "start", "vm", "--force"},
		{"vm", "info", "--timeout", "3", "vm"},
		{"vm", "start", "vm", "--bogus"}, {"--unknown", "vm", "list"},
		{"--prlctl"}, {"--guest-script", "--dry-run", "vm", "list"},
		{"--timeout", "bad", "vm", "list"}, {"--timeout", "NaN", "vm", "list"},
		{"--timeout", "Inf", "vm", "list"}, {"--timeout", "-Inf", "vm", "list"},
		{"--timeout", "0", "vm", "list"}, {"--timeout", "-1", "vm", "list"},
		{"--timeout", "601", "vm", "list"}, {"--timeout", "1e999", "vm", "list"},
		{"--dry-run=false", "vm", "list"},
		{"vm", "stop", "vm", "--force=false"},
		{"vm", "clone", "vm", "--name"},
		{"vm", "clone", "vm", "--name", "--confirm"},
		{"vm", "snapshot-restore", "vm"},
		{"vm", "recycle", "vm", "--mode", "reboot"},
		{"guest", "ui", "vm", "--hwnd", "1", "--max-depth", "1.5"},
		{"guest", "processes", "vm", "--limit", "99999999999999999999999"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			status, result := invoke(t, args...)
			if status != 1 {
				t.Fatalf("status = %d", status)
			}
			assertCode(t, result, "INVALID_ARGUMENT")
		})
	}
}

func TestNumericBoundariesValidateBeforeExecution(t *testing.T) {
	cases := [][]string{
		{"guest", "processes", "vm", "--limit", "1001"},
		{"guest", "processes", "vm", "--limit", "0"},
		{"guest", "processes", "vm", "--limit", "-2"},
		{"guest", "windows", "vm", "--pid", "2147483648"},
		{"guest", "windows", "vm", "--pid", "-1"},
		{"guest", "ui", "vm", "--hwnd", "0"},
		{"guest", "ui", "vm", "--hwnd", "1", "--max-depth", "13"},
		{"guest", "ui", "vm", "--hwnd", "1", "--timeout-ms", "20001"},
		{"guest", "ui", "vm", "--hwnd", "1", "--max-nodes", "0"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			args = append([]string{"--prlctl", "/nonexistent/starbridge-cli-test"}, args...)
			status, result := invoke(t, args...)
			if status != 1 {
				t.Fatalf("status = %d", status)
			}
			assertCode(t, result, "INVALID_ARGUMENT")
		})
	}
}

func TestGuestDryRunPreservesUnicodeAndDoesNotExecute(t *testing.T) {
	exe := helper(t, "", 0)
	marker := filepath.Join(t.TempDir(), "invoked")
	t.Setenv("STARBRIDGE_CLI_TEST_MARKER", marker)
	status, result := invoke(t, "--prlctl", exe, "--dry-run", "--guest-script", `C:\Program Files\中文\bridge.ps1`,
		"guest", "processes", "Windows 空格", "--name", "程序 中文", "--include-path")
	if status != 0 {
		t.Fatal(result)
	}
	data := resultData(t, result)
	if data["dry_run"] != true {
		t.Fatal(data)
	}
	argv := argumentVector(t, data)
	decoded, err := base64.StdEncoding.DecodeString(argv[len(argv)-1])
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if err := json.Unmarshal(decoded, &request); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"limit": float64(500), "name": "程序 中文", "include_path": true}
	if !reflect.DeepEqual(request["params"], want) {
		t.Fatalf("params = %#v", request["params"])
	}
	fileFound := false
	for i, value := range argv {
		if value == "-File" && i+1 < len(argv) && argv[i+1] == `C:\Program Files\中文\bridge.ps1` {
			fileFound = true
		}
	}
	if !fileFound {
		t.Fatalf("guest path not preserved: %#v", argv)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("dry run executed fixture: %v", err)
	}
}

func TestDeleteRequiresConfirmationBeforeTransport(t *testing.T) {
	exe := helper(t, `{}`, 0)
	marker := filepath.Join(t.TempDir(), "invoked")
	t.Setenv("STARBRIDGE_CLI_TEST_MARKER", marker)
	status, result := invoke(t, "--prlctl", exe, "vm", "delete", "vm", "--confirm", "other-vm")
	if status != 1 {
		t.Fatal(status)
	}
	assertCode(t, result, "CONFIRMATION_REQUIRED")
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("invalid confirmation reached transport: %v", err)
	}
}

func TestVMDryRunDispatchAndOptionPositions(t *testing.T) {
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"list"}, []string{"list", "-a", "-j"}},
		{[]string{"info", "vm"}, []string{"list", "-i", "-j", "vm"}},
		{[]string{"start", "vm"}, []string{"start", "vm"}},
		{[]string{"stop", "vm"}, []string{"stop", "vm"}},
		{[]string{"stop", "vm", "--force"}, []string{"stop", "vm", "--kill"}},
		{[]string{"stop", "--force", "vm"}, []string{"stop", "vm", "--kill"}},
		{[]string{"suspend", "vm"}, []string{"suspend", "vm"}},
		{[]string{"resume", "vm"}, []string{"resume", "vm"}},
		{[]string{"delete", "vm"}, []string{"delete", "vm"}},
		{[]string{"delete", "--confirm=vm", "vm"}, []string{"delete", "vm"}},
		{[]string{"clone", "vm", "--name", "克隆 空格"}, []string{"clone", "vm", "--name", "克隆 空格"}},
		{[]string{"clone", "--name=copy", "vm"}, []string{"clone", "vm", "--name", "copy"}},
		{[]string{"snapshot-list", "vm"}, []string{"snapshot-list", "vm", "-j"}},
		{[]string{"snapshot-create", "--name", "clean", "vm"}, []string{"snapshot", "vm", "--name", "clean"}},
		{[]string{"snapshot-restore", "vm", "--snapshot", "clean"}, []string{"snapshot-switch", "vm", "--id", "clean", "--skip-resume"}},
	}
	for _, tt := range cases {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			args := append([]string{"--dry-run", "--prlctl", "/not/installed/prlctl", "vm"}, tt.args...)
			status, result := invoke(t, args...)
			if status != 0 {
				t.Fatal(result)
			}
			argv := argumentVector(t, resultData(t, result))
			if !reflect.DeepEqual(argv[1:], tt.want) {
				t.Fatalf("argv = %#v, want %#v", argv[1:], tt.want)
			}
		})
	}
}

func TestRecycleDefaultAndRestorePreview(t *testing.T) {
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"vm"}, []string{"stop", "vm"}},
		{[]string{"--mode", "restore", "vm", "--snapshot", "clean"}, []string{"snapshot-switch", "vm", "--id", "clean", "--skip-resume"}},
		{[]string{"vm", "--mode=delete"}, []string{"delete", "vm"}},
	}
	for _, tt := range cases {
		status, result := invoke(t, append([]string{"--dry-run", "vm", "recycle"}, tt.args...)...)
		if status != 0 {
			t.Fatal(result)
		}
		nested := resultData(t, result)["result"].(map[string]any)
		argv := argumentVector(t, nested)
		if !reflect.DeepEqual(argv[1:], tt.want) {
			t.Fatalf("argv = %#v", argv)
		}
	}
}

func TestRecycleInvalidSnapshotNeverReachesTransport(t *testing.T) {
	exe := helper(t, `{}`, 0)
	marker := filepath.Join(t.TempDir(), "invoked")
	t.Setenv("STARBRIDGE_CLI_TEST_MARKER", marker)
	policy := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(policy, []byte(`{"default":{"mode":"restore","snapshot":"old"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"vm", "--snapshot", ""},
		{"vm", "--snapshot="},
		{"vm", "--snapshot", "", "--mode", "stop"},
		{"vm", "--snapshot", "", "--policy", policy, "--confirm", "vm"},
		{"vm", "--snapshot", " ", "--policy", policy, "--confirm", "vm"},
		{"vm", "--snapshot=-bad", "--policy", policy, "--confirm", "vm"},
		{"vm", "--snapshot", "bad\nvalue", "--policy", policy, "--confirm", "vm"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			status, result := invoke(t, append([]string{"--prlctl", exe, "vm", "recycle"}, args...)...)
			if status != 1 {
				t.Fatalf("status=%d result=%#v", status, result)
			}
			assertCode(t, result, "INVALID_ARGUMENT")
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("invalid snapshot reached transport: %v", err)
			}
		})
	}
}

func TestRecycleBlankPolicyNeverReachesTransport(t *testing.T) {
	exe := helper(t, `{}`, 0)
	for _, dryRun := range []bool{false, true} {
		for _, tt := range []struct {
			name string
			args []string
		}{
			{"empty-value", []string{"vm", "--policy", ""}},
			{"empty-equals", []string{"--policy=", "vm"}},
			{"spaces", []string{"vm", "--policy", "   "}},
			{"unicode-whitespace", []string{"--policy=\t\r\n\u2003", "vm"}},
			{"with-mode-override", []string{"DisplayName", "--policy", "", "--mode", "delete", "--confirm", "DisplayName"}},
			{"equals-with-mode-override", []string{"DisplayName", "--policy=", "--mode", "delete", "--confirm", "DisplayName"}},
		} {
			t.Run(fmt.Sprintf("%s/dry-run=%t", tt.name, dryRun), func(t *testing.T) {
				marker := filepath.Join(t.TempDir(), "invoked")
				t.Setenv("STARBRIDGE_CLI_TEST_MARKER", marker)
				args := []string{"--prlctl", exe}
				if dryRun {
					args = append(args, "--dry-run")
				}
				args = append(args, "vm", "recycle")
				status, result := invoke(t, append(args, tt.args...)...)
				if status != 1 {
					t.Fatalf("status=%d result=%#v", status, result)
				}
				assertCode(t, result, "INVALID_ARGUMENT")
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Fatalf("blank policy reached transport: %v", err)
				}
			})
		}
	}
}

func TestRecycleOmittedPolicyAndWhitespaceInPath(t *testing.T) {
	exe := helper(t, "completed", 0)
	const policyVM = "11111111-1111-4111-8111-111111111111"
	policy := filepath.Join(t.TempDir(), " policy with spaces.json ")
	if err := os.WriteFile(policy, []byte(`{"default":{"mode":"restore","snapshot":"clean"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, dryRun := range []bool{false, true} {
		for _, tt := range []struct {
			name, vm, policy, source, mode string
			want                           []string
		}{
			{"omitted-policy", "vm", "", "built-in default", "stop", []string{"stop", "vm"}},
			{"path-with-spaces", policyVM, policy, "policy default", "restore", []string{"snapshot-switch", policyVM, "--id", "clean", "--skip-resume"}},
		} {
			t.Run(fmt.Sprintf("%s/dry-run=%t", tt.name, dryRun), func(t *testing.T) {
				marker := filepath.Join(t.TempDir(), "invoked")
				t.Setenv("STARBRIDGE_CLI_TEST_MARKER", marker)
				args := []string{"--prlctl", exe}
				if dryRun {
					args = append(args, "--dry-run")
				}
				args = append(args, "vm", "recycle", tt.vm, "--confirm", tt.vm)
				if tt.policy != "" {
					args = append(args, "--policy", tt.policy)
				}
				status, result := invoke(t, args...)
				if status != 0 {
					t.Fatalf("status=%d result=%#v", status, result)
				}
				data := resultData(t, result)
				if data["policy_source"] != tt.source || data["policy"].(map[string]any)["mode"] != tt.mode {
					t.Fatalf("unexpected policy: %#v", data)
				}
				nested := data["result"].(map[string]any)
				if dryRun {
					if argv := argumentVector(t, nested); !reflect.DeepEqual(argv[1:], tt.want) {
						t.Fatalf("argv=%#v want=%#v", argv[1:], tt.want)
					}
					if _, err := os.Stat(marker); !os.IsNotExist(err) {
						t.Fatalf("dry run reached transport: %v", err)
					}
				} else {
					if nested["command_completed"] != true {
						t.Fatalf("command not completed: %#v", nested)
					}
					if _, err := os.Stat(marker); err != nil {
						t.Fatalf("expected fixture invocation: %v", err)
					}
				}
			})
		}
	}
}

func TestGuestDispatchOptionsBeforeAndAfterVM(t *testing.T) {
	cases := []struct {
		args   []string
		action string
		params map[string]any
	}{
		{[]string{"windows", "--pid", "3", "vm", "--limit=4", "--include-hidden"}, "windows.list",
			map[string]any{"pid": float64(3), "limit": float64(4), "include_hidden": true}},
		{[]string{"processes", "--name", "hi", "--include-path", "vm", "--pid=10", "--limit", "2"}, "processes.list",
			map[string]any{"name": "hi", "include_path": true, "pid": float64(10), "limit": float64(2)}},
		{[]string{"ui", "--hwnd", "0x123", "vm", "--max-depth=3", "--max-nodes", "10", "--timeout-ms", "2000"}, "ui.tree",
			map[string]any{"hwnd": "0x123", "max_depth": float64(3), "max_nodes": float64(10), "timeout_ms": float64(2000)}},
	}
	for _, tt := range cases {
		t.Run(tt.action, func(t *testing.T) {
			status, result := invoke(t, append([]string{"--dry-run", "guest"}, tt.args...)...)
			if status != 0 {
				t.Fatal(result)
			}
			argv := argumentVector(t, resultData(t, result))
			payload, err := base64.StdEncoding.DecodeString(argv[len(argv)-1])
			if err != nil {
				t.Fatal(err)
			}
			var request map[string]any
			if err := json.Unmarshal(payload, &request); err != nil {
				t.Fatal(err)
			}
			if request["action"] != tt.action || !reflect.DeepEqual(request["params"], tt.params) {
				t.Fatalf("request = %#v", request)
			}
		})
	}
}

func TestGuestErrorEnvelopeIsNotDoubleWrapped(t *testing.T) {
	exe := helper(t, `{"ok":false,"error":{"code":"NOT_INTERACTIVE","message":"No user desktop"}}`, 1)
	status, result := invoke(t, "--prlctl", exe, "guest", "windows", "vm")
	if status != 1 {
		t.Fatal(status)
	}
	assertCode(t, result, "NOT_INTERACTIVE")
	if len(result) != 2 {
		t.Fatal(result)
	}
}

func TestTimeoutReportsUnknownOutcome(t *testing.T) {
	exe := helper(t, "", 0)
	t.Setenv("STARBRIDGE_CLI_TEST_SLEEP", "1")
	status, result := invoke(t, "--prlctl", exe, "--timeout", "1", "vm", "start", "vm")
	if status != 1 {
		t.Fatal(status)
	}
	assertCode(t, result, "TIMEOUT")
	details := result["error"].(map[string]any)["details"].(map[string]any)
	if details["outcome"] != "unknown" {
		t.Fatal(details)
	}
}

func TestHelpAndVersionArePlainText(t *testing.T) {
	for _, args := range [][]string{
		{"--help"}, {"-h"}, {"--version"}, {"doctor", "--help"}, {"vm", "--help"},
		{"vm", "clone", "--help"}, {"guest", "--help"}, {"guest", "ui", "--help"}, {"serve", "--help"},
	} {
		var stdout, stderr bytes.Buffer
		status := Run(context.Background(), args, &stdout, &stderr)
		if status != 0 || stdout.Len() == 0 || stderr.Len() != 0 || json.Valid(stdout.Bytes()) {
			t.Fatalf("args=%v status=%d stdout=%q stderr=%q", args, status, stdout.String(), stderr.String())
		}
	}
}

func TestServeFailuresKeepStdoutClean(t *testing.T) {
	for _, args := range [][]string{
		{"--dry-run", "serve"}, {"serve", "--bogus"}, {"serve", "surplus"},
		{"--timeout", "nan", "serve"}, {"--unknown", "serve"},
	} {
		var stdout, stderr bytes.Buffer
		status := Run(context.Background(), args, &stdout, &stderr)
		if status != 1 || stdout.Len() != 0 {
			t.Fatalf("args=%v status=%d stdout=%q stderr=%q", args, status, stdout.String(), stderr.String())
		}
		var result map[string]any
		if err := json.Unmarshal(stderr.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		assertCode(t, result, "INVALID_ARGUMENT")
	}
}

func TestFractionalTimeoutAndEqualsOptions(t *testing.T) {
	c, err := parse([]string{"--timeout=1.5", "--prlctl=/test/cli", "--guest-script=C:\\helper.ps1", "vm", "clone", "--name=copy", "vm"})
	if err != nil {
		t.Fatal(err)
	}
	if c.opts.Timeout != 1500*time.Millisecond || c.opts.PRLCTL != "/test/cli" || c.opts.GuestScript != `C:\helper.ps1` || c.values["name"] != "copy" || c.vm != "vm" {
		t.Fatalf("unexpected parse: %#v", c)
	}
}

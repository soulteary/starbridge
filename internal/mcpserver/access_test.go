package mcpserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/soulteary/starbridge/internal/bridge"
)

const (
	accessUUIDA = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	accessUUIDB = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	accessUUIDC = "cccccccc-cccc-cccc-cccc-cccccccccccc"
)

type accessCall struct{ vm, action string }

type accessFixture struct {
	rows      any
	listError error
	calls     []accessCall
	listed    int
}

func (b *accessFixture) VMList(context.Context) (any, error) {
	b.listed++
	return b.rows, b.listError
}
func (b *accessFixture) VMInfo(_ context.Context, vm string) (any, error) {
	b.calls = append(b.calls, accessCall{vm, "info"})
	return map[string]any{"uuid": vm}, nil
}
func (b *accessFixture) VMAction(_ context.Context, action, vm string, force bool, confirmation string) (any, error) {
	if force || confirmation != "" {
		panic("unbounded mutation reached fixture")
	}
	b.calls = append(b.calls, accessCall{vm, action})
	return map[string]any{"uuid": vm, "action": action}, nil
}
func (b *accessFixture) Guest(_ context.Context, vm, action string, _ map[string]any) (bridge.Result, error) {
	b.calls = append(b.calls, accessCall{vm, action})
	return bridge.Success([]any{}), nil
}

func accessPolicy(t *testing.T, config string) *AccessPolicy {
	t.Helper()
	policy, err := parseAccessPolicy([]byte(config))
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func assertAccessCode(t *testing.T, err error, code string) {
	t.Helper()
	var failure *bridge.Error
	if !errors.As(err, &failure) || failure.Code != code {
		t.Fatalf("want %s, got %#v", code, err)
	}
}

func accessRows() []any {
	return []any{
		map[string]any{"uuid": "{AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA}", "ID": accessUUIDA, "name": "sandbox"},
		map[string]any{"ID": accessUUIDB, "Name": "second"},
		map[string]any{"id": accessUUIDC, "name": "private"},
	}
}

func TestAccessPolicyValidation(t *testing.T) {
	for _, config := range []string{
		`null`, `{}`, `{"vms":null}`, `{"vms":[]}`, `{"vms":{},"extra":true}`,
		`{"vms":{},"vms":{}}`, `{"vms":{}} {}`,
		`{"vms":{"sandbox":["vm_info"]}}`,
		`{"vms":{"` + accessUUIDA + `":null}}`,
		`{"vms":{"` + accessUUIDA + `":"vm_info"}}`,
		`{"vms":{"` + accessUUIDA + `":["vm_list"]}}`,
		`{"vms":{"` + accessUUIDA + `":["vm_delete"]}}`,
		`{"vms":{"` + accessUUIDA + `":["vm_info","vm_info"]}}`,
		`{"vms":{"` + accessUUIDA + `":[],"` + accessUUIDA + `":[]}}`,
		`{"vms":{"` + accessUUIDA + `":[],"{AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA}":[]}}`,
		strings.Repeat(" ", maxAccessPolicyBytes+1),
	} {
		t.Run(config[:min(len(config), 100)], func(t *testing.T) {
			_, err := parseAccessPolicy([]byte(config))
			assertAccessCode(t, err, "INVALID_ACCESS_POLICY")
		})
	}
	if _, err := parseAccessPolicy([]byte(`{"vms":{}}`)); err != nil {
		t.Fatal(err)
	}
	policy := accessPolicy(t, `{"vms":{"{AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA}":["vm_info"],"`+accessUUIDB+`":[]}}`)
	if !policy.grants[accessUUIDA]["vm_info"] || len(policy.grants[accessUUIDB]) != 0 {
		t.Fatal(policy)
	}
}

func TestLoadAccessPolicyFailsClosed(t *testing.T) {
	for _, path := range []string{"", filepath.Join(t.TempDir(), "missing.json")} {
		_, err := LoadAccessPolicy(path)
		assertAccessCode(t, err, "INVALID_ACCESS_POLICY")
	}
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, []byte(`{"vms":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAccessPolicy(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat(" ", maxAccessPolicyBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadAccessPolicy(path)
	assertAccessCode(t, err, "INVALID_ACCESS_POLICY")
}

func TestScopedVMListAndActionChecks(t *testing.T) {
	fixture := &accessFixture{rows: accessRows()}
	policy := accessPolicy(t, `{"vms":{"`+accessUUIDA+`":["vm_info","guest_windows","vm_start"],"`+accessUUIDB+`":[]}}`)
	b := &scopedBackend{backend: fixture, policy: policy, allowWrite: true}
	ctx := context.Background()
	list, err := b.VMList(ctx)
	if err != nil || len(list.([]any)) != 2 {
		t.Fatalf("filtered list: %#v %v", list, err)
	}
	if _, err := b.VMInfo(ctx, "sandbox"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Guest(ctx, "{AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA}", "windows.list", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := b.VMAction(ctx, "start", "sandbox", false, ""); err != nil {
		t.Fatal(err)
	}
	want := []accessCall{{accessUUIDA, "info"}, {accessUUIDA, "windows.list"}, {accessUUIDA, "start"}}
	if !reflect.DeepEqual(fixture.calls, want) {
		t.Fatalf("all calls must use the authorized UUID: %#v", fixture.calls)
	}
	for _, vm := range []string{"private", accessUUIDC, "second", accessUUIDB, "missing", ""} {
		_, err := b.VMInfo(ctx, vm)
		assertAccessCode(t, err, "ACCESS_DENIED")
		_, err = b.Guest(ctx, vm, "windows.list", nil)
		assertAccessCode(t, err, "ACCESS_DENIED")
		_, err = b.VMAction(ctx, "start", vm, false, "")
		assertAccessCode(t, err, "ACCESS_DENIED")
	}
	_, err = b.Guest(ctx, "sandbox", "processes.list", nil)
	assertAccessCode(t, err, "ACCESS_DENIED")
	_, err = b.VMAction(ctx, "stop", "sandbox", false, "")
	assertAccessCode(t, err, "ACCESS_DENIED")
	_, err = b.VMAction(ctx, "delete", "sandbox", false, "")
	assertAccessCode(t, err, "ACCESS_DENIED")
	_, err = b.VMAction(ctx, "start", "sandbox", true, "")
	assertAccessCode(t, err, "ACCESS_DENIED")
	b.allowWrite = false
	_, err = b.VMAction(ctx, "start", "sandbox", false, "")
	assertAccessCode(t, err, "ACCESS_DENIED")
	if !reflect.DeepEqual(fixture.calls, want) {
		t.Fatalf("a denied call reached the target backend: %#v", fixture.calls)
	}
}

func TestScopedNamesAreResolvedOnEveryCall(t *testing.T) {
	fixture := &accessFixture{rows: accessRows()}
	b := &scopedBackend{backend: fixture, policy: accessPolicy(t, `{"vms":{"`+accessUUIDA+`":["vm_start"]}}`), allowWrite: true}
	ctx := context.Background()
	if _, err := b.VMAction(ctx, "start", "sandbox", false, ""); err != nil {
		t.Fatal(err)
	}
	fixture.rows = []any{
		map[string]any{"uuid": accessUUIDA, "name": "renamed"},
		map[string]any{"uuid": accessUUIDB, "name": "sandbox"},
	}
	_, err := b.VMAction(ctx, "start", "sandbox", false, "")
	assertAccessCode(t, err, "ACCESS_DENIED")
	if fixture.listed != 2 || len(fixture.calls) != 1 || fixture.calls[0].vm != accessUUIDA {
		t.Fatalf("a name was cached or a different VM was modified: %#v", fixture)
	}
	fixture.rows = []any{
		map[string]any{"uuid": accessUUIDA, "name": "sandbox"},
		map[string]any{"uuid": accessUUIDB, "name": "sandbox"},
	}
	_, err = b.VMAction(ctx, "start", "sandbox", false, "")
	assertAccessCode(t, err, "ACCESS_DENIED")
	if len(fixture.calls) != 1 {
		t.Fatal("ambiguous name selected a VM")
	}
}

func TestScopedInventoryErrorsNeverReachMutation(t *testing.T) {
	policy := accessPolicy(t, `{"vms":{"`+accessUUIDA+`":["vm_start"]}}`)
	for _, rows := range []any{
		nil, map[string]any{}, []any{nil}, []any{map[string]any{"name": "sandbox"}},
		[]any{map[string]any{"uuid": "invalid", "name": "sandbox"}},
		[]any{map[string]any{"uuid": accessUUIDA, "id": accessUUIDB, "name": "sandbox"}},
		[]any{map[string]any{"uuid": accessUUIDA, "name": "sandbox", "NAME": "other"}},
		[]any{map[string]any{"uuid": accessUUIDA}, map[string]any{"UUID": strings.ToUpper(accessUUIDA)}},
	} {
		fixture := &accessFixture{rows: rows}
		b := &scopedBackend{backend: fixture, policy: policy, allowWrite: true}
		_, err := b.VMAction(context.Background(), "start", accessUUIDA, false, "")
		assertAccessCode(t, err, "INVALID_VM_INVENTORY")
		if len(fixture.calls) != 0 {
			t.Fatal("malformed inventory reached mutation")
		}
	}
	fixture := &accessFixture{listError: bridge.NewError("TIMEOUT", "inventory timeout", nil)}
	b := &scopedBackend{backend: fixture, policy: policy, allowWrite: true}
	_, err := b.VMAction(context.Background(), "start", accessUUIDA, false, "")
	assertAccessCode(t, err, "TIMEOUT")
}

func TestScopedInventoryErrorsDoNotExposeOtherVMs(t *testing.T) {
	fixture := &accessFixture{listError: bridge.NewError("PRLCTL_FAILED", "private VM name", map[string]any{"stdout": "private inventory", "stderr": "private VM path"})}
	b := &scopedBackend{backend: fixture, policy: accessPolicy(t, `{"vms":{}}`)}
	_, err := b.VMList(context.Background())
	assertAccessCode(t, err, "PRLCTL_FAILED")
	var failure *bridge.Error
	if !errors.As(err, &failure) || failure.Details != nil || strings.Contains(failure.Message, "private") {
		t.Fatalf("inventory error leaked scoped data: %#v", err)
	}
}

func connectAccess(t *testing.T, backend Backend, write bool, policy *AccessPolicy) (*mcp.ClientSession, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	sideA, sideB := mcp.NewInMemoryTransports()
	serverSession, err := NewWithAccess(backend, write, policy).Connect(ctx, sideA, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	clientSession, err := mcp.NewClient(&mcp.Implementation{Name: "access test"}, nil).Connect(ctx, sideB, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })
	return clientSession, ctx
}

func TestScopedMCPCatalogAndPerVMEnforcement(t *testing.T) {
	for _, write := range []bool{false, true} {
		fixture := &accessFixture{rows: accessRows()}
		policy := accessPolicy(t, `{"vms":{"`+accessUUIDA+`":["vm_info","vm_start"],"`+accessUUIDB+`":["guest_windows"]}}`)
		session, ctx := connectAccess(t, fixture, write, policy)
		catalog, err := session.ListTools(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for _, tool := range catalog.Tools {
			got[tool.Name] = true
		}
		want := map[string]bool{"vm_list": true, "vm_info": true, "guest_windows": true}
		if write {
			want["vm_start"] = true
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("catalog=%#v want=%#v", got, want)
		}
		payload := envelope(t, call(t, session, ctx, "vm_info", map[string]any{"vm": "sandbox"}))
		if payload["ok"] != true {
			t.Fatal(payload)
		}
		requireCode(t, envelope(t, call(t, session, ctx, "vm_info", map[string]any{"vm": accessUUIDB})), "ACCESS_DENIED")
		requireCode(t, envelope(t, call(t, session, ctx, "guest_windows", map[string]any{"vm": accessUUIDA})), "ACCESS_DENIED")
		if payload := envelope(t, call(t, session, ctx, "guest_windows", map[string]any{"vm": "second"})); payload["ok"] != true {
			t.Fatal(payload)
		}
		if last := fixture.calls[len(fixture.calls)-1]; last.vm != accessUUIDB || last.action != "windows.list" {
			t.Fatalf("guest query selected another VM: %#v", last)
		}
		if write {
			requireCode(t, envelope(t, call(t, session, ctx, "vm_start", map[string]any{"vm": accessUUIDC})), "ACCESS_DENIED")
			if payload := envelope(t, call(t, session, ctx, "vm_start", map[string]any{"vm": "sandbox"})); payload["ok"] != true {
				t.Fatal(payload)
			}
		}
	}
}

func TestEmptyPolicyAndNilPolicy(t *testing.T) {
	session, ctx := connectAccess(t, &accessFixture{rows: accessRows()}, true, accessPolicy(t, `{"vms":{}}`))
	catalog, err := session.ListTools(ctx, nil)
	if err != nil || len(catalog.Tools) != 1 || catalog.Tools[0].Name != "vm_list" {
		t.Fatalf("empty scope: %#v %v", catalog, err)
	}
	payload := envelope(t, call(t, session, ctx, "vm_list", map[string]any{}))
	if payload["ok"] != true || len(payload["data"].([]any)) != 0 {
		t.Fatal(payload)
	}
	session, ctx = connectAccess(t, &stubBackend{}, true, nil)
	catalog, err = session.ListTools(ctx, nil)
	if err != nil || len(catalog.Tools) != 9 {
		t.Fatalf("nil policy changed compatibility: %#v %v", catalog, err)
	}
}

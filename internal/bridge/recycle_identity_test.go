package bridge

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

const recycleProtectedUUID = "aabbccdd-1111-2222-3333-444444444444"

func TestRecyclePolicyRejectsNamesBeforeExecution(t *testing.T) {
	path := policyFile(t, `{"default":{"mode":"delete"},"vms":{"`+recycleProtectedUUID+`":{"mode":"stop"}}}`)
	for _, dryRun := range []bool{false, true} {
		for _, mode := range []string{"", "delete", "stop"} {
			t.Run(strings.Join([]string{map[bool]string{false: "execute", true: "preview"}[dryRun], mode}, "/"), func(t *testing.T) {
				f := newFixture(t)
				f.bridge.options.DryRun = dryRun
				_, err := f.bridge.Recycle(context.Background(), "Protected Windows", RecycleOptions{
					PolicyFile: path, Mode: mode, Confirmation: "Protected Windows",
				})
				checkCode(t, err, "INVALID_ARGUMENT")
				if f.calls != 0 {
					t.Fatal("a VM name reached the transport with a UUID policy")
				}
			})
		}
	}
}

func TestRecyclePolicyMatchesCanonicalUUID(t *testing.T) {
	for _, key := range []string{recycleProtectedUUID, strings.ToUpper(recycleProtectedUUID), "{" + strings.ToUpper(recycleProtectedUUID) + "}"} {
		for _, vm := range []string{recycleProtectedUUID, strings.ToUpper(recycleProtectedUUID), "{" + recycleProtectedUUID + "}"} {
			t.Run(key+"/"+vm, func(t *testing.T) {
				f := newFixture(t)
				path := policyFile(t, `{"default":{"mode":"delete"},"vms":{"`+key+`":{"mode":"stop"}}}`)
				value, err := f.bridge.Recycle(context.Background(), vm, RecycleOptions{PolicyFile: path})
				if err != nil {
					t.Fatal(err)
				}
				result := mapValue(t, value)
				if result["policy_source"] != "per-VM policy" || mapValue(t, result["policy"])["mode"] != "stop" {
					t.Fatal("protected VM fell through to the delete default", result)
				}
				if !reflect.DeepEqual(f.argv[1:], []string{"stop", vm}) || f.calls != 1 {
					t.Fatal("original UUID must be preserved for execution", f.argv)
				}
			})
		}
	}
}

func TestRecyclePolicyRejectsInvalidAndDuplicateUUIDKeys(t *testing.T) {
	for _, entries := range []string{
		`"Protected Windows":{"mode":"stop"}`,
		`"{` + recycleProtectedUUID + `":{"mode":"stop"}`,
		`"` + recycleProtectedUUID + `}":{"mode":"stop"}`,
		`" ` + recycleProtectedUUID + `":{"mode":"stop"}`,
		`"` + recycleProtectedUUID + `":{"mode":"stop"},"` + strings.ToUpper(recycleProtectedUUID) + `":{"mode":"stop"}`,
		`"` + recycleProtectedUUID + `":{"mode":"stop"},"{` + recycleProtectedUUID + `}":{"mode":"delete"}`,
		`"` + recycleProtectedUUID + `":{"mode":"stop"},"` + recycleProtectedUUID + `":{"mode":"delete"}`,
	} {
		t.Run(entries, func(t *testing.T) {
			f := newFixture(t)
			path := policyFile(t, `{"default":{"mode":"delete"},"vms":{`+entries+`}}`)
			_, err := f.bridge.Recycle(context.Background(), "00000000-1111-2222-3333-444444444444", RecycleOptions{
				PolicyFile: path, Mode: "stop",
			})
			checkCode(t, err, "INVALID_POLICY")
			if f.calls != 0 {
				t.Fatal("invalid unused override reached transport")
			}
		})
	}
}

func TestRecyclePolicyRejectsDuplicateJSONMembers(t *testing.T) {
	for _, config := range []string{
		`{"default":{"mode":"stop"},"default":{"mode":"delete"}}`,
		`{"default":{"mode":"stop","mode":"delete"}}`,
		`{"vms":{},"vms":{}}`,
		`{"default":{"mode":"restore","snapshot":"clean","snapshot":"other"}}`,
		`{"default":{"mode":"stop","mo\u0064e":"delete"}}`,
		`{"default":` + strings.Repeat("[", 40) + `0` + strings.Repeat("]", 40) + `}`,
	} {
		t.Run(config, func(t *testing.T) {
			f := newFixture(t)
			_, err := f.bridge.Recycle(context.Background(), recycleProtectedUUID, RecycleOptions{
				PolicyFile: policyFile(t, config), Confirmation: recycleProtectedUUID,
			})
			checkCode(t, err, "INVALID_POLICY")
			if f.calls != 0 {
				t.Fatal("duplicate JSON member reached transport")
			}
		})
	}
}

func TestRecyclePolicyPreservesExactConfirmation(t *testing.T) {
	path := policyFile(t, `{"default":{"mode":"stop"},"vms":{"`+recycleProtectedUUID+`":{"mode":"delete"}}}`)
	vm := "{" + strings.ToUpper(recycleProtectedUUID) + "}"
	f := newFixture(t)
	_, err := f.bridge.Recycle(context.Background(), vm, RecycleOptions{PolicyFile: path, Confirmation: recycleProtectedUUID})
	checkCode(t, err, "CONFIRMATION_REQUIRED")
	if f.calls != 0 {
		t.Fatal("confirmation mismatch reached transport")
	}
	_, err = f.bridge.Recycle(context.Background(), vm, RecycleOptions{PolicyFile: path, Confirmation: vm})
	if err != nil || !reflect.DeepEqual(f.argv[1:], []string{"delete", vm}) {
		t.Fatal(err, f.argv)
	}
}

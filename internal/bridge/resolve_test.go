package bridge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePRLCTLConfiguredPriority(t *testing.T) {
	dir := t.TempDir()
	modern := filepath.Join(dir, "modern CLI")
	legacy := filepath.Join(dir, "legacy CLI")
	explicit := filepath.Join(dir, "explicit CLI")
	for _, path := range []string{modern, legacy, explicit} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("STARBRIDGE_PRLCTL", modern)
	t.Setenv("PRLBRIDGE_PRLCTL", legacy)
	for _, tc := range []struct{ name, explicit, modern, legacy, want string }{
		{"explicit", explicit, modern, legacy, explicit},
		{"modern-env", "", modern, legacy, modern},
		{"legacy-env", "", "", legacy, legacy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("STARBRIDGE_PRLCTL", tc.modern)
			t.Setenv("PRLBRIDGE_PRLCTL", tc.legacy)
			got, err := resolvePRLCTL(tc.explicit)
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	notExecutable := filepath.Join(dir, "not-executable")
	if err := os.WriteFile(notExecutable, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{notExecutable, filepath.Join(dir, "missing")} {
		_, err := resolvePRLCTL(path)
		checkCode(t, err, "PRLCTL_NOT_FOUND") // A bad override must not fall through.
	}
	t.Setenv("STARBRIDGE_PRLCTL", filepath.Join(dir, "missing"))
	_, err := resolvePRLCTL("")
	checkCode(t, err, "PRLCTL_NOT_FOUND")
}

func TestResolvePRLCTLDefaultLookup(t *testing.T) {
	t.Setenv("STARBRIDGE_PRLCTL", "")
	t.Setenv("PRLBRIDGE_PRLCTL", "")
	dir := t.TempDir()
	path := filepath.Join(dir, "prlctl")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	got, err := resolvePRLCTL("")
	if info, statErr := os.Stat(AppPRLCTL); statErr == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0 {
		if err != nil || got != AppPRLCTL {
			t.Fatalf("installed app must precede PATH: %q, %v", got, err)
		}
		return
	}
	if err != nil || got != path {
		t.Fatalf("PATH fallback: %q, %v", got, err)
	}
	t.Setenv("PATH", t.TempDir())
	_, err = resolvePRLCTL("")
	checkCode(t, err, "PRLCTL_NOT_FOUND")
}

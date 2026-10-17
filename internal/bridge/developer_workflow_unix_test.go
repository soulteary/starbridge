//go:build darwin || linux

package bridge

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestDevelopmentProfileRejectsFIFOWithoutOpeningIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic-profile.json")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal("cannot create synthetic FIFO")
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := LoadDevelopmentProfile(path)
		done <- err
	}()
	select {
	case err := <-done:
		checkCode(t, err, "INVALID_PROFILE")
	case <-time.After(time.Second):
		// Release a regressed reader without sending data or leaving a process.
		writer, _ := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if writer != nil {
			writer.Close()
		}
		t.Fatal("profile input blocked on a synthetic FIFO")
	}
}

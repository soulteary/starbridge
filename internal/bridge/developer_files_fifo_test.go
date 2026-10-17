//go:build darwin || linux

package bridge

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestFileSourcesRejectFIFOWithoutWaitingForWriter(t *testing.T) {
	for _, kind := range []string{"upload source", "sync profile"} {
		t.Run(kind, func(t *testing.T) {
			fifo := filepath.Join(fileTestDir(t), "synthetic-fifo")
			if err := syscall.Mkfifo(fifo, 0600); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				if kind == "sync profile" {
					_, err := readSyncProfile(fifo)
					done <- err
					return
				}
				file, _, err := openSource(fifo)
				if file != nil {
					file.Close()
				}
				done <- err
			}()
			select {
			case err := <-done:
				checkCode(t, err, "INVALID_ARGUMENT")
			case <-time.After(time.Second):
				// Release an erroneously blocked open before failing the test, so
				// the regression does not leave a goroutine or FIFO writer behind.
				writer, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0600)
				if err == nil {
					writer.Close()
				}
				select {
				case <-done:
				case <-time.After(time.Second):
				}
				t.Fatal("non-regular file opening waited for a FIFO writer before rejecting it")
			}
		})
	}
}

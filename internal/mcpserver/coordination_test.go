package mcpserver

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/soulteary/starbridge/internal/bridge"
)

const (
	coordinateUUIDA = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	coordinateUUIDB = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
)

type blockingBackend struct {
	entered chan string
	release chan struct{}
}

func newBlockingBackend() *blockingBackend {
	return &blockingBackend{make(chan string, 32), make(chan struct{}, 32)}
}

func (b *blockingBackend) call(ctx context.Context, vm string) (any, error) {
	b.entered <- vm
	select {
	case <-b.release:
		return vm, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (b *blockingBackend) VMList(ctx context.Context) (any, error) {
	return b.call(ctx, "list")
}
func (b *blockingBackend) VMInfo(ctx context.Context, vm string) (any, error) {
	return b.call(ctx, vm)
}
func (b *blockingBackend) VMAction(ctx context.Context, _, vm string, _ bool, _ string) (any, error) {
	return b.call(ctx, vm)
}
func (b *blockingBackend) Guest(ctx context.Context, vm, _ string, _ map[string]any) (bridge.Result, error) {
	value, err := b.call(ctx, vm)
	return bridge.Success(value), err
}

func nextEntry(t *testing.T, entries <-chan string) string {
	t.Helper()
	select {
	case vm := <-entries:
		return vm
	case <-time.After(2 * time.Second):
		t.Fatal("backend was not entered")
		return ""
	}
}

func noEntry(t *testing.T, entries <-chan string) {
	t.Helper()
	select {
	case vm := <-entries:
		t.Fatalf("unexpected concurrent backend call: %q", vm)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestMCPBoundsConcurrentCommands(t *testing.T) {
	backend := newBlockingBackend()
	session, ctx := connect(t, backend, false)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "guest_windows", Arguments: map[string]any{"vm": "same-vm"}})
			if err != nil {
				t.Errorf("call: %v", err)
			}
		}()
	}
	for i := 0; i < DefaultConcurrentCommands; i++ {
		nextEntry(t, backend.entered)
	}
	noEntry(t, backend.entered)
	for i := 0; i < 12; i++ {
		backend.release <- struct{}{}
	}
	wg.Wait()
}

func TestQueuedQueryCancellationDoesNotExecute(t *testing.T) {
	backend := newBlockingBackend()
	b := newCoordinatedBackend(backend)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < DefaultConcurrentCommands; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = b.VMList(ctx)
		}()
		nextEntry(t, backend.entered)
	}
	queuedCtx, cancelQueued := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		_, err := b.VMInfo(queuedCtx, coordinateUUIDA)
		done <- err
	}()
	cancelQueued()
	select {
	case err := <-done:
		var failure *bridge.Error
		if !errors.As(err, &failure) || failure.Code != "INTERRUPTED" || failure.Details["phase"] != "queued" || failure.Details["outcome"] != "no_result" {
			t.Fatalf("cancellation: %#v", err)
		}
	case <-ctx.Done():
		t.Fatal("queued cancellation did not return")
	}
	noEntry(t, backend.entered)
	for i := 0; i < DefaultConcurrentCommands; i++ {
		backend.release <- struct{}{}
	}
	wg.Wait()
	noEntry(t, backend.entered)
}

func TestLifecycleUUIDAliasesSerializeAndOtherVMsProceed(t *testing.T) {
	backend := newBlockingBackend()
	b := newCoordinatedBackend(backend)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 3)
	write := func(vm string) {
		_, err := b.VMAction(ctx, "stop", vm, false, "")
		done <- err
	}
	go write("{AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA}")
	nextEntry(t, backend.entered)
	go write(coordinateUUIDA)
	go write(coordinateUUIDB)
	if vm := nextEntry(t, backend.entered); vm != coordinateUUIDB {
		t.Fatalf("expected a different VM to proceed, got %q", vm)
	}
	noEntry(t, backend.entered)
	backend.release <- struct{}{}
	backend.release <- struct{}{}
	if vm := nextEntry(t, backend.entered); vm != coordinateUUIDA {
		t.Fatal(vm)
	}
	backend.release <- struct{}{}
	for i := 0; i < 3; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

func TestNameWriteCannotOverlapUUIDAliasAndCanBeCancelled(t *testing.T) {
	backend := newBlockingBackend()
	b := newCoordinatedBackend(backend)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		_, _ = b.VMAction(ctx, "start", coordinateUUIDA, false, "")
	}()
	nextEntry(t, backend.entered)
	nameCtx, cancelName := context.WithCancel(ctx)
	nameDone := make(chan error, 1)
	go func() {
		_, err := b.VMAction(nameCtx, "stop", "Windows alias", false, "")
		nameDone <- err
	}()
	noEntry(t, backend.entered)
	cancelName()
	if err := <-nameDone; err == nil {
		t.Fatal("queued name write ignored cancellation")
	}
	// Cancellation must also remove the name barrier, so another UUID can run.
	otherDone := make(chan struct{})
	go func() {
		defer close(otherDone)
		_, _ = b.VMAction(ctx, "resume", coordinateUUIDB, false, "")
	}()
	if vm := nextEntry(t, backend.entered); vm != coordinateUUIDB {
		t.Fatal(vm)
	}
	backend.release <- struct{}{}
	backend.release <- struct{}{}
	<-firstDone
	<-otherDone
	noEntry(t, backend.entered)
}

func TestActiveNameWriteBlocksUUIDWrites(t *testing.T) {
	backend := newBlockingBackend()
	b := newCoordinatedBackend(backend)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan struct{}, 2)
	write := func(vm string) {
		_, _ = b.VMAction(ctx, "start", vm, false, "")
		done <- struct{}{}
	}
	go write("Windows alias")
	nextEntry(t, backend.entered)
	go write(coordinateUUIDA)
	noEntry(t, backend.entered)
	backend.release <- struct{}{}
	if vm := nextEntry(t, backend.entered); vm != coordinateUUIDA {
		t.Fatal(vm)
	}
	backend.release <- struct{}{}
	<-done
	<-done
}

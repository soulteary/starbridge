package mcpserver

import (
	"context"
	"regexp"
	"strings"
	"sync"

	"github.com/soulteary/starbridge/internal/bridge"
)

// DefaultConcurrentCommands bounds host subprocesses started by one MCP server.
// Mutations use the same pool so a burst of lifecycle calls cannot bypass it.
const DefaultConcurrentCommands = 4

var vmUUID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type coordinatedBackend struct {
	backend Backend
	slots   chan struct{}

	mu           sync.Mutex
	changed      chan struct{}
	activeUUIDs  map[string]bool
	nameActive   bool
	waitingNames int
}

func newCoordinatedBackend(backend Backend) *coordinatedBackend {
	return &coordinatedBackend{
		backend: backend, slots: make(chan struct{}, DefaultConcurrentCommands),
		changed: make(chan struct{}), activeUUIDs: map[string]bool{},
	}
}

func queuedError(ctx context.Context) error {
	code, message := "INTERRUPTED", "Cancelled while waiting for a command slot; no command was started."
	if ctx.Err() == context.DeadlineExceeded {
		code, message = "TIMEOUT", "Timed out while waiting for a command slot; no command was started."
	}
	return bridge.NewError(code, message, map[string]any{"outcome": "no_result", "phase": "queued"})
}

func (b *coordinatedBackend) acquireSlot(ctx context.Context) (func(), error) {
	if ctx.Err() != nil {
		return nil, queuedError(ctx)
	}
	select {
	case b.slots <- struct{}{}:
		if ctx.Err() != nil {
			<-b.slots
			return nil, queuedError(ctx)
		}
		return func() { <-b.slots }, nil
	case <-ctx.Done():
		return nil, queuedError(ctx)
	}
}

// writeIdentity accepts the UUID forms used by prlctl. Names cannot safely be
// keyed without resolving their current UUID, so they take an exclusive lane
// across all mutations. That also prevents name/UUID aliases bypassing a lock.
func writeIdentity(vm string) (string, bool) {
	if strings.HasPrefix(vm, "{") && strings.HasSuffix(vm, "}") {
		vm = vm[1 : len(vm)-1]
	}
	return strings.ToLower(vm), vmUUID.MatchString(vm)
}

func (b *coordinatedBackend) signalLocked() {
	close(b.changed)
	b.changed = make(chan struct{})
}

func (b *coordinatedBackend) acquireWrite(ctx context.Context, vm string) (func(), error) {
	key, isUUID := writeIdentity(vm)
	b.mu.Lock()
	if !isUUID {
		b.waitingNames++
	}
	for {
		if ctx.Err() != nil {
			if !isUUID {
				b.waitingNames--
				b.signalLocked()
			}
			b.mu.Unlock()
			return nil, queuedError(ctx)
		}
		available := !b.nameActive
		if isUUID {
			available = available && b.waitingNames == 0 && !b.activeUUIDs[key]
		} else {
			available = available && len(b.activeUUIDs) == 0
		}
		if available {
			if isUUID {
				b.activeUUIDs[key] = true
			} else {
				b.waitingNames--
				b.nameActive = true
			}
			b.mu.Unlock()
			return func() {
				b.mu.Lock()
				if isUUID {
					delete(b.activeUUIDs, key)
				} else {
					b.nameActive = false
				}
				b.signalLocked()
				b.mu.Unlock()
			}, nil
		}
		changed := b.changed
		b.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
		}
		b.mu.Lock()
	}
}

func (b *coordinatedBackend) VMList(ctx context.Context) (any, error) {
	release, err := b.acquireSlot(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return b.backend.VMList(ctx)
}

func (b *coordinatedBackend) VMInfo(ctx context.Context, vm string) (any, error) {
	release, err := b.acquireSlot(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return b.backend.VMInfo(ctx, vm)
}

func (b *coordinatedBackend) Guest(ctx context.Context, vm, action string, params map[string]any) (bridge.Result, error) {
	release, err := b.acquireSlot(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return b.backend.Guest(ctx, vm, action, params)
}

func (b *coordinatedBackend) VMAction(ctx context.Context, action, vm string, force bool, confirmation string) (any, error) {
	unlock, err := b.acquireWrite(ctx, vm)
	if err != nil {
		return nil, err
	}
	defer unlock()
	release, err := b.acquireSlot(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return b.backend.VMAction(ctx, action, vm, force, confirmation)
}

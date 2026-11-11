package policypub

import (
	"context"
	"errors"
	"sort"
	"sync"
)

// SA is a tunnel security association. Key material is treated as sensitive:
// the SADB takes ownership of the slice handed to Install and zeroizes it on
// Uninstall.
type SA struct {
	Handle    string
	Direction Direction
	Key       []byte
}

// Installer is the SA database abstraction used by the publisher.
// Implementations must be safe for concurrent use.
type Installer interface {
	Install(ctx context.Context, sa SA) error
	Uninstall(ctx context.Context, handle string) error
	Installed(handle string) bool
}

// SwapBarrier is an optional hook an Installer may implement to block a
// publication immediately before the atomic policy swap, after every new SA
// has been confirmed installed. It exists for verification harnesses.
type SwapBarrier interface {
	BeforeSwap(generation uint64)
}

// ErrAlreadyInstalled is returned when an SA handle is installed twice.
var ErrAlreadyInstalled = errors.New("policypub: SA already installed")

// SADB is an in-memory SA database implementing Installer.
type SADB struct {
	mu          sync.RWMutex
	installed   map[string]SA
	installHook func(SA) error
}

// NewSADB creates an empty SA database.
func NewSADB() *SADB {
	return &SADB{installed: make(map[string]SA)}
}

// SetInstallHook registers an optional hook invoked before every install;
// a non-nil error from the hook aborts the install.
func (d *SADB) SetInstallHook(hook func(SA) error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.installHook = hook
}

// Install stores a private copy of the SA and its key material.
func (d *SADB) Install(ctx context.Context, sa SA) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if sa.Handle == "" || sa.Direction == 0 {
		return errors.New("policypub: invalid SA")
	}
	d.mu.RLock()
	hook := d.installHook
	d.mu.RUnlock()
	if hook != nil {
		if err := hook(sa); err != nil {
			return err
		}
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if _, exists := d.installed[sa.Handle]; exists {
		return ErrAlreadyInstalled
	}
	stored := sa
	stored.Key = append([]byte(nil), sa.Key...)
	d.installed[sa.Handle] = stored
	return nil
}

// Uninstall removes an SA and zeroizes its stored key material. It is
// idempotent: uninstalling an unknown handle is not an error.
func (d *SADB) Uninstall(_ context.Context, handle string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	record, ok := d.installed[handle]
	if !ok {
		return nil
	}
	for i := range record.Key {
		record.Key[i] = 0
	}
	delete(d.installed, handle)
	return nil
}

// Installed reports whether handle is currently installed.
func (d *SADB) Installed(handle string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	_, ok := d.installed[handle]
	return ok
}

// Handles returns the sorted list of currently installed SA handles.
func (d *SADB) Handles() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]string, 0, len(d.installed))
	for handle := range d.installed {
		out = append(out, handle)
	}
	sort.Strings(out)
	return out
}

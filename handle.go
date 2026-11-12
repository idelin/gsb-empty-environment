package netsec

import (
	"sync"
	"syscall"
)

// ErrBadHandle is returned when a file handle is unknown to the table
// (never opened, or already released).
var ErrBadHandle = syscall.EBADF

// HandleTable binds each open file handle to the mapped backend identity
// that opened it. A handle may only be used through the identity it was
// bound to; any other mapped identity gets ErrAccess, so one caller can
// never reuse another caller's open handle.
type HandleTable struct {
	mu     sync.Mutex
	owners map[uint64]Identity
}

// NewHandleTable returns an empty, ready-to-use HandleTable.
func NewHandleTable() *HandleTable {
	return &HandleTable{owners: make(map[uint64]Identity)}
}

// Bind records that handle was opened under id. It must be called with the
// mapped identity obtained at open/create time.
func (t *HandleTable) Bind(handle uint64, id Identity) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.owners[handle] = id
}

// Check verifies that id is the identity handle was bound to. It returns
// ErrBadHandle for unknown handles and ErrAccess when a different mapped
// identity attempts to use the handle.
func (t *HandleTable) Check(handle uint64, id Identity) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	owner, ok := t.owners[handle]
	if !ok {
		return ErrBadHandle
	}
	if owner != id {
		return ErrAccess
	}
	return nil
}

// Release drops the binding for handle (on FUSE release/flush).
func (t *HandleTable) Release(handle uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.owners, handle)
}

package netsec

import (
	"sync"
	"time"
)

// ObjectView is the callback view supplied to Lease.Commit.  Only Data is
// written back.  Info is a snapshot for inspection.
type ObjectView struct {
	Info ObjectInfo
	Data []byte
}

// Lease is a generation-pinned authorization snapshot.  A lease does not keep
// a deleted object alive and does not rebind when the same fsid/inode (or even
// stable ID) is reused; any callback after replacement fails.
type Lease struct {
	mu sync.Mutex

	manager          *Manager
	cap              *Capability
	exportID         ExportID
	exportGeneration uint64
	objectID         StableObjectID
	objectGeneration uint64
	objectType       ObjectType
	policyDigest     [32]byte
	op               Op
	closed           bool
}

// ExportID returns the export UUID observed at acquisition time.
func (l *Lease) ExportID() ExportID {
	return l.exportID
}

// ObjectID returns the backend stable object ID observed at acquisition time.
func (l *Lease) ObjectID() StableObjectID {
	return l.objectID
}

// Generation returns the object generation observed at acquisition time.
func (l *Lease) Generation() uint64 {
	return l.objectGeneration
}

// ObjectType returns the object type observed at acquisition time.
func (l *Lease) ObjectType() ObjectType {
	return l.objectType
}

// Info returns a snapshot of the authorized object.
func (l *Lease) Info() (ObjectInfo, error) {
	if l == nil || l.manager == nil {
		return ObjectInfo{}, ErrBadHandle
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return ObjectInfo{}, ErrLeaseClosed
	}
	l.manager.mu.RLock()
	defer l.manager.mu.RUnlock()
	if err := l.verifyLocked(l.manager.now()); err != nil {
		return ObjectInfo{}, err
	}
	rec := l.manager.exports[l.exportID]
	obj := rec.objects[l.objectID]
	return objectInfo(obj), nil
}

// Verify re-runs the full authorization decision and generation snapshot
// comparison using the current clock.
func (l *Lease) Verify() error {
	if l == nil || l.manager == nil {
		return ErrBadHandle
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return ErrLeaseClosed
	}
	l.manager.mu.RLock()
	defer l.manager.mu.RUnlock()
	return l.verifyLocked(l.manager.now())
}

func (l *Lease) verifyLocked(now time.Time) error {
	if err := l.manager.checkLocked(l.cap, l.op, now); err != nil {
		return err
	}
	claims := l.cap.claims
	if claims.ExportID != l.exportID ||
		claims.ExportGeneration != l.exportGeneration ||
		claims.ObjectID != l.objectID ||
		claims.ObjectGeneration != l.objectGeneration ||
		claims.ObjectType != l.objectType ||
		claims.PolicyDigest != l.policyDigest {
		return ErrLeaseStale
	}
	return nil
}

// Commit validates the lease with WRITE policy and applies a storage callback
// while holding the manager lock.  If the object was deleted, recreated,
// rolled back, moved to another tenant, or the key grace expired, the callback
// is not invoked.  Only view.Data is written back.
func (l *Lease) Commit(fn func(view *ObjectView) error) error {
	return l.withView(OpWrite, true, fn)
}

func (l *Lease) withView(op Op, writeBack bool, fn func(view *ObjectView) error) error {
	if l == nil || l.manager == nil {
		return ErrBadHandle
	}
	if fn == nil {
		return ErrInvalidArgument
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return ErrLeaseClosed
	}
	m := l.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkLocked(l.cap, op, m.now()); err != nil {
		return err
	}
	claims := l.cap.claims
	if claims.ExportID != l.exportID ||
		claims.ExportGeneration != l.exportGeneration ||
		claims.ObjectID != l.objectID ||
		claims.ObjectGeneration != l.objectGeneration ||
		claims.ObjectType != l.objectType ||
		claims.PolicyDigest != l.policyDigest {
		return ErrLeaseStale
	}
	rec := m.exports[l.exportID]
	obj := rec.objects[l.objectID]
	view := &ObjectView{
		Info: objectInfo(obj),
		Data: append([]byte(nil), obj.data...),
	}
	if err := fn(view); err != nil {
		return err
	}
	if writeBack {
		obj.data = append([]byte(nil), view.Data...)
	}
	return nil
}

// Read returns a copy of the object data after re-checking READ policy.
func (l *Lease) Read() ([]byte, error) {
	var data []byte
	err := l.withView(OpRead, false, func(view *ObjectView) error {
		data = append([]byte(nil), view.Data...)
		return nil
	})
	return data, err
}

// Write replaces object data after re-checking WRITE policy.
func (l *Lease) Write(data []byte) error {
	return l.Commit(func(view *ObjectView) error {
		view.Data = append([]byte(nil), data...)
		return nil
	})
}

// Close marks the lease unusable.  It does not mutate storage.
func (l *Lease) Close() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
}

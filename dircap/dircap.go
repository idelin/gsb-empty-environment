// Package dircap implements a host-scoped registry of directory capabilities
// for a Go plugin host.
//
// Capabilities are owned by the host, not by a plugin instance, so handles
// registered by an old instance remain usable after a hot reload. A derived
// handle can only narrow the operation set of its parent. Closing a
// capability revokes its whole subtree: no descendant can start new reads,
// while reads already in flight keep the underlying directory object alive
// until the last release.
package dircap

import (
	"errors"
	"sync"

	"example.org/offlinecli/dircap/memfs"
)

// Ops is a bitmask of operations a capability grants on its directory.
type Ops uint16

const (
	OpRead Ops = 1 << iota
	OpWrite
	OpList
	OpExec
)

var (
	// ErrInvalidHandle is returned for handles that never existed or whose
	// slot has already been recycled by a newer capability.
	ErrInvalidHandle = errors.New("dircap: unknown or recycled handle")
	// ErrRevoked is returned for handles that were closed and whose slot
	// has not been recycled yet.
	ErrRevoked = errors.New("dircap: capability has been revoked")
	// ErrOpsEscalation is returned when Derive tries to widen the
	// operation set of the parent capability.
	ErrOpsEscalation = errors.New("dircap: derived capability cannot widen the operation set")
	// ErrOpNotGranted is returned when an operation is started through a
	// capability that does not grant it.
	ErrOpNotGranted = errors.New("dircap: operation not granted by capability")
)

// Handle identifies a capability. It packs a slot number and a generation
// counter: slots are recycled after Close, but the generation bump makes
// stale handles fail fast instead of aliasing a new capability.
type Handle uint64

func makeHandle(slot int, gen uint32) Handle {
	return Handle(uint64(gen)<<32 | uint64(uint32(slot)))
}

// node is one capability in a derivation tree.
type node struct {
	ops      Ops
	dir      *memfs.Dir
	parent   *node
	children map[*node]struct{}
	slot     int
	refs     int  // in-flight reads started through this node
	revoked  bool // subtree can no longer start operations
	retired  bool // revoked && refs == 0: unpinned from dir
}

type slotRec struct {
	gen uint32
	n   *node // nil after Close, until the slot is recycled
}

// Registry is the host-level capability table. It is safe for concurrent
// use and survives plugin hot reloads.
type Registry struct {
	mu    sync.Mutex
	slots []slotRec
	free  []int
	pins  map[*memfs.Dir]int // live capabilities + in-flight reads per dir
}

func NewRegistry() *Registry {
	return &Registry{pins: make(map[*memfs.Dir]int)}
}

// Register installs dir as a new root capability granting ops and returns
// its handle.
func (r *Registry) Register(dir *memfs.Dir, ops Ops) Handle {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := &node{ops: ops, dir: dir}
	r.pinLocked(dir)
	return r.allocLocked(n)
}

// Derive creates a child capability of parent. ops must be a subset of the
// parent's operations: derivation can only narrow, never widen.
func (r *Registry) Derive(parent Handle, ops Ops) (Handle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pn, err := r.lookupLocked(parent)
	if err != nil {
		return 0, err
	}
	if ops&^pn.ops != 0 {
		return 0, ErrOpsEscalation
	}
	n := &node{ops: ops, dir: pn.dir, parent: pn}
	if pn.children == nil {
		pn.children = make(map[*node]struct{})
	}
	pn.children[n] = struct{}{}
	r.pinLocked(pn.dir)
	return r.allocLocked(n), nil
}

// Close revokes h and its whole subtree and recycles the handle numbers.
// Reads already in flight keep the underlying directory alive; it is
// released only when the last in-flight read is released.
func (r *Registry) Close(h Handle) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, err := r.lookupLocked(h)
	if err != nil {
		return err
	}
	r.revokeLocked(n)
	return nil
}

// Open starts a read of name through capability h. Starting a read pins the
// underlying directory object: even if h (or an ancestor) is closed while
// the read is in flight, the object stays alive until Release.
func (r *Registry) Open(h Handle, name string) (*ReadFile, error) {
	r.mu.Lock()
	n, err := r.lookupLocked(h)
	if err != nil {
		r.mu.Unlock()
		return nil, err
	}
	if n.ops&OpRead == 0 {
		r.mu.Unlock()
		return nil, ErrOpNotGranted
	}
	n.refs++
	r.mu.Unlock()

	f, err := n.dir.Open(name)
	if err != nil {
		r.mu.Lock()
		n.refs--
		r.maybeRetireLocked(n)
		r.mu.Unlock()
		return nil, err
	}
	return &ReadFile{file: f, reg: r, n: n}, nil
}

func (r *Registry) lookupLocked(h Handle) (*node, error) {
	slot := int(uint32(uint64(h)))
	gen := uint32(uint64(h) >> 32)
	if gen == 0 || slot >= len(r.slots) {
		return nil, ErrInvalidHandle
	}
	rec := &r.slots[slot]
	if rec.gen != gen {
		return nil, ErrInvalidHandle
	}
	if rec.n == nil {
		return nil, ErrRevoked
	}
	return rec.n, nil
}

func (r *Registry) allocLocked(n *node) Handle {
	var slot int
	if last := len(r.free) - 1; last >= 0 {
		slot = r.free[last]
		r.free = r.free[:last]
	} else {
		slot = len(r.slots)
		r.slots = append(r.slots, slotRec{})
	}
	rec := &r.slots[slot]
	rec.gen++
	if rec.gen == 0 { // never hand out the zero handle
		rec.gen = 1
	}
	rec.n = n
	n.slot = slot
	return makeHandle(slot, rec.gen)
}

// revokeLocked revokes n and its whole subtree, recycling every handle
// number in it. Nodes with in-flight reads stay pinned until released.
func (r *Registry) revokeLocked(n *node) {
	if n.revoked {
		return // closed individually before; subtree was handled then
	}
	n.revoked = true
	rec := &r.slots[n.slot]
	rec.n = nil
	r.free = append(r.free, n.slot)
	kids := make([]*node, 0, len(n.children))
	for c := range n.children {
		kids = append(kids, c)
	}
	for _, c := range kids {
		r.revokeLocked(c)
	}
	r.maybeRetireLocked(n)
}

func (r *Registry) maybeRetireLocked(n *node) {
	if !n.revoked || n.refs > 0 || n.retired {
		return
	}
	n.retired = true
	if n.parent != nil {
		delete(n.parent.children, n)
	}
	r.unpinLocked(n.dir)
}

func (r *Registry) pinLocked(dir *memfs.Dir) {
	r.pins[dir]++
}

func (r *Registry) unpinLocked(dir *memfs.Dir) {
	r.pins[dir]--
	if r.pins[dir] == 0 {
		delete(r.pins, dir)
		dir.Release()
	}
}

// ReadFile is an in-flight read started through a capability. It keeps the
// underlying directory object alive until Release.
type ReadFile struct {
	file *memfs.File
	reg  *Registry
	n    *node
	once sync.Once
}

func (rf *ReadFile) Read(p []byte) (int, error) { return rf.file.Read(p) }

// Release ends the read. If the capability was revoked meanwhile and this
// was the last in-flight read, the directory object is released. Release is
// idempotent.
func (rf *ReadFile) Release() {
	rf.once.Do(func() {
		rf.file.Close()
		rf.reg.mu.Lock()
		rf.n.refs--
		rf.reg.maybeRetireLocked(rf.n)
		rf.reg.mu.Unlock()
	})
}

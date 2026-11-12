// Package fdtab models a file-descriptor table for a Go process host that
// runs plugin tasks as logical processes. It mirrors the semantics a host
// needs when plugin code shares a descriptor table with its host or with
// sibling plugins: cloning may share or copy the table, a process may
// detach (unshare) its table atomically, slots may be duplicated, and each
// slot carries an independent close-on-exec flag.
//
// The model tracks open file descriptions (File) with reference counts so a
// host can verify that one party cleaning up its handles never releases a
// resource another party still references.
package fdtab

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

// ErrBadFD is returned when a descriptor number does not refer to an open
// slot in the caller's table.
var ErrBadFD = errors.New("fdtab: bad file descriptor")

// ErrTooManyFiles is returned when a table has no free slot below its
// capacity limit.
var ErrTooManyFiles = errors.New("fdtab: too many open files")

// File is an open file description: the shared object that descriptor slots
// point at. Its reference count is the number of slots, across every table,
// that currently refer to it. When the count drops to zero the file is
// released exactly once.
type File struct {
	name     string
	refs     atomic.Int32
	released atomic.Bool
}

// NewFile creates a file description with no references. It becomes
// referenced when installed into a slot with Open.
func NewFile(name string) *File {
	return &File{name: name}
}

// Name returns the label passed to NewFile. It is diagnostic only.
func (f *File) Name() string { return f.name }

// Refs reports the current reference count.
func (f *File) Refs() int { return int(f.refs.Load()) }

// Released reports whether the file's reference count reached zero and the
// file was released.
func (f *File) Released() bool { return f.released.Load() }

// slot is one descriptor table entry: a reference to a file plus flags
// that are private to the slot.
type slot struct {
	file    *File
	cloexec bool
}

// SlotInfo describes one occupied slot for inspection.
type SlotInfo struct {
	File    *File
	CloExec bool
}

// Table is a descriptor table. Processes that clone with share=true point
// at the same Table; processes that clone with a copy, or that Unshare,
// receive an independent Table. All mutation is serialized by the table's
// mutex, so Unshare observes a consistent snapshot.
//
// A table tracks how many processes are bound to it; when the last one
// leaves (Exit or Unshare) its remaining slots are closed, dropping their
// file references.
type Table struct {
	mu    sync.Mutex
	slots map[int]slot
	maxFD int
	procs int
}

// NewTable creates an empty table that can hold descriptor numbers
// 0..maxFD-1.
func NewTable(maxFD int) *Table {
	if maxFD <= 0 {
		panic("fdtab: maxFD must be positive")
	}
	return &Table{slots: make(map[int]slot), maxFD: maxFD}
}

// Process is a logical process handle bound to a descriptor table. Safety
// comes from the underlying Table lock, so distinct processes may operate
// concurrently even when they share a table.
type Process struct {
	tbl *Table
}

// Process attaches a new logical process to the table.
func (t *Table) Process() *Process {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.procs++
	return &Process{tbl: t}
}

// Table returns the table the process is currently bound to.
func (p *Process) Table() *Table { return p.tbl }

// Clone derives a child process. If share is true the child references the
// same table (like CLONE_FILES): every later open, close, dup, or flag
// change by either party is visible to both. If share is false the child
// receives a private deep copy of the table; file reference counts are
// incremented so shared files stay alive, but subsequent slot changes in
// either table never affect the other.
func (p *Process) Clone(share bool) *Process {
	src := p.tbl
	src.mu.Lock()
	defer src.mu.Unlock()
	if share {
		src.procs++
		return &Process{tbl: src}
	}
	cp := &Table{slots: make(map[int]slot, len(src.slots)), maxFD: src.maxFD, procs: 1}
	for fd, s := range src.slots {
		incRef(s.file)
		cp.slots[fd] = s
	}
	return &Process{tbl: cp}
}

// Unshare detaches the process from its shared table. The copy is made
// atomically under the source table's lock, so the process obtains one
// consistent snapshot; from then on its mutations are invisible to former
// sharers, and theirs are invisible to it. If the process was the last one
// bound to the old table, that table's remaining slots are closed.
func (p *Process) Unshare() {
	src := p.tbl
	src.mu.Lock()
	defer src.mu.Unlock()
	cp := &Table{slots: make(map[int]slot, len(src.slots)), maxFD: src.maxFD, procs: 1}
	for fd, s := range src.slots {
		incRef(s.file)
		cp.slots[fd] = s
	}
	src.leaveLocked()
	p.tbl = cp
}

// Exit detaches the process from its table, as when a plugin terminates.
// If it was the last process bound to the table, the table's remaining
// slots are closed. Using the process after Exit panics.
func (p *Process) Exit() {
	t := p.tbl
	t.mu.Lock()
	defer t.mu.Unlock()
	t.leaveLocked()
	p.tbl = nil
}

// leaveLocked unbinds one process from the table, sweeping the table's
// slots when the last bound process leaves.
func (t *Table) leaveLocked() {
	t.procs--
	if t.procs == 0 {
		for fd, s := range t.slots {
			delete(t.slots, fd)
			decRef(s.file)
		}
	}
}

// Open installs f in the lowest-numbered free slot and returns its
// descriptor number. Numbers of closed slots are reused.
func (p *Process) Open(f *File) (int, error) {
	t := p.tbl
	t.mu.Lock()
	defer t.mu.Unlock()
	fd, ok := t.lowestFreeLocked()
	if !ok {
		return -1, ErrTooManyFiles
	}
	incRef(f)
	t.slots[fd] = slot{file: f}
	return fd, nil
}

// Close removes a slot and drops its file reference. Closing an unoccupied
// slot is an error.
func (p *Process) Close(fd int) error {
	t := p.tbl
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.slots[fd]
	if !ok {
		return ErrBadFD
	}
	delete(t.slots, fd)
	decRef(s.file)
	return nil
}

// Dup copies the slot at fd into the lowest-numbered free slot, like
// dup(2). The new slot does not inherit the close-on-exec flag. The file
// gains one reference.
func (p *Process) Dup(fd int) (int, error) {
	t := p.tbl
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.slots[fd]
	if !ok {
		return -1, ErrBadFD
	}
	nfd, ok := t.lowestFreeLocked()
	if !ok {
		return -1, ErrTooManyFiles
	}
	incRef(s.file)
	t.slots[nfd] = slot{file: s.file}
	return nfd, nil
}

// Dup2 copies the slot at oldfd into newfd, like dup2(2): if newfd is
// occupied its file reference is dropped first. The target slot's
// close-on-exec flag is cleared. Duplicating a slot onto itself is a
// no-op that validates oldfd.
func (p *Process) Dup2(oldfd, newfd int) error {
	if newfd < 0 || newfd >= p.tbl.maxFD {
		return ErrBadFD
	}
	t := p.tbl
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.slots[oldfd]
	if !ok {
		return ErrBadFD
	}
	if oldfd == newfd {
		return nil
	}
	if old, ok := t.slots[newfd]; ok {
		decRef(old.file)
	}
	incRef(s.file)
	t.slots[newfd] = slot{file: s.file}
	return nil
}

// SetCloExec sets or clears the close-on-exec flag of one slot. The flag
// belongs to the slot, not the file: other slots referencing the same file
// are unaffected.
func (p *Process) SetCloExec(fd int, on bool) error {
	t := p.tbl
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.slots[fd]
	if !ok {
		return ErrBadFD
	}
	s.cloexec = on
	t.slots[fd] = s
	return nil
}

// Exec closes every slot whose close-on-exec flag is set, modelling the
// descriptor sweep a host performs when a plugin process execs. Flags of
// surviving slots are left untouched.
func (p *Process) Exec() {
	t := p.tbl
	t.mu.Lock()
	defer t.mu.Unlock()
	for fd, s := range t.slots {
		if s.cloexec {
			delete(t.slots, fd)
			decRef(s.file)
		}
	}
}

// Lookup returns the file behind fd and whether the slot is open.
func (p *Process) Lookup(fd int) (*File, bool) {
	t := p.tbl
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.slots[fd]
	if !ok {
		return nil, false
	}
	return s.file, true
}

// Snapshot returns a consistent copy of the table's occupied slots.
func (t *Table) Snapshot() map[int]SlotInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[int]SlotInfo, len(t.slots))
	for fd, s := range t.slots {
		out[fd] = SlotInfo{File: s.file, CloExec: s.cloexec}
	}
	return out
}

// FDs lists the occupied descriptor numbers in ascending order.
func (t *Table) FDs() []int {
	t.mu.Lock()
	defer t.mu.Unlock()
	fds := make([]int, 0, len(t.slots))
	for fd := range t.slots {
		fds = append(fds, fd)
	}
	sort.Ints(fds)
	return fds
}

// lowestFreeLocked returns the smallest unoccupied descriptor number.
func (t *Table) lowestFreeLocked() (int, bool) {
	for fd := 0; fd < t.maxFD; fd++ {
		if _, ok := t.slots[fd]; !ok {
			return fd, true
		}
	}
	return -1, false
}

// decRef drops one reference and releases the file at zero.
func decRef(f *File) {
	n := f.refs.Add(-1)
	if n == 0 {
		f.released.Store(true)
	}
	if n < 0 {
		panic(fmt.Sprintf("fdtab: file %q reference count went negative", f.name))
	}
}

// incRef adds one reference.
func incRef(f *File) { f.refs.Add(1) }

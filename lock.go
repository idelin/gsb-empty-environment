package netsec

import (
	"fmt"
	"sort"
	"sync"
)

// LockType is the access mode of a byte-range lock.
type LockType int

const (
	Read LockType = iota
	Write
)

func (t LockType) String() string {
	if t == Write {
		return "W"
	}
	return "R"
}

// Kind selects the ownership semantics of a lock request.
type Kind int

const (
	// POSIX is a process record lock: owned by the process, dropped when
	// the process closes any descriptor it holds on the same file, and
	// not inherited across Fork.
	POSIX Kind = iota
	// OFD is an open file description lock: owned by the open file
	// description, shared by every duplicated descriptor (Dup, Fork)
	// and released only when the last reference is closed.
	OFD
)

func (k Kind) String() string {
	if k == OFD {
		return "OFD"
	}
	return "POSIX"
}

func conflicts(a, b LockType) bool { return a == Write || b == Write }

// ownerKey identifies the lock owner. Two owners are equal only when
// they refer to the same process (POSIX) or the same open file
// description (OFD); a pid alone is never enough.
type ownerKey struct {
	kind Kind
	proc *Process
	ofd  *OpenFileDescription
}

func (o ownerKey) String() string {
	if o.kind == POSIX {
		return fmt.Sprintf("posix(pid=%d)", o.proc.pid)
	}
	return fmt.Sprintf("ofd#%d", o.ofd.id)
}

// fileLock is one held byte-range lock, [start, end).
type fileLock struct {
	owner ownerKey
	typ   LockType
	start int64
	end   int64
}

func (l fileLock) String() string {
	return fmt.Sprintf("%s %s [%d,%d)", l.owner, l.typ, l.start, l.end)
}

// waitReq is a queued blocking lock request.
type waitReq struct {
	owner ownerKey
	typ   LockType
	start int64
	end   int64
	ch    chan struct{}
}

// File is a simulated inode holding the lock table and wait queue.
type File struct {
	path  string
	mu    sync.Mutex
	locks []fileLock
	queue []*waitReq
}

func (f *File) conflictsLocked(owner ownerKey, typ LockType, s, e int64) bool {
	for _, l := range f.locks {
		if l.owner == owner || l.start >= e || s >= l.end {
			continue
		}
		if conflicts(l.typ, typ) {
			return true
		}
	}
	return false
}

// applyLockLocked replaces the owner's locks overlapping [s,e) with a
// new lock of typ, splitting existing locks at the boundaries and
// coalescing adjacent same-type ranges afterwards.
func (f *File) applyLockLocked(owner ownerKey, typ LockType, s, e int64) {
	out := make([]fileLock, 0, len(f.locks)+1)
	for _, l := range f.locks {
		if l.owner != owner || l.end <= s || l.start >= e {
			out = append(out, l)
			continue
		}
		if l.start < s {
			out = append(out, fileLock{l.owner, l.typ, l.start, s})
		}
		if l.end > e {
			out = append(out, fileLock{l.owner, l.typ, e, l.end})
		}
	}
	out = append(out, fileLock{owner, typ, s, e})
	f.locks = coalesce(out)
}

// coalesce sorts locks by start and merges adjacent ranges that share
// owner and type. Same-owner locks never overlap, so per owner the
// sorted order is also the merge order; other owners may interleave.
func coalesce(locks []fileLock) []fileLock {
	sort.Slice(locks, func(i, j int) bool {
		if locks[i].start != locks[j].start {
			return locks[i].start < locks[j].start
		}
		if locks[i].end != locks[j].end {
			return locks[i].end < locks[j].end
		}
		return locks[i].typ < locks[j].typ
	})
	out := locks[:0]
	last := make(map[ownerKey]int)
	for _, l := range locks {
		if idx, ok := last[l.owner]; ok {
			top := &out[idx]
			if top.owner == l.owner && top.typ == l.typ && top.end == l.start {
				top.end = l.end
				continue
			}
		}
		last[l.owner] = len(out)
		out = append(out, l)
	}
	return out
}

func (f *File) lock(owner ownerKey, typ LockType, s, e int64, wait bool) bool {
	if s >= e {
		return true
	}
	f.mu.Lock()
	if !f.conflictsLocked(owner, typ, s, e) {
		f.applyLockLocked(owner, typ, s, e)
		f.mu.Unlock()
		return true
	}
	if !wait {
		f.mu.Unlock()
		return false
	}
	r := &waitReq{owner: owner, typ: typ, start: s, end: e, ch: make(chan struct{})}
	f.queue = append(f.queue, r)
	f.mu.Unlock()
	<-r.ch
	return true
}

// unlock releases [s,e) from owner's held locks, splitting as needed,
// then wakes queued waiters in arrival order.
func (f *File) unlock(owner ownerKey, s, e int64) {
	if s >= e {
		return
	}
	f.mu.Lock()
	out := f.locks[:0]
	for _, l := range f.locks {
		if l.owner != owner || l.end <= s || l.start >= e {
			out = append(out, l)
			continue
		}
		if l.start < s {
			out = append(out, fileLock{l.owner, l.typ, l.start, s})
		}
		if l.end > e {
			out = append(out, fileLock{l.owner, l.typ, e, l.end})
		}
	}
	f.locks = out
	f.wakeLocked()
	f.mu.Unlock()
}

// unlockOwner drops every lock held by owner (close / exit paths).
func (f *File) unlockOwner(owner ownerKey) {
	f.mu.Lock()
	out := f.locks[:0]
	for _, l := range f.locks {
		if l.owner != owner {
			out = append(out, l)
		}
	}
	f.locks = out
	f.wakeLocked()
	f.mu.Unlock()
}

// wakeLocked grants queued requests strictly in arrival order: the
// first request that still conflicts stops the scan, so no waiter is
// ever overtaken and no tenant is released early.
func (f *File) wakeLocked() {
	for len(f.queue) > 0 {
		r := f.queue[0]
		if f.conflictsLocked(r.owner, r.typ, r.start, r.end) {
			return
		}
		f.queue = f.queue[1:]
		f.applyLockLocked(r.owner, r.typ, r.start, r.end)
		close(r.ch)
	}
}

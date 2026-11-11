package noncelease

import (
	"errors"
	"io"
	"os"
	"sync"
	"time"
)

// errInjectedCrash models the I/O error a writer observes when the machine
// loses power during fsync; the durable state on disk is the torn/old state.
var errInjectedCrash = errors.New("noncelease test: simulated crash during sync")

// faultKind selects how the next Sync behaves.
type faultKind int

const (
	faultNone faultKind = iota
	// faultTorn persists only the first faultKeep bytes of the pending write,
	// emulating a torn page after a crash.
	faultTorn
	// faultLost drops the pending write entirely, emulating a crash before the
	// page cache reached stable storage.
	faultLost
)

// crashDisk is an in-memory "disk" whose durability boundary is crossed only
// on Sync. The injected fault applies to the next Sync exactly once.
type crashDisk struct {
	mu        sync.Mutex
	committed []byte
	pending   []byte
	fault     faultKind
	faultKeep int
	crashed   bool
}

func newCrashDisk() *crashDisk { return &crashDisk{} }

// armFault schedules the next Sync to tear (keeping keep bytes) or lose data.
func (d *crashDisk) armFault(k faultKind, keep int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.fault = k
	d.faultKeep = keep
}

func (d *crashDisk) writeAt(p []byte, off int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	end := int(off) + len(p)
	if end > len(d.pending) {
		buf := make([]byte, end)
		copy(buf, d.pending)
		d.pending = buf
	}
	copy(d.pending[off:], p)
}

func (d *crashDisk) readAt(p []byte, off int64) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	src := d.pending
	if d.crashed {
		src = d.committed
	}
	if off >= int64(len(src)) {
		return 0
	}
	return copy(p, src[off:])
}

// sync crosses the durability boundary, applying the armed fault.
func (d *crashDisk) sync() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch d.fault {
	case faultTorn:
		keep := d.faultKeep
		if keep > len(d.pending) {
			keep = len(d.pending)
		}
		if keep < 0 {
			keep = 0
		}
		torn := make([]byte, keep)
		copy(torn, d.pending[:keep])
		d.committed = torn
	case faultLost:
		// committed stays as it was before the pending write.
	default:
		snap := make([]byte, len(d.pending))
		copy(snap, d.pending)
		d.committed = snap
	}
	fault := d.fault
	d.fault = faultNone
	if fault != faultNone {
		return errInjectedCrash
	}
	return nil
}

// crash rewinds the volume to its last durable state, like losing power.
func (d *crashDisk) crash() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.crashed = true
	d.pending = append([]byte(nil), d.committed...)
}

func (d *crashDisk) truncate(size int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if size < int64(len(d.pending)) {
		d.pending = append([]byte(nil), d.pending[:size]...)
	}
	if size < int64(len(d.committed)) {
		d.committed = append([]byte(nil), d.committed[:size]...)
	}
}

func (d *crashDisk) len() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.crashed {
		return len(d.committed)
	}
	return len(d.pending)
}

// crashFile adapts a crashDisk to the durableFile interface.
type crashFile struct {
	disk *crashDisk
	off  int64
}

func (f *crashFile) Write(p []byte) (int, error) {
	f.disk.writeAt(p, f.off)
	f.off += int64(len(p))
	return len(p), nil
}

func (f *crashFile) ReadAt(p []byte, off int64) (int, error) {
	n := f.disk.readAt(p, off)
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (f *crashFile) Close() error { return nil }
func (f *crashFile) Name() string { return "crash.log" }
func (f *crashFile) Seek(off int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		f.off = off
	case io.SeekCurrent:
		f.off += off
	case io.SeekEnd:
		f.off = int64(f.disk.len()) + off
	}
	return f.off, nil
}
func (f *crashFile) Sync() error                { return f.disk.sync() }
func (f *crashFile) Truncate(size int64) error  { f.disk.truncate(size); f.off = size; return nil }
func (f *crashFile) Stat() (os.FileInfo, error) { return crashFileInfo{size: int64(f.disk.len())}, nil }

type crashFileInfo struct{ size int64 }

func (i crashFileInfo) Name() string       { return "crash.log" }
func (i crashFileInfo) Size() int64        { return i.size }
func (i crashFileInfo) Mode() os.FileMode  { return 0o600 }
func (i crashFileInfo) ModTime() time.Time { return time.Time{} }
func (i crashFileInfo) IsDir() bool        { return false }
func (i crashFileInfo) Sys() any           { return nil }

// reopenCrashStore closes the current view, rewinds the disk to durable state
// and replays the WAL into a fresh store, as a process restart would.
func reopenCrashStore(disk *crashDisk, prev *Store) (*Store, error) {
	if prev != nil {
		_ = prev.Close()
	}
	disk.crash()
	return newStore(&crashFile{disk: disk})
}

package publisher

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// ErrCrashed is returned by every FaultFS operation after a simulated
// crash, until Reboot is called.
var ErrCrashed = errors.New("faultfs: machine is crashed")

// OpKind enumerates the mutating cut points a publish goes through.
type OpKind string

const (
	OpMkdir   OpKind = "mkdir"
	OpCreate  OpKind = "create"
	OpWrite   OpKind = "write"
	OpSync    OpKind = "sync"
	OpRename  OpKind = "rename"
	OpRemove  OpKind = "remove"
	OpSyncDir OpKind = "syncdir"
)

// Op is one recorded mutating operation.
type Op struct {
	Kind OpKind
	Path string
}

func (o Op) String() string { return fmt.Sprintf("%s(%s)", o.Kind, o.Path) }

// FaultFS is an in-memory FS that models crash durability: content written
// to a file becomes durable only after File.Sync, and namespace changes
// (create, rename, remove) become durable only after SyncDir on the
// containing directory. Crash discards everything that is not durable.
//
// It also records every mutating operation so tests can enumerate the cut
// points of a publish and arm a crash at each of them.
type FaultFS struct {
	durableNS  map[string]bool
	durableBuf map[string][]byte
	volatileNS map[string]bool
	volatile   map[string][]byte
	pendingNS  map[string]bool // namespace changes awaiting SyncDir

	ops        []Op
	armed      bool
	crashAfter int // crash before executing the op with this index
	crashed    bool
}

func NewFaultFS() *FaultFS {
	f := &FaultFS{
		durableNS:  make(map[string]bool),
		durableBuf: make(map[string][]byte),
		volatileNS: make(map[string]bool),
		volatile:   make(map[string][]byte),
		pendingNS:  make(map[string]bool),
	}
	return f
}

// Ops returns the mutating operations recorded since the last ResetOps.
func (f *FaultFS) Ops() []Op {
	out := make([]Op, len(f.ops))
	copy(out, f.ops)
	return out
}

// ResetOps clears the recorded operation log.
func (f *FaultFS) ResetOps() { f.ops = nil }

// Arm arranges for a crash to happen just before the mutating operation
// that would be recorded at index n (counted from the last ResetOps).
// Arming with n >= number of subsequent ops means no crash.
func (f *FaultFS) Arm(n int) {
	f.armed = true
	f.crashAfter = n
}

// Reboot simulates a power-on: the machine accepts operations again, with
// only durable state surviving.
func (f *FaultFS) Reboot() {
	f.crashed = false
	f.armed = false
}

// crash drops all non-durable state.
func (f *FaultFS) crash() {
	f.volatileNS = make(map[string]bool)
	for p := range f.durableNS {
		f.volatileNS[p] = true
	}
	f.volatile = make(map[string][]byte)
	for p, b := range f.durableBuf {
		cp := make([]byte, len(b))
		copy(cp, b)
		f.volatile[p] = cp
	}
	f.pendingNS = make(map[string]bool)
	f.crashed = true
}

// gate must be called by every mutating operation. It records the op and
// triggers an armed crash.
func (f *FaultFS) gate(kind OpKind, p string) error {
	if f.crashed {
		return ErrCrashed
	}
	if f.armed && len(f.ops) >= f.crashAfter {
		f.crash()
		return ErrCrashed
	}
	f.ops = append(f.ops, Op{Kind: kind, Path: p})
	return nil
}

func (f *FaultFS) MkdirAll(dir string) error {
	return f.gate(OpMkdir, dir)
}

func (f *FaultFS) Create(name string) (File, error) {
	if err := f.gate(OpCreate, name); err != nil {
		return nil, err
	}
	f.volatileNS[name] = true
	f.volatile[name] = nil
	f.pendingNS[name] = true
	return &faultFile{fs: f, path: name}, nil
}

func (f *FaultFS) Append(name string) (File, error) {
	if err := f.gate(OpCreate, name); err != nil {
		return nil, err
	}
	if !f.volatileNS[name] {
		f.volatileNS[name] = true
		if f.durableNS[name] {
			cp := make([]byte, len(f.durableBuf[name]))
			copy(cp, f.durableBuf[name])
			f.volatile[name] = cp
		} else {
			f.volatile[name] = nil
			f.pendingNS[name] = true
		}
	}
	return &faultFile{fs: f, path: name}, nil
}

func (f *FaultFS) ReadFile(name string) ([]byte, error) {
	if f.crashed {
		return nil, ErrCrashed
	}
	if !f.volatileNS[name] {
		return nil, fmt.Errorf("faultfs: %s: no such file", name)
	}
	out := make([]byte, len(f.volatile[name]))
	copy(out, f.volatile[name])
	return out, nil
}

func (f *FaultFS) Rename(oldpath, newpath string) error {
	if err := f.gate(OpRename, oldpath+" -> "+newpath); err != nil {
		return err
	}
	if !f.volatileNS[oldpath] {
		return fmt.Errorf("faultfs: rename %s: no such file", oldpath)
	}
	// Volatile view: the name moves now.
	f.volatileNS[newpath] = true
	f.volatile[newpath] = f.volatile[oldpath]
	delete(f.volatileNS, oldpath)
	delete(f.volatile, oldpath)
	// Already-durable content travels with the inode.
	if buf, ok := f.durableBuf[oldpath]; ok {
		f.durableBuf[newpath] = buf
		delete(f.durableBuf, oldpath)
	}
	f.pendingNS[oldpath] = true
	f.pendingNS[newpath] = true
	return nil
}

func (f *FaultFS) Remove(name string) error {
	if err := f.gate(OpRemove, name); err != nil {
		return err
	}
	delete(f.volatileNS, name)
	delete(f.volatile, name)
	f.pendingNS[name] = true
	return nil
}

func (f *FaultFS) SyncDir(dir string) error {
	if err := f.gate(OpSyncDir, dir); err != nil {
		return err
	}
	dir = path.Clean(dir)
	for p := range f.pendingNS {
		if path.Dir(p) != dir {
			continue
		}
		if f.volatileNS[p] {
			f.durableNS[p] = true
		} else {
			delete(f.durableNS, p)
			delete(f.durableBuf, p)
		}
		delete(f.pendingNS, p)
	}
	return nil
}

// faultFile writes into the volatile view; Sync checkpoints content into
// the durable view.
type faultFile struct {
	fs     *FaultFS
	path   string
	closed bool
}

func (w *faultFile) Write(p []byte) (int, error) {
	if err := w.fs.gate(OpWrite, w.path); err != nil {
		return 0, err
	}
	w.fs.volatile[w.path] = append(w.fs.volatile[w.path], p...)
	return len(p), nil
}

func (w *faultFile) Sync() error {
	if err := w.fs.gate(OpSync, w.path); err != nil {
		return err
	}
	cp := make([]byte, len(w.fs.volatile[w.path]))
	copy(cp, w.fs.volatile[w.path])
	w.fs.durableBuf[w.path] = cp
	return nil
}

func (w *faultFile) Close() error {
	w.closed = true
	return nil
}

// Dump returns a human-readable durable state, for debugging tests.
func (f *FaultFS) Dump() string {
	var sb strings.Builder
	for p := range f.durableNS {
		fmt.Fprintf(&sb, "%s (%d bytes)\n", p, len(f.durableBuf[p]))
	}
	return sb.String()
}

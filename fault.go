package segvm

import (
	"errors"
	"fmt"
)

// OpKind identifies one durable-operation class. The fault model covers
// exactly these operations; it simulates per-operation success/failure and
// process exit between operations. It does NOT claim to model real power
// loss, hardware torn writes or device reordering.
type OpKind string

const (
	OpCreate   OpKind = "create" // create tmp/segment file
	OpWrite    OpKind = "write"  // write file bytes
	OpSync     OpKind = "sync"   // fsync a file
	OpTruncate OpKind = "truncate"
	OpRename   OpKind = "rename" // atomic rename tmp -> final
	OpRemove   OpKind = "remove"
	OpSyncDir  OpKind = "dirsync" // fsync a directory
)

// ErrCrash is panicked by FaultFS to simulate an abrupt process exit
// immediately after a completed operation. Tests recover it and reopen
// the store from disk, which models a restart.
var ErrCrash = errors.New("segvm: simulated process exit")

// FaultFS wraps an FS with deterministic fault injection.
//
// Two injection modes:
//   - FailKind/FailNth: the Nth call of that op kind fails with FailErr
//     (the operation is NOT applied). Models an I/O error.
//   - CrashAfter: after the Nth successfully applied mutating op
//     (in global publish order), panic(ErrCrash). Models the process
//     exiting at that exact point with all prior ops persisted.
//   - TornAt: if the Nth mutating op is a write, only half the bytes are
//     written and then ErrCrash is panicked. Models a process dying in
//     the middle of a write (produces an incomplete log tail).
type FaultFS struct {
	Base FS

	Ops        []OpKind // log of successfully applied mutating ops, in order
	CrashAfter int      // panic ErrCrash after this many applied ops; 0 = never
	TornAt     int      // torn-write at this op index (1-based); 0 = never
	FailKind   OpKind
	FailNth    int // 1-based; 0 disables
	FailErr    error

	kindCount map[OpKind]int
}

var _ FS = (*FaultFS)(nil)

func (f *FaultFS) failIf(kind OpKind) error {
	if f.kindCount == nil {
		f.kindCount = map[OpKind]int{}
	}
	f.kindCount[kind]++
	if f.FailKind == kind && f.FailNth > 0 && f.kindCount[kind] == f.FailNth {
		if f.FailErr != nil {
			return f.FailErr
		}
		return fmt.Errorf("segvm: injected fault on %s #%d", kind, f.FailNth)
	}
	return nil
}

// after records a successfully applied mutating op and maybe crashes.
func (f *FaultFS) after(kind OpKind) {
	f.Ops = append(f.Ops, kind)
	if f.CrashAfter > 0 && len(f.Ops) == f.CrashAfter {
		panic(ErrCrash)
	}
}

func (f *FaultFS) MkdirAll(dir string) error { return f.Base.MkdirAll(dir) }

func (f *FaultFS) Create(path string) (File, error) {
	if err := f.failIf(OpCreate); err != nil {
		return nil, err
	}
	file, err := f.Base.Create(path)
	if err != nil {
		return nil, err
	}
	f.after(OpCreate)
	return &faultFile{File: file, ff: f}, nil
}

func (f *FaultFS) OpenAppend(path string) (File, error) {
	file, err := f.Base.OpenAppend(path)
	if err != nil {
		return nil, err
	}
	return &faultFile{File: file, ff: f}, nil
}

func (f *FaultFS) Open(path string) (File, error) { return f.Base.Open(path) }

func (f *FaultFS) Rename(oldPath, newPath string) error {
	if err := f.failIf(OpRename); err != nil {
		return err
	}
	if err := f.Base.Rename(oldPath, newPath); err != nil {
		return err
	}
	f.after(OpRename)
	return nil
}

func (f *FaultFS) Remove(path string) error {
	if err := f.failIf(OpRemove); err != nil {
		return err
	}
	if err := f.Base.Remove(path); err != nil {
		return err
	}
	f.after(OpRemove)
	return nil
}

func (f *FaultFS) ReadDir(dir string) ([]string, error) { return f.Base.ReadDir(dir) }

func (f *FaultFS) SyncDir(dir string) error {
	if err := f.failIf(OpSyncDir); err != nil {
		return err
	}
	if err := f.Base.SyncDir(dir); err != nil {
		return err
	}
	f.after(OpSyncDir)
	return nil
}

type faultFile struct {
	File
	ff *FaultFS
}

func (w *faultFile) Write(p []byte) (int, error) {
	if err := w.ff.failIf(OpWrite); err != nil {
		return 0, err
	}
	if w.ff.TornAt > 0 && len(w.ff.Ops)+1 == w.ff.TornAt {
		n, _ := w.File.Write(p[:len(p)/2]) // partial write, then the process dies
		w.ff.Ops = append(w.ff.Ops, OpWrite)
		_ = n
		panic(ErrCrash)
	}
	n, err := w.File.Write(p)
	if err != nil {
		return n, err
	}
	w.ff.after(OpWrite)
	return n, nil
}

func (w *faultFile) Sync() error {
	if err := w.ff.failIf(OpSync); err != nil {
		return err
	}
	if err := w.File.Sync(); err != nil {
		return err
	}
	w.ff.after(OpSync)
	return nil
}

func (w *faultFile) Truncate(size int64) error {
	if err := w.ff.failIf(OpTruncate); err != nil {
		return err
	}
	if err := w.File.Truncate(size); err != nil {
		return err
	}
	w.ff.after(OpTruncate)
	return nil
}

package rootstore

import "io"

// OpKind identifies the kind of storage operation at an injection point.
type OpKind int

const (
	OpWrite OpKind = iota
	OpSync
)

func (k OpKind) String() string {
	if k == OpWrite {
		return "write"
	}
	return "sync"
}

// Op describes one storage operation passing through a SimFS injection
// point. A Hook may mutate it before it is applied:
//   - shorten/replace Data  -> torn write
//   - set Drop              -> lost write / ignored sync (lying drive)
//   - set Defer             -> reordered write (applied by Release*)
type Op struct {
	Kind  OpKind
	File  string
	Off   int64
	Data  []byte
	Drop  bool
	Defer bool
}

// CrashMode selects which crash semantics Crash simulates.
type CrashMode int

const (
	// ProcessCrash models a process death: the OS page cache survives, so
	// every byte ever written remains visible to a reopened store.
	ProcessCrash CrashMode = iota
	// PowerLoss models a machine power cut: only recoverable (hardened)
	// bytes survive; written-but-unsynced bytes are lost.
	PowerLoss
)

// SimFS is an injectable persistence simulator. For every file it tracks
// three distinct byte layers:
//
//	written     - visible to ReadAt right now (OS page cache)
//	synced      - handed to the device by Sync (volatile disk cache)
//	recoverable - survives a PowerLoss crash (actually on the medium)
//
// With the default honest-drive model Sync hardens immediately, so
// synced == recoverable. Set VolatileDiskCache to model a drive whose
// flush only stages data in a volatile cache; Settle then hardens it.
type SimFS struct {
	// Hook is invoked at every write/sync injection point.
	Hook func(*Op)
	// VolatileDiskCache models a drive that acknowledges Sync into a
	// volatile cache; only Settle makes those bytes recoverable.
	VolatileDiskCache bool

	files map[string]*simFile
	queue []*Op
}

func NewSimFS() *SimFS { return &SimFS{files: map[string]*simFile{}} }

type simFile struct {
	written     []byte
	synced      []byte
	recoverable []byte
}

func cloneBytes(b []byte) []byte {
	c := make([]byte, len(b))
	copy(c, b)
	return c
}

func (fs *SimFS) Open(name string) (File, error) {
	sf, ok := fs.files[name]
	if !ok {
		sf = &simFile{}
		fs.files[name] = sf
	}
	return &simHandle{fs: fs, name: name, sf: sf}, nil
}

type simHandle struct {
	fs   *SimFS
	name string
	sf   *simFile
}

func grow(buf []byte, n int64) []byte {
	if int64(len(buf)) >= n {
		return buf
	}
	nb := make([]byte, n)
	copy(nb, buf)
	return nb
}

func (h *simHandle) WriteAt(p []byte, off int64) (int, error) {
	op := &Op{Kind: OpWrite, File: h.name, Off: off, Data: cloneBytes(p)}
	if h.fs.Hook != nil {
		h.fs.Hook(op)
	}
	switch {
	case op.Drop:
		// Lost write: the caller believes it succeeded.
	case op.Defer:
		h.fs.queue = append(h.fs.queue, op)
	default:
		h.fs.applyWrite(h.sf, op.Off, op.Data)
	}
	return len(p), nil
}

func (h *simHandle) Sync() error {
	op := &Op{Kind: OpSync, File: h.name}
	if h.fs.Hook != nil {
		h.fs.Hook(op)
	}
	switch {
	case op.Drop:
		// Lying drive: sync acknowledged, nothing persisted.
	case op.Defer:
		h.fs.queue = append(h.fs.queue, op)
	default:
		h.fs.applySync(h.sf)
	}
	return nil
}

func (h *simHandle) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(h.sf.written)) {
		return 0, io.EOF
	}
	n := copy(p, h.sf.written[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (h *simHandle) Size() (int64, error) { return int64(len(h.sf.written)), nil }
func (h *simHandle) Close() error         { return nil }

func (fs *SimFS) applyWrite(sf *simFile, off int64, data []byte) {
	sf.written = grow(sf.written, off+int64(len(data)))
	copy(sf.written[off:], data)
}

func (fs *SimFS) applySync(sf *simFile) {
	sf.synced = cloneBytes(sf.written)
	if !fs.VolatileDiskCache {
		sf.recoverable = cloneBytes(sf.written)
	}
}

func (fs *SimFS) apply(op *Op) {
	sf := fs.files[op.File]
	if sf == nil {
		return
	}
	if op.Kind == OpWrite {
		fs.applyWrite(sf, op.Off, op.Data)
	} else {
		fs.applySync(sf)
	}
}

// Settle drains the volatile disk cache: synced bytes become recoverable.
func (fs *SimFS) Settle() {
	for _, sf := range fs.files {
		sf.recoverable = cloneBytes(sf.synced)
	}
}

// Crash simulates a crash and clears any deferred (in-flight) operations.
func (fs *SimFS) Crash(mode CrashMode) {
	fs.queue = nil
	if mode == ProcessCrash {
		return // OS buffers survive; written bytes stay visible.
	}
	for _, sf := range fs.files {
		sf.written = cloneBytes(sf.recoverable)
		sf.synced = cloneBytes(sf.recoverable)
	}
}

// Release applies deferred operations in their original order.
func (fs *SimFS) Release() {
	q := fs.queue
	fs.queue = nil
	for _, op := range q {
		fs.apply(op)
	}
}

// ReleaseReversed applies deferred operations in reverse order, modelling
// reordering of writes issued before a sync barrier.
func (fs *SimFS) ReleaseReversed() {
	q := fs.queue
	fs.queue = nil
	for i := len(q) - 1; i >= 0; i-- {
		fs.apply(q[i])
	}
}

// LayerSizes reports the (written, synced, recoverable) byte counts of a
// file, exposing the three durability layers for inspection.
func (fs *SimFS) LayerSizes(name string) (written, synced, recoverable int) {
	sf := fs.files[name]
	if sf == nil {
		return 0, 0, 0
	}
	return len(sf.written), len(sf.synced), len(sf.recoverable)
}

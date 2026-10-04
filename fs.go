// Package manifestmgr implements a crash-recoverable, versioned manifest
// manager on top of a minimal, fault-injectable file adapter.
//
// Scope note: the fault injection in this package simulates operation
// success/failure and process restart only. It does NOT claim to simulate
// real power loss, hardware write reordering, or torn sector writes beyond
// an explicitly truncated log append.
package manifestmgr

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
)

// OpKind identifies the category of a mutating adapter operation so faults
// can be injected per operation class.
type OpKind string

const (
	OpWrite   OpKind = "write"   // create/replace a file's content (not durable)
	OpAppend  OpKind = "append"  // append bytes to a file (not durable)
	OpSync    OpKind = "sync"    // fsync a file
	OpRename  OpKind = "rename"  // atomic rename within the store
	OpSyncDir OpKind = "dirsync" // fsync a directory
	OpRemove  OpKind = "remove"  // delete a file
)

// Adapter is the storage contract the manager relies on. Implementations
// must make Rename atomic and must make bytes persisted by Write/Append
// visible to Read after the call returns (durability is only guaranteed
// after Sync/SyncDir).
type Adapter interface {
	ReadFile(p string) ([]byte, error)
	WriteFile(p string, data []byte) error
	AppendFile(p string, data []byte) error
	SyncFile(p string) error
	Rename(old, new string) error
	SyncDir(dir string) error
	Remove(p string) error
	MkdirAll(dir string) error
	ListDir(dir string) ([]string, error) // sorted entry names (files and dirs)
	Exists(p string) bool
}

// ---------------------------------------------------------------------------
// OSFS: real filesystem adapter.
// ---------------------------------------------------------------------------

// OSFS adapts the local filesystem rooted at Root.
type OSFS struct{ Root string }

func (o OSFS) full(p string) string { return path.Join(o.Root, p) }

func (o OSFS) ReadFile(p string) ([]byte, error)  { return os.ReadFile(o.full(p)) }
func (o OSFS) WriteFile(p string, d []byte) error { return os.WriteFile(o.full(p), d, 0o644) }
func (o OSFS) AppendFile(p string, d []byte) error {
	f, err := os.OpenFile(o.full(p), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(d)
	return err
}
func (o OSFS) SyncFile(p string) error {
	f, err := os.OpenFile(o.full(p), os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func (o OSFS) Rename(old, new string) error { return os.Rename(o.full(old), o.full(new)) }
func (o OSFS) SyncDir(dir string) error {
	d, err := os.Open(o.full(dir))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (o OSFS) Remove(p string) error     { return os.Remove(o.full(p)) }
func (o OSFS) MkdirAll(dir string) error { return os.MkdirAll(o.full(dir), 0o755) }
func (o OSFS) Exists(p string) bool {
	_, err := os.Stat(o.full(p))
	return err == nil
}
func (o OSFS) ListDir(dir string) ([]string, error) {
	ents, err := os.ReadDir(o.full(dir))
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

// ---------------------------------------------------------------------------
// MemFS: in-memory adapter. State survives "process restarts" (a restarted
// manager is just a new Manager over the same MemFS), which makes it ideal
// for crash-recovery tests.
// ---------------------------------------------------------------------------

type MemFS struct {
	mu    sync.Mutex
	dirs  map[string]bool
	files map[string][]byte
}

func NewMemFS() *MemFS {
	return &MemFS{dirs: map[string]bool{".": true}, files: map[string][]byte{}}
}

var errNotDir = errors.New("memfs: not a directory")

func (m *MemFS) MkdirAll(dir string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	parts := strings.Split(path.Clean(dir), "/")
	cur := ""
	for _, p := range parts {
		if p == "." || p == "" {
			continue
		}
		cur = path.Join(cur, p)
		m.dirs[cur] = true
	}
	return nil
}

func (m *MemFS) parentOK(p string) bool {
	dir := path.Dir(p)
	if dir == "" {
		dir = "."
	}
	return m.dirs[dir]
}

func (m *MemFS) ReadFile(p string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.files[p]
	if !ok {
		return nil, fs.ErrNotExist
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out, nil
}

func (m *MemFS) WriteFile(p string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.parentOK(p) {
		return errNotDir
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	m.files[p] = cp
	return nil
}

func (m *MemFS) AppendFile(p string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.parentOK(p) {
		return errNotDir
	}
	m.files[p] = append(m.files[p], data...)
	return nil
}

func (m *MemFS) SyncFile(p string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[p]; !ok {
		return fs.ErrNotExist
	}
	return nil
}

func (m *MemFS) Rename(old, new string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.files[old]
	if !ok {
		return fs.ErrNotExist
	}
	if !m.parentOK(new) {
		return errNotDir
	}
	delete(m.files, old)
	m.files[new] = b
	return nil
}

func (m *MemFS) SyncDir(dir string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.dirs[path.Clean(dir)] && dir != "." {
		return errNotDir
	}
	return nil
}

func (m *MemFS) Remove(p string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[p]; !ok {
		return fs.ErrNotExist
	}
	delete(m.files, p)
	return nil
}

func (m *MemFS) Exists(p string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[p]; ok {
		return true
	}
	return m.dirs[path.Clean(p)]
}

func (m *MemFS) ListDir(dir string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	clean := path.Clean(dir)
	if !m.dirs[clean] {
		return nil, errNotDir
	}
	prefix := clean + "/"
	seen := map[string]bool{}
	var names []string
	collect := func(p string) {
		if !strings.HasPrefix(p, prefix) {
			return
		}
		rest := strings.TrimPrefix(p, prefix)
		first := strings.SplitN(rest, "/", 2)[0]
		if !seen[first] {
			seen[first] = true
			names = append(names, first)
		}
	}
	for p := range m.files {
		collect(p)
	}
	for d := range m.dirs {
		if d != clean {
			collect(d)
		}
	}
	sort.Strings(names)
	return names, nil
}

// ---------------------------------------------------------------------------
// FaultFS: fault-injecting wrapper. It distinguishes write / append / sync /
// rename / dir-sync / remove so tests can fail any class of operation, and
// can simulate a process crash (all subsequent ops fail) plus a torn log
// append (only a byte prefix reaches "disk").
// ---------------------------------------------------------------------------

// ErrCrash is returned by FaultFS after a simulated crash or injected fault.
var ErrCrash = errors.New("simulated fault: operation failed")

// FaultFS wraps an Adapter with programmable failures.
type FaultFS struct {
	Inner Adapter

	mu     sync.Mutex
	Counts map[OpKind]int // per-op-kind call counts (1-based when reported)
	Total  int            // total mutating-op calls

	// Hook, if non-nil, is consulted before every mutating op with the
	// per-kind count and total count; returning non-nil fails the op.
	Hook func(kind OpKind, perKind, total int, path string) error

	// TornAppendOn: when the per-kind count of OpAppend reaches this value
	// (and it is > 0), only TornBytes of the payload are persisted and the
	// op reports failure, simulating a crash in the middle of a log append.
	TornAppendOn int
	TornBytes    int
}

func NewFaultFS(inner Adapter) *FaultFS {
	return &FaultFS{Inner: inner, Counts: map[OpKind]int{}}
}

func (f *FaultFS) gate(kind OpKind, p string) (perKind int, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Counts[kind]++
	f.Total++
	perKind = f.Counts[kind]
	if f.Hook != nil {
		if herr := f.Hook(kind, perKind, f.Total, p); herr != nil {
			return perKind, herr
		}
	}
	return perKind, nil
}

func (f *FaultFS) ReadFile(p string) ([]byte, error) { return f.Inner.ReadFile(p) }
func (f *FaultFS) MkdirAll(dir string) error         { return f.Inner.MkdirAll(dir) }
func (f *FaultFS) ListDir(dir string) ([]string, error) {
	return f.Inner.ListDir(dir)
}
func (f *FaultFS) Exists(p string) bool { return f.Inner.Exists(p) }

func (f *FaultFS) WriteFile(p string, d []byte) error {
	if _, err := f.gate(OpWrite, p); err != nil {
		return err
	}
	return f.Inner.WriteFile(p, d)
}

func (f *FaultFS) AppendFile(p string, d []byte) error {
	perKind, err := f.gate(OpAppend, p)
	if err != nil {
		return err
	}
	f.mu.Lock()
	torn := f.TornAppendOn > 0 && perKind == f.TornAppendOn
	tornBytes := f.TornBytes
	f.mu.Unlock()
	if torn {
		if tornBytes > len(d) {
			tornBytes = len(d)
		}
		if werr := f.Inner.AppendFile(p, d[:tornBytes]); werr != nil {
			return werr
		}
		return fmt.Errorf("%w: torn append after %d/%d bytes", ErrCrash, tornBytes, len(d))
	}
	return f.Inner.AppendFile(p, d)
}

func (f *FaultFS) SyncFile(p string) error {
	if _, err := f.gate(OpSync, p); err != nil {
		return err
	}
	return f.Inner.SyncFile(p)
}

func (f *FaultFS) Rename(old, new string) error {
	if _, err := f.gate(OpRename, new); err != nil {
		return err
	}
	return f.Inner.Rename(old, new)
}

func (f *FaultFS) SyncDir(dir string) error {
	if _, err := f.gate(OpSyncDir, dir); err != nil {
		return err
	}
	return f.Inner.SyncDir(dir)
}

func (f *FaultFS) Remove(p string) error {
	if _, err := f.gate(OpRemove, p); err != nil {
		return err
	}
	return f.Inner.Remove(p)
}

// CrashAfter returns a Hook that simulates a process crash: the first
// `after` mutating ops succeed, every subsequent op fails with ErrCrash.
// Clear the hook (set nil) to simulate the process restarting.
func CrashAfter(after int) func(OpKind, int, int, string) error {
	return func(_ OpKind, _, total int, _ string) error {
		if total > after {
			return ErrCrash
		}
		return nil
	}
}

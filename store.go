// Package overflow stores arbitrarily sized records as chains of
// fixed-size overflow pages inside a single data file.
//
// Update protocol (crash safe):
//  1. allocate fresh pages (from the recycled set or by appending);
//  2. write the new chain and fsync the data file;
//  3. atomically switch the root reference (directory file replace);
//  4. only then recycle the now unreachable old chain.
//
// A failure at any step before the root switch leaves the old record
// fully intact. Pages that become garbage because of a crash are
// reclaimed on the next Open or Collect; recycling never touches a
// page that is still reachable from a live root.
package overflow

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

const (
	// PageSize is the fixed size of every page in the data file.
	PageSize = 256

	superblockMagic = "OVFLSTR1"
	pageMagic       = 0x4F564652 // "OVFR"
	headerSize      = 16         // magic(4) + next(8) + length(4)
)

// CorruptError reports a structurally invalid store: a chain cycle, a
// page referenced more than once, an out-of-range pointer, or a page
// with a bad magic number.
type CorruptError struct {
	Detail string
}

func (e *CorruptError) Error() string {
	return "overflow: corrupt structure: " + e.Detail
}

func corruptf(format string, args ...interface{}) *CorruptError {
	return &CorruptError{Detail: fmt.Sprintf(format, args...)}
}

// Ledger is the result of an independent full traversal of the store:
// a complete page accounting plus a content digest of every record.
type Ledger struct {
	Pages   uint64   // total pages in the file, including the superblock
	Used    uint64   // pages reachable from live record roots
	Free    uint64   // unreachable pages available for recycling
	Records int      // number of live records
	Digest  [32]byte // sha256 over sorted (name, 0x00, content, 0x00) tuples
}

// Store manages one data file plus a sidecar directory file that maps
// record names to chain root page ids. It is safe for concurrent use.
type Store struct {
	mu       sync.Mutex
	f        *os.File
	dataPath string
	dirPath  string
	pages    uint64 // total pages, including the superblock (page 0)
	roots    map[string]uint64
	free     []uint64 // recycled page ids, in-memory only

	// failHook, when non-nil, runs before every physical write and may
	// abort it. Tests use it to inject failures. Never set in production.
	failHook func(op string) error
}

// Open opens (or creates) the store at dataPath. It validates the whole
// structure: any cycle, duplicated page reference, or dangling pointer
// is reported as a *CorruptError. Pages left unreachable by an
// interrupted update are recycled into the free set.
func Open(dataPath string) (*Store, error) {
	f, err := os.OpenFile(dataPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	s := &Store{
		f:        f,
		dataPath: dataPath,
		dirPath:  dataPath + ".dir",
		roots:    make(map[string]uint64),
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if st.Size() == 0 {
		if err := s.writeSuperblock(); err != nil {
			f.Close()
			return nil, err
		}
		s.pages = 1
	} else {
		if st.Size()%PageSize != 0 {
			f.Close()
			return nil, corruptf("file size %d is not a multiple of the page size", st.Size())
		}
		if err := s.readSuperblock(); err != nil {
			f.Close()
			return nil, err
		}
		s.pages = uint64(st.Size()) / PageSize
	}
	if err := s.loadDir(); err != nil {
		f.Close()
		return nil, err
	}
	if err := s.reclaim(); err != nil {
		f.Close()
		return nil, err
	}
	return s, nil
}

// Close flushes and closes the data file.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f == nil {
		return nil
	}
	err := s.f.Sync()
	if cerr := s.f.Close(); err == nil {
		err = cerr
	}
	s.f = nil
	return err
}

// Get returns the content of the named record, or an empty slice if the
// record does not exist or has an empty payload.
func (s *Store) Get(name string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readRecord(s.roots[name])
}

// Put stores data under name, replacing any previous value. The new
// chain is fully written and synced before the root reference is
// switched, so a failure at any point preserves the old record. An
// empty payload removes the record and recycles its chain.
func (s *Store) Put(name string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Validate the old chain up front while the update can still abort.
	oldChain, err := s.walk(s.roots[name])
	if err != nil {
		return err
	}

	cap := PageSize - headerSize
	need := (len(data) + cap - 1) / cap
	oldPages := s.pages

	// Allocate: recycle first, append when the free set is empty.
	alloc := make([]uint64, 0, need)
	for len(alloc) < need {
		if n := len(s.free); n > 0 {
			alloc = append(alloc, s.free[n-1])
			s.free = s.free[:n-1]
		} else {
			alloc = append(alloc, s.pages)
			s.pages++
		}
	}

	// rollback undoes the in-memory allocation; the on-disk root still
	// points at the old chain, so no record data can be lost.
	rollback := func(err error) error {
		for _, p := range alloc {
			if p < oldPages {
				s.free = append(s.free, p)
			}
		}
		if s.pages > oldPages {
			s.f.Truncate(int64(oldPages) * PageSize)
			s.pages = oldPages
		}
		return err
	}

	// Step 1: write the new chain.
	buf := make([]byte, PageSize)
	for i, p := range alloc {
		var next uint64
		if i+1 < len(alloc) {
			next = alloc[i+1]
		}
		lo, hi := i*cap, (i+1)*cap
		if hi > len(data) {
			hi = len(data)
		}
		encodePage(buf, next, data[lo:hi])
		if err := s.writeAt("data", buf, int64(p)*PageSize); err != nil {
			return rollback(err)
		}
	}
	if len(alloc) > 0 {
		if err := s.sync("data"); err != nil {
			return rollback(err)
		}
	}

	// Step 2: atomically switch the root reference.
	newRoots := make(map[string]uint64, len(s.roots)+1)
	for k, v := range s.roots {
		newRoots[k] = v
	}
	if len(data) == 0 {
		delete(newRoots, name)
	} else {
		newRoots[name] = alloc[0]
	}
	if err := s.writeDir(newRoots); err != nil {
		return rollback(err)
	}
	s.roots = newRoots

	// Step 3: the old chain is now unreachable; recycle it.
	s.free = append(s.free, oldChain...)
	return nil
}

// Delete removes the named record and recycles its chain.
func (s *Store) Delete(name string) error {
	return s.Put(name, nil)
}

// Collect rescans the file and recycles every page that is not reachable
// from a live root, returning the number of newly reclaimed pages.
func (s *Store) Collect() (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	before := make(map[uint64]bool, len(s.free))
	for _, p := range s.free {
		before[p] = true
	}
	if err := s.reclaim(); err != nil {
		return 0, err
	}
	var reclaimed uint64
	for _, p := range s.free {
		if !before[p] {
			reclaimed++
		}
	}
	return reclaimed, nil
}

// Verify performs a full traversal independent of the in-memory free
// set and returns the resulting ledger. Structural damage is reported
// as a *CorruptError.
func (s *Store) Verify() (*Ledger, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	used, err := s.reachablePages()
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	for _, name := range sortedKeys(s.roots) {
		data, err := s.readRecord(s.roots[name])
		if err != nil {
			return nil, err
		}
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(data)
		h.Write([]byte{0})
	}
	var digest [32]byte
	copy(digest[:], h.Sum(nil))
	return &Ledger{
		Pages:   s.pages,
		Used:    uint64(len(used)),
		Free:    s.pages - 1 - uint64(len(used)),
		Records: len(s.roots),
		Digest:  digest,
	}, nil
}

// reclaim rebuilds the free set from scratch: every page that is not
// reachable from a live root is recyclable. It also validates the whole
// structure, so it doubles as the corruption check on Open.
func (s *Store) reclaim() error {
	used, err := s.reachablePages()
	if err != nil {
		return err
	}
	s.free = s.free[:0]
	for p := uint64(1); p < s.pages; p++ {
		if !used[p] {
			s.free = append(s.free, p)
		}
	}
	return nil
}

// reachablePages walks every record chain, rejecting cycles and pages
// referenced by more than one chain.
func (s *Store) reachablePages() (map[uint64]bool, error) {
	used := make(map[uint64]bool)
	for _, name := range sortedKeys(s.roots) {
		chain, err := s.walk(s.roots[name])
		if err != nil {
			return nil, err
		}
		for _, p := range chain {
			if used[p] {
				return nil, corruptf("page %d is referenced by more than one record", p)
			}
			used[p] = true
		}
	}
	return used, nil
}

// walk follows the chain starting at root and returns its page ids in
// order. A repeated page id means the chain contains a cycle.
func (s *Store) walk(root uint64) ([]uint64, error) {
	if root == 0 {
		return nil, nil
	}
	seen := make(map[uint64]bool)
	var chain []uint64
	for pid := root; pid != 0; {
		if pid >= s.pages {
			return nil, corruptf("page %d is out of range (store has %d pages)", pid, s.pages)
		}
		if seen[pid] {
			return nil, corruptf("cycle detected at page %d", pid)
		}
		seen[pid] = true
		next, _, err := s.readPage(pid)
		if err != nil {
			return nil, err
		}
		chain = append(chain, pid)
		pid = next
	}
	return chain, nil
}

// readRecord concatenates the payloads of the chain starting at root.
func (s *Store) readRecord(root uint64) ([]byte, error) {
	chain, err := s.walk(root)
	if err != nil {
		return nil, err
	}
	var out []byte
	for _, p := range chain {
		_, payload, err := s.readPage(p)
		if err != nil {
			return nil, err
		}
		out = append(out, payload...)
	}
	return out, nil
}

// readPage reads and validates one overflow page.
func (s *Store) readPage(pid uint64) (next uint64, payload []byte, err error) {
	buf := make([]byte, PageSize)
	if _, err := s.f.ReadAt(buf, int64(pid)*PageSize); err != nil {
		return 0, nil, err
	}
	if binary.LittleEndian.Uint32(buf[0:4]) != pageMagic {
		return 0, nil, corruptf("page %d has a bad magic number", pid)
	}
	next = binary.LittleEndian.Uint64(buf[4:12])
	n := binary.LittleEndian.Uint32(buf[12:16])
	if n > PageSize-headerSize {
		return 0, nil, corruptf("page %d claims %d payload bytes", pid, n)
	}
	return next, buf[headerSize : headerSize+n], nil
}

// encodePage serializes one overflow page into buf.
func encodePage(buf []byte, next uint64, payload []byte) {
	binary.LittleEndian.PutUint32(buf[0:4], pageMagic)
	binary.LittleEndian.PutUint64(buf[4:12], next)
	binary.LittleEndian.PutUint32(buf[12:16], uint32(len(payload)))
	copy(buf[headerSize:], payload)
	for i := headerSize + len(payload); i < len(buf); i++ {
		buf[i] = 0
	}
}

func (s *Store) writeSuperblock() error {
	buf := make([]byte, PageSize)
	copy(buf[0:8], superblockMagic)
	if err := s.writeAt("super", buf, 0); err != nil {
		return err
	}
	return s.sync("super")
}

func (s *Store) readSuperblock() error {
	buf := make([]byte, PageSize)
	if _, err := s.f.ReadAt(buf, 0); err != nil {
		return err
	}
	if string(buf[0:8]) != superblockMagic {
		return corruptf("bad superblock magic")
	}
	return nil
}

// loadDir reads the root-reference directory, if it exists.
func (s *Store) loadDir() error {
	data, err := ioutil.ReadFile(s.dirPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var d struct {
		Roots map[string]uint64 `json:"roots"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return fmt.Errorf("overflow: cannot parse directory: %v", err)
	}
	if d.Roots != nil {
		s.roots = d.Roots
	}
	return nil
}

// writeDir atomically replaces the root-reference directory: write a
// temporary file, fsync it, rename over the old file, fsync the parent.
func (s *Store) writeDir(roots map[string]uint64) error {
	payload, err := json.Marshal(struct {
		Roots map[string]uint64 `json:"roots"`
	}{roots})
	if err != nil {
		return err
	}
	if err := s.hook("dir"); err != nil {
		return err
	}
	tmp := s.dirPath + ".tmp"
	if err := ioutil.WriteFile(tmp, payload, 0644); err != nil {
		return err
	}
	if tf, err := os.OpenFile(tmp, os.O_RDWR, 0644); err == nil {
		tf.Sync()
		tf.Close()
	}
	if err := s.hook("rename"); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, s.dirPath); err != nil {
		return err
	}
	if d, err := os.Open(filepath.Dir(s.dirPath)); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

func (s *Store) writeAt(op string, buf []byte, off int64) error {
	if err := s.hook(op); err != nil {
		return err
	}
	n, err := s.f.WriteAt(buf, off)
	if err == nil && n != len(buf) {
		err = io.ErrShortWrite
	}
	return err
}

func (s *Store) sync(op string) error {
	if err := s.hook(op); err != nil {
		return err
	}
	return s.f.Sync()
}

func (s *Store) hook(op string) error {
	if s.failHook != nil {
		return s.failHook(op)
	}
	return nil
}

func sortedKeys(m map[string]uint64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

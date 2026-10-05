package overflow

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"sort"
	"sync"
)

// Ledger is an independent page accounting produced by Verify.
type Ledger struct {
	Total     uint64 // data pages known to the store (excludes superblock)
	Free      uint64 // pages on the free list
	Reachable uint64 // pages reachable from record roots
	Garbage   uint64 // used but unreachable pages, reclaimable by Reclaim
}

// Store is a copy-on-write overflow page record store.
type Store struct {
	mu        sync.Mutex
	p         pager
	pageSize  int
	pageCount uint64
	free      []uint64
	freeSet   map[uint64]bool
	roots     map[string]uint64
}

// Open opens or creates the store at path. pageSize is only used when
// creating a new store; 0 selects the default.
func Open(path string, pageSize int) (*Store, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if st.Size() == 0 {
		return createStore(f, pageSize)
	}
	return loadStore(f)
}

func createStore(f *os.File, pageSize int) (*Store, error) {
	if pageSize == 0 {
		pageSize = defaultPageSz
	}
	if pageSize < minPageSize {
		f.Close()
		return nil, fmt.Errorf("overflow: page size %d below minimum %d", pageSize, minPageSize)
	}
	s := &Store{
		p:         &filePager{f: f, pageSize: pageSize},
		pageSize:  pageSize,
		pageCount: 1, // page 0 is the superblock
		freeSet:   make(map[uint64]bool),
		roots:     make(map[string]uint64),
	}
	if err := s.commitLocked(); err != nil {
		f.Close()
		return nil, err
	}
	return s, nil
}

func loadStore(f *os.File) (*Store, error) {
	hdr := make([]byte, 12)
	if _, err := f.ReadAt(hdr, 0); err != nil {
		f.Close()
		return nil, err
	}
	if string(hdr[:8]) != sbMagic {
		f.Close()
		return nil, fmt.Errorf("overflow: bad magic %q", hdr[:8])
	}
	pageSize := int(binary.LittleEndian.Uint32(hdr[8:12]))
	if pageSize < minPageSize {
		f.Close()
		return nil, fmt.Errorf("overflow: stored page size %d below minimum %d", pageSize, minPageSize)
	}
	s := &Store{
		p:        &filePager{f: f, pageSize: pageSize},
		pageSize: pageSize,
		freeSet:  make(map[uint64]bool),
		roots:    make(map[string]uint64),
	}
	sb, err := s.p.readPage(sbPage)
	if err != nil {
		f.Close()
		return nil, err
	}
	if err := s.parseSuperblock(sb); err != nil {
		f.Close()
		return nil, err
	}
	// Rebuild the free list by scanning page status bytes. A page is
	// owned by whoever last set it USED, so an interrupted update or
	// reclaim can leak pages but never double-assign them; Reclaim
	// picks the leaked pages up later.
	for id := uint64(1); id < s.pageCount; id++ {
		page, err := s.p.readPage(id)
		if err != nil {
			f.Close()
			return nil, err
		}
		switch page[0] {
		case statusFree:
			s.freeSet[id] = true
			s.free = append(s.free, id)
		case statusUsed:
		default:
			f.Close()
			return nil, &CorruptError{Kind: CorruptBadStatus, Page: id}
		}
	}
	return s, nil
}

// Close syncs and closes the store.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.p.sync(); err != nil {
		s.p.close()
		return err
	}
	return s.p.close()
}

func (s *Store) capacity() int { return s.pageSize - headerSize }

// Put stores payload under name. It writes the full new chain and syncs
// it before switching the root reference, so any failure leaves the old
// record intact. The previous chain becomes unreachable garbage and is
// only deleted by Reclaim.
func (s *Store) Put(name string, payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkName(name); err != nil {
		return err
	}
	cap := s.capacity()
	n := 0
	if len(payload) > 0 {
		n = (len(payload) + cap - 1) / cap
	}
	pages := make([]uint64, 0, n)
	for i := 0; i < n; i++ {
		pages = append(pages, s.allocLocked())
	}
	written := 0
	abort := func(err error) error {
		// Unwritten allocations were never marked USED on disk; return
		// them to the free list. Written pages stay as unreachable
		// garbage for Reclaim.
		for _, id := range pages[written:] {
			s.unallocLocked(id)
		}
		return err
	}
	for i, id := range pages {
		next := uint64(nilPage)
		if i+1 < len(pages) {
			next = pages[i+1]
		}
		lo, hi := i*cap, (i+1)*cap
		if hi > len(payload) {
			hi = len(payload)
		}
		buf := make([]byte, s.pageSize)
		encodeChainPage(buf, next, payload[lo:hi])
		if err := s.p.writePage(id, buf); err != nil {
			return abort(err)
		}
		written++
	}
	if err := s.p.sync(); err != nil {
		return abort(err)
	}
	// Atomic root switch: the superblock is the only place roots live.
	old, existed := s.roots[name]
	if n == 0 {
		s.roots[name] = nilPage
	} else {
		s.roots[name] = pages[0]
	}
	if err := s.commitLocked(); err != nil {
		if existed {
			s.roots[name] = old
		} else {
			delete(s.roots, name)
		}
		return err
	}
	return nil
}

// Get returns the payload stored under name.
func (s *Store) Get(name string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root, ok := s.roots[name]
	if !ok {
		return nil, ErrNotFound
	}
	if root == nilPage {
		return []byte{}, nil
	}
	_, payload, err := s.walkLocked(name, root, nil)
	return payload, err
}

// Delete removes name. Its chain becomes unreachable and is only
// deleted by Reclaim.
func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	root, ok := s.roots[name]
	if !ok {
		return ErrNotFound
	}
	delete(s.roots, name)
	if err := s.commitLocked(); err != nil {
		s.roots[name] = root
		return err
	}
	return nil
}

// Reclaim deletes every used page that is unreachable from all record
// roots. It refuses to free anything if the structure is corrupt.
func (s *Store) Reclaim() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	reachable, _, err := s.markReachableLocked()
	if err != nil {
		return err
	}
	for id := uint64(1); id < s.pageCount; id++ {
		if s.freeSet[id] {
			continue
		}
		if _, ok := reachable[id]; ok {
			continue
		}
		if err := s.p.writePage(id, make([]byte, s.pageSize)); err != nil {
			return err
		}
		s.freeSet[id] = true
		s.free = append(s.free, id)
	}
	return s.p.sync()
}

// Verify independently traverses every record chain and the free list,
// reporting cycles, shared pages and dangling references as corruption.
// It returns the page ledger and a digest of all record contents.
func (s *Store) Verify() (Ledger, [sha256.Size]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var led Ledger
	var digest [sha256.Size]byte
	reachable, payloads, err := s.markReachableLocked()
	if err != nil {
		return led, digest, err
	}
	for id := range s.freeSet {
		page, err := s.p.readPage(id)
		if err != nil {
			return led, digest, err
		}
		if page[0] != statusFree {
			return led, digest, &CorruptError{Kind: CorruptBadStatus, Page: id}
		}
	}
	h := sha256.New()
	for _, name := range s.sortedNamesLocked() {
		h.Write([]byte(name))
		h.Write(payloads[name])
	}
	copy(digest[:], h.Sum(nil))
	led.Total = s.pageCount - 1
	led.Free = uint64(len(s.freeSet))
	led.Reachable = uint64(len(reachable))
	led.Garbage = led.Total - led.Free - led.Reachable
	return led, digest, nil
}

// walkLocked follows the chain at root, detecting cycles. If shared is
// non-nil, a page already present there is reported as a shared-page
// corruption and the page is recorded otherwise.
func (s *Store) walkLocked(record string, root uint64, shared map[uint64]string) (map[uint64]bool, []byte, error) {
	seen := make(map[uint64]bool)
	var payload []byte
	for id := root; id != nilPage; {
		if id >= s.pageCount {
			return seen, nil, &CorruptError{Kind: CorruptOutOfRange, Record: record, Page: id}
		}
		if seen[id] {
			return seen, nil, &CorruptError{Kind: CorruptCycle, Record: record, Page: id}
		}
		if shared != nil {
			if owner, ok := shared[id]; ok && owner != record {
				return seen, nil, &CorruptError{Kind: CorruptSharedPage, Record: record, Page: id}
			}
			shared[id] = record
		}
		seen[id] = true
		page, err := s.p.readPage(id)
		if err != nil {
			return seen, nil, err
		}
		if page[0] == statusFree {
			return seen, nil, &CorruptError{Kind: CorruptFreePageRef, Record: record, Page: id}
		}
		if page[0] != statusUsed {
			return seen, nil, &CorruptError{Kind: CorruptBadStatus, Record: record, Page: id}
		}
		next, chunk := decodeChainPage(page)
		if len(chunk) > s.capacity() {
			return seen, nil, &CorruptError{Kind: CorruptBadLength, Record: record, Page: id}
		}
		payload = append(payload, chunk...)
		id = next
	}
	return seen, payload, nil
}

// markReachableLocked walks every record chain and returns the set of
// reachable pages plus each record's payload.
func (s *Store) markReachableLocked() (map[uint64]string, map[string][]byte, error) {
	reachable := make(map[uint64]string)
	payloads := make(map[string][]byte)
	for _, name := range s.sortedNamesLocked() {
		root := s.roots[name]
		if root == nilPage {
			payloads[name] = []byte{}
			continue
		}
		_, payload, err := s.walkLocked(name, root, reachable)
		if err != nil {
			return nil, nil, err
		}
		payloads[name] = payload
	}
	return reachable, payloads, nil
}

func (s *Store) allocLocked() uint64 {
	if n := len(s.free); n > 0 {
		id := s.free[n-1]
		s.free = s.free[:n-1]
		delete(s.freeSet, id)
		return id
	}
	id := s.pageCount
	s.pageCount++
	return id
}

// unallocLocked returns an allocated-but-never-written page to the free
// list in memory only; its on-disk status is still FREE.
func (s *Store) unallocLocked(id uint64) {
	if id == s.pageCount-1 {
		s.pageCount--
		return
	}
	s.freeSet[id] = true
	s.free = append(s.free, id)
}

func (s *Store) sortedNamesLocked() []string {
	names := make([]string, 0, len(s.roots))
	for name := range s.roots {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (s *Store) checkName(name string) error {
	if name == "" || len(name) > 0xFFFF {
		return fmt.Errorf("overflow: invalid record name length %d", len(name))
	}
	return nil
}

// commitLocked writes the superblock (all record roots) and syncs it.
// This single-page write is the atomic root switch for updates.
func (s *Store) commitLocked() error {
	names := s.sortedNamesLocked()
	buf := make([]byte, 0, s.pageSize)
	buf = append(buf, []byte(sbMagic)...)
	buf = appendU32(buf, uint32(s.pageSize))
	buf = appendU64(buf, s.pageCount)
	buf = appendU32(buf, uint32(len(names)))
	for _, name := range names {
		buf = appendU16(buf, uint16(len(name)))
		buf = append(buf, name...)
		buf = appendU64(buf, s.roots[name])
	}
	buf = appendU32(buf, crc32.ChecksumIEEE(buf))
	if len(buf) > s.pageSize {
		return fmt.Errorf("overflow: superblock of %d bytes exceeds page size %d", len(buf), s.pageSize)
	}
	page := make([]byte, s.pageSize)
	copy(page, buf)
	if err := s.p.writePage(sbPage, page); err != nil {
		return err
	}
	return s.p.sync()
}

func (s *Store) parseSuperblock(page []byte) error {
	if string(page[:8]) != sbMagic {
		return fmt.Errorf("overflow: bad magic %q", page[:8])
	}
	off := 8
	u32 := func() uint32 { v := binary.LittleEndian.Uint32(page[off:]); off += 4; return v }
	u64 := func() uint64 { v := binary.LittleEndian.Uint64(page[off:]); off += 8; return v }
	u16 := func() uint16 { v := binary.LittleEndian.Uint16(page[off:]); off += 2; return v }
	if got := int(u32()); got != s.pageSize {
		return fmt.Errorf("overflow: page size mismatch: superblock %d, file %d", got, s.pageSize)
	}
	s.pageCount = u64()
	n := u32()
	for i := uint32(0); i < n; i++ {
		nl := int(u16())
		name := string(page[off : off+nl])
		off += nl
		s.roots[name] = u64()
	}
	if crc32.ChecksumIEEE(page[:off]) != u32() {
		return &CorruptError{Kind: CorruptChecksum, Page: sbPage}
	}
	return nil
}

func appendU16(b []byte, v uint16) []byte {
	var t [2]byte
	binary.LittleEndian.PutUint16(t[:], v)
	return append(b, t[:]...)
}

func appendU32(b []byte, v uint32) []byte {
	var t [4]byte
	binary.LittleEndian.PutUint32(t[:], v)
	return append(b, t[:]...)
}

func appendU64(b []byte, v uint64) []byte {
	var t [8]byte
	binary.LittleEndian.PutUint64(t[:], v)
	return append(b, t[:]...)
}

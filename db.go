package btree

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// File layout of btree.db:
//
//	[0:4096]     superblock slot 0
//	[4096:8192]  superblock slot 1
//	[8192:]      append-only data region: node pages and node-map segments
//
// The superblock is the commit record. Generation g is written to slot g%2,
// so a torn write of one slot never destroys the previous committed state.
// Recovery picks the valid slot with the highest generation and truncates
// any uncommitted tail garbage.
const (
	sbMagic    uint32 = 0x42535442 // "BTSB" little-endian
	sbVersion  uint32 = 1
	sbSlotSize        = 4096
	sbSlots           = 2
	dataStart         = sbSlotSize * sbSlots

	// DefaultPageSize is used when Options.PageSize is zero.
	DefaultPageSize = 512
	// MinPageSize and MaxPageSize bound Options.PageSize.
	MinPageSize = 64
	MaxPageSize = 4096

	// FileName is the database file created inside the directory given to Open.
	FileName = "btree.db"
)

// superblock is the root of trust for a committed version.
type superblock struct {
	gen      uint64
	pageSize uint32
	numNodes uint32
	rootID   uint32
	mapOff   uint64
	dataEnd  uint64
}

func (sb *superblock) encode() []byte {
	buf := make([]byte, sbSlotSize)
	binary.LittleEndian.PutUint32(buf[0:], sbMagic)
	binary.LittleEndian.PutUint32(buf[4:], sbVersion)
	binary.LittleEndian.PutUint64(buf[8:], sb.gen)
	binary.LittleEndian.PutUint32(buf[16:], sb.pageSize)
	binary.LittleEndian.PutUint32(buf[20:], sb.numNodes)
	binary.LittleEndian.PutUint32(buf[24:], sb.rootID)
	binary.LittleEndian.PutUint64(buf[28:], sb.mapOff)
	binary.LittleEndian.PutUint64(buf[36:], sb.dataEnd)
	crc := crc32.ChecksumIEEE(buf[:44])
	binary.LittleEndian.PutUint32(buf[44:], crc)
	return buf
}

func decodeSuperblock(buf []byte) (*superblock, error) {
	if len(buf) < 48 {
		return nil, ErrCorrupt
	}
	if binary.LittleEndian.Uint32(buf[0:]) != sbMagic {
		return nil, ErrCorrupt
	}
	if binary.LittleEndian.Uint32(buf[4:]) != sbVersion {
		return nil, ErrCorrupt
	}
	crc := crc32.ChecksumIEEE(buf[:44])
	if binary.LittleEndian.Uint32(buf[44:]) != crc {
		return nil, ErrCorrupt
	}
	sb := &superblock{
		gen:      binary.LittleEndian.Uint64(buf[8:]),
		pageSize: binary.LittleEndian.Uint32(buf[16:]),
		numNodes: binary.LittleEndian.Uint32(buf[20:]),
		rootID:   binary.LittleEndian.Uint32(buf[24:]),
		mapOff:   binary.LittleEndian.Uint64(buf[28:]),
		dataEnd:  binary.LittleEndian.Uint64(buf[36:]),
	}
	if sb.pageSize < MinPageSize || sb.pageSize > MaxPageSize {
		return nil, ErrCorrupt
	}
	if sb.rootID == nilID || sb.rootID >= sb.numNodes {
		return nil, ErrCorrupt
	}
	mapLen := uint64(sb.numNodes) * 8
	if sb.mapOff < dataStart || sb.mapOff+mapLen > sb.dataEnd {
		return nil, ErrCorrupt
	}
	return sb, nil
}

// Options configures Open.
type Options struct {
	// PageSize is the B-tree node page size in bytes, used only when creating
	// a new database. Small pages force frequent node splits (useful for
	// testing). Existing databases keep the page size they were created with.
	PageSize int
	// FaultHook, if set, is called at every FaultPoint during a commit.
	// Returning true simulates an instantaneous process crash: the DB handle
	// becomes unusable (ErrCrashed) and the database must be reopened.
	FaultHook func(fp FaultPoint) bool
}

// DB is a single-writer, multi-reader disk B-tree. A crashed DB (after an
// injected fault) is dead and must be reopened.
type DB struct {
	f        *os.File
	path     string
	pageSize int

	mu      sync.RWMutex
	gen     uint64
	rootID  uint32
	m       []uint64 // nodeID -> file offset; entry 0 unused
	dataEnd uint64

	hook    func(FaultPoint) bool
	crashed bool
}

// Open opens (or creates) the database file inside dir. All files live in
// the caller-specified directory.
func Open(dir string, opts *Options) (*DB, error) {
	if opts == nil {
		opts = &Options{}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, FileName)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	d := &DB{f: f, path: path, hook: opts.FaultHook}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if st.Size() == 0 {
		ps := opts.PageSize
		if ps == 0 {
			ps = DefaultPageSize
		}
		if ps < MinPageSize || ps > MaxPageSize {
			f.Close()
			return nil, fmt.Errorf("btree: page size %d out of range [%d, %d]", ps, MinPageSize, MaxPageSize)
		}
		if err := d.init(ps); err != nil {
			f.Close()
			return nil, err
		}
		syncDir(dir)
	} else {
		if err := d.recover(); err != nil {
			f.Close()
			return nil, err
		}
	}
	return d, nil
}

// init formats a brand-new database: an empty root leaf (ID 1), its node
// map, and the first superblock (generation 1, slot 1).
func (d *DB) init(pageSize int) error {
	d.pageSize = pageSize
	root := &node{typ: nodeLeaf, id: 1}
	buf, err := root.encode(pageSize)
	if err != nil {
		return err
	}
	rootOff := uint64(dataStart)
	m := []uint64{0, rootOff}
	mapOff := rootOff + uint64(pageSize)
	mapBuf := encodeMap(m)
	newEnd := mapOff + uint64(len(mapBuf))
	if _, err := d.f.WriteAt(buf, int64(rootOff)); err != nil {
		return err
	}
	if _, err := d.f.WriteAt(mapBuf, int64(mapOff)); err != nil {
		return err
	}
	if err := d.f.Sync(); err != nil {
		return err
	}
	sb := superblock{gen: 1, pageSize: uint32(pageSize), numNodes: 2, rootID: 1, mapOff: mapOff, dataEnd: newEnd}
	if _, err := d.f.WriteAt(sb.encode(), sbSlotSize); err != nil {
		return err
	}
	if err := d.f.Sync(); err != nil {
		return err
	}
	d.gen = 1
	d.rootID = 1
	d.m = m
	d.dataEnd = newEnd
	return nil
}

// recover replays no log: it validates the two superblock slots, adopts the
// newest valid generation, loads its node map, and drops the uncommitted
// tail. Any state between the last two committed generations is atomic.
func (d *DB) recover() error {
	var best *superblock
	for slot := 0; slot < sbSlots; slot++ {
		buf := make([]byte, sbSlotSize)
		if _, err := d.f.ReadAt(buf, int64(slot)*sbSlotSize); err != nil {
			continue
		}
		sb, err := decodeSuperblock(buf)
		if err != nil {
			continue
		}
		if best == nil || sb.gen > best.gen {
			best = sb
		}
	}
	if best == nil {
		return ErrCorrupt
	}
	d.gen = best.gen
	d.pageSize = int(best.pageSize)
	d.rootID = best.rootID
	d.dataEnd = best.dataEnd

	mapBuf := make([]byte, uint64(best.numNodes)*8)
	if _, err := d.f.ReadAt(mapBuf, int64(best.mapOff)); err != nil {
		return err
	}
	d.m = decodeMap(mapBuf)
	for id := uint32(1); id < best.numNodes; id++ {
		off := d.m[id]
		if off < dataStart || off+uint64(d.pageSize) > best.dataEnd {
			return ErrCorrupt
		}
	}
	st, err := d.f.Stat()
	if err != nil {
		return err
	}
	if st.Size() < int64(best.dataEnd) {
		return ErrCorrupt
	}
	if st.Size() > int64(best.dataEnd) {
		// Uncommitted tail from an interrupted commit: discard it.
		if err := d.f.Truncate(int64(best.dataEnd)); err != nil {
			return err
		}
	}
	return nil
}

func encodeMap(m []uint64) []byte {
	buf := make([]byte, len(m)*8)
	for i, off := range m {
		binary.LittleEndian.PutUint64(buf[i*8:], off)
	}
	return buf
}

func decodeMap(buf []byte) []uint64 {
	m := make([]uint64, len(buf)/8)
	for i := range m {
		m[i] = binary.LittleEndian.Uint64(buf[i*8:])
	}
	return m
}

// commit is the only place that mutates on-disk committed state.
//
// Protocol:
//  1. append all dirty node pages at dataEnd (new offsets, old pages intact)
//  2. append the new node map
//  3. fsync data
//  4. write superblock slot (gen+1)%2
//  5. fsync
//
// A crash before step 4 completes leaves the previous generation intact;
// a crash after leaves the new generation fully readable. There is no
// observable intermediate state.
func (d *DB) commit(tx *txn) error {
	ids := make([]uint32, 0, len(tx.pages))
	for id := range tx.pages {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	type write struct {
		buf []byte
		off int64
	}
	writes := make([]write, 0, len(ids))
	off := d.dataEnd
	for _, id := range ids {
		buf, err := tx.pages[id].encode(d.pageSize)
		if err != nil {
			return err
		}
		tx.m[id] = off
		writes = append(writes, write{buf: buf, off: int64(off)})
		off += uint64(d.pageSize)
	}
	mapOff := off
	mapBuf := encodeMap(tx.m)
	newEnd := mapOff + uint64(len(mapBuf))

	for _, w := range writes {
		if _, err := d.f.WriteAt(w.buf, w.off); err != nil {
			return err
		}
		if d.inject(FPAfterDataPage) {
			return ErrCrashed
		}
	}
	if _, err := d.f.WriteAt(mapBuf, int64(mapOff)); err != nil {
		return err
	}
	if d.inject(FPAfterDataWrite) {
		return ErrCrashed
	}
	if err := d.f.Sync(); err != nil {
		return err
	}
	if d.inject(FPAfterDataSync) {
		return ErrCrashed
	}
	sb := superblock{
		gen:      d.gen + 1,
		pageSize: uint32(d.pageSize),
		numNodes: uint32(len(tx.m)),
		rootID:   tx.rootID,
		mapOff:   mapOff,
		dataEnd:  newEnd,
	}
	slot := (d.gen + 1) % sbSlots
	if _, err := d.f.WriteAt(sb.encode(), int64(slot)*sbSlotSize); err != nil {
		return err
	}
	if d.inject(FPAfterSuperWrite) {
		return ErrCrashed
	}
	if err := d.f.Sync(); err != nil {
		return err
	}
	if d.inject(FPAfterSuperSync) {
		return ErrCrashed
	}

	d.gen++
	d.rootID = tx.rootID
	d.m = tx.m
	d.dataEnd = newEnd
	return nil
}

// inject returns true (and kills the handle) when the fault hook fires.
func (d *DB) inject(fp FaultPoint) bool {
	if d.hook != nil && d.hook(fp) {
		d.crashed = true
		return true
	}
	return false
}

// readNode reads and validates one node page through the given node map.
func (d *DB) readNode(id uint32, m []uint64) (*node, error) {
	if id == nilID || int(id) >= len(m) {
		return nil, ErrCorrupt
	}
	buf := make([]byte, d.pageSize)
	if _, err := d.f.ReadAt(buf, int64(m[id])); err != nil {
		return nil, err
	}
	n, err := decodeNode(buf)
	if err != nil {
		return nil, err
	}
	if n.id != id {
		return nil, ErrCorrupt
	}
	return n, nil
}

// MaxKeySize is the largest key that fits in an internal node (which must
// always be able to hold at least two keys after a split).
func (d *DB) MaxKeySize() int {
	usable := d.pageSize - nodeHdrSize - nodeCRCSize
	return (usable-4)/2 - 6
}

// MaxValueSize is the largest value that fits in a leaf next to a key of
// length keyLen.
func (d *DB) MaxValueSize(keyLen int) int {
	return d.pageSize - nodeHdrSize - nodeCRCSize - 6 - keyLen
}

// Gen returns the current committed generation (commit sequence number).
func (d *DB) Gen() uint64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.gen
}

// Path returns the database file path.
func (d *DB) Path() string { return d.path }

// PageSize returns the database page size.
func (d *DB) PageSize() int { return d.pageSize }

// Close closes the database file. It is safe (and a no-op state-wise) on a
// crashed handle.
func (d *DB) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.f == nil {
		return nil
	}
	err := d.f.Close()
	d.f = nil
	return err
}

func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}

package sparseidx

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	defaultStride     = 16
	defaultMaxRecSize = 1 << 20 // 1 MiB decode budget per record
)

// Options configures a Store.
type Options struct {
	// Stride is the sparse-index interval: locating the k-th record decodes
	// at most Stride records. Defaults to 16.
	Stride int
	// MaxRecordBytes bounds the payload (key+value) the decoder will accept
	// or allocate for a single record. Defaults to 1 MiB.
	MaxRecordBytes int
	// AccessHook, if set, is called with a block ID every time a read path
	// touches that block. Used to audit which blocks a snapshot reads.
	AccessHook func(blockID uint64)
}

// Store is an append-only, block-immutable record store.
type Store struct {
	dir            string
	stride         int
	maxRecordBytes int
	accessHook     func(uint64)

	blocks   []*blockInfo          // manifest order
	byID     map[uint64]*blockInfo // all loaded blocks, incl. pinned old ones
	pos      map[uint64]int        // block ID -> position in blocks
	idx      *sparseIndex
	pending  []Record
	live     map[string][]byte
	nextID   uint64
	snaps    map[uint64]*Snapshot
	nextSnap uint64

	lastSeekDecoded int

	// RecoveredBlocks counts blocks that failed strict validation during
	// Open and were recovered by keeping only their valid record prefix.
	RecoveredBlocks int
}

// Open opens or creates a store in dir and runs crash recovery:
//   - the manifest is the only source of truth for live blocks; orphan
//     blocks (published but never committed) are ignored and collectable;
//   - a block that fails strict validation is recovered by keeping only its
//     valid record prefix, so a half-written record is never accepted;
//   - a missing or corrupt index is rebuilt from the blocks.
func Open(dir string, opts *Options) (*Store, error) {
	if opts == nil {
		opts = &Options{}
	}
	s := &Store{
		dir:            dir,
		stride:         cmpOr(opts.Stride, defaultStride),
		maxRecordBytes: cmpOr(opts.MaxRecordBytes, defaultMaxRecSize),
		accessHook:     opts.AccessHook,
		byID:           map[uint64]*blockInfo{},
		pos:            map[uint64]int{},
		live:           map[string][]byte{},
		snaps:          map[uint64]*Snapshot{},
		nextID:         1,
	}
	if err := os.MkdirAll(filepath.Join(dir, "blocks"), 0o755); err != nil {
		return nil, err
	}
	ids, err := s.loadManifest()
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		data, err := os.ReadFile(blockPath(dir, id))
		if err != nil {
			return nil, fmt.Errorf("manifest block %d unreadable: %w", id, err)
		}
		b, err := readBlockStrict(data, s.maxRecordBytes)
		if err != nil {
			b, err = scanBlockPrefix(data, s.maxRecordBytes)
			if err != nil {
				return nil, fmt.Errorf("block %d unrecoverable: %w", id, err)
			}
			s.RecoveredBlocks++
		}
		s.blocks = append(s.blocks, b)
		s.byID[b.id] = b
		if b.id >= s.nextID {
			s.nextID = b.id + 1
		}
	}
	s.recomputePositions()
	if err := s.loadOrRebuildIndex(); err != nil {
		return nil, err
	}
	return s, s.replay(s.blocks, s.live)
}

func cmpOr(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

func (s *Store) recomputePositions() {
	s.pos = map[uint64]int{}
	for i, b := range s.blocks {
		s.pos[b.id] = i
	}
}

// loadManifest reads the committed manifest; if it is missing or corrupt
// (e.g. crash before first commit) it falls back to scanning the blocks dir.
func (s *Store) loadManifest() ([]uint64, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, "MANIFEST"))
	if err == nil {
		if ids, err := parseManifest(data); err == nil {
			return ids, nil
		}
	}
	entries, err := os.ReadDir(filepath.Join(s.dir, "blocks"))
	if err != nil {
		return nil, err
	}
	var ids []uint64
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".blk") {
			continue // stray tmp files from a crashed publish are ignored
		}
		id, err := strconv.ParseUint(strings.TrimSuffix(name, ".blk"), 16, 64)
		if err != nil {
			continue
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

// loadOrRebuildIndex loads the published index, or rebuilds and republishes
// it from the blocks when it is missing or structurally corrupt.
func (s *Store) loadOrRebuildIndex() error {
	if data, err := os.ReadFile(filepath.Join(s.dir, "index.idx")); err == nil {
		if idx, err := parseIndex(data); err == nil && idx.total == s.countRecords() {
			s.idx = idx
			return nil
		}
	}
	s.idx = buildIndex(s.blocks, s.stride)
	return s.publishIndex()
}

func (s *Store) countRecords() uint64 {
	var n uint64
	for _, b := range s.blocks {
		n += uint64(len(b.offsets))
	}
	return n
}

func (s *Store) publishIndex() error {
	return writeFileAtomic(filepath.Join(s.dir, "index.idx"), s.idx.marshal())
}

// publishManifest atomically commits the current block list. It is the last
// step of every mutation: data blocks and the index are already durable, so
// a crash either sees the old manifest or the new one, never a mixture.
func (s *Store) publishManifest() error {
	ids := make([]uint64, 0, len(s.blocks))
	for _, b := range s.blocks {
		ids = append(ids, b.id)
	}
	return writeFileAtomic(filepath.Join(s.dir, "MANIFEST"), marshalManifest(ids))
}

// replay folds all records of the given blocks into the live view.
func (s *Store) replay(blocks []*blockInfo, live map[string][]byte) error {
	for _, b := range blocks {
		for _, off := range b.offsets {
			rec, _, err := decodeRecordAt(b.data, off, s.maxRecordBytes)
			if err != nil {
				return err
			}
			if err := applyRecord(live, rec); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) checkSize(key, value []byte) error {
	if len(key) > 0xffff {
		return fmt.Errorf("key too long: %w", ErrTooLarge)
	}
	if len(key)+len(value) > s.maxRecordBytes {
		return ErrTooLarge
	}
	return nil
}

// Put appends a full key/value record.
func (s *Store) Put(key, value []byte) error {
	if err := s.checkSize(key, value); err != nil {
		return err
	}
	s.pending = append(s.pending, Record{Flags: FlagPut, Key: key, Value: value})
	return nil
}

// Delete appends a tombstone for key.
func (s *Store) Delete(key []byte) error {
	if err := s.checkSize(key, nil); err != nil {
		return err
	}
	s.pending = append(s.pending, Record{Flags: FlagTombstone, Key: key})
	return nil
}

// Patch appends a partial replacement: the current value's bytes
// [offset, offset+len(data)) are replaced by data.
func (s *Store) Patch(key []byte, offset uint64, data []byte) error {
	if err := s.checkSize(key, data); err != nil {
		return err
	}
	s.pending = append(s.pending, Record{Flags: FlagPatch, Key: key, Value: encodePatch(offset, data)})
	return nil
}

// Flush writes pending records as a new immutable block and atomically
// publishes block + index + manifest. Existing blocks are never rewritten.
func (s *Store) Flush() error {
	if len(s.pending) == 0 {
		return nil
	}
	b, err := writeBlockFile(s.dir, s.nextID, s.pending)
	if err != nil {
		return err
	}
	s.nextID++
	s.blocks = append(s.blocks, b)
	s.byID[b.id] = b
	s.recomputePositions()
	for _, rec := range s.pending {
		if err := applyRecord(s.live, rec); err != nil {
			return err
		}
	}
	s.pending = nil
	s.idx = buildIndex(s.blocks, s.stride)
	if err := s.publishIndex(); err != nil {
		return err
	}
	return s.publishManifest()
}

// NumRecords returns the total number of committed records.
func (s *Store) NumRecords() uint64 { return s.idx.total }

// LastSeekDecoded reports how many records the last Kth call decoded;
// it is always <= Stride.
func (s *Store) LastSeekDecoded() int { return s.lastSeekDecoded }

// Get returns the current value of key. found is false for keys that were
// never written or were tombstoned; an empty value with found=true is a
// genuinely empty record, not a missing one.
func (s *Store) Get(key []byte) (value []byte, found bool) {
	v, ok := s.live[string(key)]
	return v, ok
}

// Kth returns the k-th committed record (0-based, manifest order). It seeks
// via the sparse index, validates that the indexed offset is a real record
// boundary (checksum-verified decode) instead of trusting it, and then
// decodes forward at most Stride records, crossing block boundaries when
// needed.
func (s *Store) Kth(k uint64) (Record, error) {
	if k >= s.idx.total {
		return Record{}, ErrNotFound
	}
	e := s.idx.floor(k)
	blockPos, ok := s.pos[e.blockID]
	if !ok {
		return Record{}, fmt.Errorf("index references unknown block %d: %w", e.blockID, ErrCorrupt)
	}
	off := int(e.offset)
	idx := e.recordIndex
	decoded := 0
	for {
		b := s.blocks[blockPos]
		if s.accessHook != nil {
			s.accessHook(b.id)
		}
		if off < blockHeaderSize || off >= b.dataEnd {
			return Record{}, fmt.Errorf("index offset %d out of bounds in block %d: %w", off, b.id, ErrCorrupt)
		}
		for off < b.dataEnd {
			rec, next, err := decodeRecordAt(b.data, off, s.maxRecordBytes)
			if err != nil {
				return Record{}, fmt.Errorf("index offset %d is not a record boundary in block %d: %w", off, b.id, ErrCorrupt)
			}
			decoded++
			if idx == k {
				s.lastSeekDecoded = decoded
				return rec, nil
			}
			idx++
			off = next
		}
		blockPos++
		if blockPos >= len(s.blocks) {
			return Record{}, fmt.Errorf("record %d beyond last block: %w", k, ErrCorrupt)
		}
		off = blockHeaderSize
	}
}

// KthSlow returns the k-th record by decoding every record from the start.
// It is the ground truth used to cross-check Kth.
func (s *Store) KthSlow(k uint64) (Record, error) {
	if k >= s.idx.total {
		return Record{}, ErrNotFound
	}
	var i uint64
	for _, b := range s.blocks {
		if s.accessHook != nil {
			s.accessHook(b.id)
		}
		for _, off := range b.offsets {
			rec, _, err := decodeRecordAt(b.data, off, s.maxRecordBytes)
			if err != nil {
				return Record{}, err
			}
			if i == k {
				return rec, nil
			}
			i++
		}
	}
	return Record{}, ErrCorrupt
}

// Compact rewrites the live key/value view as a single fresh block and
// atomically republishes. Old blocks stay on disk, pinned by open snapshots,
// until CollectGarbage reclaims them.
func (s *Store) Compact() error {
	if err := s.Flush(); err != nil {
		return err
	}
	keys := make([]string, 0, len(s.live))
	for k := range s.live {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	records := make([]Record, 0, len(keys))
	for _, k := range keys {
		records = append(records, Record{Flags: FlagPut, Key: []byte(k), Value: s.live[k]})
	}
	b, err := writeBlockFile(s.dir, s.nextID, records)
	if err != nil {
		return err
	}
	s.nextID++
	s.blocks = []*blockInfo{b}
	s.byID[b.id] = b
	s.recomputePositions()
	s.idx = buildIndex(s.blocks, s.stride)
	if err := s.publishIndex(); err != nil {
		return err
	}
	return s.publishManifest()
}

// CollectGarbage deletes block files that are neither committed in the
// manifest nor pinned by an open snapshot, plus stray tmp files. A block
// pinned by a snapshot is never reclaimed, so old snapshots can never
// observe a reclaimed block.
func (s *Store) CollectGarbage() error {
	pinned := map[uint64]bool{}
	for _, b := range s.blocks {
		pinned[b.id] = true
	}
	for _, sn := range s.snaps {
		for _, id := range sn.blocks {
			pinned[id] = true
		}
	}
	entries, err := os.ReadDir(filepath.Join(s.dir, "blocks"))
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".tmp") {
			os.Remove(filepath.Join(s.dir, "blocks", name))
			continue
		}
		if !strings.HasSuffix(name, ".blk") {
			continue
		}
		id, err := strconv.ParseUint(strings.TrimSuffix(name, ".blk"), 16, 64)
		if err != nil {
			continue
		}
		if pinned[id] {
			continue
		}
		if err := os.Remove(blockPath(s.dir, id)); err != nil {
			return err
		}
		delete(s.byID, id)
	}
	return nil
}

// Snapshot is a consistent read view over the blocks committed when it was
// taken. Its blocks are pinned against garbage collection until Close.
type Snapshot struct {
	store  *Store
	id     uint64
	blocks []uint64
	closed bool
}

// Snapshot captures the current committed block list and pins it.
func (s *Store) Snapshot() *Snapshot {
	if err := s.Flush(); err != nil {
		return nil
	}
	sn := &Snapshot{store: s, id: s.nextSnap}
	s.nextSnap++
	for _, b := range s.blocks {
		sn.blocks = append(sn.blocks, b.id)
	}
	s.snaps[sn.id] = sn
	return sn
}

// Get replays only the snapshot's own pinned blocks, so it observes exactly
// the committed state at snapshot time and never touches reclaimed blocks.
func (sn *Snapshot) Get(key []byte) (value []byte, found bool, err error) {
	if sn.closed {
		return nil, false, fmt.Errorf("snapshot closed")
	}
	live := map[string][]byte{}
	for _, id := range sn.blocks {
		b, ok := sn.store.byID[id]
		if !ok {
			return nil, false, fmt.Errorf("pinned block %d missing: %w", id, ErrCorrupt)
		}
		if sn.store.accessHook != nil {
			sn.store.accessHook(id)
		}
		for _, off := range b.offsets {
			rec, _, err := decodeRecordAt(b.data, off, sn.store.maxRecordBytes)
			if err != nil {
				return nil, false, err
			}
			if err := applyRecord(live, rec); err != nil {
				return nil, false, err
			}
		}
	}
	v, ok := live[string(key)]
	return v, ok, nil
}

// Close releases the snapshot's pins, allowing its blocks to be reclaimed.
func (sn *Snapshot) Close() {
	if !sn.closed {
		delete(sn.store.snaps, sn.id)
		sn.closed = true
	}
}

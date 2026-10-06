package keydir

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"sort"
	"strconv"
	"strings"
)

var (
	// ErrNotFound means the key is absent from the directory.
	ErrNotFound = errors.New("keydir: key not found")
	// ErrStaleDir means a directory entry pointed at bytes that are not
	// the expected record (wrong file, torn data, or key/version
	// mismatch). Stale entries must never leak wrong data to callers.
	ErrStaleDir = errors.New("keydir: stale directory entry")
)

// dirEntry locates one live record in a data file.
type dirEntry struct {
	fileID  uint32
	offset  int64
	size    int
	version uint64
}

// Store is an append-only log with an in-memory key directory.
// The data log is the source of truth; the persisted directory file is
// only a checkpoint that speeds up recovery.
type Store struct {
	fs       FS
	dir      map[string]dirEntry
	files    map[uint32]File
	activeID uint32
	nextID   uint32
	version  uint64
}

const dirFileName = "keydir.dir"
const dirTmpName = "keydir.tmp"

func dataFileName(id uint32) string { return fmt.Sprintf("%06d.data", id) }

func parseDataFileID(name string) (uint32, bool) {
	if !strings.HasSuffix(name, ".data") {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimSuffix(name, ".data"), 10, 32)
	if err != nil {
		return 0, false
	}
	return uint32(n), true
}

// Open opens (or initializes) a store. It loads the directory checkpoint
// if it is intact, then replays the data log past the checkpoint
// watermark. A missing or corrupt directory falls back to a full log
// scan: the log alone decides what is live.
func Open(fs FS) (*Store, error) {
	s := &Store{fs: fs, dir: make(map[string]dirEntry), files: make(map[uint32]File)}
	names, err := fs.List()
	if err != nil {
		return nil, err
	}
	var ids []uint32
	var haveDirFile bool
	for _, name := range names {
		if name == dirFileName {
			haveDirFile = true
			continue
		}
		if id, ok := parseDataFileID(name); ok {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	// Checkpointed directory state, valid only up to the watermark.
	var wmFile uint32
	var wmOffset int64
	if haveDirFile {
		if cp, err := s.loadDirFile(); err == nil {
			s.dir = cp.entries
			s.version = cp.maxVersion
			wmFile, wmOffset = cp.fileID, cp.offset
		}
		// Corrupt checkpoint: ignore it and rebuild from the log.
	}

	var maxID uint32
	for _, id := range ids {
		f, err := fs.Open(dataFileName(id))
		if err != nil {
			return nil, err
		}
		s.files[id] = f
		maxID = id
		start := int64(0)
		if id == wmFile {
			start = wmOffset
		} else if id < wmFile {
			continue // fully covered by the checkpoint
		}
		end, err := s.replayFile(id, f, start)
		if err != nil {
			return nil, err
		}
		if id == maxID {
			// Drop a torn tail so future appends stay reachable.
			if err := f.Truncate(end); err != nil {
				return nil, err
			}
		}
	}

	if len(ids) == 0 {
		s.nextID = 1
	} else {
		s.nextID = maxID + 1
	}
	if len(ids) == 0 {
		f, err := fs.Create(dataFileName(s.nextID))
		if err != nil {
			return nil, err
		}
		s.files[s.nextID] = f
		s.activeID = s.nextID
		s.nextID++
	} else {
		s.activeID = maxID
	}
	return s, nil
}

// replayFile scans one data file from start, applying every intact record
// and stopping silently at the first torn or corrupt tail record.
// It returns the offset just past the last intact record.
func (s *Store) replayFile(id uint32, f File, start int64) (int64, error) {
	size, err := f.Size()
	if err != nil {
		return 0, err
	}
	off := start
	for off+recordHeaderSize <= size {
		hdr := make([]byte, recordHeaderSize)
		if err := readFullAt(f, hdr, off); err != nil {
			break // torn header: incomplete tail, ignore it
		}
		_, _, _, total, err := decodeRecordHeader(hdr)
		if err != nil {
			break
		}
		if off+int64(total) > size {
			break // torn payload: incomplete tail, ignore it
		}
		buf := make([]byte, total)
		if err := readFullAt(f, buf, off); err != nil {
			break
		}
		rec, err := decodeRecord(buf)
		if err != nil {
			break // CRC mismatch: treat as incomplete tail
		}
		s.apply(id, off, total, rec)
		off += int64(total)
	}
	return off, nil
}

// apply folds one replayed record into the directory. Versions order
// writes globally, so a record only wins if it is at least as new as
// what the directory already holds.
func (s *Store) apply(fileID uint32, off int64, size int, rec record) {
	if cur, ok := s.dir[rec.key]; !ok || rec.version >= cur.version {
		s.dir[rec.key] = dirEntry{fileID: fileID, offset: off, size: size, version: rec.version}
	}
	if rec.version > s.version {
		s.version = rec.version
	}
}

// Put appends the record to the data log and syncs it before touching
// the in-memory directory. A crash between the two steps loses only the
// directory update, which recovery rebuilds from the log.
func (s *Store) Put(key string, value []byte) error {
	s.version++
	rec := record{version: s.version, key: key, value: value}
	buf, err := encodeRecord(rec)
	if err != nil {
		return err
	}
	active := s.files[s.activeID]
	off, err := active.Size()
	if err != nil {
		return err
	}
	if _, err := active.Append(buf); err != nil {
		return err
	}
	if err := active.Sync(); err != nil {
		return err
	}
	s.dir[key] = dirEntry{fileID: s.activeID, offset: off, size: len(buf), version: rec.version}
	return nil
}

// Get resolves key through the directory and then validates that the
// record at that location really carries this key and version. A stale
// offset (e.g. from before compaction) is reported, never misread.
func (s *Store) Get(key string) ([]byte, error) {
	e, ok := s.dir[key]
	if !ok {
		return nil, ErrNotFound
	}
	return s.readEntry(key, e)
}

// readEntry fetches and validates the record a directory entry points to.
func (s *Store) readEntry(key string, e dirEntry) ([]byte, error) {
	f, ok := s.files[e.fileID]
	if !ok {
		return nil, ErrStaleDir // file is gone (e.g. reclaimed by compaction)
	}
	buf := make([]byte, e.size)
	if err := readFullAt(f, buf, e.offset); err != nil {
		return nil, ErrStaleDir
	}
	rec, err := decodeRecord(buf)
	if err != nil {
		return nil, ErrStaleDir
	}
	if rec.key != key || rec.version != e.version {
		return nil, ErrStaleDir
	}
	return rec.value, nil
}

// Checkpoint persists the in-memory directory atomically (tmp + rename)
// together with the log position it covers.
func (s *Store) Checkpoint() error {
	active := s.files[s.activeID]
	off, err := active.Size()
	if err != nil {
		return err
	}
	cp := checkpoint{fileID: s.activeID, offset: off, maxVersion: s.version, entries: s.dir}
	buf := encodeCheckpoint(cp)
	tmp, err := s.fs.Create(dirTmpName)
	if err != nil {
		return err
	}
	if _, err := tmp.Append(buf); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return s.fs.Rename(dirTmpName, dirFileName)
}

// Compact rewrites every live record into a fresh data file, switches
// the directory to it in one shot, and reclaims the old files. Records
// keep their original versions so log ordering is preserved. Old
// directory entries become dangling and are rejected on read.
func (s *Store) Compact() error {
	newID := s.nextID
	s.nextID++
	f, err := s.fs.Create(dataFileName(newID))
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(s.dir))
	for k := range s.dir {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	newDir := make(map[string]dirEntry, len(s.dir))
	off := int64(0)
	for _, k := range keys {
		e := s.dir[k]
		src, ok := s.files[e.fileID]
		if !ok {
			return ErrStaleDir
		}
		buf := make([]byte, e.size)
		if err := readFullAt(src, buf, e.offset); err != nil {
			return err
		}
		rec, err := decodeRecord(buf)
		if err != nil {
			return err
		}
		if rec.key != k || rec.version != e.version {
			return ErrStaleDir
		}
		if _, err := f.Append(buf); err != nil { // copy bytes verbatim
			return err
		}
		newDir[k] = dirEntry{fileID: newID, offset: off, size: e.size, version: e.version}
		off += int64(e.size)
	}
	if err := f.Sync(); err != nil {
		return err
	}
	// Switch over, then reclaim.
	old := s.files
	s.files = map[uint32]File{newID: f}
	s.activeID = newID
	s.dir = newDir
	for id, of := range old {
		of.Close()
		if err := s.fs.Remove(dataFileName(id)); err != nil {
			return err
		}
	}
	return s.Checkpoint()
}

// Close checkpoints the directory and closes all files.
func (s *Store) Close() error {
	if err := s.Checkpoint(); err != nil {
		return err
	}
	for _, f := range s.files {
		f.Close()
	}
	return nil
}

func readFullAt(f File, buf []byte, off int64) error {
	for len(buf) > 0 {
		n, err := f.ReadAt(buf, off)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrUnexpectedEOF
		}
		buf = buf[n:]
		off += int64(n)
	}
	return nil
}

// ---- directory checkpoint file ----

// Layout: magic(4) | fileID(4) | offset(8) | maxVersion(8) | count(4) |
// entries... | crc32(4). CRC covers everything before it.
// Entry: keyLen(2) key | version(8) | fileID(4) | offset(8) | size(4).

var dirMagic = []byte("KDIR")

const dirFixedHeader = 4 + 4 + 8 + 8 + 4

type checkpoint struct {
	fileID     uint32
	offset     int64
	maxVersion uint64
	entries    map[string]dirEntry
}

func encodeCheckpoint(cp checkpoint) []byte {
	keys := make([]string, 0, len(cp.entries))
	for k := range cp.entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	size := dirFixedHeader + 4
	for _, k := range keys {
		size += 2 + len(k) + 8 + 4 + 8 + 4
	}
	buf := make([]byte, size)
	copy(buf[0:4], dirMagic)
	binary.LittleEndian.PutUint32(buf[4:8], cp.fileID)
	binary.LittleEndian.PutUint64(buf[8:16], uint64(cp.offset))
	binary.LittleEndian.PutUint64(buf[16:24], cp.maxVersion)
	binary.LittleEndian.PutUint32(buf[24:28], uint32(len(keys)))
	p := 28
	for _, k := range keys {
		e := cp.entries[k]
		binary.LittleEndian.PutUint16(buf[p:p+2], uint16(len(k)))
		p += 2
		copy(buf[p:p+len(k)], k)
		p += len(k)
		binary.LittleEndian.PutUint64(buf[p:p+8], e.version)
		p += 8
		binary.LittleEndian.PutUint32(buf[p:p+4], e.fileID)
		p += 4
		binary.LittleEndian.PutUint64(buf[p:p+8], uint64(e.offset))
		p += 8
		binary.LittleEndian.PutUint32(buf[p:p+4], uint32(e.size))
		p += 4
	}
	binary.LittleEndian.PutUint32(buf[p:p+4], crc32.ChecksumIEEE(buf[:p]))
	return buf
}

var ErrCorruptDir = errors.New("keydir: corrupt directory checkpoint")

func decodeCheckpoint(buf []byte) (checkpoint, error) {
	if len(buf) < dirFixedHeader+4 || string(buf[0:4]) != string(dirMagic) {
		return checkpoint{}, ErrCorruptDir
	}
	if crc32.ChecksumIEEE(buf[:len(buf)-4]) != binary.LittleEndian.Uint32(buf[len(buf)-4:]) {
		return checkpoint{}, ErrCorruptDir
	}
	cp := checkpoint{
		fileID:     binary.LittleEndian.Uint32(buf[4:8]),
		offset:     int64(binary.LittleEndian.Uint64(buf[8:16])),
		maxVersion: binary.LittleEndian.Uint64(buf[16:24]),
		entries:    make(map[string]dirEntry),
	}
	count := int(binary.LittleEndian.Uint32(buf[24:28]))
	p := 28
	for i := 0; i < count; i++ {
		if p+2 > len(buf)-4 {
			return checkpoint{}, ErrCorruptDir
		}
		klen := int(binary.LittleEndian.Uint16(buf[p : p+2]))
		p += 2
		if p+klen+24 > len(buf)-4 {
			return checkpoint{}, ErrCorruptDir
		}
		key := string(buf[p : p+klen])
		p += klen
		e := dirEntry{
			version: binary.LittleEndian.Uint64(buf[p : p+8]),
			fileID:  binary.LittleEndian.Uint32(buf[p+8 : p+12]),
			offset:  int64(binary.LittleEndian.Uint64(buf[p+12 : p+20])),
			size:    int(binary.LittleEndian.Uint32(buf[p+20 : p+24])),
		}
		p += 24
		cp.entries[key] = e
	}
	if p != len(buf)-4 {
		return checkpoint{}, ErrCorruptDir
	}
	return cp, nil
}

func (s *Store) loadDirFile() (checkpoint, error) {
	f, err := s.fs.Open(dirFileName)
	if err != nil {
		return checkpoint{}, err
	}
	defer f.Close()
	size, err := f.Size()
	if err != nil {
		return checkpoint{}, err
	}
	buf := make([]byte, size)
	if err := readFullAt(f, buf, 0); err != nil {
		return checkpoint{}, err
	}
	return decodeCheckpoint(buf)
}

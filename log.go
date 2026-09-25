// Package applog implements an append-only log with multi-consumer ack
// positions and a consumer-driven retention watermark.
//
// On-disk layout (all files live in the caller-specified directory):
//
//	<dir>/<base:020d>.log   segment files, records: [crc32][len32][payload]
//	<dir>/MANIFEST          JSON: consumer cursor positions, atomically replaced
//
// Durability & consistency contract:
//
//   - Append returns only after the record is written and the segment file is
//     fsynced. A failed Append leaves no trace (unflushed bytes are truncated).
//   - Cursor updates (Ack/AckBatch/RegisterConsumer/RemoveConsumer) return only
//     after the new MANIFEST is written, fsynced, atomically renamed and the
//     directory is fsynced. Batch updates are all-or-nothing.
//   - Ack positions are monotonic and bounded by the high watermark (next
//     append offset); re-acking the current position is an idempotent no-op,
//     so a retry after a lost response never moves the position backwards.
//   - Reclaim deletes only sealed segments entirely below the retention
//     watermark (min over consumer positions). Segment files are the source of
//     truth and are rediscovered by directory scan on Open, so a crash
//     mid-reclaim cannot corrupt state.
//   - Recovery scans segments in order, verifies per-record CRCs, truncates a
//     torn tail and drops any segments after a continuity gap.
package applog

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const (
	headerSize     = 8 // crc32(payload) uint32 + payload length uint32
	segmentExt     = ".log"
	manifestName   = "MANIFEST"
	manifestTmp    = manifestName + ".tmp"
	maxRecordSize  = 64 << 20 // 64 MiB sanity bound while scanning
	defaultSegMax  = 1024
	filePermission = 0o644
)

// Options configures a Log. The zero value is valid.
type Options struct {
	// SegmentMaxRecords is the number of records after which the active
	// segment is sealed and a new one started. Defaults to 1024.
	SegmentMaxRecords uint64
	// Hooks are fault-injection points, see the Hooks doc.
	Hooks Hooks
}

type segment struct {
	base  uint64 // offset of the first record in this segment
	path  string
	f     *os.File
	index []int64 // file offset of each record, index[i] belongs to base+i
}

func (s *segment) count() uint64 { return uint64(len(s.index)) }
func (s *segment) end() uint64   { return s.base + s.count() } // next offset after last record

// Log is an append-only log with consumer cursors. It is safe for concurrent
// use; all operations are serialized by a single mutex.
type Log struct {
	dir           string
	segMaxRecords uint64
	hooks         Hooks

	mu         sync.Mutex
	segments   []*segment // ordered by base; the last one is active
	nextOffset uint64     // high watermark: offset the next Append will assign
	consumers  map[string]uint64
	closed     bool
}

// Open opens (creating if necessary) the log stored in dir and runs crash
// recovery: torn tails are truncated and segments after a continuity gap are
// removed.
func Open(dir string, opts *Options) (*Log, error) {
	if opts == nil {
		opts = &Options{}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	l := &Log{
		dir:           dir,
		segMaxRecords: opts.SegmentMaxRecords,
		hooks:         opts.Hooks,
		consumers:     map[string]uint64{},
	}
	if l.segMaxRecords == 0 {
		l.segMaxRecords = defaultSegMax
	}
	if err := l.loadManifest(); err != nil {
		return nil, err
	}
	if err := l.recoverSegments(); err != nil {
		return nil, err
	}
	return l, nil
}

// Close closes all segment files. It does not flush anything: every mutation
// is already fsynced before the corresponding call returned.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	var firstErr error
	for _, s := range l.segments {
		if err := s.f.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// NextOffset returns the high watermark: the offset the next Append assigns.
// Valid ack positions are exactly [0, NextOffset].
func (l *Log) NextOffset() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.nextOffset
}

// FirstOffset returns the oldest offset still readable; offsets below it were
// reclaimed.
func (l *Log) FirstOffset() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.segments) == 0 {
		return l.nextOffset
	}
	return l.segments[0].base
}

// Append writes payload as the next record and returns its offset. The record
// is durable (segment fsynced) when Append returns nil.
func (l *Log) Append(payload []byte) (uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return 0, ErrClosed
	}
	if err := l.ensureActiveLocked(); err != nil {
		return 0, err
	}
	seg := l.segments[len(l.segments)-1]
	rec := encodeRecord(payload)
	start := int64(0)
	if n := len(seg.index); n > 0 {
		start = seg.index[n-1] + recordSizeAt(seg.f, seg.index[n-1])
	}
	if _, err := seg.f.WriteAt(rec, start); err != nil {
		return 0, err
	}
	if err := l.hooks.beforeSegmentSync(); err != nil {
		_ = seg.f.Truncate(start) // roll back the unflushed record
		return 0, err
	}
	if err := seg.f.Sync(); err != nil {
		_ = seg.f.Truncate(start)
		return 0, err
	}
	l.hooks.afterSegmentSync()
	seg.index = append(seg.index, start)
	off := l.nextOffset
	l.nextOffset++
	return off, nil
}

// Read returns the payload stored at offset.
func (l *Log) Read(offset uint64) ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, ErrClosed
	}
	if offset >= l.nextOffset {
		return nil, ErrOffsetOutOfRange
	}
	i := sort.Search(len(l.segments), func(i int) bool { return l.segments[i].base > offset }) - 1
	if i < 0 {
		return nil, ErrOffsetReclaimed
	}
	seg := l.segments[i]
	idx := offset - seg.base
	if idx >= seg.count() {
		return nil, ErrOffsetOutOfRange
	}
	hdr := make([]byte, headerSize)
	if _, err := seg.f.ReadAt(hdr, seg.index[idx]); err != nil {
		return nil, err
	}
	wantCRC := binary.LittleEndian.Uint32(hdr[0:4])
	length := binary.LittleEndian.Uint32(hdr[4:8])
	payload := make([]byte, length)
	if _, err := seg.f.ReadAt(payload, seg.index[idx]+headerSize); err != nil {
		return nil, err
	}
	if crc32.ChecksumIEEE(payload) != wantCRC {
		return nil, fmt.Errorf("applog: crc mismatch at offset %d", offset)
	}
	return payload, nil
}

// --- internals ---

func encodeRecord(payload []byte) []byte {
	buf := make([]byte, headerSize+len(payload))
	binary.LittleEndian.PutUint32(buf[0:4], crc32.ChecksumIEEE(payload))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(payload)))
	copy(buf[headerSize:], payload)
	return buf
}

func recordSizeAt(f *os.File, at int64) int64 {
	hdr := make([]byte, headerSize)
	if _, err := f.ReadAt(hdr, at); err != nil {
		return 0
	}
	return headerSize + int64(binary.LittleEndian.Uint32(hdr[4:8]))
}

func segmentPath(dir string, base uint64) string {
	return filepath.Join(dir, fmt.Sprintf("%020d%s", base, segmentExt))
}

// ensureActiveLocked makes sure there is an appendable (non-full) segment.
func (l *Log) ensureActiveLocked() error {
	if n := len(l.segments); n > 0 && l.segments[n-1].count() < l.segMaxRecords {
		return nil
	}
	path := segmentPath(l.dir, l.nextOffset)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, filePermission)
	if err != nil {
		return err
	}
	l.segments = append(l.segments, &segment{base: l.nextOffset, path: path, f: f})
	return syncDir(l.dir) // make the new directory entry durable
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// scanSegment reads a segment file and returns the in-file offset of every
// record plus the size of the clean prefix. A torn or corrupt record ends the
// scan without error; torn reports whether that happened.
func scanSegment(f *os.File) (index []int64, cleanSize int64, torn bool, err error) {
	var off int64
	hdr := make([]byte, headerSize)
	for {
		n, rerr := f.ReadAt(hdr, off)
		if rerr == io.EOF {
			if n == 0 {
				return index, off, false, nil // clean end of segment
			}
			return index, off, true, nil // partial header: torn write
		}
		if rerr != nil {
			return nil, 0, false, rerr
		}
		wantCRC := binary.LittleEndian.Uint32(hdr[0:4])
		length := binary.LittleEndian.Uint32(hdr[4:8])
		if length > maxRecordSize {
			return index, off, true, nil
		}
		payload := make([]byte, length)
		if _, rerr := f.ReadAt(payload, off+headerSize); rerr != nil {
			return index, off, true, nil // short payload: torn write
		}
		if crc32.ChecksumIEEE(payload) != wantCRC {
			return index, off, true, nil
		}
		index = append(index, off)
		off += headerSize + int64(length)
	}
}

// recoverSegments discovers segment files from the directory (they are the
// source of truth), truncates a torn tail and drops segments after a
// continuity gap.
func (l *Log) recoverSegments() error {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return err
	}
	var bases []uint64
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, segmentExt) {
			continue
		}
		base, perr := strconv.ParseUint(strings.TrimSuffix(name, segmentExt), 10, 64)
		if perr != nil {
			continue
		}
		bases = append(bases, base)
	}
	sort.Slice(bases, func(i, j int) bool { return bases[i] < bases[j] })

	expected := uint64(0)
	for i, base := range bases {
		if i > 0 && base != expected {
			// Continuity gap: everything from here on is unreachable garbage
			// left by an interrupted operation; remove it.
			for _, b := range bases[i:] {
				_ = os.Remove(segmentPath(l.dir, b))
			}
			break
		}
		path := segmentPath(l.dir, base)
		f, err := os.OpenFile(path, os.O_RDWR, filePermission)
		if err != nil {
			return err
		}
		index, cleanSize, torn, err := scanSegment(f)
		if err != nil {
			f.Close()
			return err
		}
		if torn {
			if err := f.Truncate(cleanSize); err != nil {
				f.Close()
				return err
			}
			if err := f.Sync(); err != nil {
				f.Close()
				return err
			}
			// A torn tail invalidates every later segment.
			for _, b := range bases[i+1:] {
				_ = os.Remove(segmentPath(l.dir, b))
			}
		}
		l.segments = append(l.segments, &segment{base: base, path: path, f: f, index: index})
		expected = base + uint64(len(index))
		if torn {
			break
		}
	}
	l.nextOffset = expected
	if len(bases) == 0 {
		l.nextOffset = 0
	}
	return nil
}

// --- manifest (consumer cursors) ---

type manifest struct {
	Consumers map[string]uint64 `json:"consumers"`
}

func (l *Log) loadManifest() error {
	data, err := os.ReadFile(filepath.Join(l.dir, manifestName))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("applog: corrupt manifest: %w", err)
	}
	for id, pos := range m.Consumers {
		l.consumers[id] = pos
	}
	return nil
}

// persistManifestLocked commits the current consumer cursors atomically:
// write temp file, fsync, rename, fsync directory. The caller must hold l.mu
// and must roll back its in-memory change if this returns an error (any error
// is raised before the rename, so the previously committed manifest is still
// in effect).
func (l *Log) persistManifestLocked() error {
	data, err := json.Marshal(manifest{Consumers: l.consumers})
	if err != nil {
		return err
	}
	tmp := filepath.Join(l.dir, manifestTmp)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, filePermission)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := l.hooks.beforeManifestSync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := l.hooks.beforeManifestRename(); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(l.dir, manifestName)); err != nil {
		return err
	}
	l.hooks.afterManifestRename()
	return syncDir(l.dir)
}

// Package logstore implements an append-only record log with a sidecar
// index and crash-safe recovery.
//
// On-disk formats
//
//	Data file:  sequence of records
//	  | magic uint32 | payloadLen uint32 | payload ... | crc32 uint32 |
//	  crc32 covers the 8-byte header and the payload.
//
//	Index file: fixed-size 20-byte entries, one per record
//	  | offset uint64 | payloadLen uint32 | digest uint32 | entryCRC uint32 |
//	  digest   = crc32 of the whole on-disk record (header+payload+crc)
//	  entryCRC = crc32 of the first 16 bytes of the entry
//
// The index binds each record's offset, length and content digest, so an
// entry can be validated against the data file independently.
//
// Fault points where data and index writes interleave (write order in
// Append is: data, fsync(data), index, fsync(index)):
//
//	F1  data persisted, index entry never written   -> index lags data
//	F2  index entry torn (partial write)            -> garbage index tail
//	F3  data record torn (partial write)            -> garbage data tail
//	F4  index persisted, data record lost             -> index leads data
//	F5  complete record corrupted (bit rot, stray write) -> must be reported
//
// Recovery (Open) handles all five:
//
//  1. Scan the index and keep the leading run of well-formed entries
//     (entryCRC valid). A torn or corrupt tail (F2) is discarded.
//  2. Verify every kept entry against the data: its digest must match the
//     record bytes it points at. Entries past a missing/unmeasurable
//     record are dropped (F4); an entry pointing at a measurable but
//     corrupt record is dropped individually (F5).
//  3. Scan every data region not covered by a verified entry - the gaps
//     left by dropped entries and the tail beyond index coverage. Valid
//     records are indexed (F1). A torn record at the very tail (F3) is
//     truncated. A complete record with a bad CRC (F5) is reported as
//     corrupt and skipped - it is never silently treated as an ordinary
//     tail truncation.
//  4. Persist: rewrite or extend the index and fsync it first, then
//     truncate the data tail and fsync the data. A crash anywhere in this
//     sequence leaves a state that the next Open repairs to the same
//     result, so reopening neither misses nor duplicates records.
package logstore

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
)

const (
	recordMagic    uint32 = 0x4c4f4752 // "LOGR"
	recordHdrSize         = 8
	recordCrcSize         = 4
	maxPayloadSize        = 64 << 20

	indexEntrySize = 20

	dataFileName  = "data.log"
	indexFileName = "index.log"
)

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

type indexEntry struct {
	offset uint64
	length uint32 // payload length
	digest uint32 // crc32 of the full on-disk record
}

func (e indexEntry) recordSize() uint64 {
	return recordHdrSize + uint64(e.length) + recordCrcSize
}

func encodeRecord(payload []byte) []byte {
	buf := make([]byte, recordHdrSize+len(payload)+recordCrcSize)
	binary.LittleEndian.PutUint32(buf[0:4], recordMagic)
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(payload)))
	copy(buf[recordHdrSize:], payload)
	crc := crc32.Checksum(buf[:recordHdrSize+len(payload)], castagnoli)
	binary.LittleEndian.PutUint32(buf[recordHdrSize+len(payload):], crc)
	return buf
}

func encodeIndexEntry(e indexEntry) []byte {
	buf := make([]byte, indexEntrySize)
	binary.LittleEndian.PutUint64(buf[0:8], e.offset)
	binary.LittleEndian.PutUint32(buf[8:12], e.length)
	binary.LittleEndian.PutUint32(buf[12:16], e.digest)
	binary.LittleEndian.PutUint32(buf[16:20], crc32.Checksum(buf[:16], castagnoli))
	return buf
}

func decodeIndexEntry(buf []byte) (indexEntry, bool) {
	if len(buf) != indexEntrySize {
		return indexEntry{}, false
	}
	if crc32.Checksum(buf[:16], castagnoli) != binary.LittleEndian.Uint32(buf[16:20]) {
		return indexEntry{}, false
	}
	return indexEntry{
		offset: binary.LittleEndian.Uint64(buf[0:8]),
		length: binary.LittleEndian.Uint32(buf[8:12]),
		digest: binary.LittleEndian.Uint32(buf[12:16]),
	}, true
}

// CorruptRecord describes a complete but invalid record found in the data
// file. Such records are reported, excluded from the index, and never
// silently discarded as tail truncation.
type CorruptRecord struct {
	Offset uint64
	Length uint64 // on-disk length; 0 when the header itself was unreadable
	Reason string
}

// RepairReport describes everything recovery changed or found.
type RepairReport struct {
	DataSizeBefore uint64
	DataSizeAfter  uint64
	DataTruncated  bool // a torn data tail (F3) was removed

	IndexSizeBefore     int64 // bytes
	IndexEntriesBefore  int   // well-formed entries found on disk
	IndexEntriesKept    int   // entries that also verified against data
	IndexTruncatedBytes int64 // bytes removed from the index tail (F2/F4)
	IndexRewritten      bool  // index was rebuilt from scratch (F5 drops)

	RebuiltEntries int    // entries appended while re-scanning data (F1)
	RebuildStart   uint64 // data offset where the incremental scan began
	RebuildEnd     uint64 // data offset where the scan stopped

	Corrupt []CorruptRecord // complete-but-bad records (F5)
}

// Clean reports whether recovery found nothing to repair.
func (r *RepairReport) Clean() bool {
	return !r.DataTruncated &&
		r.IndexTruncatedBytes == 0 &&
		!r.IndexRewritten &&
		r.IndexEntriesBefore == r.IndexEntriesKept &&
		r.RebuiltEntries == 0 &&
		len(r.Corrupt) == 0
}

// Store is an open, recovered log.
type Store struct {
	data    *os.File
	index   *os.File
	entries []indexEntry
	end     uint64 // offset just past the last valid data byte
}

// Open opens (creating if necessary) the log in dir and runs recovery.
// The returned report describes the repair; it is never nil on success.
func Open(dir string) (*Store, *RepairReport, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	data, err := os.OpenFile(filepath.Join(dir, dataFileName), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, nil, err
	}
	index, err := os.OpenFile(filepath.Join(dir, indexFileName), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		data.Close()
		return nil, nil, err
	}
	s := &Store{data: data, index: index}
	rep, err := s.recover()
	if err != nil {
		data.Close()
		index.Close()
		return nil, nil, err
	}
	if d, derr := os.Open(dir); derr == nil {
		d.Sync() // persist file creation, if any
		d.Close()
	}
	return s, rep, nil
}

func (s *Store) recover() (*RepairReport, error) {
	rep := &RepairReport{}

	dataStat, err := s.data.Stat()
	if err != nil {
		return nil, err
	}
	dataSize := uint64(dataStat.Size())
	rep.DataSizeBefore = dataSize

	indexStat, err := s.index.Stat()
	if err != nil {
		return nil, err
	}
	indexSize := indexStat.Size()
	rep.IndexSizeBefore = indexSize

	// Step 1: keep the leading run of well-formed entries (drops F2 tail).
	raw := make([]byte, indexSize)
	if _, err := s.index.ReadAt(raw, 0); err != nil && indexSize > 0 {
		return nil, err
	}
	var entries []indexEntry
	for off := int64(0); off+indexEntrySize <= indexSize; off += indexEntrySize {
		e, ok := decodeIndexEntry(raw[off : off+indexEntrySize])
		if !ok {
			break
		}
		entries = append(entries, e)
	}
	rep.IndexEntriesBefore = len(entries)

	// Step 2: verify each entry against the data it points at.
	var kept []indexEntry
	var coverageEnd uint64
	needRewrite := false
	for _, e := range entries {
		if e.offset >= coverageEnd && s.verifyEntry(e, dataSize) {
			kept = append(kept, e)
			coverageEnd = e.offset + e.recordSize()
			continue
		}
		if s.recordMeasurableAt(e.offset, dataSize) {
			// The record is complete on disk but the entry does not match
			// it (F5 corruption or a stale entry). Drop only this entry;
			// the gap scan below re-processes the region and reports any
			// corruption it finds.
			needRewrite = true
			continue
		}
		// The record cannot even be measured (F4: data lost or truncated).
		// Drop this entry and everything after it.
		break
	}
	rep.IndexEntriesKept = len(kept)

	// Step 3: scan every data region not covered by a verified entry: the
	// gaps left by dropped entries and the tail beyond index coverage.
	var finalEntries []indexEntry
	var pos uint64
	truncateAt := dataSize
	rebuildStart := dataSize
	rebuildStartSet := false
	markGap := func(from uint64) {
		if !rebuildStartSet {
			rebuildStart = from
			rebuildStartSet = true
		}
	}
	for _, e := range kept {
		if e.offset > pos {
			markGap(pos)
			found, _, err := s.scanGap(pos, e.offset, false, rep)
			if err != nil {
				return nil, err
			}
			if len(found) > 0 {
				needRewrite = true
			}
			finalEntries = append(finalEntries, found...)
		}
		finalEntries = append(finalEntries, e)
		pos = e.offset + e.recordSize()
	}
	if pos < dataSize {
		markGap(pos)
		found, tr, err := s.scanGap(pos, dataSize, true, rep)
		if err != nil {
			return nil, err
		}
		finalEntries = append(finalEntries, found...)
		truncateAt = tr
	}
	rep.RebuildStart = rebuildStart
	rep.RebuildEnd = truncateAt
	rep.RebuiltEntries = len(finalEntries) - len(kept)

	// Step 4: persist. Index first, then data truncation; a crash between
	// the two leaves a state the next Open repairs identically.
	keptBytes := int64(len(kept)) * indexEntrySize
	if needRewrite {
		// Entries were dropped from (or inserted into) the middle, so the
		// whole index is rewritten. Truncate to zero first: a crash then
		// leaves an empty or prefix-valid index, both rebuildable.
		if err := s.index.Truncate(0); err != nil {
			return nil, err
		}
		if err := s.index.Sync(); err != nil {
			return nil, err
		}
		buf := make([]byte, 0, len(finalEntries)*indexEntrySize)
		for _, e := range finalEntries {
			buf = append(buf, encodeIndexEntry(e)...)
		}
		if _, err := s.index.WriteAt(buf, 0); err != nil {
			return nil, err
		}
		rep.IndexRewritten = true
	} else {
		// Fast path: kept entries are an intact prefix of the on-disk
		// index; drop the tail and append the newly scanned entries.
		if keptBytes < indexSize {
			if err := s.index.Truncate(keptBytes); err != nil {
				return nil, err
			}
			rep.IndexTruncatedBytes = indexSize - keptBytes
		}
		if len(finalEntries) > len(kept) {
			buf := make([]byte, 0, (len(finalEntries)-len(kept))*indexEntrySize)
			for _, e := range finalEntries[len(kept):] {
				buf = append(buf, encodeIndexEntry(e)...)
			}
			if _, err := s.index.WriteAt(buf, keptBytes); err != nil {
				return nil, err
			}
		}
	}
	if err := s.index.Sync(); err != nil {
		return nil, err
	}

	if truncateAt < dataSize {
		if err := s.data.Truncate(int64(truncateAt)); err != nil {
			return nil, err
		}
		rep.DataTruncated = true
	}
	if err := s.data.Sync(); err != nil {
		return nil, err
	}

	rep.DataSizeAfter = truncateAt
	s.entries = finalEntries
	s.end = truncateAt
	return rep, nil
}

// verifyEntry reports whether the entry's digest matches the record bytes
// currently in the data file.
func (s *Store) verifyEntry(e indexEntry, dataSize uint64) bool {
	if e.offset > dataSize || e.recordSize() > dataSize-e.offset {
		return false
	}
	rec := make([]byte, e.recordSize())
	if _, err := s.data.ReadAt(rec, int64(e.offset)); err != nil {
		return false
	}
	return crc32.Checksum(rec, castagnoli) == e.digest
}

// recordMeasurableAt reports whether a complete, well-formed record header
// at off describes a record that fits entirely within dataSize. The record
// may still fail its CRC; "measurable" only means its on-disk extent is
// known, so scanning can continue past it.
func (s *Store) recordMeasurableAt(off, dataSize uint64) bool {
	if off > dataSize || dataSize-off < recordHdrSize {
		return false
	}
	var hdr [recordHdrSize]byte
	if _, err := s.data.ReadAt(hdr[:], int64(off)); err != nil {
		return false
	}
	if binary.LittleEndian.Uint32(hdr[0:4]) != recordMagic {
		return false
	}
	length := binary.LittleEndian.Uint32(hdr[4:8])
	if length > maxPayloadSize {
		return false
	}
	return recordHdrSize+uint64(length)+recordCrcSize <= dataSize-off
}

// scanGap scans the data range [from, to) for records. Valid records are
// returned as new index entries. Complete but corrupt records are reported
// and skipped. When tail is true the range ends at the data file's end, so
// an incomplete record there is a torn write (F3): the returned truncation
// point marks it. When tail is false the range is bounded by a verified
// record, so anything unparseable is corruption and is reported.
func (s *Store) scanGap(from, to uint64, tail bool, rep *RepairReport) ([]indexEntry, uint64, error) {
	var out []indexEntry
	scanPos := from
	for scanPos < to {
		remaining := to - scanPos
		if remaining < recordHdrSize {
			if tail {
				return out, scanPos, nil // torn header at the tail (F3)
			}
			rep.Corrupt = append(rep.Corrupt, CorruptRecord{
				Offset: scanPos,
				Reason: "unparseable trailing bytes",
			})
			return out, to, nil
		}
		var hdr [recordHdrSize]byte
		if _, err := s.data.ReadAt(hdr[:], int64(scanPos)); err != nil {
			return nil, 0, err
		}
		magic := binary.LittleEndian.Uint32(hdr[0:4])
		length := binary.LittleEndian.Uint32(hdr[4:8])
		if magic != recordMagic || length > maxPayloadSize {
			// The header is garbage, so the record cannot be measured and
			// scanning cannot continue past it. Report it as corruption
			// (never a silent tail skip); at the tail, truncate it away.
			rep.Corrupt = append(rep.Corrupt, CorruptRecord{
				Offset: scanPos,
				Reason: "invalid record header",
			})
			return out, scanPos, nil
		}
		recLen := recordHdrSize + uint64(length) + recordCrcSize
		if remaining < recLen {
			if tail {
				return out, scanPos, nil // torn record at the tail (F3)
			}
			rep.Corrupt = append(rep.Corrupt, CorruptRecord{
				Offset: scanPos,
				Reason: "record overruns indexed region",
			})
			return out, to, nil
		}
		rec := make([]byte, recLen)
		if _, err := s.data.ReadAt(rec, int64(scanPos)); err != nil {
			return nil, 0, err
		}
		stored := binary.LittleEndian.Uint32(rec[recLen-recordCrcSize:])
		if crc32.Checksum(rec[:recLen-recordCrcSize], castagnoli) != stored {
			// Complete but corrupt record (F5): report, skip, keep scanning.
			rep.Corrupt = append(rep.Corrupt, CorruptRecord{
				Offset: scanPos,
				Length: recLen,
				Reason: "crc mismatch",
			})
			scanPos += recLen
			continue
		}
		out = append(out, indexEntry{
			offset: scanPos,
			length: length,
			digest: crc32.Checksum(rec, castagnoli),
		})
		scanPos += recLen
	}
	return out, to, nil
}

// Append writes one record and its index entry, fsyncing data before index.
// It returns the record's offset in the data file.
func (s *Store) Append(payload []byte) (uint64, error) {
	if len(payload) > maxPayloadSize {
		return 0, errors.New("logstore: payload too large")
	}
	rec := encodeRecord(payload)
	off := s.end
	if _, err := s.data.WriteAt(rec, int64(off)); err != nil {
		return 0, err
	}
	if err := s.data.Sync(); err != nil {
		return 0, err
	}
	e := indexEntry{
		offset: off,
		length: uint32(len(payload)),
		digest: crc32.Checksum(rec, castagnoli),
	}
	if _, err := s.index.WriteAt(encodeIndexEntry(e), int64(len(s.entries))*indexEntrySize); err != nil {
		return 0, err
	}
	if err := s.index.Sync(); err != nil {
		return 0, err
	}
	s.entries = append(s.entries, e)
	s.end += uint64(len(rec))
	return off, nil
}

// Read returns the payload of the i-th indexed record.
func (s *Store) Read(i int) ([]byte, error) {
	if i < 0 || i >= len(s.entries) {
		return nil, errors.New("logstore: record index out of range")
	}
	e := s.entries[i]
	rec := make([]byte, e.recordSize())
	if _, err := s.data.ReadAt(rec, int64(e.offset)); err != nil {
		return nil, err
	}
	if crc32.Checksum(rec, castagnoli) != e.digest {
		return nil, fmt.Errorf("logstore: record %d: digest mismatch", i)
	}
	payload := rec[recordHdrSize : e.recordSize()-recordCrcSize]
	out := make([]byte, len(payload))
	copy(out, payload)
	return out, nil
}

// Len returns the number of indexed records.
func (s *Store) Len() int { return len(s.entries) }

// Close flushes and closes both files.
func (s *Store) Close() error {
	errData := s.data.Close()
	errIndex := s.index.Close()
	if errData != nil {
		return errData
	}
	return errIndex
}

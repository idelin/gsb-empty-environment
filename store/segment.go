package store

import (
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
)

// Segment file layout:
//
//	[0:8]   magic "MPXSEG\x00\x01"
//	[8:10]  format version (uint16 LE)
//	[10:16] reserved (zero)
//	[16:]   records: crc32(uint32 LE) | length(uint32 LE) | payload...
//
// The CRC covers the 4 length bytes and the payload. Every record is framed
// by its length, so a reader can skip records whose payload version it does
// not fully understand (forward compatibility) and a torn tail write is
// detected as truncation rather than corruption.
const (
	HeaderSize       = 16
	recordHeaderSize = 8
)

var magic = [8]byte{'M', 'P', 'X', 'S', 'E', 'G', 0x00, 0x01}

// Writer appends framed, checksummed records to a segment file.
type Writer struct {
	f       *os.File
	version uint16
	offset  int64
	scratch []byte
}

// CreateWriter creates (or truncates) a segment file at path and writes its
// header with the given format version.
func CreateWriter(path string, version uint16) (*Writer, error) {
	if version < FormatV1 || version > CurrentFormat {
		return nil, ErrCorrupt
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	hdr := make([]byte, HeaderSize)
	copy(hdr, magic[:])
	binary.LittleEndian.PutUint16(hdr[8:10], version)
	if _, err := f.Write(hdr); err != nil {
		f.Close()
		return nil, err
	}
	return &Writer{f: f, version: version, offset: HeaderSize, scratch: make([]byte, 0, 4096)}, nil
}

// Append frames and writes one entry.
func (w *Writer) Append(e Entry) error {
	payloadLen := payloadSize(e, w.version)
	if payloadLen > MaxRecordSize {
		return ErrTooLarge
	}
	need := recordHeaderSize + payloadLen
	if cap(w.scratch) < need {
		w.scratch = make([]byte, 0, 2*need)
	}
	rec := w.scratch[:need]
	binary.LittleEndian.PutUint32(rec[4:8], uint32(payloadLen))
	rec = AppendEncode(rec[:8], e, w.version)
	crc := crc32.ChecksumIEEE(rec[4:])
	binary.LittleEndian.PutUint32(rec[0:4], crc)
	if _, err := w.f.Write(rec); err != nil {
		return err
	}
	w.offset += int64(len(rec))
	return nil
}

// OpenWriter opens an existing segment for appending, or creates it if
// missing. The header is validated and a torn tail (from a crash mid-write)
// is truncated so new records always start at a clean boundary.
func OpenWriter(path string, version uint16) (*Writer, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return CreateWriter(path, version)
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*Writer, error) {
		f.Close()
		return nil, err
	}
	hdr := make([]byte, HeaderSize)
	if _, err := io.ReadFull(f, hdr); err != nil {
		return fail(ErrTruncated)
	}
	if string(hdr[0:6]) != "MPXSEG" {
		return fail(ErrCorrupt)
	}
	if v := binary.LittleEndian.Uint16(hdr[8:10]); v < FormatV1 || v > CurrentFormat {
		return fail(ErrCorrupt)
	}
	// Scan to the end of the last complete, well-checksummed record.
	off := int64(HeaderSize)
	buf := make([]byte, recordHeaderSize)
	for {
		if _, err := f.ReadAt(buf, off); err != nil {
			break // clean EOF or torn header
		}
		length := binary.LittleEndian.Uint32(buf[4:8])
		if length > MaxRecordSize {
			break
		}
		frame := make([]byte, recordHeaderSize+int(length))
		if _, err := f.ReadAt(frame, off); err != nil {
			break // torn payload
		}
		if crc32.ChecksumIEEE(frame[4:]) != binary.LittleEndian.Uint32(frame[0:4]) {
			break // corrupt record: stop before it
		}
		off += int64(len(frame))
	}
	if err := f.Truncate(off); err != nil {
		return fail(err)
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return fail(err)
	}
	return &Writer{f: f, version: version, offset: off, scratch: make([]byte, 0, 4096)}, nil
}

// Sync flushes the file to stable storage.
func (w *Writer) Sync() error { return w.f.Sync() }

// Close syncs and closes the underlying file.
func (w *Writer) Close() error {
	if err := w.f.Sync(); err != nil {
		w.f.Close()
		return err
	}
	return w.f.Close()
}

// Segment is a read-only, memory-mapped view of a segment file. Entries
// decoded from it alias the mapped pages (zero-copy).
type Segment struct {
	data   []byte
	unmap  func([]byte) error
	Format uint16
}

// Open maps the segment file at path into memory and validates its header.
func Open(path string) (*Segment, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	data, unmap, err := mmapReadOnly(f, fi.Size())
	if err != nil {
		return nil, err
	}
	s := &Segment{data: data, unmap: unmap}
	if err := s.checkHeader(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (s *Segment) checkHeader() error {
	if len(s.data) < HeaderSize {
		return ErrTruncated
	}
	if string(s.data[0:6]) != "MPXSEG" {
		return ErrCorrupt
	}
	s.Format = binary.LittleEndian.Uint16(s.data[8:10])
	if s.Format < FormatV1 || s.Format > CurrentFormat {
		return ErrCorrupt
	}
	return nil
}

// Bytes exposes the raw mapped bytes (read-only). Intended for tests.
func (s *Segment) Bytes() []byte { return s.data }

// Next decodes the record starting at offset off (use HeaderSize for the
// first record) and returns the entry plus the offset of the next record.
// It returns io.EOF at a clean end of segment and ErrTruncated on a torn
// tail write. The returned Entry aliases the mapped memory.
func (s *Segment) Next(off int) (Entry, int, error) {
	var e Entry
	if off < HeaderSize || off > len(s.data) {
		return e, off, ErrCorrupt
	}
	if off == len(s.data) {
		return e, off, io.EOF
	}
	if len(s.data)-off < recordHeaderSize {
		return e, off, ErrTruncated
	}
	length := binary.LittleEndian.Uint32(s.data[off+4 : off+8])
	if length > MaxRecordSize {
		return e, off, ErrTooLarge
	}
	if uint64(len(s.data)-off-recordHeaderSize) < uint64(length) {
		return e, off, ErrTruncated
	}
	frame := s.data[off+4 : off+recordHeaderSize+int(length)]
	if crc32.ChecksumIEEE(frame) != binary.LittleEndian.Uint32(s.data[off:off+4]) {
		return e, off, ErrChecksum
	}
	payload := s.data[off+recordHeaderSize : off+recordHeaderSize+int(length)]
	entry, err := DecodeEntry(payload)
	if err != nil {
		return e, off, err
	}
	return entry, off + recordHeaderSize + int(length), nil
}

// ForEach iterates all complete records in the segment. A torn tail write
// (ErrTruncated) terminates iteration without error; any other corruption is
// returned.
func (s *Segment) ForEach(fn func(Entry) error) error {
	off := HeaderSize
	for {
		e, next, err := s.Next(off)
		switch err {
		case nil:
			if err := fn(e); err != nil {
				return err
			}
			off = next
		case io.EOF, ErrTruncated:
			return nil
		default:
			return err
		}
	}
}

// Close unmaps the segment. Entries decoded from it must not be used after
// Close.
func (s *Segment) Close() error {
	if s.unmap == nil {
		return nil
	}
	err := s.unmap(s.data)
	s.unmap = nil
	s.data = nil
	return err
}

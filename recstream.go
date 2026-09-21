// Package recstream implements a self-describing, length-delimited record
// stream and a random-access index over it.
//
// On-disk record frame:
//
//	uvarint frameLen ; number of bytes that follow
//	[ frameLen bytes ] {
//	    body                          ; serialized fields (may be empty)
//	    uvarint crc32(ieee) of body   ; trailing checksum
//	}
//
// A field inside a body is:
//
//	uvarint (fieldID << 3) | wireType
//	value, according to wireType:
//	  WireVarint(0):  uvarint
//	  WireFixed32(1): 4 bytes little endian
//	  WireFixed64(2): 8 bytes little endian
//	  WireBytes(3):   uvarint length + raw bytes
//
// Fields are self-delimited, so a newer writer may append unknown field IDs
// while an older reader simply skips them. Field IDs are the schema; wire
// types are attached to every field, which lets a reader locate a field across
// writer/reader versions and reject an ID reused with an incompatible type.
package recstream

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
)

// Wire types carried in every field tag.
const (
	WireVarint  = 0
	WireFixed32 = 1
	WireFixed64 = 2
	WireBytes   = 3
)

// DefaultMaxRecordSize is the default quota for a single record frame payload.
const DefaultMaxRecordSize = 16 << 20 // 16 MiB

// Sentinel errors. They are intentionally specific so callers can tell
// "not enough bytes yet" apart from "the bytes are bad".
var (
	// ErrNeedMore means the input is a valid prefix but more bytes are
	// required to finish the current frame (chunked input only).
	ErrNeedMore = errors.New("recstream: incomplete record, need more data")
	// ErrTruncated means the stream ended in the middle of an announced
	// frame. The data promised by a length is missing.
	ErrTruncated = errors.New("recstream: truncated stream")
	// ErrCorrupt means the bytes present are structurally invalid: a bad
	// checksum, an invalid tag, a bad varint, an inner length that does
	// not reach the frame boundary, etc.
	ErrCorrupt = errors.New("recstream: corrupt record")
	// ErrRecordTooLarge means an announced length exceeds the configured
	// quota. The check happens before the buffer is grown or the payload
	// is allocated, so a hostile length header cannot exhaust memory.
	ErrRecordTooLarge = errors.New("recstream: record exceeds length quota")
	// ErrIndexCorrupt means an index file fails its own validation.
	ErrIndexCorrupt = errors.New("recstream: corrupt index")
	// ErrSourceMismatch means the source stream's size or digest does not
	// match the summary stored in the index: the index belongs to another
	// (or an overwritten) file.
	ErrSourceMismatch = errors.New("recstream: index/source mismatch")
	// ErrBoundary means a requested offset is not a real record boundary,
	// or the bytes found there do not form the indexed record.
	ErrBoundary = errors.New("recstream: offset is not a record boundary")
)

var crcTable = crc32.IEEETable

// Field is one self-describing field. Unknown fields encountered while
// reading are preserved verbatim in Raw (with their ID, Wire and Value kept
// for known types) so a record can be decoded, inspected and re-encoded
// without loss ("unknown fields are skipped, not destroyed").
type Field struct {
	ID    uint64
	Wire  int
	Value uint64 // varint/fixed32/fixed64
	Raw   []byte // bytes payload, or canonical raw value for unknown fields
}

// Record is an ordered collection of fields. Empty records are valid.
type Record struct {
	Fields []Field
}

// NewRecord returns a record holding the given fields.
func NewRecord(fields ...Field) *Record { return &Record{Fields: fields} }

// NewVarint, NewFixed32, NewFixed64 and NewBytes construct known fields.
func NewVarint(id, v uint64) Field { return Field{ID: id, Wire: WireVarint, Value: v} }
func NewFixed32(id uint64, v uint32) Field {
	return Field{ID: id, Wire: WireFixed32, Value: uint64(v)}
}
func NewFixed64(id uint64, v uint64) Field {
	return Field{ID: id, Wire: WireFixed64, Value: v}
}
func NewBytes(id uint64, b []byte) Field {
	cp := make([]byte, len(b))
	copy(cp, b)
	return Field{ID: id, Wire: WireBytes, Raw: cp}
}

// Get returns the first field with the given id and whether it existed.
func (r *Record) Get(id uint64) (Field, bool) {
	for i := range r.Fields {
		if r.Fields[i].ID == id {
			return r.Fields[i], true
		}
	}
	return Field{}, false
}

// Typed accessors. ok is false if the field is absent or carries a different
// wire type, so cross-version type drift is visible rather than misread.
func (f Field) Varint() (uint64, bool) {
	if f.Wire != WireVarint {
		return 0, false
	}
	return f.Value, true
}
func (f Field) Fixed32() (uint32, bool) {
	if f.Wire != WireFixed32 {
		return 0, false
	}
	return uint32(f.Value), true
}
func (f Field) Fixed64() (uint64, bool) {
	if f.Wire != WireFixed64 {
		return 0, false
	}
	return f.Value, true
}
func (f Field) Bytes() ([]byte, bool) {
	if f.Wire != WireBytes {
		return nil, false
	}
	return f.Raw, true
}

func appendUvarint(b []byte, v uint64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], v)
	return append(b, buf[:n]...)
}

func (f Field) validate() error {
	if f.ID == 0 {
		return ErrCorrupt // field numbers start at 1
	}
	switch f.Wire {
	case WireVarint, WireFixed32, WireFixed64, WireBytes:
		return nil
	default:
		return errors.New("recstream: unsupported wire type")
	}
}

// encodeBody appends the serialized fields to dst.
func (r *Record) encodeBody(dst []byte) ([]byte, error) {
	for i := range r.Fields {
		f := &r.Fields[i]
		if err := f.validate(); err != nil {
			return nil, err
		}
		dst = appendUvarint(dst, f.ID<<3|uint64(f.Wire))
		switch f.Wire {
		case WireVarint:
			dst = appendUvarint(dst, f.Value)
		case WireFixed32:
			var buf [4]byte
			binary.LittleEndian.PutUint32(buf[:], uint32(f.Value))
			dst = append(dst, buf[:]...)
		case WireFixed64:
			var buf [8]byte
			binary.LittleEndian.PutUint64(buf[:], f.Value)
			dst = append(dst, buf[:]...)
		case WireBytes:
			dst = appendUvarint(dst, uint64(len(f.Raw)))
			dst = append(dst, f.Raw...)
		}
	}
	return dst, nil
}

// encodeFrame serializes one record into its length-delimited frame and
// appends it to dst. The length quota is checked before allocation.
func encodeFrame(dst []byte, r *Record, maxSize int) ([]byte, error) {
	body, err := r.encodeBody(nil)
	if err != nil {
		return nil, err
	}
	// Checksum domain is the 8-byte little-endian body length followed by
	// the body. Including the length means a corrupted (shortened or
	// lengthened) frame cannot masquerade as a valid empty frame: the
	// CRC of an empty body is a fixed nonzero value, never 0.
	var len8 [8]byte
	binary.LittleEndian.PutUint64(len8[:], uint64(len(body)))
	h := crc32.New(crcTable)
	h.Write(len8[:])
	h.Write(body)
	var crcBuf [binary.MaxVarintLen64]byte
	crcN := binary.PutUvarint(crcBuf[:], uint64(h.Sum32()))
	frameLen := len(body) + crcN
	if frameLen > maxSize {
		return nil, ErrRecordTooLarge
	}
	dst = appendUvarint(dst, uint64(frameLen))
	dst = append(dst, body...)
	dst = append(dst, crcBuf[:crcN]...)
	return dst, nil
}

// EncodeFrame serializes a record using DefaultMaxRecordSize.
func EncodeFrame(r *Record) ([]byte, error) {
	return encodeFrame(nil, r, DefaultMaxRecordSize)
}

// Writer appends frames to an io.Writer. Offset returns the byte position of
// the record most recently written.
type Writer struct {
	w          io.Writer
	maxSize    int
	offset     int64
	lastOffset int64
}

// WriterOption configures a Writer.
type WriterOption func(*Writer)

// WithMaxRecordSize sets the per-record length quota for a Writer.
func WithMaxRecordSize(n int) WriterOption {
	return func(w *Writer) { w.maxSize = n }
}

// NewWriter constructs a Writer.
func NewWriter(w io.Writer, opts ...WriterOption) *Writer {
	wr := &Writer{w: w, maxSize: DefaultMaxRecordSize, offset: 0, lastOffset: -1}
	for _, o := range opts {
		o(wr)
	}
	if wr.maxSize < 1 {
		wr.maxSize = 1
	}
	return wr
}

// Write appends one record and returns its offset and total frame size.
func (w *Writer) Write(r *Record) (offset, frameSize int64, err error) {
	buf, err := encodeFrame(nil, r, w.maxSize)
	if err != nil {
		return 0, 0, err
	}
	if _, err = w.w.Write(buf); err != nil {
		return 0, 0, err
	}
	w.lastOffset = w.offset
	w.offset += int64(len(buf))
	return w.lastOffset, int64(len(buf)), nil
}

// Size returns the number of bytes written so far.
func (w *Writer) Size() int64 { return w.offset }

// LastOffset returns the offset of the most recently written record, or -1.
func (w *Writer) LastOffset() int64 { return w.lastOffset }

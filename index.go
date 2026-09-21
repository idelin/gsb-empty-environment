package recstream

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"os"
)

// Index format (little-endian framing):
//
//	magic      = "RSIDX001\n" (9 bytes)
//	uvarint    source size in bytes
//	[32]byte   SHA-256 of the whole source file
//	uvarint    number of entries
//	repeated entry:
//	    uvarint offset     byte offset of the frame's length varint
//	    uvarint frameSize  total frame size (length varint included)
//	    uvarint crc        body CRC stored in the frame
//
// The source summary (size + digest) pins an index to the exact file it was
// built from, so a stale or copied index cannot be applied to another file.
var indexMagic = []byte("RSIDX001\n")

// Entry locates one record in the source stream.
type Entry struct {
	Offset    int64
	FrameSize int64
	CRC       uint32
}

// Index is the random-access map for one source stream.
type Index struct {
	SourceSize   int64
	SourceDigest [sha256.Size]byte
	Entries      []Entry
}

// Count returns the number of indexed records.
func (x *Index) Count() int { return len(x.Entries) }

// writeUvarint / readUvarint framing helpers.
func writeUvarint(w io.Writer, v uint64) error {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], v)
	_, err := w.Write(buf[:n])
	return err
}

// BuildIndex scans src sequentially, records every frame boundary and writes
// no source bytes elsewhere. The returned Index carries the source size and
// SHA-256 digest.
func BuildIndex(src io.Reader, opts ...DecoderOption) (*Index, error) {
	s := newScanner(src, DefaultMaxRecordSize)
	for _, o := range opts {
		o(s)
	}
	if s.maxSize < 1 {
		s.maxSize = 1
	}
	h := sha256.New()
	tr := io.TeeReader(s.r, h)
	s.r = tr

	idx := &Index{}
	var expectOffset int64
	for {
		_, crc, off, size, err := s.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if off != expectOffset {
			return nil, ErrIndexCorrupt
		}
		idx.Entries = append(idx.Entries, Entry{
			Offset: off, FrameSize: size, CRC: crc,
		})
		expectOffset = off + size
	}
	idx.SourceSize = s.offset
	sum := h.Sum(nil)
	copy(idx.SourceDigest[:], sum)
	return idx, nil
}

// WriteTo serializes the index.
func (x *Index) WriteTo(w io.Writer) (int64, error) {
	cw := &countWriter{w: w}
	if _, err := cw.Write(indexMagic); err != nil {
		return cw.n, err
	}
	if err := writeUvarint(cw, uint64(x.SourceSize)); err != nil {
		return cw.n, err
	}
	if _, err := cw.Write(x.SourceDigest[:]); err != nil {
		return cw.n, err
	}
	if err := writeUvarint(cw, uint64(len(x.Entries))); err != nil {
		return cw.n, err
	}
	for _, e := range x.Entries {
		if err := writeUvarint(cw, uint64(e.Offset)); err != nil {
			return cw.n, err
		}
		if err := writeUvarint(cw, uint64(e.FrameSize)); err != nil {
			return cw.n, err
		}
		if err := writeUvarint(cw, uint64(e.CRC)); err != nil {
			return cw.n, err
		}
	}
	return cw.n, nil
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	m, err := c.w.Write(p)
	c.n += int64(m)
	return m, err
}

// readUvarint returns ErrIndexCorrupt on truncated/overflowing values.
func readUvarint(r io.ByteReader) (uint64, error) {
	v, err := binary.ReadUvarint(r)
	if err != nil {
		return 0, ErrIndexCorrupt
	}
	return v, nil
}

// ParseIndex reads and validates an index.
func ParseIndex(r io.Reader) (*Index, error) {
	br, ok := r.(io.ByteReader)
	if !ok {
		br = byteReaderWrapper{r: r}
	}
	magic := make([]byte, len(indexMagic))
	if _, err := io.ReadFull(r, magic); err != nil {
		return nil, ErrIndexCorrupt
	}
	if !bytes.Equal(magic, indexMagic) {
		return nil, ErrIndexCorrupt
	}
	size, err := readUvarint(br)
	if err != nil {
		return nil, err
	}
	digest := make([]byte, sha256.Size)
	if _, err := io.ReadFull(r, digest); err != nil {
		return nil, ErrIndexCorrupt
	}
	count, err := readUvarint(br)
	if err != nil {
		return nil, err
	}
	x := &Index{SourceSize: int64(size)}
	copy(x.SourceDigest[:], digest)
	if count > uint64(size) && count > 0 {
		// At least one byte per record; impossible to have more records.
		return nil, ErrIndexCorrupt
	}
	var expectOffset uint64
	var i uint64
	for i = 0; i < count; i++ {
		off, err := readUvarint(br)
		if err != nil {
			return nil, err
		}
		fs, err := readUvarint(br)
		if err != nil {
			return nil, err
		}
		crc, err := readUvarint(br)
		if err != nil {
			return nil, err
		}
		if fs == 0 || off != expectOffset || off+fs > size || off+fs < off {
			return nil, ErrIndexCorrupt
		}
		x.Entries = append(x.Entries, Entry{
			Offset:    int64(off),
			FrameSize: int64(fs),
			CRC:       uint32(crc),
		})
		expectOffset = off + fs
	}
	if expectOffset != size {
		// Indexed records do not cover exactly the whole source.
		return nil, ErrIndexCorrupt
	}
	return x, nil
}

// byteReaderWrapper adapts a plain Reader to ByteReader one byte at a time.
type byteReaderWrapper struct{ r io.Reader }

func (b byteReaderWrapper) ReadByte() (byte, error) {
	var buf [1]byte
	_, err := io.ReadFull(b.r, buf[:])
	if err != nil {
		return 0, err
	}
	return buf[0], nil
}

// VerifySource recomputes the source summary and returns ErrSourceMismatch if
// the current src differs from what the index was built for.
func (x *Index) VerifySource(src io.Reader) error {
	h := sha256.New()
	n, err := io.Copy(h, src)
	if err != nil {
		return err
	}
	if n != x.SourceSize {
		return ErrSourceMismatch
	}
	if !bytes.Equal(h.Sum(nil), x.SourceDigest[:]) {
		return ErrSourceMismatch
	}
	return nil
}

// RandomReader performs verified random-access reads against a ReaderAt.
type RandomReader struct {
	ra      io.ReaderAt
	idx     *Index
	maxSize int
}

// RandomOption configures a RandomReader.
type RandomOption func(*RandomReader)

// WithRandomMaxRecordSize sets the length quota for random reads.
func WithRandomMaxRecordSize(n int) RandomOption {
	return func(r *RandomReader) { r.maxSize = n }
}

// NewRandomReader binds an index to a ReaderAt. It does not touch the source;
// call VerifySource first when the source identity matters.
func NewRandomReader(ra io.ReaderAt, idx *Index, opts ...RandomOption) *RandomReader {
	r := &RandomReader{ra: ra, idx: idx, maxSize: DefaultMaxRecordSize}
	for _, o := range opts {
		o(r)
	}
	if r.maxSize < 1 {
		r.maxSize = 1
	}
	return r
}

// ReadRecord reads and verifies record i. The hit must land on a real frame
// boundary whose offset, size and CRC all agree with the index; any deviation
// (corrupt source, tampered index, wrong file) is ErrBoundary, never a record
// parsed from mid-stream.
func (r *RandomReader) ReadRecord(i int) (*Record, error) {
	if i < 0 || i >= len(r.idx.Entries) {
		return nil, ErrBoundary
	}
	e := r.idx.Entries[i]
	if e.FrameSize > int64(r.maxSize) {
		return nil, ErrRecordTooLarge
	}
	buf := make([]byte, e.FrameSize)
	if _, err := r.ra.ReadAt(buf, e.Offset); err != nil && err != io.EOF {
		return nil, err
	}
	body, crc, n, err := splitFrame(buf, r.maxSize)
	if err != nil {
		return nil, ErrBoundary
	}
	if int64(n) != e.FrameSize || crc != e.CRC {
		return nil, ErrBoundary
	}
	fields, err := parseBody(body)
	if err != nil {
		return nil, ErrBoundary
	}
	return &Record{Fields: fields}, nil
}

// OpenAndVerify is a convenience: it opens path, verifies it against the
// index digest and returns a RandomReader plus the open file.
func OpenAndVerify(path string, idx *Index, opts ...RandomOption) (*os.File, *RandomReader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	if err := idx.VerifySource(f); err != nil {
		f.Close()
		return nil, nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, NewRandomReader(f, idx, opts...), nil
}

// SaveIndexFile writes idx to path.
func SaveIndexFile(path string, idx *Index) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = idx.WriteTo(f)
	return err
}

// LoadIndexFile reads an index from path.
func LoadIndexFile(path string) (*Index, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ParseIndex(f)
}

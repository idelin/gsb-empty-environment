package safezip

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"hash"
	"hash/crc32"
	"io"
	"strings"
)

// defaultReadChunk bounds each individual ranged GET while streaming a
// member, even when a caller hands Read an oversized buffer.
const defaultReadChunk = 4 << 20

// MemberReader streams one member through ranged reads. It must be closed;
// Close rejects partial reads and verifies CRC-32 (and any data descriptor)
// before a successful return. Bytes emitted before Close are untrusted
// until Close returns nil, which is why Fetch/CacheMember only commit then.
type MemberReader struct {
	archive *Archive
	member  *Member
	src     *rangedReader
	body    io.Reader
	hasher  hash.Hash32
	read    int64
	closed  bool
	flate   io.ReadCloser
}

// OpenMember starts a streaming read of m. The returned reader must be
// Close-ed; Close returns nil only when the complete member was consumed,
// the CRC matches and, when present, the data descriptor matches.
func (a *Archive) OpenMember(m *Member) (*MemberReader, error) {
	if m == nil {
		return nil, ErrFormat
	}
	if got, ok := a.members[m.Name]; !ok || got != m {
		return nil, ErrBinding
	}
	mr := &MemberReader{
		archive: a,
		member:  m,
		src: &rangedReader{
			store: a.store,
			base:  m.DataOffset,
			limit: m.CompressedSize,
			chunk: defaultReadChunk,
		},
		hasher: crc32.NewIEEE(),
	}
	mr.body = mr.src
	if m.Method == methodDeflate {
		mr.flate = flate.NewReader(mr.src)
		mr.body = mr.flate
	}
	return mr, nil
}

// Read returns decompressed bytes. The bytes are only trustworthy after
// Close has returned nil.
func (r *MemberReader) Read(p []byte) (int, error) {
	if r.closed {
		return 0, ErrIncomplete
	}
	n, err := r.body.Read(p)
	if n > 0 {
		r.hasher.Write(p[:n])
		r.read += int64(n)
	}
	if err != nil && err != io.EOF && r.flate != nil {
		err = classifyPayloadError(err)
	}
	return n, err
}

// Close finalizes the member. It returns ErrIncomplete if not all bytes were
// read, ErrCRC on a mismatch, or another sentinel for structural problems.
// A non-nil Close guarantees nothing was committed upstream.
func (r *MemberReader) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true

	if r.flate != nil {
		// Drain trailing deflate bytes so a truncated stream is detected
		// even when the caller stopped reading early.
		if _, err := io.Copy(io.Discard, r.flate); err != nil {
			return classifyPayloadError(err)
		}
		if err := r.flate.Close(); err != nil {
			return classifyPayloadError(err)
		}
	}
	if r.read != r.member.Uncompressed {
		return ErrIncomplete
	}
	if r.hasher.Sum32() != r.member.CRC32 {
		return ErrCRC
	}
	if err := r.verifyDescriptor(); err != nil {
		return err
	}
	return nil
}

// classifyPayloadError maps compression-layer corruption to ErrCRC: any
// failure that prevents reconstructing the exact central-directory bytes is
// an integrity failure rather than a transport error.
func classifyPayloadError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "flate") || strings.Contains(err.Error(), "zlib") {
		return ErrCRC
	}
	return err
}

// verifyDescriptor reads the data descriptor following the payload and binds
// its CRC and sizes to the central directory. Its bytes sit outside the
// payload region and are fetched as separate ranges.
func (r *MemberReader) verifyDescriptor() error {
	if !r.member.DataDescriptor {
		return nil
	}
	off := r.member.payloadEnd
	head := make([]byte, 4)
	if err := readFullAt(r.archive.store, off, head); err != nil {
		return err
	}
	withSig := binary.LittleEndian.Uint32(head) == sigDataDesc
	fieldOff := off
	if withSig {
		fieldOff = off + 4
	}

	var crc uint32
	var comp, uncomp int64
	if r.member.zip64 {
		block := make([]byte, 20)
		if err := readFullAt(r.archive.store, fieldOff, block); err != nil {
			return err
		}
		crc = binary.LittleEndian.Uint32(block[0:])
		comp = int64(binary.LittleEndian.Uint64(block[4:]))
		uncomp = int64(binary.LittleEndian.Uint64(block[12:]))
	} else {
		block := make([]byte, 12)
		if err := readFullAt(r.archive.store, fieldOff, block); err != nil {
			return err
		}
		crc = binary.LittleEndian.Uint32(block[0:])
		comp = int64(binary.LittleEndian.Uint32(block[4:]))
		uncomp = int64(binary.LittleEndian.Uint32(block[8:]))
	}
	if comp < 0 || uncomp < 0 {
		return ErrFormat
	}
	if crc != r.member.CRC32 || comp != r.member.CompressedSize ||
		uncomp != r.member.Uncompressed {
		return ErrBinding
	}
	return nil
}

// Fetch streams m completely and hands the verified bytes to sink. The
// sink is invoked only after the full member has been read and CRC-checked,
// so failed verification can never populate an upper-layer cache.
func (a *Archive) Fetch(m *Member, sink Sink) (int64, error) {
	r, err := a.OpenMember(m)
	if err != nil {
		return 0, err
	}
	var buf bytes.Buffer
	n, copyErr := io.Copy(&buf, r)
	closeErr := r.Close()
	if copyErr != nil {
		return 0, copyErr
	}
	if closeErr != nil {
		return 0, closeErr
	}
	if n != m.Uncompressed {
		return 0, ErrIncomplete
	}
	if err := sink.Commit(a.cacheKey(m), bytes.NewReader(buf.Bytes())); err != nil {
		return 0, err
	}
	return n, nil
}

// Sink receives a fully verified member. Implementations must make Commit
// atomic from the point of view of readers: either the complete content is
// visible or nothing is.
type Sink interface {
	Commit(key string, content io.Reader) error
}

// rangedReader serves a fixed payload window as bounded ranged ReadAt
// requests. Each Read maps to at most one chunk-sized range.
type rangedReader struct {
	store io.ReaderAt
	base  int64
	pos   int64
	limit int64
	chunk int64
}

func (rr *rangedReader) Read(p []byte) (int, error) {
	if rr.pos >= rr.limit {
		return 0, io.EOF
	}
	want := int64(len(p))
	if want > rr.chunk {
		want = rr.chunk
	}
	if remaining := rr.limit - rr.pos; want > remaining {
		want = remaining
	}
	buf := make([]byte, want)
	n, err := rr.store.ReadAt(buf, rr.base+rr.pos)
	rr.pos += int64(n)
	copy(p, buf[:n])
	if n == int(want) {
		return n, nil
	}
	if err == nil || err == io.EOF {
		return n, io.ErrUnexpectedEOF
	}
	return n, err
}

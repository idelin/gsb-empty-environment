package blkx

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"hash/crc64"
	"io"
)

// Target format "B" (version 2) wire layout:
//
//	Header:  magic "BLKB" (4) | version u8 | declaredLen u64 | metaLen u32 | meta
//	Block:   index u32 | payloadLen u32 | payloadCRC64 u64 | payload
//	Trailer: index 0xFFFFFFFF | totalLen u64 | contentCRC64 u64  ("complete")
//
// All integers are big-endian. The trailer is the only complete marker;
// a stream missing it is uncommitted and must be rejected.
const (
	MagicB   = "BLKB"
	VersionB = 2

	completeIndex = 0xFFFFFFFF
)

var crc64Table = crc64.MakeTable(crc64.ISO)

// BWriter emits a format-B stream block by block.
type BWriter struct {
	w        io.Writer
	declared uint64
	index    uint32
	written  uint64
	crc      hash.Hash64
	finished bool
}

// NewBWriter writes the header and returns a writer ready for blocks.
func NewBWriter(w io.Writer, meta []byte, declared uint64) (*BWriter, error) {
	if len(meta) > MaxMetaSize {
		return nil, fmt.Errorf("blkx: metadata too large: %d bytes", len(meta))
	}
	b := &BWriter{w: w, declared: declared, crc: crc64.New(crc64Table)}
	hdr := make([]byte, 0, 17+len(meta))
	hdr = append(hdr, MagicB...)
	hdr = append(hdr, VersionB)
	var num [8]byte
	binary.BigEndian.PutUint64(num[:], declared)
	hdr = append(hdr, num[:]...)
	binary.BigEndian.PutUint32(num[:4], uint32(len(meta)))
	hdr = append(hdr, num[:4]...)
	hdr = append(hdr, meta...)
	if _, err := b.w.Write(hdr); err != nil {
		return nil, fmt.Errorf("blkx: writing B header: %w", err)
	}
	return b, nil
}

// WriteBlock appends one data block with a monotonically increasing index.
func (b *BWriter) WriteBlock(p []byte) error {
	if b.finished {
		return errors.New("blkx: write after finish")
	}
	if len(p) > MaxBlockSize {
		return fmt.Errorf("blkx: block too large: %d bytes", len(p))
	}
	if b.written+uint64(len(p)) > b.declared {
		return fmt.Errorf("blkx: payload exceeds declared length %d", b.declared)
	}
	if b.index == completeIndex {
		return errors.New("blkx: too many blocks")
	}
	var hdr [16]byte
	binary.BigEndian.PutUint32(hdr[0:4], b.index)
	binary.BigEndian.PutUint32(hdr[4:8], uint32(len(p)))
	binary.BigEndian.PutUint64(hdr[8:16], crc64.Checksum(p, crc64Table))
	if _, err := b.w.Write(hdr[:]); err != nil {
		return fmt.Errorf("blkx: writing block header: %w", err)
	}
	if _, err := b.w.Write(p); err != nil {
		return fmt.Errorf("blkx: writing block payload: %w", err)
	}
	b.crc.Write(p)
	b.written += uint64(len(p))
	b.index++
	return nil
}

// Finish writes the complete marker. It is the commit point of the stream
// and fails if the payload does not match the declared length.
func (b *BWriter) Finish() error {
	if b.finished {
		return errors.New("blkx: finish called twice")
	}
	b.finished = true
	if b.written != b.declared {
		return fmt.Errorf("blkx: declared length %d != actual %d", b.declared, b.written)
	}
	var tail [20]byte
	binary.BigEndian.PutUint32(tail[0:4], completeIndex)
	binary.BigEndian.PutUint64(tail[4:12], b.written)
	binary.BigEndian.PutUint64(tail[12:20], b.crc.Sum64())
	if _, err := b.w.Write(tail[:]); err != nil {
		return fmt.Errorf("blkx: writing complete marker: %w", err)
	}
	return nil
}

// BReader parses a format-B stream with a bounded output quota.
type BReader struct {
	r           *bufio.Reader
	Meta        []byte
	DeclaredLen uint64
	quota       uint64
	crc         hash.Hash64
	read        uint64
	expectIndex uint32
	done        bool
}

// NewBReader reads the header and rejects streams whose declared length
// already exceeds maxOutput, before any payload is produced.
func NewBReader(r io.Reader, maxOutput uint64) (*BReader, error) {
	br := bufio.NewReader(r)
	magic := make([]byte, 4)
	if _, err := io.ReadFull(br, magic); err != nil {
		return nil, fmt.Errorf("blkx: reading B magic: %w", err)
	}
	if string(magic) != MagicB {
		return nil, fmt.Errorf("blkx: bad B magic %q", magic)
	}
	ver, err := br.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("blkx: reading B version: %w", err)
	}
	if ver != VersionB {
		return nil, fmt.Errorf("blkx: unsupported B version %d", ver)
	}
	var num [8]byte
	if _, err := io.ReadFull(br, num[:]); err != nil {
		return nil, fmt.Errorf("blkx: reading declared length: %w", err)
	}
	declared := binary.BigEndian.Uint64(num[:])
	if declared > maxOutput {
		return nil, fmt.Errorf("blkx: declared length %d exceeds output quota %d", declared, maxOutput)
	}
	if _, err := io.ReadFull(br, num[:4]); err != nil {
		return nil, fmt.Errorf("blkx: reading metadata length: %w", err)
	}
	metaLen := binary.BigEndian.Uint32(num[:4])
	if metaLen > MaxMetaSize {
		return nil, fmt.Errorf("blkx: metadata too large: %d bytes", metaLen)
	}
	meta := make([]byte, metaLen)
	if _, err := io.ReadFull(br, meta); err != nil {
		return nil, fmt.Errorf("blkx: reading metadata: %w", err)
	}
	return &BReader{r: br, Meta: meta, DeclaredLen: declared, quota: maxOutput, crc: crc64.New(crc64Table)}, nil
}

// NextBlock returns the next payload. The running total never exceeds the
// quota. io.EOF is returned only after the complete marker, total length,
// and content checksum have all been validated.
func (b *BReader) NextBlock() ([]byte, error) {
	if b.done {
		return nil, errors.New("blkx: read after complete")
	}
	var idxBuf [4]byte
	if _, err := io.ReadFull(b.r, idxBuf[:]); err != nil {
		return nil, fmt.Errorf("blkx: truncated stream, no complete marker: %w", err)
	}
	index := binary.BigEndian.Uint32(idxBuf[:])
	if index == completeIndex {
		var tail [16]byte
		if _, err := io.ReadFull(b.r, tail[:]); err != nil {
			return nil, fmt.Errorf("blkx: truncated complete marker: %w", err)
		}
		total := binary.BigEndian.Uint64(tail[0:8])
		wantCRC := binary.BigEndian.Uint64(tail[8:16])
		if total != b.read || total != b.DeclaredLen {
			return nil, fmt.Errorf("blkx: total length %d != read %d (declared %d)", total, b.read, b.DeclaredLen)
		}
		if wantCRC != b.crc.Sum64() {
			return nil, errors.New("blkx: final content checksum mismatch")
		}
		b.done = true
		return nil, io.EOF
	}
	if index != b.expectIndex {
		return nil, fmt.Errorf("blkx: block index %d out of order, expected %d", index, b.expectIndex)
	}
	var hdr [12]byte
	if _, err := io.ReadFull(b.r, hdr[:]); err != nil {
		return nil, fmt.Errorf("blkx: truncated block header: %w", err)
	}
	n := binary.BigEndian.Uint32(hdr[0:4])
	wantCRC := binary.BigEndian.Uint64(hdr[4:12])
	if n > MaxBlockSize {
		return nil, fmt.Errorf("blkx: block too large: %d bytes", n)
	}
	if b.read+uint64(n) > b.quota {
		return nil, fmt.Errorf("blkx: output quota %d exceeded", b.quota)
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(b.r, p); err != nil {
		return nil, fmt.Errorf("blkx: truncated block payload: %w", err)
	}
	if crc64.Checksum(p, crc64Table) != wantCRC {
		return nil, fmt.Errorf("blkx: block %d checksum mismatch", index)
	}
	b.crc.Write(p)
	b.read += uint64(n)
	b.expectIndex++
	return p, nil
}

// Decode streams a format-B input to dst, never emitting more than
// maxOutput bytes. It returns the preserved metadata and the number of
// payload bytes written.
func Decode(dst io.Writer, src io.Reader, maxOutput uint64) ([]byte, uint64, error) {
	br, err := NewBReader(src, maxOutput)
	if err != nil {
		return nil, 0, err
	}
	var total uint64
	for {
		blk, err := br.NextBlock()
		if err == io.EOF {
			return br.Meta, total, nil
		}
		if err != nil {
			return nil, total, err
		}
		if _, err := dst.Write(blk); err != nil {
			return nil, total, fmt.Errorf("blkx: writing decoded output: %w", err)
		}
		total += uint64(len(blk))
	}
}

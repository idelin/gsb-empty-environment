package blkx

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
)

// Source format "A" (version 1) wire layout:
//
//	Header:  magic "BLKA" (4) | version u8 | declaredLen u64 | metaLen u32 | meta
//	Block:   tag 0x01 | payloadLen u32 | payloadCRC32 u32 | payload
//	Trailer: tag 0x02 | contentCRC32 u32   (the "complete" marker)
//
// All integers are big-endian. A stream without the trailer is incomplete
// and must be rejected by readers.
const (
	MagicA   = "BLKA"
	VersionA = 1

	tagData     = 0x01
	tagComplete = 0x02

	// MaxBlockSize bounds a single block so a hostile length prefix
	// cannot force a huge allocation.
	MaxBlockSize = 64 << 20
	// MaxMetaSize bounds the metadata blob.
	MaxMetaSize = 1 << 20
)

// AWriter emits a format-A stream block by block.
type AWriter struct {
	w        io.Writer
	declared uint64
	written  uint64
	crc      hash.Hash32
	finished bool
}

// NewAWriter writes the header and returns a writer ready for blocks.
// declared is the total original length the stream promises to carry.
func NewAWriter(w io.Writer, meta []byte, declared uint64) (*AWriter, error) {
	if len(meta) > MaxMetaSize {
		return nil, fmt.Errorf("blkx: metadata too large: %d bytes", len(meta))
	}
	a := &AWriter{w: w, declared: declared, crc: crc32.NewIEEE()}
	hdr := make([]byte, 0, 17+len(meta))
	hdr = append(hdr, MagicA...)
	hdr = append(hdr, VersionA)
	var num [8]byte
	binary.BigEndian.PutUint64(num[:], declared)
	hdr = append(hdr, num[:]...)
	binary.BigEndian.PutUint32(num[:4], uint32(len(meta)))
	hdr = append(hdr, num[:4]...)
	hdr = append(hdr, meta...)
	if _, err := a.w.Write(hdr); err != nil {
		return nil, fmt.Errorf("blkx: writing A header: %w", err)
	}
	return a, nil
}

// WriteBlock appends one data block.
func (a *AWriter) WriteBlock(p []byte) error {
	if a.finished {
		return errors.New("blkx: write after finish")
	}
	if len(p) > MaxBlockSize {
		return fmt.Errorf("blkx: block too large: %d bytes", len(p))
	}
	if a.written+uint64(len(p)) > a.declared {
		return fmt.Errorf("blkx: payload exceeds declared length %d", a.declared)
	}
	var hdr [9]byte
	hdr[0] = tagData
	binary.BigEndian.PutUint32(hdr[1:5], uint32(len(p)))
	binary.BigEndian.PutUint32(hdr[5:9], crc32.ChecksumIEEE(p))
	if _, err := a.w.Write(hdr[:]); err != nil {
		return fmt.Errorf("blkx: writing block header: %w", err)
	}
	if _, err := a.w.Write(p); err != nil {
		return fmt.Errorf("blkx: writing block payload: %w", err)
	}
	a.crc.Write(p)
	a.written += uint64(len(p))
	return nil
}

// Finish writes the complete marker. It fails if the actual payload does
// not match the declared length, so a lying stream is never sealed.
func (a *AWriter) Finish() error {
	if a.finished {
		return errors.New("blkx: finish called twice")
	}
	a.finished = true
	if a.written != a.declared {
		return fmt.Errorf("blkx: declared length %d != actual %d", a.declared, a.written)
	}
	var tail [5]byte
	tail[0] = tagComplete
	binary.BigEndian.PutUint32(tail[1:5], a.crc.Sum32())
	if _, err := a.w.Write(tail[:]); err != nil {
		return fmt.Errorf("blkx: writing complete marker: %w", err)
	}
	return nil
}

// AReader parses a format-A stream block by block.
type AReader struct {
	r           *bufio.Reader
	Meta        []byte
	DeclaredLen uint64
	crc         hash.Hash32
	read        uint64
	done        bool
}

// NewAReader reads and validates the stream header.
func NewAReader(r io.Reader) (*AReader, error) {
	br := bufio.NewReader(r)
	magic := make([]byte, 4)
	if _, err := io.ReadFull(br, magic); err != nil {
		return nil, fmt.Errorf("blkx: reading A magic: %w", err)
	}
	if string(magic) != MagicA {
		return nil, fmt.Errorf("blkx: bad A magic %q", magic)
	}
	ver, err := br.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("blkx: reading A version: %w", err)
	}
	if ver != VersionA {
		return nil, fmt.Errorf("blkx: unsupported A version %d", ver)
	}
	var num [8]byte
	if _, err := io.ReadFull(br, num[:]); err != nil {
		return nil, fmt.Errorf("blkx: reading declared length: %w", err)
	}
	declared := binary.BigEndian.Uint64(num[:])
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
	return &AReader{r: br, Meta: meta, DeclaredLen: declared, crc: crc32.NewIEEE()}, nil
}

// NextBlock returns the next payload. It returns io.EOF only after the
// complete marker, the running content checksum, and the declared length
// have all been validated.
func (a *AReader) NextBlock() ([]byte, error) {
	if a.done {
		return nil, errors.New("blkx: read after complete")
	}
	tag, err := a.r.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("blkx: truncated stream, no complete marker: %w", err)
	}
	switch tag {
	case tagData:
		var hdr [8]byte
		if _, err := io.ReadFull(a.r, hdr[:]); err != nil {
			return nil, fmt.Errorf("blkx: truncated block header: %w", err)
		}
		n := binary.BigEndian.Uint32(hdr[0:4])
		wantCRC := binary.BigEndian.Uint32(hdr[4:8])
		if n > MaxBlockSize {
			return nil, fmt.Errorf("blkx: block too large: %d bytes", n)
		}
		p := make([]byte, n)
		if _, err := io.ReadFull(a.r, p); err != nil {
			return nil, fmt.Errorf("blkx: truncated block payload: %w", err)
		}
		if crc32.ChecksumIEEE(p) != wantCRC {
			return nil, fmt.Errorf("blkx: block checksum mismatch at offset %d", a.read)
		}
		a.crc.Write(p)
		a.read += uint64(n)
		if a.read > a.DeclaredLen {
			return nil, fmt.Errorf("blkx: payload exceeds declared length %d", a.DeclaredLen)
		}
		return p, nil
	case tagComplete:
		var crcBuf [4]byte
		if _, err := io.ReadFull(a.r, crcBuf[:]); err != nil {
			return nil, fmt.Errorf("blkx: truncated complete marker: %w", err)
		}
		if binary.BigEndian.Uint32(crcBuf[:]) != a.crc.Sum32() {
			return nil, errors.New("blkx: final content checksum mismatch")
		}
		if a.read != a.DeclaredLen {
			return nil, fmt.Errorf("blkx: declared length %d != actual %d", a.DeclaredLen, a.read)
		}
		a.done = true
		return nil, io.EOF
	default:
		return nil, fmt.Errorf("blkx: unknown block tag 0x%02x", tag)
	}
}

package blkconv

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"hash/crc64"
	"io"
)

// Format identifies a block wire encoding.
type Format int

const (
	// FormatA: fixed u32 fields, CRC-32 block checksums, RLE-A payloads.
	FormatA Format = iota
	// FormatB: uvarint fields, CRC-64 block checksums, RLE-B payloads.
	FormatB
)

func (f Format) String() string {
	switch f {
	case FormatA:
		return "A"
	case FormatB:
		return "B"
	}
	return "?"
}

// ParseFormat parses "a" or "b" (case-insensitive).
func ParseFormat(s string) (Format, error) {
	switch s {
	case "a", "A":
		return FormatA, nil
	case "b", "B":
		return FormatB, nil
	}
	return 0, fmt.Errorf("blkconv: unknown format %q (want a|b)", s)
}

const (
	// Version is the wire format version, stored in every header.
	Version = 1
	// BlockSize is the maximum plaintext bytes carried by one block.
	BlockSize = 1 << 16
	// MaxMetaLen bounds the JSON metadata blob in the header.
	MaxMetaLen = 1 << 15
	// DefaultMaxDecode is the default decode output budget (1 GiB).
	DefaultMaxDecode = 1 << 30

	tagBlock   = 0x01
	tagTrailer = 0x02

	headerFixedLen = 4 + 1 + 1 + 2 + 8 + 32 // magic, ver, flags, metaLen, origLen, digest
	// maxBlockWire is kept for reference; per-format bounds are in maxWireLen.
)

var (
	magicA = [4]byte{'B', 'L', 'K', 'A'}
	magicB = [4]byte{'B', 'L', 'K', 'B'}

	markerA = [4]byte{'C', 'M', 'T', '1'}
	markerB = [4]byte{'C', 'M', 'T', '2'}

	crc64Table = crc64.MakeTable(crc64.ISO)
)

var (
	ErrBadMagic       = errors.New("blkconv: bad magic")
	ErrVersion        = errors.New("blkconv: unsupported format version")
	ErrCorrupt        = errors.New("blkconv: corrupt stream")
	ErrNoCommit       = errors.New("blkconv: missing commit marker (incomplete stream)")
	ErrLengthMismatch = errors.New("blkconv: declared original length does not match actual")
	ErrDigestMismatch = errors.New("blkconv: content digest mismatch")
	ErrBudget         = errors.New("blkconv: decode output budget exceeded")
)

func (f Format) magic() [4]byte {
	if f == FormatA {
		return magicA
	}
	return magicB
}

func (f Format) marker() [4]byte {
	if f == FormatA {
		return markerA
	}
	return markerB
}

func (f Format) newSum() hash.Hash {
	if f == FormatA {
		return crc32.NewIEEE()
	}
	return crc64.New(crc64Table)
}

// Header is the stream preamble: version, metadata, declared original
// length and the SHA-256 digest of the plaintext.
type Header struct {
	Format  Format
	Meta    map[string]string
	OrigLen uint64
	Digest  [32]byte
}

func writeHeader(w io.Writer, h *Header) error {
	meta := h.Meta
	if meta == nil {
		meta = map[string]string{}
	}
	blob, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if len(blob) > MaxMetaLen {
		return fmt.Errorf("blkconv: metadata too large (%d > %d)", len(blob), MaxMetaLen)
	}
	buf := make([]byte, 0, headerFixedLen+len(blob))
	m := h.Format.magic()
	buf = append(buf, m[:]...)
	buf = append(buf, Version, 0) // version, flags
	var tmp [8]byte
	binary.BigEndian.PutUint16(tmp[:2], uint16(len(blob)))
	buf = append(buf, tmp[:2]...)
	binary.BigEndian.PutUint64(tmp[:8], h.OrigLen)
	buf = append(buf, tmp[:8]...)
	buf = append(buf, h.Digest[:]...)
	buf = append(buf, blob...)
	_, err = w.Write(buf)
	return err
}

// readHeader parses a header and also returns the exact wire bytes so the
// caller can seed the running stream checksum.
func readHeader(r io.Reader) (*Header, []byte, error) {
	fixed := make([]byte, headerFixedLen)
	if _, err := io.ReadFull(r, fixed); err != nil {
		return nil, nil, fmt.Errorf("%w: reading header: %v", ErrNoCommit, err)
	}
	var f Format
	switch string(fixed[0:4]) {
	case string(magicA[:]):
		f = FormatA
	case string(magicB[:]):
		f = FormatB
	default:
		return nil, nil, ErrBadMagic
	}
	if fixed[4] != Version {
		return nil, nil, fmt.Errorf("%w: got %d", ErrVersion, fixed[4])
	}
	if fixed[5] != 0 {
		return nil, nil, fmt.Errorf("%w: unknown header flags 0x%02x", ErrCorrupt, fixed[5])
	}
	metaLen := int(binary.BigEndian.Uint16(fixed[6:8]))
	blob := make([]byte, metaLen)
	if _, err := io.ReadFull(r, blob); err != nil {
		return nil, nil, fmt.Errorf("%w: reading metadata: %v", ErrNoCommit, err)
	}
	h := &Header{Format: f, OrigLen: binary.BigEndian.Uint64(fixed[8:16])}
	copy(h.Digest[:], fixed[16:48])
	if err := json.Unmarshal(blob, &h.Meta); err != nil {
		return nil, nil, fmt.Errorf("%w: metadata JSON: %v", ErrCorrupt, err)
	}
	raw := make([]byte, 0, headerFixedLen+metaLen)
	raw = append(raw, fixed...)
	raw = append(raw, blob...)
	return h, raw, nil
}

// writeBlock emits one tagged block record: tag, lengths, per-block
// checksum of the *plaintext*, then the encoded payload.
func writeBlock(w io.Writer, f Format, raw, enc []byte) error {
	hdr := []byte{tagBlock}
	switch f {
	case FormatA:
		var b [12]byte
		binary.BigEndian.PutUint32(b[0:4], uint32(len(enc)))
		binary.BigEndian.PutUint32(b[4:8], uint32(len(raw)))
		binary.BigEndian.PutUint32(b[8:12], crc32.ChecksumIEEE(raw))
		hdr = append(hdr, b[:]...)
	case FormatB:
		hdr = binary.AppendUvarint(hdr, uint64(len(enc)))
		hdr = binary.AppendUvarint(hdr, uint64(len(raw)))
		var c [8]byte
		binary.BigEndian.PutUint64(c[:], crc64.Checksum(raw, crc64Table))
		hdr = append(hdr, c[:]...)
	}
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	_, err := w.Write(enc)
	return err
}

// writeTrailer emits the commit marker. Everything except the final
// checksum goes through tee (which feeds the running stream checksum);
// the checksum itself is written last, directly to raw.
func writeTrailer(tee, raw io.Writer, f Format, sum hash.Hash, blocks uint64) error {
	buf := []byte{tagTrailer}
	m := f.marker()
	buf = append(buf, m[:]...)
	if f == FormatA {
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], uint32(blocks))
		buf = append(buf, b[:]...)
	} else {
		buf = binary.AppendUvarint(buf, blocks)
	}
	if _, err := tee.Write(buf); err != nil {
		return err
	}
	_, err := raw.Write(sum.Sum(nil))
	return err
}

// maxWireLen bounds one encoded block on the wire. RLE-A expands by at
// most 1/128 (one token per 128 literals; a literal run only ends early
// at a 3+ run, which itself shrinks). RLE-B expands by at most 4/3
// (worst case: a 2-byte run token followed by a 1-byte literal token,
// 4 bytes out for 3 bytes in).
func maxWireLen(f Format) uint64 {
	if f == FormatA {
		return BlockSize + BlockSize/128 + 8
	}
	return BlockSize*4/3 + 16
}

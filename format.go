package bwt

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
)

// On-disk container format (all integers little-endian):
//
//	offset  size  field
//	0       4     magic "BWT1"
//	4       2     format version (currently 1)
//	6       2     flags (must be 0)
//	8       8     original block length n
//	16      8     primary index (0 <= primary < n; 0 when n == 0)
//	24      4     CRC-32 (IEEE) of the original block
//	28      1024  symbol counts: counts[b] = occurrences of byte b
//	1052    n     BWT payload
//
// The empty block is defined as n == 0, primary == 0, CRC 0, all counts 0,
// and no payload; it decodes to a zero-length output.
const (
	magicString = "BWT1"
	// Version is the current format version.
	Version    uint16 = 1
	headerSize        = 4 + 2 + 2 + 8 + 8 + 4 + 256*4
)

var (
	// ErrBadMagic indicates the input is not a BWT container.
	ErrBadMagic = errors.New("bwt: bad magic")
	// ErrUnsupportedVersion indicates an unknown format version.
	ErrUnsupportedVersion = errors.New("bwt: unsupported format version")
	// ErrTooLarge indicates the declared length exceeds the decode budget.
	ErrTooLarge = errors.New("bwt: declared length exceeds output limit")
	// ErrCorrupt indicates inconsistent or truncated container data.
	ErrCorrupt = errors.New("bwt: corrupt container")
	// ErrChecksum indicates the decoded data failed the CRC-32 check.
	ErrChecksum = errors.New("bwt: checksum mismatch")
)

// Write encodes src as a BWT container and writes it to w.
func Write(w io.Writer, src []byte) error {
	payload, primary := Encode(src)
	var hdr [headerSize]byte
	copy(hdr[0:4], magicString)
	binary.LittleEndian.PutUint16(hdr[4:6], Version)
	// hdr[6:8] flags: zero
	binary.LittleEndian.PutUint64(hdr[8:16], uint64(len(src)))
	binary.LittleEndian.PutUint64(hdr[16:24], uint64(primary))
	binary.LittleEndian.PutUint32(hdr[24:28], crc32.ChecksumIEEE(src))
	var counts [256]uint32
	for _, b := range src {
		counts[b]++
	}
	for i := 0; i < 256; i++ {
		binary.LittleEndian.PutUint32(hdr[28+i*4:32+i*4], counts[i])
	}
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// Read decodes a BWT container from r. The declared block length must not
// exceed maxLen, bounding the decode output; otherwise ErrTooLarge is
// returned before any payload is read. Header counts, primary index and the
// CRC-32 of the decoded block are all verified.
func Read(r io.Reader, maxLen uint64) ([]byte, error) {
	var hdr [headerSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, ErrCorrupt
	}
	if string(hdr[0:4]) != magicString {
		return nil, ErrBadMagic
	}
	if binary.LittleEndian.Uint16(hdr[4:6]) != Version {
		return nil, ErrUnsupportedVersion
	}
	if binary.LittleEndian.Uint16(hdr[6:8]) != 0 {
		return nil, ErrCorrupt
	}
	n := binary.LittleEndian.Uint64(hdr[8:16])
	primary := binary.LittleEndian.Uint64(hdr[16:24])
	crc := binary.LittleEndian.Uint32(hdr[24:28])
	var counts [256]uint32
	var sum uint64
	for i := 0; i < 256; i++ {
		counts[i] = binary.LittleEndian.Uint32(hdr[28+i*4 : 32+i*4])
		sum += uint64(counts[i])
	}
	if sum != n {
		return nil, ErrCorrupt
	}
	if n > maxLen {
		return nil, ErrTooLarge
	}
	if n == 0 {
		// Empty block: canonical form requires primary 0 and CRC 0.
		if primary != 0 || crc != 0 {
			return nil, ErrCorrupt
		}
		return []byte{}, nil
	}
	if primary >= n {
		return nil, ErrCorrupt
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, ErrCorrupt
	}
	var got [256]uint32
	for _, b := range payload {
		got[b]++
	}
	if got != counts {
		return nil, ErrCorrupt
	}
	out, err := Decode(payload, int(primary))
	if err != nil {
		return nil, err
	}
	if crc32.ChecksumIEEE(out) != crc {
		return nil, ErrChecksum
	}
	return out, nil
}

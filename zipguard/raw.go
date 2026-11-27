package zipguard

import (
	"encoding/binary"
	"fmt"
)

// On-disk signatures (little endian).
const (
	sigLFH  uint32 = 0x04034b50
	sigCDH  uint32 = 0x02014b50
	sigEOCD uint32 = 0x06054b50
	sigDD   uint32 = 0x08074b50
	sigZ64  uint32 = 0x06064b50
	sigZ64L uint32 = 0x07064b50
)

// Fixed header sizes.
const (
	lfhFixedSize  = 30
	cdhFixedSize  = 46
	eocdFixedSize = 22
	ddFixedSize   = 12
	z64eocdFixed  = 56
	z64locFixed   = 20
)

const (
	methodStored  uint16 = 0
	methodDeflate uint16 = 8
)

// General purpose bit flag bits relevant to validation.
const (
	flagDataDescriptor uint16 = 1 << 3
	flagUTF8           uint16 = 1 << 11
)

// Sentinel values that trigger ZIP64 lookups.
const (
	u32Sentinel = 0xFFFFFFFF
	u16Sentinel = 0xFFFF
)

type eocd struct {
	diskNumber      uint16
	cdDiskNumber    uint16
	diskEntries     uint16
	totalEntries    uint16
	cdSize          uint32
	cdOffset        uint32
	commentLength   uint16
	offset          int64
	zip64           bool
	z64TotalEntries uint64
	z64CDSize       uint64
	z64CDOffset     uint64
}

type cdh struct {
	versionMadeBy    uint16
	hostSystem       uint8
	versionNeeded    uint16
	flags            uint16
	method           uint16
	modTime          uint16
	modDate          uint16
	crc32            uint32
	compressedSize   uint64
	uncompressedSize uint64
	name             string
	extra            []byte
	comment          []byte
	externalAttrs    uint32
	localOffset      uint64
	headerOffset     int64
}

type lfh struct {
	flags            uint16
	method           uint16
	crc32            uint32
	compressedSize   uint64
	uncompressedSize uint64
	name             string
	extra            []byte
	headerOffset     int64
	dataOffset       int64
}

// findEOCD locates the single terminating EOCD by scanning the raw tail.
// Any additional EOCD signature (including one embedded inside the EOCD
// comment or earlier payload) is treated as a forgery attempt.
func findEOCD(b []byte) (*eocd, error) {
	n := int64(len(b))
	if n < eocdFixedSize {
		return nil, ErrEOCDNotFound
	}
	limit := n - eocdFixedSize - 65535
	if limit < 0 {
		limit = 0
	}

	// Locate the terminating, structurally complete EOCD first. Only when one
	// exists can an earlier EOCD signature be classified as a forgery;
	// otherwise the archive is simply truncated or carries trailing bytes.
	terminating := int64(-1)
	for i := n - eocdFixedSize; i >= 0; i-- {
		if binary.LittleEndian.Uint32(b[i:]) != sigEOCD {
			continue
		}
		commentLen := int64(binary.LittleEndian.Uint16(b[i+20:]))
		if i+eocdFixedSize+commentLen <= n &&
			i+eocdFixedSize+commentLen == n {
			terminating = i
			break
		}
	}
	if terminating >= 0 {
		for i := int64(0); i+eocdFixedSize <= terminating; i++ {
			if binary.LittleEndian.Uint32(b[i:]) == sigEOCD {
				return nil, fmt.Errorf("%w: forged EOCD signature at offset %d", ErrMultipleEOCD, i)
			}
		}
	}

	type hit struct {
		off        int64
		good       bool
		terminates bool
		e          *eocd
	}
	var hits []hit
	for i := n - eocdFixedSize; i >= limit; i-- {
		if binary.LittleEndian.Uint32(b[i:]) != sigEOCD {
			continue
		}
		e, good, terminates := parseEOCDAt(b, i)
		hits = append(hits, hit{off: i, good: good, terminates: terminates, e: e})
		if good {
			break
		}
	}

	var goodHits []hit
	for _, h := range hits {
		if h.good {
			goodHits = append(goodHits, h)
		}
	}
	if len(goodHits) == 0 {
		// A structurally complete EOCD followed by extra bytes is a trailing
		// data violation rather than a missing record.
		for _, h := range hits {
			if h.terminates {
				return nil, fmt.Errorf("%w: %d byte(s) after EOCD",
					ErrTrailingData, n-(h.off+eocdFixedSize+int64(h.e.commentLength)))
			}
		}
		return nil, ErrEOCDNotFound
	}
	if len(goodHits) > 1 {
		return nil, fmt.Errorf("%w: %d candidate EOCD records", ErrMultipleEOCD, len(goodHits))
	}

	e := goodHits[0].e
	commentEnd := e.offset + eocdFixedSize + int64(e.commentLength)
	for _, h := range hits {
		if h.off > e.offset && h.off < commentEnd {
			return nil, fmt.Errorf("%w: EOCD signature inside comment at offset %d", ErrMultipleEOCD, h.off)
		}
	}
	if commentEnd != n {
		return nil, fmt.Errorf("%w: %d byte(s) after EOCD", ErrTrailingData, n-commentEnd)
	}
	if e.diskNumber != 0 || e.cdDiskNumber != 0 {
		return nil, ErrSplitArchive
	}

	if e.cdOffset == u32Sentinel || e.cdSize == u32Sentinel ||
		e.totalEntries == u16Sentinel || e.diskEntries == u16Sentinel {
		if err := parseZip64EOCD(b, e); err != nil {
			return nil, err
		}
	}
	return e, nil
}

func parseEOCDAt(b []byte, off int64) (*eocd, bool, bool) {
	if off+eocdFixedSize > int64(len(b)) {
		return nil, false, false
	}
	commentLen := binary.LittleEndian.Uint16(b[off+20:])
	end := off + eocdFixedSize + int64(commentLen)
	if end > int64(len(b)) {
		return nil, false, false
	}
	e := &eocd{
		diskNumber:    binary.LittleEndian.Uint16(b[off+4:]),
		cdDiskNumber:  binary.LittleEndian.Uint16(b[off+6:]),
		diskEntries:   binary.LittleEndian.Uint16(b[off+8:]),
		totalEntries:  binary.LittleEndian.Uint16(b[off+10:]),
		cdSize:        binary.LittleEndian.Uint32(b[off+12:]),
		cdOffset:      binary.LittleEndian.Uint32(b[off+16:]),
		commentLength: commentLen,
		offset:        off,
	}
	return e, end == int64(len(b)), end <= int64(len(b))
}

func parseZip64EOCD(b []byte, e *eocd) error {
	if e.offset < z64locFixed {
		return fmt.Errorf("%w: truncated zip64 locator", ErrTruncated)
	}
	locOff := e.offset - z64locFixed
	if binary.LittleEndian.Uint32(b[locOff:]) != sigZ64L {
		return fmt.Errorf("%w: zip64 sentinel without locator", ErrBadSignature)
	}
	if binary.LittleEndian.Uint32(b[locOff+4:]) != 0 {
		return ErrSplitArchive
	}
	z64Off := int64(binary.LittleEndian.Uint64(b[locOff+8:]))
	if binary.LittleEndian.Uint32(b[locOff+16:]) != 1 {
		return ErrSplitArchive
	}
	if z64Off < 0 || z64Off+z64eocdFixed > int64(len(b)) || z64Off >= locOff {
		return fmt.Errorf("%w: zip64 EOCD out of bounds", ErrTruncated)
	}
	if binary.LittleEndian.Uint32(b[z64Off:]) != sigZ64 {
		return fmt.Errorf("%w: bad zip64 EOCD signature", ErrBadSignature)
	}
	recordSize := binary.LittleEndian.Uint64(b[z64Off+4:])
	if z64Off+int64(12+recordSize) != locOff {
		return fmt.Errorf("%w: zip64 EOCD record size mismatch", ErrBadSignature)
	}
	if binary.LittleEndian.Uint16(b[z64Off+12:]) != 45 ||
		binary.LittleEndian.Uint16(b[z64Off+14:]) != 45 {
		return fmt.Errorf("%w: zip64 version fields", ErrUnsupported)
	}
	if binary.LittleEndian.Uint32(b[z64Off+16:]) != 0 ||
		binary.LittleEndian.Uint32(b[z64Off+20:]) != 0 {
		return ErrSplitArchive
	}
	e.zip64 = true
	e.z64TotalEntries = binary.LittleEndian.Uint64(b[z64Off+32:])
	e.z64CDSize = binary.LittleEndian.Uint64(b[z64Off+40:])
	e.z64CDOffset = binary.LittleEndian.Uint64(b[z64Off+48:])
	return nil
}

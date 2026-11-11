package safezip

import (
	"encoding/binary"
	"math"
)

// Zip structure signatures and constants from APPNOTE.TXT.
const (
	sigLocal        = 0x04034b50
	sigCentral      = 0x02014b50
	sigEOCD         = 0x06054b50
	sigZip64EOCD    = 0x06064b50
	sigZip64Locator = 0x07064b50
	sigDataDesc     = 0x08074b50

	extraZip64         = 0x0001
	flagDataDescriptor = 1 << 3
	methodStore        = 0
	methodDeflate      = 8
	msdosDirectoryAttr = 0x10
	maxEOCDComment     = 1 << 16
	eocdFixedSize      = 22
	locatorSize        = 20
	zip64EOCDFixedSize = 56
	localHeaderFixed   = 30
	centralHeaderFixed = 46
)

type eocdInfo struct {
	cdOffset int64
	cdSize   int64
	entries  int64
}

type centralEntry struct {
	name       string
	flags      uint16
	method     uint16
	crc        uint32
	compSize   int64
	uncompSize int64
	localOff   int64
	extAttr    uint32
	hasZip64   bool
}

// readEOCD locates the end-of-central-directory record, following the
// ZIP64 locator when the classic record carries 0xFFFFFFFF placeholders.
func readEOCD(st Store, size int64) (*eocdInfo, error) {
	tailLen := size
	if tailLen > int64(maxEOCDComment+eocdFixedSize) {
		tailLen = int64(maxEOCDComment + eocdFixedSize)
	}
	tail := make([]byte, tailLen)
	tailBase := size - tailLen
	if err := readFullAt(st, tailBase, tail); err != nil {
		return nil, err
	}

	pos := -1
	for i := len(tail) - eocdFixedSize; i >= 0; i-- {
		if binary.LittleEndian.Uint32(tail[i:]) == sigEOCD {
			pos = i
			break
		}
	}
	if pos < 0 {
		return nil, ErrFormat
	}
	rec := tail[pos:]
	commentLen := int(binary.LittleEndian.Uint16(rec[20:]))
	if pos+eocdFixedSize+commentLen > len(tail) {
		return nil, ErrFormat
	}
	disk := binary.LittleEndian.Uint16(rec[4:])
	cdDisk := binary.LittleEndian.Uint16(rec[6:])
	entriesDisk := binary.LittleEndian.Uint16(rec[8:])
	entriesTotal := binary.LittleEndian.Uint16(rec[10:])
	cdSize32 := int64(binary.LittleEndian.Uint32(rec[12:]))
	cdOffset32 := int64(binary.LittleEndian.Uint32(rec[16:]))

	eocdAbs := tailBase + int64(pos)
	info := &eocdInfo{
		cdOffset: cdOffset32,
		cdSize:   cdSize32,
		entries:  int64(entriesTotal),
	}

	zip64 := cdSize32 == math.MaxUint32 || cdOffset32 == math.MaxUint32
	if !zip64 {
		if disk != 0 || cdDisk != 0 ||
			entriesDisk != entriesTotal || entriesDisk == math.MaxUint16 {
			return nil, ErrUnsupported
		}
		if info.cdOffset < 0 || info.cdSize < 0 || info.cdOffset+info.cdSize != eocdAbs {
			return nil, ErrFormat
		}
		return info, nil
	}

	// The ZIP64 end-of-central-directory locator sits directly before EOCD.
	if eocdAbs < locatorSize {
		return nil, ErrFormat
	}
	loc := make([]byte, locatorSize)
	if err := readFullAt(st, eocdAbs-locatorSize, loc); err != nil {
		return nil, err
	}
	if binary.LittleEndian.Uint32(loc[0:]) != sigZip64Locator {
		return nil, ErrFormat
	}
	if binary.LittleEndian.Uint32(loc[4:]) != 0 { // disk with ZIP64 EOCD
		return nil, ErrUnsupported
	}
	recordOff := int64(binary.LittleEndian.Uint64(loc[8:]))
	if binary.LittleEndian.Uint32(loc[16:]) != 1 {
		return nil, ErrUnsupported
	}

	rec64 := make([]byte, zip64EOCDFixedSize)
	if err := readFullAt(st, recordOff, rec64); err != nil {
		return nil, err
	}
	if binary.LittleEndian.Uint32(rec64[0:]) != sigZip64EOCD {
		return nil, ErrFormat
	}
	// Layout after the 4-byte signature: 8-byte record size (4), version
	// fields at 12/14, disk number (16), disk with CD start (20), entries
	// on this disk (24), total entries (32), CD size (40), CD offset (48).
	if binary.LittleEndian.Uint32(rec64[16:]) != 0 || binary.LittleEndian.Uint32(rec64[20:]) != 0 {
		return nil, ErrUnsupported
	}
	entriesDisk64 := int64(binary.LittleEndian.Uint64(rec64[24:]))
	entriesTotal64 := int64(binary.LittleEndian.Uint64(rec64[32:]))
	cdSize64 := int64(binary.LittleEndian.Uint64(rec64[40:]))
	cdOffset64 := int64(binary.LittleEndian.Uint64(rec64[48:]))
	if entriesDisk64 != entriesTotal64 || entriesDisk64 < 0 {
		return nil, ErrFormat
	}
	if cdOffset64 < 0 || cdSize64 < 0 ||
		recordOff+int64(zip64EOCDFixedSize) > eocdAbs-locatorSize {
		return nil, ErrFormat
	}
	if cdOffset64+cdSize64 != recordOff {
		return nil, ErrFormat
	}
	info.cdOffset = cdOffset64
	info.cdSize = cdSize64
	info.entries = entriesTotal64
	return info, nil
}

// parseCentralDirectory reads and parses every central directory file header.
func parseCentralDirectory(st Store, info *eocdInfo, limits Limits) ([]*centralEntry, error) {
	if limits.MaxMetadata > 0 && info.cdSize > limits.MaxMetadata {
		return nil, ErrDeclaredTooLong
	}
	buf := make([]byte, info.cdSize)
	if err := readFullAt(st, info.cdOffset, buf); err != nil {
		return nil, err
	}

	entries := make([]*centralEntry, 0, info.entries)
	cur := 0
	for idx := int64(0); idx < info.entries; idx++ {
		if cur+centralHeaderFixed > len(buf) {
			return nil, ErrTruncated
		}
		h := buf[cur:]
		if binary.LittleEndian.Uint32(h[0:]) != sigCentral {
			return nil, ErrFormat
		}
		nameLen := int(binary.LittleEndian.Uint16(h[28:]))
		extraLen := int(binary.LittleEndian.Uint16(h[30:]))
		commentLen := int(binary.LittleEndian.Uint16(h[32:]))
		total := centralHeaderFixed + nameLen + extraLen + commentLen
		if cur+total > len(buf) {
			return nil, ErrTruncated
		}
		name := string(h[centralHeaderFixed : centralHeaderFixed+nameLen])
		extra := h[centralHeaderFixed+nameLen : centralHeaderFixed+nameLen+extraLen]

		e := &centralEntry{
			name:       name,
			flags:      binary.LittleEndian.Uint16(h[8:]),
			method:     binary.LittleEndian.Uint16(h[10:]),
			crc:        binary.LittleEndian.Uint32(h[16:]),
			compSize:   int64(binary.LittleEndian.Uint32(h[20:])),
			uncompSize: int64(binary.LittleEndian.Uint32(h[24:])),
			localOff:   int64(binary.LittleEndian.Uint32(h[42:])),
			extAttr:    binary.LittleEndian.Uint32(h[38:]),
		}
		z64, err := parseCentralExtra(extra)
		if err != nil {
			return nil, err
		}
		if z64 != nil {
			e.hasZip64 = true
			if e.uncompSize == math.MaxUint32 {
				if !z64.hasUncomp {
					return nil, ErrFormat
				}
				e.uncompSize = z64.uncomp
			}
			if e.compSize == math.MaxUint32 {
				if !z64.hasComp {
					return nil, ErrFormat
				}
				e.compSize = z64.comp
			}
			if e.localOff == math.MaxUint32 {
				if !z64.hasOffset {
					return nil, ErrFormat
				}
				e.localOff = z64.offset
			}
		}
		if e.compSize < 0 || e.uncompSize < 0 || e.localOff < 0 {
			return nil, ErrFormat
		}
		entries = append(entries, e)
		cur += total
	}
	if cur != len(buf) {
		return nil, ErrFormat
	}
	return entries, nil
}

package zipguard

import (
	"encoding/binary"
	"fmt"
)

// interval is a fully referenced, half-open byte range [start, end).
type interval struct {
	start int64
	end   int64
	kind  string
}

// parseLFH parses the local file header referenced by c, verifies every
// shared declaration against the CDH and returns the parsed header together
// with the half-open interval occupied by header + payload (+ data
// descriptor).
func parseLFH(b []byte, c *cdh) (*lfh, interval, error) {
	off := int64(c.localOffset)
	if off < 0 || off+lfhFixedSize > int64(len(b)) {
		return nil, interval{}, fmt.Errorf("%w: LFH for %q out of bounds", ErrTruncated, c.name)
	}
	if binary.LittleEndian.Uint32(b[off:]) != sigLFH {
		return nil, interval{}, fmt.Errorf("%w: expected LFH for %q at offset %d", ErrBadSignature, c.name, off)
	}
	l := &lfh{
		headerOffset: off,
		flags:        binary.LittleEndian.Uint16(b[off+6:]),
		method:       binary.LittleEndian.Uint16(b[off+8:]),
		crc32:        binary.LittleEndian.Uint32(b[off+14:]),
	}
	cSize32 := binary.LittleEndian.Uint32(b[off+18:])
	uSize32 := binary.LittleEndian.Uint32(b[off+22:])
	l.compressedSize = uint64(cSize32)
	l.uncompressedSize = uint64(uSize32)
	nameLen := int(binary.LittleEndian.Uint16(b[off+26:]))
	extraLen := int(binary.LittleEndian.Uint16(b[off+28:]))

	tail := off + lfhFixedSize
	if int64(nameLen+extraLen) > int64(len(b))-tail {
		return nil, interval{}, fmt.Errorf("%w: LFH for %q variable fields", ErrTruncated, c.name)
	}
	l.name = string(b[tail : tail+int64(nameLen)])
	l.extra = append([]byte(nil), b[tail+int64(nameLen):tail+int64(nameLen+extraLen)]...)

	if cSize32 == u32Sentinel || uSize32 == u32Sentinel {
		z, err := parseZ64Extra(l.extra)
		if err != nil {
			return nil, interval{}, err
		}
		if cSize32 == u32Sentinel {
			l.compressedSize = z.compressedSize
		}
		if uSize32 == u32Sentinel {
			l.uncompressedSize = z.uncompressedSize
		}
	}

	// Hard LFH/CDH consistency checks. When the data-descriptor flag (bit 3)
	// is set the LFH is allowed to zero crc/size fields; every other field
	// must still match exactly.
	if l.name != c.name {
		return nil, interval{}, fmt.Errorf("%w: %q name (LFH %q vs CDH %q)", ErrHeaderMismatch, c.name, l.name, c.name)
	}
	if l.method != c.method {
		return nil, interval{}, fmt.Errorf("%w: %q method (LFH %d vs CDH %d)", ErrHeaderMismatch, c.name, l.method, c.method)
	}
	if l.flags != c.flags {
		return nil, interval{}, fmt.Errorf("%w: %q general purpose flags (LFH %#04x vs CDH %#04x)", ErrHeaderMismatch, c.name, l.flags, c.flags)
	}
	hasDD := c.flags&flagDataDescriptor != 0
	if !hasDD {
		if l.crc32 != c.crc32 {
			return nil, interval{}, fmt.Errorf("%w: %q crc-32 (LFH %#08x vs CDH %#08x)", ErrHeaderMismatch, c.name, l.crc32, c.crc32)
		}
		if l.compressedSize != c.compressedSize {
			return nil, interval{}, fmt.Errorf("%w: %q compressed size (LFH %d vs CDH %d)", ErrHeaderMismatch, c.name, l.compressedSize, c.compressedSize)
		}
		if l.uncompressedSize != c.uncompressedSize {
			return nil, interval{}, fmt.Errorf("%w: %q uncompressed size (LFH %d vs CDH %d)", ErrHeaderMismatch, c.name, l.uncompressedSize, c.uncompressedSize)
		}
	}

	l.dataOffset = tail + int64(nameLen+extraLen)
	payloadEnd := l.dataOffset + int64(c.compressedSize)
	if payloadEnd > int64(len(b)) {
		return nil, interval{}, fmt.Errorf("%w: %q payload overruns archive", ErrTruncated, c.name)
	}

	regionEnd := payloadEnd
	if hasDD {
		ddLen, err := verifyDataDescriptor(b, c, payloadEnd)
		if err != nil {
			return nil, interval{}, err
		}
		regionEnd = payloadEnd + int64(ddLen)
		if regionEnd > int64(len(b)) {
			return nil, interval{}, fmt.Errorf("%w: %q data descriptor overruns archive", ErrTruncated, c.name)
		}
	}
	return l, interval{start: off, end: regionEnd, kind: "entry:" + c.name}, nil
}

// verifyDataDescriptor validates the optional-signature data descriptor that
// follows the payload and returns its on-disk length (12 or 16 bytes).
func verifyDataDescriptor(b []byte, c *cdh, payloadEnd int64) (int, error) {
	withSig := payloadEnd + ddFixedSize + 4
	noSig := payloadEnd + ddFixedSize
	if withSig <= int64(len(b)) && binary.LittleEndian.Uint32(b[payloadEnd:]) == sigDD {
		ddCRC := binary.LittleEndian.Uint32(b[payloadEnd+4:])
		ddCSize := binary.LittleEndian.Uint32(b[payloadEnd+8:])
		ddUSize := binary.LittleEndian.Uint32(b[payloadEnd+12:])
		if ddCRC != c.crc32 || uint64(ddCSize) != c.compressedSize || uint64(ddUSize) != c.uncompressedSize {
			return 0, fmt.Errorf("%w: %q descriptor fields do not match CDH", ErrBadDescriptor, c.name)
		}
		return ddFixedSize + 4, nil
	}
	if noSig > int64(len(b)) {
		return 0, fmt.Errorf("%w: %q missing data descriptor", ErrBadDescriptor, c.name)
	}
	ddCRC := binary.LittleEndian.Uint32(b[payloadEnd:])
	ddCSize := binary.LittleEndian.Uint32(b[payloadEnd+4:])
	ddUSize := binary.LittleEndian.Uint32(b[payloadEnd+8:])
	if ddCRC != c.crc32 || uint64(ddCSize) != c.compressedSize || uint64(ddUSize) != c.uncompressedSize {
		return 0, fmt.Errorf("%w: %q descriptor fields do not match CDH", ErrBadDescriptor, c.name)
	}
	return ddFixedSize, nil
}

// checkCoverage requires that the sorted intervals cover every byte of the
// archive without overlap and without unreferenced gaps.
func checkCoverage(ivs []interval, archiveSize int64) error {
	for i := 1; i < len(ivs); i++ {
		prev, cur := ivs[i-1], ivs[i]
		if cur.start < prev.end {
			return fmt.Errorf("%w: %q and %q overlap at [%d,%d)",
				ErrOverlap, prev.kind, cur.kind, cur.start, prev.end)
		}
		if cur.start > prev.end {
			return fmt.Errorf("%w: %d hidden byte(s) at [%d,%d)",
				ErrGap, cur.start-prev.end, prev.end, cur.start)
		}
	}
	if len(ivs) == 0 {
		if archiveSize != 0 {
			return fmt.Errorf("%w: %d unreferenced byte(s)", ErrGap, archiveSize)
		}
		return nil
	}
	if ivs[0].start != 0 {
		return fmt.Errorf("%w: %d leading hidden byte(s)", ErrGap, ivs[0].start)
	}
	if ivs[len(ivs)-1].end != archiveSize {
		return fmt.Errorf("%w: trailing hidden byte(s)", ErrGap)
	}
	return nil
}

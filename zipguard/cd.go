package zipguard

import (
	"encoding/binary"
	"fmt"
)

type z64Extra struct {
	compressedSize   uint64
	uncompressedSize uint64
	localOffset      uint64
}

// parseZ64Extra extracts the 0x0001 zip64 extra field.
func parseZ64Extra(extra []byte) (z64Extra, error) {
	var z z64Extra
	pos := 0
	for pos+4 <= len(extra) {
		tag := binary.LittleEndian.Uint16(extra[pos:])
		size := int(binary.LittleEndian.Uint16(extra[pos+2:]))
		body := pos + 4
		if body+size > len(extra) {
			return z, fmt.Errorf("%w: truncated extra field", ErrTruncated)
		}
		if tag == 0x0001 {
			cursor := body
			end := body + size
			// Fields appear in fixed order; LFH records omit the offset, and
			// some writers omit fields whose 32-bit value was not a
			// sentinel. Only consume as many as actually present.
			if cursor+8 <= end {
				z.uncompressedSize = binary.LittleEndian.Uint64(extra[cursor:])
				cursor += 8
			}
			if cursor+8 <= end {
				z.compressedSize = binary.LittleEndian.Uint64(extra[cursor:])
				cursor += 8
			}
			if cursor+8 <= end {
				z.localOffset = binary.LittleEndian.Uint64(extra[cursor:])
				cursor += 8
			}
			if cursor != end {
				return z, fmt.Errorf("%w: malformed zip64 extra field length", ErrBadSignature)
			}
			return z, nil
		}
		pos = body + size
	}
	return z, fmt.Errorf("%w: zip64 sentinel without zip64 extra field", ErrBadSignature)
}

// parseCentralDirectory walks every CDH starting at the EOCD-declared offset.
func parseCentralDirectory(b []byte, e *eocd, p Policy) ([]*cdh, error) {
	var (
		count  uint64
		offset uint64
		size   uint64
	)
	if e.zip64 {
		count = e.z64TotalEntries
		offset = e.z64CDOffset
		size = e.z64CDSize
	} else {
		count = uint64(e.totalEntries)
		offset = uint64(e.cdOffset)
		size = uint64(e.cdSize)
	}
	if count == 0 {
		if size != 0 || offset != uint64(e.offset) {
			return nil, fmt.Errorf("%w: empty archive with non-empty central directory", ErrBadSignature)
		}
		return nil, nil
	}
	if count > uint64(p.MaxEntries) {
		return nil, fmt.Errorf("%w: %d entries (limit %d)", ErrTooManyEntries, count, p.MaxEntries)
	}
	if offset > uint64(len(b)) {
		return nil, fmt.Errorf("%w: central directory offset %d", ErrTruncated, offset)
	}
	limit := offset + size
	if limit > uint64(len(b)) || limit > uint64(e.offset) {
		return nil, fmt.Errorf("%w: central directory overruns archive", ErrTruncated)
	}

	out := make([]*cdh, 0, count)
	pos := int64(offset)
	for i := uint64(0); i < count; i++ {
		if pos+cdhFixedSize > int64(limit) {
			return nil, fmt.Errorf("%w: CDH %d fixed header", ErrTruncated, i)
		}
		if binary.LittleEndian.Uint32(b[pos:]) != sigCDH {
			return nil, fmt.Errorf("%w: expected CDH at offset %d", ErrBadSignature, pos)
		}
		c := &cdh{
			headerOffset:  pos,
			versionMadeBy: binary.LittleEndian.Uint16(b[pos+4:]),
			versionNeeded: binary.LittleEndian.Uint16(b[pos+6:]),
			flags:         binary.LittleEndian.Uint16(b[pos+8:]),
			method:        binary.LittleEndian.Uint16(b[pos+10:]),
			modTime:       binary.LittleEndian.Uint16(b[pos+12:]),
			modDate:       binary.LittleEndian.Uint16(b[pos+14:]),
			crc32:         binary.LittleEndian.Uint32(b[pos+16:]),
			externalAttrs: binary.LittleEndian.Uint32(b[pos+38:]),
		}
		c.hostSystem = uint8(c.versionMadeBy >> 8)
		nameLen := int(binary.LittleEndian.Uint16(b[pos+28:]))
		extraLen := int(binary.LittleEndian.Uint16(b[pos+30:]))
		commentLen := int(binary.LittleEndian.Uint16(b[pos+32:]))
		diskStart := binary.LittleEndian.Uint16(b[pos+34:])
		cSize32 := binary.LittleEndian.Uint32(b[pos+20:])
		uSize32 := binary.LittleEndian.Uint32(b[pos+24:])
		off32 := binary.LittleEndian.Uint32(b[pos+42:])
		c.compressedSize = uint64(cSize32)
		c.uncompressedSize = uint64(uSize32)
		c.localOffset = uint64(off32)

		tail := pos + cdhFixedSize
		if int64(nameLen+extraLen+commentLen) > int64(limit)-tail {
			return nil, fmt.Errorf("%w: CDH %d variable fields", ErrTruncated, i)
		}
		c.name = string(b[tail : tail+int64(nameLen)])
		c.extra = append([]byte(nil), b[tail+int64(nameLen):tail+int64(nameLen+extraLen)]...)
		c.comment = append([]byte(nil), b[tail+int64(nameLen+extraLen):tail+int64(nameLen+extraLen+commentLen)]...)

		if diskStart != 0 {
			return nil, fmt.Errorf("%w: entry %q spans disks", ErrSplitArchive, c.name)
		}
		if c.method != methodStored && c.method != methodDeflate {
			return nil, fmt.Errorf("%w: compression method %d for %q", ErrUnsupported, c.method, c.name)
		}

		if cSize32 == u32Sentinel || uSize32 == u32Sentinel || off32 == u32Sentinel {
			z, err := parseZ64Extra(c.extra)
			if err != nil {
				return nil, err
			}
			if cSize32 == u32Sentinel {
				c.compressedSize = z.compressedSize
			}
			if uSize32 == u32Sentinel {
				c.uncompressedSize = z.uncompressedSize
			}
			if off32 == u32Sentinel {
				c.localOffset = z.localOffset
			}
		}
		out = append(out, c)
		pos = tail + int64(nameLen+extraLen+commentLen)
	}
	if pos != int64(limit) {
		return nil, fmt.Errorf("%w: central directory size mismatch", ErrBadSignature)
	}
	return out, nil
}

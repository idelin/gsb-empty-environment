package safezip

import "encoding/binary"

type zip64Extra struct {
	hasUncomp bool
	uncomp    int64
	hasComp   bool
	comp      int64
	hasOffset bool
	offset    int64
}

// parseCentralExtra walks a central-directory extra-field block. The ZIP64
// record must contain exactly the three 8-byte fields (uncompressed size,
// compressed size, local header offset); unknown critical headers (high bit
// of the id set) are refused.
func parseCentralExtra(extra []byte) (*zip64Extra, error) {
	var z *zip64Extra
	for len(extra) >= 4 {
		id := binary.LittleEndian.Uint16(extra[0:])
		size := int(binary.LittleEndian.Uint16(extra[2:]))
		extra = extra[4:]
		if size > len(extra) {
			return nil, ErrFormat
		}
		data := extra[:size]
		extra = extra[size:]

		if id == extraZip64 {
			if z != nil || size != 24 {
				return nil, ErrFormat
			}
			z = &zip64Extra{
				hasUncomp: true,
				uncomp:    int64(binary.LittleEndian.Uint64(data[0:])),
				hasComp:   true,
				comp:      int64(binary.LittleEndian.Uint64(data[8:])),
				hasOffset: true,
				offset:    int64(binary.LittleEndian.Uint64(data[16:])),
			}
			if z.uncomp < 0 || z.comp < 0 || z.offset < 0 {
				return nil, ErrFormat
			}
			continue
		}
		if id&0x8000 != 0 {
			return nil, ErrUnsupported
		}
	}
	if len(extra) != 0 {
		return nil, ErrFormat
	}
	return z, nil
}

// parseLocalExtra walks a local-header extra-field block. A local ZIP64
// record must contain exactly the two 8-byte size fields.
func parseLocalExtra(extra []byte) (*zip64Extra, error) {
	var z *zip64Extra
	for len(extra) >= 4 {
		id := binary.LittleEndian.Uint16(extra[0:])
		size := int(binary.LittleEndian.Uint16(extra[2:]))
		extra = extra[4:]
		if size > len(extra) {
			return nil, ErrFormat
		}
		data := extra[:size]
		extra = extra[size:]

		if id == extraZip64 {
			if z != nil || size != 16 {
				return nil, ErrFormat
			}
			z = &zip64Extra{
				hasUncomp: true,
				uncomp:    int64(binary.LittleEndian.Uint64(data[0:])),
				hasComp:   true,
				comp:      int64(binary.LittleEndian.Uint64(data[8:])),
			}
			if z.uncomp < 0 || z.comp < 0 {
				return nil, ErrFormat
			}
			continue
		}
		if id&0x8000 != 0 {
			return nil, ErrUnsupported
		}
	}
	if len(extra) != 0 {
		return nil, ErrFormat
	}
	return z, nil
}

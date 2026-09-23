package slz

import (
	"encoding/binary"
	"hash/crc32"
)

// Decompress decodes src, refusing to produce more than maxOutput bytes.
// It verifies the format version, every block header, all match distances
// (no references to bytes not yet produced or evicted by a checkpoint),
// the declared total length, and the CRC-32 of the output.
func Decompress(src []byte, maxOutput int) ([]byte, error) {
	if len(src) < headerSize || string(src[:4]) != Magic {
		return nil, errCorrupt
	}
	if src[4] != Version {
		return nil, errBadVersion
	}
	totalLen := binary.LittleEndian.Uint64(src[6:14])
	crc := binary.LittleEndian.Uint32(src[14:18])
	if totalLen > uint64(maxOutput) {
		return nil, errTooLarge
	}

	out := make([]byte, 0, totalLen)
	resetBase := 0 // no match may reference out[:resetBase]
	off := headerSize
	for off < len(src) {
		if len(src)-off < blockHeaderSize {
			return nil, errCorrupt
		}
		rawLen := binary.LittleEndian.Uint32(src[off : off+4])
		bitLen := binary.LittleEndian.Uint32(src[off+4 : off+8])
		flags := src[off+8]
		off += blockHeaderSize
		if flags & ^byte(flagResetWindow) != 0 {
			return nil, errCorrupt
		}
		payloadLen := int(bitLen+7) / 8
		if uint64(len(out))+uint64(rawLen) > totalLen || len(src)-off < payloadLen {
			return nil, errCorrupt
		}
		payload := src[off : off+payloadLen]
		off += payloadLen

		if flags&flagResetWindow != 0 {
			resetBase = len(out) // checkpoint: bind the dictionary window
		}
		blockEnd := len(out) + int(rawLen)
		r := &bitReader{data: payload}
		for len(out) < blockEnd {
			tag, err := r.readBits(1)
			if err != nil {
				return nil, err
			}
			if tag == 0 {
				b, err := r.readBits(8)
				if err != nil {
					return nil, err
				}
				out = append(out, byte(b))
				continue
			}
			d, err := r.readBits(WindowBits)
			if err != nil {
				return nil, err
			}
			l, err := r.readBits(LenBits)
			if err != nil {
				return nil, err
			}
			dist := int(d) + 1
			length := int(l) + MinMatch
			if dist > len(out)-resetBase {
				// References bytes not yet produced, or evicted by a
				// checkpoint. (dist <= WindowSize holds by encoding.)
				return nil, errBadDistance
			}
			if len(out)+length > blockEnd {
				return nil, errCorrupt
			}
			// Byte-at-a-time copy: overlapping matches (dist < length,
			// e.g. dist==1 runs) extend correctly.
			for k := 0; k < length; k++ {
				out = append(out, out[len(out)-dist])
			}
		}
		// The token stream must end within the declared bit length,
		// leaving only sub-byte padding.
		if r.bitPos > int(bitLen) || int(bitLen)-r.bitPos >= 8 {
			return nil, errCorrupt
		}
	}
	if uint64(len(out)) != totalLen {
		return nil, errBadLength
	}
	if crc32.ChecksumIEEE(out) != crc {
		return nil, errChecksum
	}
	return out, nil
}

package blkconv

import "errors"

// errOverflow is returned by the block decoders when decoding would
// produce more than the caller-supplied output limit.
var errOverflow = errors.New("blkconv: block decode output limit exceeded")

// encodeBlock compresses one block with the format's RLE codec.
func encodeBlock(f Format, dst, src []byte) []byte {
	if f == FormatA {
		return encodeA(dst, src)
	}
	return encodeB(dst, src)
}

// decodeBlock decompresses one block, producing at most maxOut bytes.
func decodeBlock(f Format, dst, src []byte, maxOut int) ([]byte, error) {
	if f == FormatA {
		return decodeA(dst, src, maxOut)
	}
	return decodeB(dst, src, maxOut)
}

// --- RLE-A: PackBits-style tokens ---
// token < 128:  literal run of token+1 bytes follows
// token >= 128: run of (token-128+3) copies of the next byte

func encodeA(dst, src []byte) []byte {
	i := 0
	for i < len(src) {
		run := 1
		for i+run < len(src) && src[i+run] == src[i] && run < 130 {
			run++
		}
		if run >= 3 {
			dst = append(dst, byte(128+run-3), src[i])
			i += run
			continue
		}
		start := i
		i += run
		for i < len(src) && i-start < 128 {
			r := 1
			for i+r < len(src) && src[i+r] == src[i] && r < 3 {
				r++
			}
			if r >= 3 {
				break
			}
			i++
		}
		dst = append(dst, byte(i-start-1))
		dst = append(dst, src[start:i]...)
	}
	return dst
}

func decodeA(dst, src []byte, maxOut int) ([]byte, error) {
	for len(src) > 0 {
		c := src[0]
		src = src[1:]
		if c < 128 {
			n := int(c) + 1
			if len(src) < n {
				return nil, ErrCorrupt
			}
			if len(dst)+n > maxOut {
				return nil, errOverflow
			}
			dst = append(dst, src[:n]...)
			src = src[n:]
		} else {
			n := int(c) - 128 + 3
			if len(src) < 1 {
				return nil, ErrCorrupt
			}
			if len(dst)+n > maxOut {
				return nil, errOverflow
			}
			v := src[0]
			src = src[1:]
			for ; n > 0; n-- {
				dst = append(dst, v)
			}
		}
	}
	return dst, nil
}

// --- RLE-B: tagged-byte tokens, different run/literal split ---
// token & 0x80 != 0: literal run of (token&0x7f)+1 bytes follows
// token & 0x80 == 0: run of (token&0x7f)+2 copies of the next byte

func encodeB(dst, src []byte) []byte {
	i := 0
	for i < len(src) {
		run := 1
		for i+run < len(src) && src[i+run] == src[i] && run < 129 {
			run++
		}
		if run >= 2 {
			dst = append(dst, byte(run-2), src[i])
			i += run
			continue
		}
		start := i
		i++
		for i < len(src) && i-start < 128 {
			if i+1 < len(src) && src[i+1] == src[i] {
				break
			}
			i++
		}
		dst = append(dst, 0x80|byte(i-start-1))
		dst = append(dst, src[start:i]...)
	}
	return dst
}

func decodeB(dst, src []byte, maxOut int) ([]byte, error) {
	for len(src) > 0 {
		t := src[0]
		src = src[1:]
		if t&0x80 != 0 {
			n := int(t&0x7f) + 1
			if len(src) < n {
				return nil, ErrCorrupt
			}
			if len(dst)+n > maxOut {
				return nil, errOverflow
			}
			dst = append(dst, src[:n]...)
			src = src[n:]
		} else {
			n := int(t&0x7f) + 2
			if len(src) < 1 {
				return nil, ErrCorrupt
			}
			if len(dst)+n > maxOut {
				return nil, errOverflow
			}
			v := src[0]
			src = src[1:]
			for ; n > 0; n-- {
				dst = append(dst, v)
			}
		}
	}
	return dst, nil
}

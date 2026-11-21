package pagearchive

import (
	"errors"
	"fmt"
)

// EncodePatch returns the canonical patch from base to target.
//
// Every maximal run of differing bytes is represented by the unsigned LEB128
// gap from the previous run, the unsigned LEB128 run length, and the target
// bytes of that run. Equal pages encode to an empty byte slice.
func EncodePatch(base, target []byte) ([]byte, error) {
	if len(base) != len(target) {
		return nil, fmt.Errorf("pagearchive: patch pages have different lengths: %d and %d", len(base), len(target))
	}

	var out []byte
	lastRunEnd := 0
	for pos := 0; pos < len(target); {
		if target[pos] == base[pos] {
			pos++
			continue
		}

		start := pos
		for pos < len(target) && target[pos] != base[pos] {
			pos++
		}

		out = appendULEB128(out, uint64(start-lastRunEnd))
		out = appendULEB128(out, uint64(pos-start))
		out = append(out, target[start:pos]...)
		lastRunEnd = pos
	}
	return out, nil
}

// DecodePatch applies a canonical patch to base and returns the target page.
func DecodePatch(base, patch []byte) ([]byte, error) {
	out := append([]byte(nil), base...)
	runEnd := 0
	for len(patch) > 0 {
		gap, n, err := readULEB128(patch)
		if err != nil {
			return nil, err
		}
		patch = patch[n:]

		runLen, n, err := readULEB128(patch)
		if err != nil {
			return nil, err
		}
		patch = patch[n:]

		start := runEnd + int(gap)
		end := start + int(runLen)
		if gap > uint64(len(out)) || runLen > uint64(len(out)) || uint64(start) != uint64(runEnd)+gap || uint64(end) != uint64(start)+runLen || end > len(out) {
			return nil, errors.New("pagearchive: patch range is outside the page")
		}
		if uint64(len(patch)) < runLen {
			return nil, errors.New("pagearchive: patch is missing target bytes")
		}
		for offset := uint64(0); offset < runLen; offset++ {
			if patch[offset] == base[start+int(offset)] {
				return nil, errors.New("pagearchive: patch contains an unchanged target byte")
			}
		}
		copy(out[start:end], patch[:runLen])
		patch = patch[runLen:]
		runEnd = end
	}
	return out, nil
}

func appendULEB128(dst []byte, value uint64) []byte {
	for {
		b := byte(value & 0x7f)
		value >>= 7
		if value != 0 {
			dst = append(dst, b|0x80)
		} else {
			return append(dst, b)
		}
	}
}

func readULEB128(data []byte) (uint64, int, error) {
	var value uint64
	for i := 0; i < len(data); i++ {
		b := data[i]
		if i == 9 && b > 1 {
			return 0, 0, errors.New("pagearchive: LEB128 value overflows uint64")
		}
		if i >= 10 {
			return 0, 0, errors.New("pagearchive: LEB128 value is too long")
		}
		value |= uint64(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			if i > 0 && b == 0 {
				return 0, 0, errors.New("pagearchive: LEB128 value is not minimally encoded")
			}
			return value, i + 1, nil
		}
	}
	return 0, 0, errors.New("pagearchive: truncated LEB128 value")
}

func canonicalPatch(base, target []byte) []byte {
	patch, err := EncodePatch(base, target)
	if err != nil {
		panic(err)
	}
	return patch
}

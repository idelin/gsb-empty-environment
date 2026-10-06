package binpatch

import (
	"bytes"
	"crypto/sha256"
)

// blockSize is the minimum match length the simple differ looks for.
const blockSize = 32

// Diff computes a patch that transforms baseline into target. The returned
// header binds the patch to both byte strings, and every copy operation
// refers to offsets in the original baseline.
//
// This is a deliberately simple greedy matcher: it indexes fixed-size
// baseline blocks and emits the longest match it can extend at each target
// position. It favours correctness and clarity over minimality.
func Diff(baseline, target []byte) (Header, []Op, error) {
	var h Header
	h.BaselineHash = sha256.Sum256(baseline)
	h.TargetLength = uint64(len(target))
	h.TargetHash = sha256.Sum256(target)

	index := make(map[string][]int)
	for i := 0; i+blockSize <= len(baseline); i++ {
		key := string(baseline[i : i+blockSize])
		index[key] = append(index[key], i)
	}

	var ops []Op
	var pending []byte
	flushInsert := func() {
		if len(pending) > 0 {
			ops = append(ops, Op{Insert: true, Data: pending})
			pending = nil
		}
	}
	emitCopy := func(offset, length int) {
		last := len(ops) - 1
		if last >= 0 && !ops[last].Insert &&
			ops[last].Offset+ops[last].Length == uint64(offset) {
			ops[last].Length += uint64(length)
			return
		}
		ops = append(ops, Op{Offset: uint64(offset), Length: uint64(length)})
	}

	i := 0
	for i < len(target) {
		bestOff, bestLen := -1, 0
		if i+blockSize <= len(target) {
			for _, off := range index[string(target[i:i+blockSize])] {
				n := blockSize
				for off+n < len(baseline) && i+n < len(target) &&
					baseline[off+n] == target[i+n] {
					n++
				}
				if n > bestLen {
					bestOff, bestLen = off, n
				}
			}
		}
		if bestLen >= blockSize {
			flushInsert()
			emitCopy(bestOff, bestLen)
			i += bestLen
			continue
		}
		pending = append(pending, target[i])
		i++
	}
	flushInsert()
	return h, ops, nil
}

// PatchBytes diffs baseline into target and returns the encoded patch.
func PatchBytes(baseline, target []byte) ([]byte, error) {
	h, ops, err := Diff(baseline, target)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := Encode(&buf, h, ops); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

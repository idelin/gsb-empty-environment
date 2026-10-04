// Package shrink reduces an interesting input to a minimal replayable
// sample using ddmin-style chunk elimination over lines, then over bytes.
package shrink

import (
	"bytes"
)

// Shrink returns a minimal input that still satisfies interesting().
// It first runs ddmin over lines, then over raw bytes of the result.
func Shrink(input []byte, interesting func([]byte) bool) []byte {
	if !interesting(input) {
		return input
	}
	lines := bytes.SplitAfter(input, []byte("\n"))
	lines = ddmin(lines, interesting)
	out := bytes.Join(lines, nil)

	// Byte-level pass: try to drop chunks of remaining bytes (helps when
	// the input has no newlines).
	chunks := make([][]byte, 0, len(out))
	for _, b := range out {
		chunks = append(chunks, []byte{b})
	}
	chunks = ddmin(chunks, interesting)
	return bytes.Join(chunks, nil)
}

// ddmin is the classic delta-debugging minimization over chunks.
func ddmin(chunks [][]byte, interesting func([]byte) bool) [][]byte {
	n := 2
	for len(chunks) >= 2 {
		subsets := split(chunks, n)
		reduced := false
		// Try removing each subset (complement test).
		for i := range subsets {
			var complement [][]byte
			for j := range subsets {
				if j != i {
					complement = append(complement, subsets[j]...)
				}
			}
			if interesting(bytes.Join(complement, nil)) {
				chunks = complement
				n = max(n-1, 2)
				reduced = true
				break
			}
		}
		if reduced {
			continue
		}
		// Try keeping each subset alone.
		for i := range subsets {
			if interesting(bytes.Join(subsets[i], nil)) {
				chunks = subsets[i]
				n = 2
				reduced = true
				break
			}
		}
		if reduced {
			continue
		}
		if n >= len(chunks) {
			break
		}
		n = min(n*2, len(chunks))
	}
	return chunks
}

// split divides chunks into n roughly equal contiguous groups.
func split(chunks [][]byte, n int) [][][]byte {
	if n > len(chunks) {
		n = len(chunks)
	}
	groups := make([][][]byte, 0, n)
	size := len(chunks) / n
	extra := len(chunks) % n
	start := 0
	for i := 0; i < n; i++ {
		end := start + size
		if i < extra {
			end++
		}
		groups = append(groups, chunks[start:end])
		start = end
	}
	return groups
}

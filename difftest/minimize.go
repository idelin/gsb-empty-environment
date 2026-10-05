package difftest

import "bytes"

// Minimize shrinks input with a ddmin-style search, keeping only inputs for
// which keep() stays true (i.e. the same verdict class reproduces).
// It first works line-by-line, then falls back to byte chunks when the
// input has no line structure. maxRuns bounds the number of keep() calls.
func Minimize(input []byte, keep func([]byte) bool, maxRuns int) []byte {
	m := &minimizer{keep: keep, budget: maxRuns}
	if !keep(input) {
		return input
	}
	cur := input
	if bytes.IndexByte(cur, '\n') >= 0 {
		cur = m.ddmin(splitLines(cur), func(parts [][]byte) []byte {
			return bytes.Join(parts, nil)
		})
	}
	// Byte-level pass to shave intra-line fat.
	cur = m.ddmin(splitBytes(cur, 64), func(parts [][]byte) []byte {
		return bytes.Join(parts, nil)
	})
	return cur
}

type minimizer struct {
	keep   func([]byte) bool
	budget int
}

func (m *minimizer) try(candidate []byte) bool {
	if m.budget <= 0 {
		return false
	}
	m.budget--
	return m.keep(candidate)
}

// ddmin reduces parts while join(parts) still satisfies keep().
func (m *minimizer) ddmin(parts [][]byte, join func([][]byte) []byte) []byte {
	granularity := 2
	for len(parts) >= 2 {
		subsets := splitInto(parts, granularity)
		reduced := false
		// Try removing each subset.
		for i := range subsets {
			var candidate [][]byte
			for j := range subsets {
				if j != i {
					candidate = append(candidate, subsets[j]...)
				}
			}
			if len(candidate) == len(parts) {
				continue
			}
			if m.try(join(candidate)) {
				parts = candidate
				granularity = max(granularity-1, 2)
				reduced = true
				break
			}
		}
		if reduced {
			continue
		}
		// Try keeping only each subset (complement search).
		for i := range subsets {
			if len(subsets[i]) == len(parts) || len(subsets[i]) == 0 {
				continue
			}
			if m.try(join(subsets[i])) {
				parts = subsets[i]
				granularity = 2
				reduced = true
				break
			}
		}
		if reduced {
			continue
		}
		if granularity >= len(parts) {
			break
		}
		granularity = min(granularity*2, len(parts))
	}
	return join(parts)
}

func splitLines(b []byte) [][]byte {
	lines := bytes.SplitAfter(b, []byte("\n"))
	out := lines[:0]
	for _, l := range lines {
		if len(l) > 0 {
			out = append(out, l)
		}
	}
	return out
}

func splitBytes(b []byte, chunk int) [][]byte {
	if len(b) <= chunk {
		parts := make([][]byte, 0, len(b))
		for i := 0; i < len(b); i++ {
			parts = append(parts, b[i:i+1])
		}
		return parts
	}
	var parts [][]byte
	for i := 0; i < len(b); i += chunk {
		end := i + chunk
		if end > len(b) {
			end = len(b)
		}
		parts = append(parts, b[i:end])
	}
	return parts
}

func splitInto(parts [][]byte, n int) [][][]byte {
	if n > len(parts) {
		n = len(parts)
	}
	out := make([][][]byte, n)
	for i, p := range parts {
		idx := i * n / len(parts)
		out[idx] = append(out[idx], p)
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

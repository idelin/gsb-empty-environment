package imgcodec

import (
	"fmt"
)

// Encode returns a minimum-cost instruction sequence encoding pix under p.
// pix must hold exactly Width*Height pixels in row-major order.
//
// The search is a shortest-path DP over pixel positions, pruned to
// transitions that can appear in some optimal solution:
//
//   - dp[j] is non-increasing in j (any encoding of the suffix from j
//     yields an encoding of the suffix from j+1 that costs no more:
//     shrink its first instruction by one pixel). A repeat instruction
//     costs the same at any length, so a repeat is always taken at its
//     maximal (wire-capped) length; a reference likewise at its maximal
//     feasible length.
//   - Two adjacent literals always merge cheaper, and a literal followed
//     by a length-1 repeat is never better than extending the literal,
//     so a literal ends only at the image end, the wire-length cap, or
//     where a run of >= 2 equal pixels or a reference match begins.
//
// A literal ending at j costs LitHeader + (j-i) + dp[j], i.e. a constant
// plus (j + dp[j]), so the best literal end in the sliding window of
// candidates is tracked with a monotone queue. Total time is O(N).
//
// The exhaustive test in brute_test.go confirms the pruned DP matches
// trying every instruction length at every position.
func Encode(pix []byte, p Params) ([]Instr, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if len(pix) != p.Width*p.Height {
		return nil, fmt.Errorf("imgcodec: got %d pixels, want %d", len(pix), p.Width*p.Height)
	}
	n := len(pix)
	w := p.Width

	// run[i]: length of the maximal run of equal pixels starting at i.
	run := make([]int, n+1)
	// ref[i]: length of the maximal match with the previous row at i.
	ref := make([]int, n+1)
	for i := n - 1; i >= 0; i-- {
		run[i] = 1
		if i+1 < n && pix[i] == pix[i+1] {
			run[i] = run[i+1] + 1
		}
		if i >= w && pix[i] == pix[i-w] {
			ref[i] = ref[i+1] + 1
		}
	}

	// inSet[j]: j is a legal literal end (besides the wire-length cap).
	inSet := make([]bool, n+1)
	inSet[n] = true
	for j := 1; j < n; j++ {
		inSet[j] = run[j] >= 2 || ref[j] >= 1
	}

	const inf = int(^uint(0) >> 1)
	dp := make([]int, n+1)
	type choice struct {
		op     Op
		length int
	}
	ch := make([]choice, n+1)

	// Monotone queue of candidate literal ends in the window
	// (i, i+maxWireLen], ordered by increasing g[j] = j + dp[j].
	g := make([]int, n+1)
	g[n] = n
	queue := []int{n} // front is the best candidate
	head := 0

	for i := n - 1; i >= 0; i-- {
		// Drop candidates that fell out of the window's right edge.
		for head < len(queue) && queue[head] > i+maxWireLen {
			head++
		}

		best, bop, blen := inf, OpLiteral, 1

		// Literal: best candidate end in the window, plus the forced
		// split at the wire-length cap.
		if head < len(queue) {
			j := queue[head]
			best, blen = p.LitHeader-i+g[j], j-i
		}
		if cap := i + maxWireLen; cap < n {
			if c := p.LitHeader - i + g[cap]; c < best {
				best, blen = c, cap-i
			}
		}

		// Repeat: maximal (wire-capped) run length only.
		maxRep := run[i]
		if maxRep > maxWireLen {
			maxRep = maxWireLen
		}
		if c := p.RepHeader + 1 + dp[i+maxRep]; c < best {
			best, bop, blen = c, OpRepeat, maxRep
		}

		// Reference: maximal feasible length only.
		if i >= w && ref[i] > 0 {
			length := ref[i]
			if length > p.MaxRefLen {
				length = p.MaxRefLen
			}
			if rowLeft := w - i%w; length > rowLeft {
				length = rowLeft
			}
			if c := p.RefHeader + dp[i+length]; c < best {
				best, bop, blen = c, OpRef, length
			}
		}

		dp[i], ch[i] = best, choice{bop, blen}

		// Admit i as a literal end for earlier positions.
		g[i] = i + dp[i]
		if i >= 1 && inSet[i] {
			for len(queue) > head && g[queue[len(queue)-1]] >= g[i] {
				queue = queue[:len(queue)-1]
			}
			queue = append(queue, i)
		}
	}

	var out []Instr
	for i := 0; i < n; {
		c := ch[i]
		switch c.op {
		case OpLiteral:
			out = append(out, Instr{Op: OpLiteral, Len: c.length, Data: append([]byte(nil), pix[i:i+c.length]...)})
		case OpRepeat:
			out = append(out, Instr{Op: OpRepeat, Len: c.length, Val: pix[i]})
		case OpRef:
			out = append(out, Instr{Op: OpRef, Len: c.length})
		}
		i += c.length
	}
	return out, nil
}

// Package lpc builds length-limited prefix codes.
//
// Code lengths are found with the package-merge algorithm: an exact
// length-constrained optimum, never a truncated ordinary Huffman tree
// (truncating a Huffman code can reuse an already-assigned codeword and
// therefore violate the prefix property).
//
// All code words are then assigned canonically from the lengths alone
// (most-significant-bit first), so the wire format stores only lengths.
package lpc

import (
	"errors"
	"math/big"
	"sort"
)

const (
	// Magic identifies the serialized code-header format.
	Magic = "LCPC"
	// FormatVersion is the only supported on-disk format version.
	FormatVersion byte = 1
	// MaxLength is the largest permitted maximum code length. Code words
	// must fit into a uint64, so one value must stay available for the
	// running prefix during canonical assignment.
	MaxLength = 62
)

// Sentinel errors. Comparisons use == (no wrapping).
var (
	// ErrInfeasible means the symbol alphabet cannot fit under the limit:
	// the necessary condition 2^MaxLength >= SymbolCount fails.
	ErrInfeasible = errors.New("lpc: infeasible: 2^L < number of symbols")
	// ErrBadArgument is returned for invalid weights, limits or codes.
	ErrBadArgument = errors.New("lpc: bad argument")
	// ErrBadHeader is returned for a malformed or mismatched code header.
	ErrBadHeader = errors.New("lpc: bad code header")
	// ErrChecksum is returned when the stored CRC-32 does not match.
	ErrChecksum = errors.New("lpc: checksum mismatch")
	// ErrInvalidBitstream is returned for corrupt or unparseable payloads.
	ErrInvalidBitstream = errors.New("lpc: invalid bitstream")
	// ErrOutputLimit is returned when decoding would exceed the caller's
	// bounded output quota.
	ErrOutputLimit = errors.New("lpc: decoded output quota exceeded")
)

// Code is a canonical length-limited prefix code.
type Code struct {
	Version byte
	MaxLen  int
	Lengths []int // canonical code length per symbol
	words   []uint64
	Kraft   *big.Rat // sum 2^-Lengths[symbol]
}

// Report is the result of Build.
type Report struct {
	Code     *Code
	Feasible bool
	// WeightedLength is sum weight[symbol]*Lengths[symbol] / sum(weight),
	// the expected code length per symbol under the supplied frequencies.
	CostNum     *big.Int   // numerator of the exact weighted length
	CostDen     *big.Int   // denominator of the exact weighted length
	Cost        *big.Float // CostNum/CostDen at 80-bit float precision
	TotalWeight *big.Int
}

// item is one coin of the package-merge collection.
type item struct {
	weight *big.Int
	minSym int   // tie-breaker: smallest contained symbol
	syms   []int // contained symbol ids (length >= 1)
}

// Build constructs an optimal prefix code whose lengths are all <= maxLen
// for the given non-negative integer weights. Zero-weight symbols are still
// assigned a code word. At least one weight must be positive.
//
// When 2^maxLen < len(weights) the request is infeasible and Build returns
// ErrInfeasible with a nil code.
func Build(weights []uint64, maxLen int) (*Report, error) {
	n := len(weights)
	if maxLen < 0 || maxLen > MaxLength {
		return nil, ErrBadArgument
	}
	total := new(big.Int)
	ws := make([]*big.Int, n)
	for i, w := range weights {
		ws[i] = new(big.Int).SetUint64(w)
		total.Add(total, ws[i])
	}
	if n > 0 && total.Sign() == 0 {
		return nil, ErrBadArgument
	}

	if n == 0 {
		return nil, ErrBadArgument
	}

	// Single-symbol alphabet: the empty code word (length 0).
	if n == 1 {
		if maxLen == 0 {
			// length 0 is the only possible assignment
		}
		c, err := makeCode(maxLen, []int{0})
		if err != nil {
			return nil, err
		}
		return finalizeReport(c, ws, total), nil
	}

	if maxLen == 0 || !feasible(n, maxLen) {
		return nil, ErrInfeasible
	}

	// Package-merge. pool holds the surviving items of the previous
	// (coarser) level; level L down to 1.
	pool := make([]item, 0, 2*n)
	coin := func(sym int) item {
		return item{weight: new(big.Int).Set(ws[sym]), minSym: sym, syms: []int{sym}}
	}
	merge := func(a, b item) item {
		ms := a.minSym
		if b.minSym < ms {
			ms = b.minSym
		}
		syms := make([]int, 0, len(a.syms)+len(b.syms))
		syms = append(syms, a.syms...)
		syms = append(syms, b.syms...)
		return item{
			weight: new(big.Int).Add(a.weight, b.weight),
			minSym: ms,
			syms:   syms,
		}
	}

	for level := maxLen; level >= 1; level-- {
		cur := make([]item, 0, len(pool)+n)
		for s := 0; s < n; s++ {
			cur = append(cur, coin(s))
		}
		cur = append(cur, pool...)
		sort.SliceStable(cur, func(i, j int) bool {
			if c := cur[i].weight.Cmp(cur[j].weight); c != 0 {
				return c < 0
			}
			return cur[i].minSym < cur[j].minSym
		})

		if level == 1 {
			// Coarsest level: keep the cheapest 2n-2 items, which are the
			// n-1 merges of a complete binary tree over the alphabet.
			keep := 2*n - 2
			if len(cur) < keep {
				// Defensive: feasibility was already proved, so this means
				// an internal error.
				return nil, ErrInfeasible
			}
			pool = cur[:keep]
			break
		}

		// Pair adjacent survivors; the odd item (if any) is discarded.
		next := make([]item, 0, len(cur)/2)
		for k := 0; k+1 < len(cur); k += 2 {
			next = append(next, merge(cur[k], cur[k+1]))
		}
		pool = next
	}

	depth := make([]int, n)
	for _, it := range pool {
		for _, s := range it.syms {
			depth[s]++
		}
	}
	for _, d := range depth {
		if d <= 0 || d > maxLen {
			return nil, ErrBadArgument
		}
	}

	c, err := makeCode(maxLen, depth)
	if err != nil {
		return nil, err
	}
	return finalizeReport(c, ws, total), nil
}

func finalizeReport(c *Code, ws []*big.Int, total *big.Int) *Report {
	num := new(big.Int)
	for i, w := range ws {
		num.Add(num, new(big.Int).Mul(w, big.NewInt(int64(c.Lengths[i]))))
	}
	r := &Report{
		Code:        c,
		Feasible:    true,
		CostNum:     num,
		CostDen:     new(big.Int).Set(total),
		TotalWeight: new(big.Int).Set(total),
	}
	if total.Sign() > 0 {
		var f big.Float
		f.SetPrec(256)
		f.Quo(new(big.Float).SetInt(num), new(big.Float).SetInt(total))
		r.Cost = &f
	}
	return r
}

// feasible reports whether n symbols can each receive a distinct code word
// of length <= L: exactly when n <= 2^L. The shift is guarded against
// overflow; for L <= MaxLength (62) 1<<L always fits in uint64.
func feasible(n, L int) bool {
	if L >= 63 {
		return true
	}
	cap := uint64(1) << uint(L)
	return uint64(n) <= cap
}

// makeCode assigns canonical code words to the supplied lengths, verifies
// the Kraft equality and all stated limits.
func makeCode(maxLen int, lengths []int) (*Code, error) {
	if maxLen < 0 || maxLen > MaxLength {
		return nil, ErrBadArgument
	}
	n := len(lengths)
	for _, l := range lengths {
		if l < 0 || l > maxLen {
			return nil, ErrBadArgument
		}
	}

	words := make([]uint64, n)

	if n == 1 && lengths[0] == 0 {
		// Empty code word for a one-symbol alphabet.
		kraft := new(big.Rat)
		kraft.SetInt64(1)
		return &Code{
			Version: FormatVersion,
			MaxLen:  maxLen,
			Lengths: append([]int(nil), lengths...),
			words:   words,
			Kraft:   kraft,
		}, nil
	}

	// Canonical assignment, MSB first: sort symbols by (length, id).
	type sl struct{ sym, length int }
	order := make([]sl, n)
	for i, l := range lengths {
		order[i] = sl{i, l}
		if l == 0 {
			return nil, ErrBadArgument
		}
	}
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].length != order[j].length {
			return order[i].length < order[j].length
		}
		return order[i].sym < order[j].sym
	})

	var code uint64
	prevLen := 0
	for _, o := range order {
		if prevLen == 0 {
			code = 0
		} else {
			code++
			code <<= uint(o.length - prevLen)
		}
		// code must fit in length bits: top bit at position length-1.
		if o.length < 64 && code >= (uint64(1)<<uint(o.length)) {
			return nil, ErrBadArgument
		}
		words[o.sym] = code
		prevLen = o.length
	}

	kraft := kraftSum(lengths)
	if kraft.Cmp(big.NewRat(1, 1)) != 0 {
		return nil, ErrBadArgument
	}

	return &Code{
		Version: FormatVersion,
		MaxLen:  maxLen,
		Lengths: append([]int(nil), lengths...),
		words:   words,
		Kraft:   kraft,
	}, nil
}

// kraftSum returns sum_i 2^-lengths[i] exactly.
func kraftSum(lengths []int) *big.Rat {
	sum := new(big.Rat)
	for _, l := range lengths {
		if l == 0 {
			sum.Add(sum, big.NewRat(1, 1))
			continue
		}
		denom := new(big.Int).Lsh(big.NewInt(1), uint(l))
		sum.Add(sum, new(big.Rat).SetFrac(big.NewInt(1), denom))
	}
	return sum
}

// Word returns the canonical code word and its length for a symbol.
func (c *Code) Word(sym int) (uint64, int, error) {
	if sym < 0 || sym >= len(c.Lengths) {
		return 0, 0, ErrBadArgument
	}
	return c.words[sym], c.Lengths[sym], nil
}

// SymbolCount reports the alphabet size.
func (c *Code) SymbolCount() int { return len(c.Lengths) }

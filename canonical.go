package prefixcode

import (
	"fmt"
	"math"
	"math/big"
	"sort"
)

// MaxMaxLen is the largest supported code length, in bits.
const MaxMaxLen = 32

// Code is a canonical prefix code for one symbol: the low Len bits of Bits,
// emitted most-significant bit first.
type Code struct {
	Bits uint32
	Len  uint8
}

// Validate checks a code-length assignment against maxLen. It returns
// ErrOversubscribed when the Kraft sum exceeds 1 (decoding would be
// ambiguous) and ErrIncomplete when it is below 1 (some bit patterns have
// no decoding). A nil result means the table is complete and exact.
func Validate(lengths []int, maxLen int) error {
	if maxLen < 1 || maxLen > MaxMaxLen {
		return fmt.Errorf("%v: %d", ErrMaxLen, maxLen)
	}
	full := uint64(1) << uint(maxLen)
	var sum uint64
	for _, l := range lengths {
		if l < 0 || l > maxLen {
			return fmt.Errorf("%v: %d (maxLen %d)", ErrBadLength, l, maxLen)
		}
		if l == 0 {
			continue
		}
		sum += uint64(1) << uint(maxLen-l)
		if sum > full {
			return ErrOversubscribed
		}
	}
	if sum < full {
		return ErrIncomplete
	}
	return nil
}

// CanonicalCodes assigns canonical codes for the given lengths: symbols are
// ordered by (length, symbol value) and codes are handed out consecutively,
// so identical lengths always produce identical codes. It fails on
// oversubscribed or otherwise invalid lengths; incomplete tables are
// accepted (unused bit patterns will be rejected at decode time).
func CanonicalCodes(lengths []int) ([]Code, error) {
	maxLen := 0
	for _, l := range lengths {
		if l < 0 || l > MaxMaxLen {
			return nil, fmt.Errorf("%v: %d", ErrBadLength, l)
		}
		if l > maxLen {
			maxLen = l
		}
	}
	codes := make([]Code, len(lengths))
	if maxLen == 0 {
		return codes, nil
	}
	full := uint64(1) << uint(maxLen)
	var sum uint64
	for _, l := range lengths {
		if l == 0 {
			continue
		}
		sum += uint64(1) << uint(maxLen-l)
		if sum > full {
			return nil, ErrOversubscribed
		}
	}
	order := make([]int, 0, len(lengths))
	for sym, l := range lengths {
		if l > 0 {
			order = append(order, sym)
		}
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := order[i], order[j]
		if lengths[a] != lengths[b] {
			return lengths[a] < lengths[b]
		}
		return a < b
	})
	var code uint32
	prevLen := 0
	for _, sym := range order {
		l := lengths[sym]
		code <<= uint(l - prevLen)
		codes[sym] = Code{Bits: code, Len: uint8(l)}
		code++
		prevLen = l
	}
	return codes, nil
}

// Kraft returns the exact Kraft-McMillan sum of the lengths: sum of 2^-l
// over all assigned codes. The value is 1 for a complete code, below 1 for
// an incomplete code, and above 1 for an oversubscribed (invalid) one.
func Kraft(lengths []int) *big.Rat {
	sum := new(big.Rat)
	term := new(big.Rat)
	one := big.NewInt(1)
	for _, l := range lengths {
		if l <= 0 {
			continue
		}
		term.SetFrac(one, new(big.Int).Lsh(one, uint(l)))
		sum.Add(sum, term)
	}
	return sum
}

// Cost returns the total encoded size in bits: sum(freq[i] * length[i]).
func Cost(freqs []uint64, lengths []int) uint64 {
	var c uint64
	for i, f := range freqs {
		c += f * uint64(lengths[i])
	}
	return c
}

// Entropy returns the zero-order entropy of the frequency distribution in
// bits per symbol, the information-theoretic lower bound for Cost.
func Entropy(freqs []uint64) float64 {
	var total uint64
	for _, f := range freqs {
		total += f
	}
	if total == 0 {
		return 0
	}
	var h float64
	for _, f := range freqs {
		if f == 0 {
			continue
		}
		p := float64(f) / float64(total)
		h -= p * math.Log2(p)
	}
	return h
}

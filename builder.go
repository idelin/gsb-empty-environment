package bigsqrt

import (
	"errors"
	"fmt"
	"math/big"
)

// ErrDigitLimitExceeded is returned by Builder.Feed when a chunk
// would push the total digit count past the configured limit.
// The accumulated value is left unchanged.
var ErrDigitLimitExceeded = errors.New("bigsqrt: decimal digit limit exceeded")

// feedOps is the number of big-integer operations charged per feed:
// parsing the chunk, raising 10 to the chunk length, one multiply
// and one add to fold the chunk into the accumulator.
const feedOps = 4

// Builder accumulates a non-negative integer from a stream of
// decimal chunks. Feeding a chunk that would exceed the digit
// limit is rejected before any expansion happens.
type Builder struct {
	maxDigits int
	digits    int
	n         *big.Int
}

// NewBuilder returns a Builder accepting at most maxDigits decimal
// digits in total.
func NewBuilder(maxDigits int) (*Builder, error) {
	if maxDigits <= 0 {
		return nil, fmt.Errorf("bigsqrt: maxDigits must be positive, got %d", maxDigits)
	}
	return &Builder{maxDigits: maxDigits, n: new(big.Int)}, nil
}

// Feed appends one decimal chunk. The chunk must contain only ASCII
// digits; an empty chunk is a no-op. If the chunk would exceed the
// digit limit, ErrDigitLimitExceeded is returned and the accumulated
// value is not modified. Each accepted chunk costs feedOps against
// the budget (nil budget = unlimited); on budget exhaustion the
// value is likewise left unchanged so the feed can be retried.
func (b *Builder) Feed(chunk string, budget *Budget) error {
	for i := 0; i < len(chunk); i++ {
		if c := chunk[i]; c < '0' || c > '9' {
			return fmt.Errorf("bigsqrt: invalid decimal chunk %q", chunk)
		}
	}
	if len(chunk) == 0 {
		return nil
	}
	if b.digits+len(chunk) > b.maxDigits {
		return ErrDigitLimitExceeded
	}
	if err := budget.charge(feedOps); err != nil {
		return err
	}
	v, ok := new(big.Int).SetString(chunk, 10)
	if !ok {
		return fmt.Errorf("bigsqrt: invalid decimal chunk %q", chunk)
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(len(chunk))), nil)
	b.n.Mul(b.n, scale)
	b.n.Add(b.n, v)
	b.digits += len(chunk)
	return nil
}

// Digits reports how many decimal digits have been accepted.
func (b *Builder) Digits() int { return b.digits }

// Num returns a deep copy of the accumulated value; mutating the
// result does not affect the Builder.
func (b *Builder) Num() *big.Int { return new(big.Int).Set(b.n) }

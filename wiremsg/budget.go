package wiremsg

import "errors"

// Limits caps the resources a single Parse may consume. The zero value is
// not usable; use DefaultLimits or construct explicit limits.
type Limits struct {
	MaxTotalBytes int // total input bytes charged across all nesting levels
	MaxFields     int // total fields decoded across all nesting levels
	MaxDepth      int // maximum nesting depth of TypeMessage fields
}

// DefaultLimits are conservative defaults suitable for untrusted input.
var DefaultLimits = Limits{
	MaxTotalBytes: 1 << 20, // 1 MiB
	MaxFields:     1 << 16,
	MaxDepth:      32,
}

// ErrBudget is returned when a parse would exceed its Limits.
var ErrBudget = errors.New("wiremsg: budget exceeded")

// Budget tracks consumption against Limits during one Parse. Because unknown
// fields share the input buffer instead of copying, the budget is the only
// guard needed against hostile length prefixes: every length is validated
// against the remaining input before it is charged or sliced.
type Budget struct {
	limits Limits
	bytes  int
	fields int
}

// NewBudget creates a Budget enforcing the given limits.
func NewBudget(l Limits) *Budget { return &Budget{limits: l} }

// Used reports bytes and fields charged so far.
func (b *Budget) Used() (bytes, fields int) { return b.bytes, b.fields }

func (b *Budget) chargeBytes(n int) error {
	if b.bytes+n > b.limits.MaxTotalBytes {
		return ErrBudget
	}
	b.bytes += n
	return nil
}

func (b *Budget) chargeField() error {
	if b.fields+1 > b.limits.MaxFields {
		return ErrBudget
	}
	b.fields++
	return nil
}

package strdict

// Budget is a byte allowance for one decode session. It is charged for the
// dictionary bytes of every generation the session binds to, and for every
// staging buffer / decoded output byte produced while expanding the stream.
// A negative limit means unlimited.
type Budget struct {
	limit int64
	used  int64
}

// NewBudget returns a Budget allowing at most limit bytes. limit < 0 means
// no limit.
func NewBudget(limit int64) *Budget { return &Budget{limit: limit} }

// Charge accounts n bytes. It returns ErrBudgetExceeded, without recording
// the charge, when the charge would push usage past the limit.
func (b *Budget) Charge(n int64) error {
	if b == nil || b.limit < 0 {
		return nil
	}
	if n > b.limit-b.used {
		return ErrBudgetExceeded
	}
	b.used += n
	return nil
}

// Used reports the bytes charged so far.
func (b *Budget) Used() int64 {
	if b == nil {
		return 0
	}
	return b.used
}

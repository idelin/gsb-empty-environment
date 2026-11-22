package policy

// RowLimiter tracks how many rows a statement has touched. The executor
// must call Add as it applies changes and roll the statement back when
// Add returns ErrRowLimitExceeded.
type RowLimiter struct {
	max   int
	count int
}

// NewRowLimiter returns a limiter allowing up to max rows. max <= 0
// means unlimited.
func NewRowLimiter(max int) *RowLimiter {
	return &RowLimiter{max: max}
}

// Add records n newly affected rows.
func (l *RowLimiter) Add(n int) error {
	l.count += n
	if l.max > 0 && l.count > l.max {
		return ErrRowLimitExceeded
	}
	return nil
}

// Count returns the number of rows recorded so far.
func (l *RowLimiter) Count() int { return l.count }

package retrysys

// Budget is the single shared request budget and deadline of one top-level
// call. Both the upper retry layer (proxy) and the lower retry layer
// (service) draw physical replica requests from the *same* Budget, so layers
// can never multiply into an uncontrolled number of backend requests.
//
// The simulator is single-threaded by construction, so no synchronization is
// needed: every check-and-decrement is an atomic step in the event loop.
type Budget struct {
	deadline  int
	remaining int
	used      int
	expired   bool
}

// NewBudget creates a budget allowing at most maxRequests physical replica
// requests, none of them sent at a tick >= deadlineTick.
func NewBudget(maxRequests, deadlineTick int) *Budget {
	return &Budget{deadline: deadlineTick, remaining: maxRequests}
}

// Acquire is the only gate through which a physical replica request may be
// sent. It returns true only when the deadline has not passed and budget
// remains. The check and the decrement happen in one simulation step, which
// is the shared-budget invariant: "3 upper x 3 lower" attempts compete for
// one pool instead of producing 9 (or more) requests.
func (b *Budget) Acquire(now int) bool {
	if b.expired || now >= b.deadline || b.remaining <= 0 {
		return false
	}
	b.remaining--
	b.used++
	return true
}

// Expire marks the deadline as reached. Afterwards Acquire always fails and
// in-flight attempts are cancelled by their owners.
func (b *Budget) Expire() { b.expired = true }

// Expired reports whether the deadline has been reached.
func (b *Budget) Expired() bool { return b.expired }

// Used reports how many physical replica requests were actually sent.
func (b *Budget) Used() int { return b.used }

// Remaining reports the unused request budget.
func (b *Budget) Remaining() int { return b.remaining }

// Deadline returns the deadline tick; no request may be sent at or after it.
func (b *Budget) Deadline() int { return b.deadline }

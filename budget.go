package netsec

import (
	"errors"
	"sync/atomic"
)

// ErrBudgetExhausted reports that a root request has already consumed its
// total query budget. It is returned without blocking, so a malicious
// delegation chain cannot keep spawning subqueries past the limit.
var ErrBudgetExhausted = errors.New("netsec: query budget exhausted")

// Budget is a node in the request budget tree.
//
// Every recursive root request owns a root Budget holding the total number
// of subqueries the request is allowed to drive. Nameserver address
// lookups and alias tracking obtain child Budgets (via Child) so that all
// branches of the recursion draw from the same shared total.
//
// A Budget is safe for concurrent use.
type Budget struct {
	root   *Budget
	parent *Budget
	limit  int64

	used   atomic.Int64
	childN atomic.Int64
}

// NewRootBudget creates a root Budget with the given total query limit.
// A zero limit rejects every subquery; a negative limit means unlimited.
func NewRootBudget(limit int) *Budget {
	b := &Budget{limit: int64(limit)}
	b.root = b
	return b
}

// Root returns the root Budget this node draws from.
func (b *Budget) Root() *Budget { return b.root }

// Parent returns the parent Budget, or nil for a root.
func (b *Budget) Parent() *Budget { return b.parent }

// Child creates a child Budget that shares the root total. The child keeps
// its own usage counter for observability, but every charge also counts
// against the root's remaining total.
func (b *Budget) Child() *Budget {
	c := &Budget{
		root:   b.root,
		parent: b,
		limit:  b.root.limit,
	}
	b.root.childN.Add(1)
	return c
}

// Remaining reports the number of subqueries still chargeable against the
// shared root total. A negative result means the budget is unlimited.
func (b *Budget) Remaining() int64 {
	if b.root.limit < 0 {
		return -1
	}
	return b.root.limit - b.root.used.Load()
}

// Used reports how many subqueries this node itself has charged.
func (b *Budget) Used() int64 { return b.used.Load() }

// TotalUsed reports the aggregate charges against the root total.
func (b *Budget) TotalUsed() int64 { return b.root.used.Load() }

// Children reports how many child Budgets have been created under the root.
func (b *Budget) Children() int64 { return b.root.childN.Load() }

// Acquire charges one subquery against the shared root total. It fails
// immediately with ErrBudgetExhausted instead of waiting when the total is
// already spent, or when the limit has been configured as zero.
func (b *Budget) Acquire() error {
	return b.acquire(1)
}

// TryAcquire charges n subqueries only when the whole amount fits inside
// the remaining root total; otherwise it charges nothing and reports false.
func (b *Budget) TryAcquire(n int) bool {
	if n <= 0 {
		return true
	}
	r := b.root
	if r.limit < 0 {
		r.used.Add(int64(n))
		b.used.Add(int64(n))
		return true
	}
	for {
		cur := r.used.Load()
		if cur+int64(n) > r.limit {
			return false
		}
		if r.used.CompareAndSwap(cur, cur+int64(n)) {
			b.used.Add(int64(n))
			return true
		}
	}
}

// Release gives n previously charged subqueries back to the root total.
// The root counter never goes below zero.
func (b *Budget) Release(n int) {
	if n <= 0 {
		return
	}
	r := b.root
	for {
		cur := r.used.Load()
		if cur <= 0 {
			return
		}
		dec := int64(n)
		if dec > cur {
			dec = cur
		}
		if r.used.CompareAndSwap(cur, cur-dec) {
			b.releaseLocal(int64(n))
			return
		}
	}
}

func (b *Budget) releaseLocal(n int64) {
	for {
		cur := b.used.Load()
		if cur <= 0 {
			return
		}
		dec := n
		if dec > cur {
			dec = cur
		}
		if b.used.CompareAndSwap(cur, cur-dec) {
			return
		}
	}
}

func (b *Budget) acquire(n int64) error {
	r := b.root
	if r.limit < 0 {
		r.used.Add(n)
		b.used.Add(n)
		return nil
	}
	for {
		cur := r.used.Load()
		if cur >= r.limit {
			return ErrBudgetExhausted
		}
		if r.used.CompareAndSwap(cur, cur+n) {
			b.used.Add(n)
			return nil
		}
	}
}

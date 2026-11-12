// Package netsec provides a comparison gate for restricted query hosts.
//
// Plugins running in a restricted query environment may only compare
// secret-index values through a trusted Comparator. The Gate wraps that
// comparator so that every comparison is bound to a query ID, a field and
// an operation purpose, the per-query budget is atomically deducted before
// the comparator runs, and the outcome is bound to an idempotent request
// ID. Replaying a request ID with a different operation is rejected, and a
// refunded failure cannot be re-probed under the same request ID.
package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sync"
	"sync/atomic"
)

// Sentinel errors returned by the gate.
var (
	// ErrInvalidRequest indicates a request missing a required binding.
	ErrInvalidRequest = errors.New("netsec: comparison request missing query, request, field or purpose binding")
	// ErrUnknownQuery indicates the query ID has no authorized budget.
	ErrUnknownQuery = errors.New("netsec: query has no authorized comparison budget")
	// ErrBudgetExhausted indicates the query budget cannot cover the comparison.
	ErrBudgetExhausted = errors.New("netsec: comparison budget exhausted")
	// ErrRequestIDConflict indicates a request ID was replayed with a
	// different operation than the one it is bound to.
	ErrRequestIDConflict = errors.New("netsec: request ID replayed with a different operation")
)

// compareCost is the budget cost of a single comparison.
const compareCost int64 = 1

// Comparator is the trusted comparison oracle. Implementations must be
// reentrant: the gate may invoke Compare concurrently and never holds a
// lock while doing so.
type Comparator interface {
	// Compare returns -1, 0 or +1 comparing left against right on field.
	Compare(field string, left, right []byte) (int, error)
}

// ComparatorFunc adapts a function to the Comparator interface.
type ComparatorFunc func(field string, left, right []byte) (int, error)

// Compare implements Comparator.
func (f ComparatorFunc) Compare(field string, left, right []byte) (int, error) {
	return f(field, left, right)
}

// Request binds a single comparison to its query context.
type Request struct {
	QueryID   string // authorized query the comparison is charged to
	RequestID string // idempotency key chosen by the caller
	Field     string // indexed field being compared
	Purpose   string // operation purpose, e.g. "filter", "sort", "join"
	Left      []byte // left operand
	Right     []byte // right operand
}

// Receipt is the outcome of a comparison, bound to its request ID.
type Receipt struct {
	RequestID string
	QueryID   string
	Field     string
	Purpose   string
	Result    int  // comparator outcome: -1, 0 or +1
	Replayed  bool // true when served from the idempotency record
}

// record is the stored idempotency entry for one request ID.
type record struct {
	fingerprint [sha256.Size]byte
	done        chan struct{} // closed when result/err are final
	result      int
	err         error
}

// Gate mediates all plugin comparisons against a trusted comparator.
type Gate struct {
	cmp Comparator

	mu      sync.Mutex // guards budgets and records
	budgets map[string]*atomic.Int64
	records map[string]*record
}

// NewGate returns a Gate wrapping the trusted comparator cmp.
func NewGate(cmp Comparator) *Gate {
	return &Gate{
		cmp:     cmp,
		budgets: make(map[string]*atomic.Int64),
		records: make(map[string]*record),
	}
}

// Authorize grants additional comparison budget to a query ID.
func (g *Gate) Authorize(queryID string, amount int64) {
	g.mu.Lock()
	b, ok := g.budgets[queryID]
	if !ok {
		b = &atomic.Int64{}
		g.budgets[queryID] = b
	}
	g.mu.Unlock()
	b.Add(amount)
}

// Balance reports the remaining budget for a query ID.
func (g *Gate) Balance(queryID string) (int64, bool) {
	g.mu.Lock()
	b, ok := g.budgets[queryID]
	g.mu.Unlock()
	if !ok {
		return 0, false
	}
	return b.Load(), true
}

// Compare executes one gated comparison.
//
// The request ID is first bound to the operation fingerprint; a replay of
// the same ID returns the recorded outcome without re-deducting budget or
// re-invoking the comparator, while a replay carrying a different
// operation is rejected. Budget is then atomically deducted before the
// comparator runs; a comparator failure refunds exactly one deduction, and
// the failure itself is recorded so the same request ID cannot probe again.
func (g *Gate) Compare(req Request) (Receipt, error) {
	if req.QueryID == "" || req.RequestID == "" || req.Field == "" || req.Purpose == "" {
		return Receipt{}, ErrInvalidRequest
	}
	fp := fingerprint(req)

	g.mu.Lock()
	if rec, ok := g.records[req.RequestID]; ok {
		g.mu.Unlock()
		<-rec.done
		if rec.fingerprint != fp {
			return Receipt{}, ErrRequestIDConflict
		}
		return Receipt{
			RequestID: req.RequestID,
			QueryID:   req.QueryID,
			Field:     req.Field,
			Purpose:   req.Purpose,
			Result:    rec.result,
			Replayed:  true,
		}, rec.err
	}
	rec := &record{fingerprint: fp, done: make(chan struct{})}
	g.records[req.RequestID] = rec
	budget, ok := g.budgets[req.QueryID]
	g.mu.Unlock()

	receipt := Receipt{
		RequestID: req.RequestID,
		QueryID:   req.QueryID,
		Field:     req.Field,
		Purpose:   req.Purpose,
	}

	if !ok {
		rec.err = ErrUnknownQuery
		close(rec.done)
		return Receipt{}, rec.err
	}
	if !deduct(budget, compareCost) {
		rec.err = ErrBudgetExhausted
		close(rec.done)
		return Receipt{}, rec.err
	}

	result, err := g.cmp.Compare(req.Field, req.Left, req.Right)
	if err != nil {
		budget.Add(compareCost) // refund the single failed deduction
		rec.err = err
		close(rec.done)
		return Receipt{}, err
	}

	rec.result = result
	close(rec.done)
	receipt.Result = result
	return receipt, nil
}

// deduct atomically removes cost from b, failing without side effects when
// the balance is insufficient.
func deduct(b *atomic.Int64, cost int64) bool {
	for {
		cur := b.Load()
		if cur < cost {
			return false
		}
		if b.CompareAndSwap(cur, cur-cost) {
			return true
		}
	}
}

// fingerprint binds a request ID to its full operation shape.
func fingerprint(req Request) [sha256.Size]byte {
	h := sha256.New()
	writePart(h, []byte(req.QueryID))
	writePart(h, []byte(req.Field))
	writePart(h, []byte(req.Purpose))
	writePart(h, req.Left)
	writePart(h, req.Right)
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

func writePart(h interface{ Write([]byte) (int, error) }, p []byte) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(p)))
	h.Write(n[:])
	h.Write(p)
}

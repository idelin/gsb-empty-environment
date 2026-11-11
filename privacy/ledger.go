package privacy

import (
	"errors"
	"fmt"
	"maps"
	"sync"
)

// ErrInsufficientBudget is returned when any involved group cannot cover the
// query cost. The reservation is all-or-nothing: no group's budget is touched.
var ErrInsufficientBudget = errors.New("insufficient privacy budget")

// errAlreadySettled is returned when reserving a query key whose budget was
// already published; callers should serve the stored result instead.
var errAlreadySettled = errors.New("query key already settled")

// Ledger tracks per-group privacy budgets. A query reserves budget from every
// group it touches atomically; the reservation is settled only when the result
// is published, and released (refunded) only if the query never publishes.
type Ledger struct {
	mu        sync.Mutex
	remaining map[string]float64
	pending   map[string]map[string]float64 // query key -> group -> reserved cost
	spent     map[string]map[string]float64 // query key -> group -> settled cost
	results   map[string]float64            // query key -> published noisy value
	journal   *Journal
}

// NewLedger rebuilds the ledger from the journal replay on top of the given
// total budgets. Reservations left pending by a crash (never settled, never
// released) are refunded: budget is only consumed by published results.
func NewLedger(j *Journal, replay []record, budgets map[string]float64) *Ledger {
	l := &Ledger{
		remaining: maps.Clone(budgets),
		pending:   make(map[string]map[string]float64),
		spent:     make(map[string]map[string]float64),
		results:   make(map[string]float64),
		journal:   j,
	}
	for _, r := range replay {
		switch r.Type {
		case recReserve:
			l.applyReserve(r.Key, r.Costs)
		case recRelease:
			l.applyRelease(r.Key)
		case recResult:
			l.applySettle(r.Key, r.Value)
		}
	}
	for key := range l.pending {
		l.applyRelease(key)
	}
	return l
}

// Reserve atomically deducts costs from every involved group. If any group
// lacks budget, nothing is deducted.
func (l *Ledger) Reserve(key string, costs map[string]float64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.spent[key]; ok {
		return errAlreadySettled
	}
	if _, ok := l.pending[key]; ok {
		return fmt.Errorf("reservation %q already in flight", key)
	}
	for g, c := range costs {
		if l.remaining[g] < c {
			return fmt.Errorf("group %q: %w", g, ErrInsufficientBudget)
		}
	}
	if err := l.journal.append(record{Type: recReserve, Key: key, Costs: costs}); err != nil {
		return err
	}
	l.applyReserve(key, costs)
	return nil
}

// Settle publishes the noisy result for key, converting its reservation into
// permanent spend. It is idempotent: a repeated call after a crash is a no-op.
func (l *Ledger) Settle(key string, value float64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.spent[key]; ok {
		return nil
	}
	if err := l.journal.append(record{Type: recResult, Key: key, Value: value}); err != nil {
		return err
	}
	l.applySettle(key, value)
	return nil
}

// Release refunds a pending reservation. Once the key is settled (published),
// release is a no-op: cancellation never refunds published budget.
func (l *Ledger) Release(key string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.spent[key]; ok {
		return nil
	}
	if _, ok := l.pending[key]; !ok {
		return nil
	}
	if err := l.journal.append(record{Type: recRelease, Key: key}); err != nil {
		return err
	}
	l.applyRelease(key)
	return nil
}

// Result returns the published noisy value for key, if any.
func (l *Ledger) Result(key string) (float64, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	v, ok := l.results[key]
	return v, ok
}

// Remaining returns the group's unspent, unreserved budget.
func (l *Ledger) Remaining(group string) float64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.remaining[group]
}

// Spent returns the group's total settled (published) spend.
func (l *Ledger) Spent(group string) float64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	var total float64
	for _, costs := range l.spent {
		total += costs[group]
	}
	return total
}

func (l *Ledger) applyReserve(key string, costs map[string]float64) {
	for g, c := range costs {
		l.remaining[g] -= c
	}
	l.pending[key] = maps.Clone(costs)
}

func (l *Ledger) applyRelease(key string) {
	costs, ok := l.pending[key]
	if !ok {
		return
	}
	for g, c := range costs {
		l.remaining[g] += c
	}
	delete(l.pending, key)
}

func (l *Ledger) applySettle(key string, value float64) {
	l.spent[key] = l.pending[key]
	delete(l.pending, key)
	l.results[key] = value
}

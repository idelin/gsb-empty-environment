// Package deduct implements a deduction credit pool (抵扣库) whose quotas
// carry an expiry time and an applicable category. Consumption is split
// across quotas by earliest expiry then id, never touching inapplicable
// quotas. Revoking a consumption restores the original quota amounts, but
// any portion whose quota has already expired at revoke time is only
// recorded as an invalid return (失效返还). Every mutation is appended to a
// timeline ledger for auditing.
package deduct

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

var (
	ErrInsufficient   = errors.New("deduct: insufficient applicable quota")
	ErrNotFound       = errors.New("deduct: consumption not found")
	ErrAlreadyRevoked = errors.New("deduct: consumption already revoked")
)

// Quota is a single credit line with an applicable category and an expiry.
// A quota is usable for consumption when now is strictly before ExpiresAt.
type Quota struct {
	ID        int64
	Category  string
	Amount    int64
	Remaining int64
	ExpiresAt time.Time
}

// EventType identifies the kind of ledger entry.
type EventType string

const (
	EventGrant   EventType = "GRANT"
	EventConsume EventType = "CONSUME"
	EventRevoke  EventType = "REVOKE"
)

// SplitLine records how much of an event touched one quota. On revoke
// lines, Restored is the amount returned to the usable balance and Expired
// is the amount only recorded as an invalid return because the quota had
// already expired.
type SplitLine struct {
	QuotaID  int64
	Amount   int64
	Restored int64
	Expired  int64
}

// Event is one immutable entry in the timeline ledger.
type Event struct {
	Seq        int64
	At         time.Time
	Type       EventType
	Category   string
	Amount     int64
	Lines      []SplitLine
	ConsumeSeq int64 // revoke only: seq of the consumption being revoked
}

// Store holds quotas and the append-only timeline ledger.
type Store struct {
	quotas  map[int64]*Quota
	events  []Event
	revoked map[int64]bool // consume seq -> revoked
	nextID  int64
}

func NewStore() *Store {
	return &Store{
		quotas:  make(map[int64]*Quota),
		revoked: make(map[int64]bool),
	}
}

// Grant adds a quota and records a GRANT event at now.
func (s *Store) Grant(category string, amount int64, expiresAt, now time.Time) int64 {
	s.nextID++
	q := &Quota{ID: s.nextID, Category: category, Amount: amount, Remaining: amount, ExpiresAt: expiresAt}
	s.quotas[q.ID] = q
	s.append(Event{At: now, Type: EventGrant, Category: category, Amount: amount,
		Lines: []SplitLine{{QuotaID: q.ID, Amount: amount}}})
	return q.ID
}

func (s *Store) append(e Event) Event {
	e.Seq = int64(len(s.events)) + 1
	s.events = append(s.events, e)
	return e
}

// Consume splits amount across applicable quotas (matching category, not
// expired at now) ordered by earliest ExpiresAt then smallest ID. It is
// atomic: if the applicable total is insufficient nothing is deducted.
func (s *Store) Consume(category string, amount int64, now time.Time) (Event, error) {
	if amount <= 0 {
		return Event{}, fmt.Errorf("deduct: amount must be positive")
	}
	var eligible []*Quota
	var total int64
	for _, q := range s.quotas {
		if q.Category == category && now.Before(q.ExpiresAt) && q.Remaining > 0 {
			eligible = append(eligible, q)
			total += q.Remaining
		}
	}
	if total < amount {
		return Event{}, ErrInsufficient
	}
	sort.Slice(eligible, func(i, j int) bool {
		a, b := eligible[i], eligible[j]
		if !a.ExpiresAt.Equal(b.ExpiresAt) {
			return a.ExpiresAt.Before(b.ExpiresAt)
		}
		return a.ID < b.ID
	})
	left := amount
	var lines []SplitLine
	for _, q := range eligible {
		if left == 0 {
			break
		}
		take := q.Remaining
		if take > left {
			take = left
		}
		q.Remaining -= take
		left -= take
		lines = append(lines, SplitLine{QuotaID: q.ID, Amount: take})
	}
	return s.append(Event{At: now, Type: EventConsume, Category: category, Amount: amount, Lines: lines}), nil
}

// Revoke undoes the consumption with the given event seq. Each split line
// is returned to its original quota: if the quota is still valid at now the
// amount is restored to the usable balance, otherwise it is only recorded
// as an invalid return (失效返还) on the ledger line.
func (s *Store) Revoke(consumeSeq int64, now time.Time) (Event, error) {
	var target *Event
	for i := range s.events {
		if s.events[i].Seq == consumeSeq && s.events[i].Type == EventConsume {
			target = &s.events[i]
			break
		}
	}
	if target == nil {
		return Event{}, ErrNotFound
	}
	if s.revoked[consumeSeq] {
		return Event{}, ErrAlreadyRevoked
	}
	s.revoked[consumeSeq] = true
	var lines []SplitLine
	for _, l := range target.Lines {
		q := s.quotas[l.QuotaID]
		rl := SplitLine{QuotaID: l.QuotaID, Amount: l.Amount}
		if now.Before(q.ExpiresAt) {
			q.Remaining += l.Amount
			rl.Restored = l.Amount
		} else {
			rl.Expired = l.Amount
		}
		lines = append(lines, rl)
	}
	return s.append(Event{At: now, Type: EventRevoke, Category: target.Category,
		Amount: target.Amount, Lines: lines, ConsumeSeq: consumeSeq}), nil
}

// Balance sums the remaining usable amount for category at now.
func (s *Store) Balance(category string, now time.Time) int64 {
	var sum int64
	for _, q := range s.quotas {
		if q.Category == category && now.Before(q.ExpiresAt) {
			sum += q.Remaining
		}
	}
	return sum
}

// InvalidReturns sums all expired (失效返还) amounts recorded by revokes.
func (s *Store) InvalidReturns() int64 {
	var sum int64
	for _, e := range s.events {
		if e.Type != EventRevoke {
			continue
		}
		for _, l := range e.Lines {
			sum += l.Expired
		}
	}
	return sum
}

// Ledger returns a copy of the timeline of events in order.
func (s *Store) Ledger() []Event {
	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out
}

// Quota returns a snapshot of a quota by id.
func (s *Store) Quota(id int64) (Quota, bool) {
	q, ok := s.quotas[id]
	if !ok {
		return Quota{}, false
	}
	return *q, true
}

// Package quotaledger manages deduction quotas (抵扣额度) that carry an
// expiry time and a set of applicable categories, and keeps a timeline
// ledger of every grant, consumption split and revocation so balances can
// be audited against the event stream.
//
// Rules:
//   - A quota is usable at time t iff t is not after ExpiresAt (the expiry
//     instant itself is still usable; same-day boundary is inclusive).
//   - Consumption only splits across quotas applicable to the consumed
//     category; it never touches an inapplicable quota.
//   - Consumption drains quotas ordered by earliest ExpiresAt, then by ID.
//   - Revoking a consumption returns each split portion to its original
//     quota; portions whose quota has already expired at revocation time
//     are recorded as expired returns (失效返还) and are not restored.
package quotaledger

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Category is a business category a quota may apply to.
type Category string

var (
	// ErrInsufficient is returned when applicable, unexpired quotas cannot
	// cover the requested amount. The ledger is left untouched.
	ErrInsufficient = errors.New("quotaledger: insufficient applicable quota")
	// ErrNotFound is returned for unknown consumption IDs.
	ErrNotFound = errors.New("quotaledger: consumption not found")
	// ErrAlreadyRevoked is returned when a consumption is revoked twice.
	ErrAlreadyRevoked = errors.New("quotaledger: consumption already revoked")
	// ErrInvalidAmount is returned for non-positive amounts.
	ErrInvalidAmount = errors.New("quotaledger: amount must be positive")
)

// Quota is a single deduction quota (额度).
type Quota struct {
	ID        int64
	Amount    int64 // originally granted amount
	Remaining int64 // currently available amount
	// Categories lists the categories this quota applies to.
	// An empty set means the quota is unrestricted (通用).
	Categories map[Category]bool
	ExpiresAt  time.Time
}

// AppliesTo reports whether the quota may be consumed for category c.
func (q *Quota) AppliesTo(c Category) bool {
	if len(q.Categories) == 0 {
		return true
	}
	return q.Categories[c]
}

// usableAt reports whether the quota can be consumed at time t.
// The expiry instant itself is still usable (inclusive boundary).
func (q *Quota) usableAt(t time.Time) bool {
	return !t.After(q.ExpiresAt)
}

// Split is one portion of a consumption charged to a single quota.
type Split struct {
	QuotaID int64
	Amount  int64
}

// Consumption is a recorded consume operation with its quota splits.
type Consumption struct {
	ID       int64
	Category Category
	Amount   int64
	At       time.Time
	Splits   []Split
	Revoked  bool
}

// EntryKind classifies timeline ledger entries.
type EntryKind int

const (
	EntryGrant EntryKind = iota + 1
	EntryConsume
	EntryRevokeRestore // portion returned to a still-valid quota
	EntryRevokeExpire  // 失效返还: portion expired before revocation
)

func (k EntryKind) String() string {
	switch k {
	case EntryGrant:
		return "GRANT"
	case EntryConsume:
		return "CONSUME"
	case EntryRevokeRestore:
		return "REVOKE_RESTORE"
	case EntryRevokeExpire:
		return "REVOKE_EXPIRE"
	default:
		return "UNKNOWN"
	}
}

// Entry is one line of the timeline ledger (时间线账).
type Entry struct {
	Seq       int64 // monotonically increasing, defines event order
	At        time.Time
	Kind      EntryKind
	QuotaID   int64
	ConsumeID int64 // 0 for grants
	Category  Category
	Amount    int64
}

// Ledger manages quotas and records the timeline of all movements.
type Ledger struct {
	mu           sync.Mutex
	now          func() time.Time
	nextQuotaID  int64
	nextConsumID int64
	seq          int64
	quotas       map[int64]*Quota
	consumptions map[int64]*Consumption
	entries      []Entry
}

// NewLedger creates an empty ledger. now supplies the current time;
// pass time.Now for production or a manual clock in tests.
func NewLedger(now func() time.Time) *Ledger {
	if now == nil {
		now = time.Now
	}
	return &Ledger{
		now:          now,
		quotas:       make(map[int64]*Quota),
		consumptions: make(map[int64]*Consumption),
	}
}

// Grant adds a new quota and records a GRANT entry. categories may be
// empty to create an unrestricted quota.
func (l *Ledger) Grant(amount int64, categories []Category, expiresAt time.Time) (*Quota, error) {
	if amount <= 0 {
		return nil, ErrInvalidAmount
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	l.nextQuotaID++
	q := &Quota{
		ID:        l.nextQuotaID,
		Amount:    amount,
		Remaining: amount,
		ExpiresAt: expiresAt,
	}
	if len(categories) > 0 {
		q.Categories = make(map[Category]bool, len(categories))
		for _, c := range categories {
			q.Categories[c] = true
		}
	}
	l.quotas[q.ID] = q
	l.append(Entry{At: l.now(), Kind: EntryGrant, QuotaID: q.ID, Amount: amount})
	return q, nil
}

// Consume charges amount against quotas applicable to category, splitting
// across quotas ordered by earliest expiry then ID. It never splits into
// inapplicable or expired quotas. On insufficient funds nothing changes.
func (l *Ledger) Consume(category Category, amount int64) (*Consumption, error) {
	if amount <= 0 {
		return nil, ErrInvalidAmount
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	at := l.now()
	candidates := make([]*Quota, 0, len(l.quotas))
	for _, q := range l.quotas {
		if q.Remaining > 0 && q.AppliesTo(category) && q.usableAt(at) {
			candidates = append(candidates, q)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if !a.ExpiresAt.Equal(b.ExpiresAt) {
			return a.ExpiresAt.Before(b.ExpiresAt)
		}
		return a.ID < b.ID
	})

	var total int64
	for _, q := range candidates {
		total += q.Remaining
	}
	if total < amount {
		return nil, ErrInsufficient
	}

	l.nextConsumID++
	c := &Consumption{ID: l.nextConsumID, Category: category, Amount: amount, At: at}
	left := amount
	for _, q := range candidates {
		if left == 0 {
			break
		}
		take := q.Remaining
		if take > left {
			take = left
		}
		q.Remaining -= take
		left -= take
		c.Splits = append(c.Splits, Split{QuotaID: q.ID, Amount: take})
		l.append(Entry{
			At: at, Kind: EntryConsume, QuotaID: q.ID,
			ConsumeID: c.ID, Category: category, Amount: take,
		})
	}
	l.consumptions[c.ID] = c
	return c, nil
}

// Revoke undoes a consumption, returning each split portion to its
// original quota. Portions whose quota has already expired at revocation
// time are recorded as expired returns (失效返还) instead of being
// restored to the quota balance.
func (l *Ledger) Revoke(consumeID int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	c, ok := l.consumptions[consumeID]
	if !ok {
		return ErrNotFound
	}
	if c.Revoked {
		return ErrAlreadyRevoked
	}
	c.Revoked = true

	at := l.now()
	for _, s := range c.Splits {
		q := l.quotas[s.QuotaID]
		if at.After(q.ExpiresAt) {
			l.append(Entry{
				At: at, Kind: EntryRevokeExpire, QuotaID: q.ID,
				ConsumeID: c.ID, Category: c.Category, Amount: s.Amount,
			})
			continue
		}
		q.Remaining += s.Amount
		l.append(Entry{
			At: at, Kind: EntryRevokeRestore, QuotaID: q.ID,
			ConsumeID: c.ID, Category: c.Category, Amount: s.Amount,
		})
	}
	return nil
}

// Quota returns a snapshot of the quota with the given ID.
func (l *Ledger) Quota(id int64) (Quota, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	q, ok := l.quotas[id]
	if !ok {
		return Quota{}, false
	}
	return *q, true
}

// Entries returns the full timeline ledger in event order.
func (l *Ledger) Entries() []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Entry, len(l.entries))
	copy(out, l.entries)
	return out
}

// RemainingAt reports the total remaining balance across all quotas that
// are applicable to category and still usable at time t. An empty
// category matches only unrestricted quotas' applicability rules, so pass
// a concrete category.
func (l *Ledger) RemainingAt(category Category, t time.Time) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	var sum int64
	for _, q := range l.quotas {
		if q.Remaining > 0 && q.AppliesTo(category) && q.usableAt(t) {
			sum += q.Remaining
		}
	}
	return sum
}

func (l *Ledger) append(e Entry) {
	l.seq++
	e.Seq = l.seq
	l.entries = append(l.entries, e)
}

func (c *Consumption) String() string {
	return fmt.Sprintf("Consumption#%d %s %d at %s splits=%v revoked=%v",
		c.ID, c.Category, c.Amount, c.At.Format(time.RFC3339), c.Splits, c.Revoked)
}

// Package bitemporal implements a bitemporal price rule ledger.
//
// Each price rule is recorded along two time axes:
//   - Valid time: the business interval [ValidFrom, ValidTo) during which
//     the price is in effect. Intervals are left-closed, right-open.
//   - Transaction time (KnownAt): the moment the system learned the rule.
//
// Queries are evaluated as of a given transaction time: only rules known at
// or before that instant are visible. Overlapping rules are resolved by
// "last known wins" — the rule with the greatest KnownAt takes precedence.
// Retractions only affect queries whose transaction time is at or after the
// retraction instant; history before that point is immutable.
//
// All amounts are integer cents.
package bitemporal

import "errors"

// ErrNotFound is returned when no visible rule covers the queried valid time.
var ErrNotFound = errors.New("bitemporal: no price rule covers the query")

// Rule is a single price fact recorded in the ledger.
type Rule struct {
	// ID identifies the rule; retractions reference it.
	ID string
	// ValidFrom is the inclusive start of the business interval.
	ValidFrom int64
	// ValidTo is the exclusive end of the business interval.
	ValidTo int64
	// PriceCents is the price in integer cents.
	PriceCents int64
	// KnownAt is the transaction time at which the rule became known.
	KnownAt int64
}

// retraction records that rule ID was withdrawn at transaction time At.
type retraction struct {
	ruleID string
	at     int64
}

// Ledger is an append-only bitemporal store of price rules.
type Ledger struct {
	rules       []Rule
	retractions []retraction
}

// NewLedger returns an empty ledger.
func NewLedger() *Ledger { return &Ledger{} }

// Add records a price rule. Rules are immutable once added.
func (l *Ledger) Add(r Rule) {
	l.rules = append(l.rules, r)
}

// Retract withdraws the rule with the given ID as of transaction time at.
// The retraction is only visible to queries whose transaction time is >= at;
// earlier as-of views still see the rule.
func (l *Ledger) Retract(ruleID string, at int64) {
	l.retractions = append(l.retractions, retraction{ruleID: ruleID, at: at})
}

// retractedAsOf reports whether the rule was retracted at or before asOf.
func (l *Ledger) retractedAsOf(ruleID string, asOf int64) bool {
	for _, rt := range l.retractions {
		if rt.ruleID == ruleID && rt.at <= asOf {
			return true
		}
	}
	return false
}

// PriceAt returns the price in cents effective at business time validTime,
// using only knowledge available at transaction time asOf.
//
// A rule is visible when it was known at or before asOf and not retracted at
// or before asOf. Among visible rules whose interval covers validTime, the
// one with the greatest KnownAt wins; ties break in favor of the rule added
// last. ErrNotFound is returned when no visible rule covers validTime.
func (l *Ledger) PriceAt(validTime, asOf int64) (int64, error) {
	best := -1
	for i, r := range l.rules {
		if r.KnownAt > asOf {
			continue
		}
		if validTime < r.ValidFrom || validTime >= r.ValidTo {
			continue
		}
		if l.retractedAsOf(r.ID, asOf) {
			continue
		}
		if best == -1 || r.KnownAt >= l.rules[best].KnownAt {
			best = i
		}
	}
	if best == -1 {
		return 0, ErrNotFound
	}
	return l.rules[best].PriceCents, nil
}

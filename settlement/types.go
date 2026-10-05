// Package settlement implements a coverage settlement simulator.
//
// The simulator is driven entirely by caller-supplied rules (coverage
// window, waiting period, deductible, and limits). It does not encode
// any real insurance policy clauses.
//
// All monetary amounts are integer cents to avoid floating point drift.
package settlement

import (
	"fmt"
	"time"
)

// DateLayout is the civil-date format used by rules and events.
const DateLayout = "2006-01-02"

// LimitScope determines how a limit is applied.
type LimitScope string

const (
	// ScopePerEvent caps the payout of each individual event.
	ScopePerEvent LimitScope = "per_event"
	// ScopeAggregate caps the cumulative payout across all matching
	// events. A limit with multiple (or zero) categories is a shared
	// limit consumed by every matching event in processing order.
	ScopeAggregate LimitScope = "aggregate"
)

// DeductibleScope determines how the deductible is applied.
type DeductibleScope string

const (
	// DeductiblePerEvent subtracts the deductible from every event.
	DeductiblePerEvent DeductibleScope = "per_event"
	// DeductibleAggregate subtracts the deductible once, consumed by
	// events in processing order until exhausted.
	DeductibleAggregate DeductibleScope = "aggregate"
)

// LimitRule caps payouts. Categories selects which event categories the
// limit applies to; an empty list matches every category (fully shared).
type LimitRule struct {
	ID         string     `json:"id"`
	Scope      LimitScope `json:"scope"`
	Amount     int64      `json:"amount"` // cents
	Categories []string   `json:"categories,omitempty"`
}

// AppliesTo reports whether the limit matches the given category.
func (l LimitRule) AppliesTo(category string) bool {
	if len(l.Categories) == 0 {
		return true
	}
	for _, c := range l.Categories {
		if c == category {
			return true
		}
	}
	return false
}

// DeductibleRule configures the deductible.
type DeductibleRule struct {
	Scope  DeductibleScope `json:"scope"`
	Amount int64           `json:"amount"` // cents
}

// Rule is the full settlement rule supplied by the caller.
type Rule struct {
	CoverageStart string          `json:"coverage_start"` // DateLayout
	CoverageEnd   string          `json:"coverage_end"`   // inclusive
	WaitingDays   int             `json:"waiting_days"`   // days from CoverageStart
	Deductible    *DeductibleRule `json:"deductible,omitempty"`
	Limits        []LimitRule     `json:"limits,omitempty"`
}

// ClaimEvent is a claim. Events may arrive in any order relative to
// their incident dates; the arrival order is the submission order.
type ClaimEvent struct {
	ID           string `json:"id"`
	IncidentDate string `json:"incident_date"` // DateLayout
	Amount       int64  `json:"amount"`        // cents claimed
	Category     string `json:"category"`
}

// Deduction reason codes recorded on ledger records.
const (
	ReasonOutOfCoverage = "OUT_OF_COVERAGE" // 不在保障区间内
	ReasonWaitingPeriod = "WAITING_PERIOD"  // 等待期内
	ReasonDeductible    = "DEDUCTIBLE"      // 免赔额扣减
	ReasonPerEventLimit = "PER_EVENT_LIMIT" // 单次限额扣减
	ReasonSharedLimit   = "SHARED_LIMIT"    // 累计/共享限额扣减
)

// Deduction is one reason an event's payout was reduced.
type Deduction struct {
	Reason  string `json:"reason"`
	Amount  int64  `json:"amount"` // cents withheld for this reason
	LimitID string `json:"limit_id,omitempty"`
}

// Ledger record types.
const (
	RecordPayment  = "PAYMENT"  // 赔付
	RecordDenied   = "DENIED"   // 拒赔（全额扣减）
	RecordCatchUp  = "CATCH_UP" // 追补：重算后补付差额
	RecordClawback = "CLAWBACK" // 追回：重算后收回差额
)

// Record is one ledger entry. Amount is signed: positive pays out,
// negative recovers. Trigger is the event whose arrival produced this
// record. Deductions reflects the event's current full deduction list.
type Record struct {
	Seq        int         `json:"seq"`
	Type       string      `json:"type"`
	EventID    string      `json:"event_id"`
	Trigger    string      `json:"trigger"`
	Amount     int64       `json:"amount"` // cents, signed
	Deductions []Deduction `json:"deductions,omitempty"`
}

// LimitUsage reports cumulative consumption of one aggregate limit.
type LimitUsage struct {
	LimitID string `json:"limit_id"`
	Used    int64  `json:"used"`
	Cap     int64  `json:"cap"`
}

// Summary aggregates the final settlement state.
type Summary struct {
	TotalPaid  int64            `json:"total_paid"`
	NetByEvent map[string]int64 `json:"net_by_event"`
	LimitUsage []LimitUsage     `json:"limit_usage"`
}

func parseDate(s string) (time.Time, error) {
	t, err := time.Parse(DateLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q (want YYYY-MM-DD)", s)
	}
	return t, nil
}

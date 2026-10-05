package settlement

import (
	"fmt"
	"sort"
	"time"
)

// Engine settles claim events against a Rule. Events are submitted in
// arrival order; late-arriving events (incident date earlier than
// already-settled events) trigger a deterministic recomputation, and
// differences are reconciled through catch-up / clawback records.
type Engine struct {
	rule       Rule
	start      time.Time
	end        time.Time
	waitingEnd time.Time // first eligible incident date

	events []ClaimEvent   // arrival order
	seq    map[string]int // event ID -> arrival sequence
	paid   map[string]int64
	usage  map[string]int64 // aggregate limit ID -> cumulative paid
	ledger []Record
}

// NewEngine validates the rule and returns a ready engine.
func NewEngine(rule Rule) (*Engine, error) {
	start, err := parseDate(rule.CoverageStart)
	if err != nil {
		return nil, fmt.Errorf("coverage_start: %v", err)
	}
	end, err := parseDate(rule.CoverageEnd)
	if err != nil {
		return nil, fmt.Errorf("coverage_end: %v", err)
	}
	if end.Before(start) {
		return nil, fmt.Errorf("coverage_end %s is before coverage_start %s", rule.CoverageEnd, rule.CoverageStart)
	}
	if rule.WaitingDays < 0 {
		return nil, fmt.Errorf("waiting_days must be >= 0")
	}
	if rule.Deductible != nil {
		if rule.Deductible.Amount < 0 {
			return nil, fmt.Errorf("deductible amount must be >= 0")
		}
		switch rule.Deductible.Scope {
		case DeductiblePerEvent, DeductibleAggregate:
		default:
			return nil, fmt.Errorf("unknown deductible scope %q", rule.Deductible.Scope)
		}
	}
	seen := map[string]bool{}
	for _, l := range rule.Limits {
		if l.ID == "" {
			return nil, fmt.Errorf("limit id must not be empty")
		}
		if seen[l.ID] {
			return nil, fmt.Errorf("duplicate limit id %q", l.ID)
		}
		seen[l.ID] = true
		if l.Amount < 0 {
			return nil, fmt.Errorf("limit %q amount must be >= 0", l.ID)
		}
		switch l.Scope {
		case ScopePerEvent, ScopeAggregate:
		default:
			return nil, fmt.Errorf("limit %q: unknown scope %q", l.ID, l.Scope)
		}
	}
	return &Engine{
		rule:       rule,
		start:      start,
		end:        end,
		waitingEnd: start.AddDate(0, 0, rule.WaitingDays),
		seq:        map[string]int{},
		paid:       map[string]int64{},
		usage:      map[string]int64{},
	}, nil
}

// outcome is the recomputed settlement of one event.
type outcome struct {
	approved   int64
	deductions []Deduction
}

// sortedEvents returns all known events in deterministic processing
// order: incident date first, arrival sequence as tie-breaker.
func (e *Engine) sortedEvents() []ClaimEvent {
	evs := make([]ClaimEvent, len(e.events))
	copy(evs, e.events)
	sort.SliceStable(evs, func(i, j int) bool {
		di, _ := parseDate(evs[i].IncidentDate)
		dj, _ := parseDate(evs[j].IncidentDate)
		if !di.Equal(dj) {
			return di.Before(dj)
		}
		return e.seq[evs[i].ID] < e.seq[evs[j].ID]
	})
	return evs
}

// compute deterministically settles every known event from scratch and
// returns per-event outcomes plus aggregate-limit usage.
func (e *Engine) compute() (map[string]outcome, map[string]int64, error) {
	outcomes := map[string]outcome{}
	usage := map[string]int64{}
	var deductibleLeft int64
	if e.rule.Deductible != nil && e.rule.Deductible.Scope == DeductibleAggregate {
		deductibleLeft = e.rule.Deductible.Amount
	}

	for _, ev := range e.sortedEvents() {
		date, err := parseDate(ev.IncidentDate)
		if err != nil {
			return nil, nil, fmt.Errorf("event %q: %v", ev.ID, err)
		}
		var ds []Deduction
		eligible := ev.Amount

		switch {
		case date.Before(e.start) || date.After(e.end):
			ds = append(ds, Deduction{Reason: ReasonOutOfCoverage, Amount: eligible})
			eligible = 0
		case date.Before(e.waitingEnd):
			ds = append(ds, Deduction{Reason: ReasonWaitingPeriod, Amount: eligible})
			eligible = 0
		}

		if eligible > 0 && e.rule.Deductible != nil {
			var d int64
			switch e.rule.Deductible.Scope {
			case DeductiblePerEvent:
				d = min64(e.rule.Deductible.Amount, eligible)
			case DeductibleAggregate:
				d = min64(deductibleLeft, eligible)
				deductibleLeft -= d
			}
			if d > 0 {
				ds = append(ds, Deduction{Reason: ReasonDeductible, Amount: d})
				eligible -= d
			}
		}

		if eligible > 0 {
			// Per-event limits: the most restrictive matching cap wins.
			var capID string
			capAmount := int64(-1)
			for _, l := range e.rule.Limits {
				if l.Scope != ScopePerEvent || !l.AppliesTo(ev.Category) {
					continue
				}
				if capAmount < 0 || l.Amount < capAmount {
					capAmount, capID = l.Amount, l.ID
				}
			}
			if capAmount >= 0 && eligible > capAmount {
				ds = append(ds, Deduction{Reason: ReasonPerEventLimit, Amount: eligible - capAmount, LimitID: capID})
				eligible = capAmount
			}

			// Aggregate (shared) limits: consumed in rule order.
			for _, l := range e.rule.Limits {
				if l.Scope != ScopeAggregate || !l.AppliesTo(ev.Category) {
					continue
				}
				available := l.Amount - usage[l.ID]
				if eligible > available {
					ds = append(ds, Deduction{Reason: ReasonSharedLimit, Amount: eligible - available, LimitID: l.ID})
					eligible = available
				}
			}
			for _, l := range e.rule.Limits {
				if l.Scope == ScopeAggregate && l.AppliesTo(ev.Category) {
					usage[l.ID] += eligible
				}
			}
		}

		outcomes[ev.ID] = outcome{approved: eligible, deductions: ds}
	}
	return outcomes, usage, nil
}

// Submit registers one event in arrival order, recomputes the full
// settlement, and appends payment / catch-up / clawback records so the
// ledger converges to the recomputed state.
func (e *Engine) Submit(ev ClaimEvent) error {
	if ev.ID == "" {
		return fmt.Errorf("event id must not be empty")
	}
	if _, dup := e.seq[ev.ID]; dup {
		return fmt.Errorf("duplicate event id %q", ev.ID)
	}
	if ev.Amount < 0 {
		return fmt.Errorf("event %q: amount must be >= 0", ev.ID)
	}
	if _, err := parseDate(ev.IncidentDate); err != nil {
		return fmt.Errorf("event %q: %v", ev.ID, err)
	}

	e.seq[ev.ID] = len(e.events)
	e.events = append(e.events, ev)

	outcomes, usage, err := e.compute()
	if err != nil {
		return err
	}
	// Invariant: shared/aggregate limits are never exceeded.
	for _, l := range e.rule.Limits {
		if l.Scope == ScopeAggregate && usage[l.ID] > l.Amount {
			return fmt.Errorf("internal error: limit %q exceeded: used %d > cap %d", l.ID, usage[l.ID], l.Amount)
		}
	}
	e.usage = usage

	for _, ev := range e.sortedEvents() {
		o := outcomes[ev.ID]
		prev, seen := e.paid[ev.ID]
		switch {
		case !seen:
			typ := RecordPayment
			if o.approved == 0 {
				typ = RecordDenied
			}
			e.append(Record{Type: typ, EventID: ev.ID, Trigger: ev.ID, Amount: o.approved, Deductions: o.deductions})
			e.paid[ev.ID] = o.approved
		case o.approved != prev:
			diff := o.approved - prev
			typ := RecordCatchUp
			if diff < 0 {
				typ = RecordClawback
			}
			e.append(Record{Type: typ, EventID: ev.ID, Trigger: e.events[len(e.events)-1].ID, Amount: diff, Deductions: o.deductions})
			e.paid[ev.ID] = o.approved
		}
	}
	return nil
}

func (e *Engine) append(r Record) {
	r.Seq = len(e.ledger) + 1
	e.ledger = append(e.ledger, r)
}

// Ledger returns the records emitted so far, in order.
func (e *Engine) Ledger() []Record {
	out := make([]Record, len(e.ledger))
	copy(out, e.ledger)
	return out
}

// NetPaid returns the current net paid amount per event ID.
func (e *Engine) NetPaid() map[string]int64 {
	out := make(map[string]int64, len(e.paid))
	for k, v := range e.paid {
		out[k] = v
	}
	return out
}

// Validate replays the ledger and checks the settlement invariants:
// ledger nets match engine state, per-event nets stay within [0,
// claimed], and no aggregate limit is exceeded.
func (e *Engine) Validate() error {
	net := map[string]int64{}
	for _, r := range e.ledger {
		net[r.EventID] += r.Amount
	}
	amounts := map[string]int64{}
	for _, ev := range e.events {
		amounts[ev.ID] = ev.Amount
	}
	for id, n := range net {
		if n != e.paid[id] {
			return fmt.Errorf("event %q: ledger net %d != engine paid %d", id, n, e.paid[id])
		}
		if n < 0 || n > amounts[id] {
			return fmt.Errorf("event %q: net paid %d outside [0, %d]", id, n, amounts[id])
		}
	}
	for _, l := range e.rule.Limits {
		if l.Scope == ScopeAggregate && e.usage[l.ID] > l.Amount {
			return fmt.Errorf("limit %q exceeded: used %d > cap %d", l.ID, e.usage[l.ID], l.Amount)
		}
	}
	return nil
}

// Summarize returns the final settlement summary.
func (e *Engine) Summarize() Summary {
	s := Summary{NetByEvent: e.NetPaid()}
	for _, n := range s.NetByEvent {
		s.TotalPaid += n
	}
	for _, l := range e.rule.Limits {
		if l.Scope == ScopeAggregate {
			s.LimitUsage = append(s.LimitUsage, LimitUsage{LimitID: l.ID, Used: e.usage[l.ID], Cap: l.Amount})
		}
	}
	return s
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

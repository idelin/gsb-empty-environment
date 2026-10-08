package backtest

import (
	"errors"
	"fmt"
	"time"
)

// Strategy is the user-supplied decision logic. It only ever interacts
// with the world through the Context handed to OnEvent, so every piece of
// information it consumes is audited.
type Strategy interface {
	OnEvent(ctx *Context, ev Event)
}

// StrategyFunc adapts a plain function to the Strategy interface.
type StrategyFunc func(ctx *Context, ev Event)

// OnEvent implements Strategy.
func (f StrategyFunc) OnEvent(ctx *Context, ev Event) { f(ctx, ev) }

// Context is the strategy's only window into engine state. All reads go
// through Read (audited) and all decisions go through Act (append-only).
type Context struct {
	now       time.Time
	trigger   Event
	store     *store
	audit     *AuditLog
	actionSeq int64
}

// Now returns the current engine time (arrival time of the event being
// processed). The strategy has no other clock.
func (c *Context) Now() time.Time { return c.now }

// Read returns the newest revision of the given event visible right now
// and records the read in the audit log. Events that have not arrived yet
// are reported as not found.
func (c *Context) Read(eventID string) (Event, bool) {
	ev, ok := c.store.latest(eventID, c.now)
	rec := ReadRecord{EventID: eventID, ReadAt: c.now, Found: ok}
	if ok {
		rec.EventTime = ev.EventTime
		rec.RevArrivalTime = ev.ArrivalTime
	}
	c.audit.recordRead(rec)
	return ev, ok
}

// Act records an irreversible strategy decision.
func (c *Context) Act(kind, detail string) {
	c.actionSeq++
	c.audit.recordAction(Action{
		Seq:     c.actionSeq,
		At:      c.now,
		Kind:    kind,
		Detail:  detail,
		Trigger: c.trigger.ID,
	})
}

// Engine dispatches events to a strategy in (ArrivalTime, Seq) order.
type Engine struct {
	strategy Strategy
}

// NewEngine builds an engine around the given strategy.
func NewEngine(s Strategy) *Engine {
	return &Engine{strategy: s}
}

// Result is the outcome of a run: the full audit trail.
type Result struct {
	Audit *AuditLog
}

// Run executes the backtest.
//
// The input slice order defines the sequence numbers used to break ties
// between events with equal arrival times. Events are dispatched in
// (ArrivalTime, Seq) order regardless of their EventTime, so a correction
// that arrives late is applied only from its arrival onward — actions
// already logged are never touched.
func (e *Engine) Run(events []Event) (*Result, error) {
	if e.strategy == nil {
		return nil, errors.New("backtest: nil strategy")
	}
	numbered := make([]Event, len(events))
	seen := make(map[string]bool, len(events))
	for i, ev := range events {
		if ev.ID == "" {
			return nil, fmt.Errorf("backtest: event at input position %d has empty ID", i)
		}
		if seen[ev.ID] {
			return nil, fmt.Errorf("backtest: duplicate event ID %q", ev.ID)
		}
		seen[ev.ID] = true
		ev.Seq = int64(i)
		numbered[i] = ev
	}
	for _, ev := range numbered {
		if ev.Type == EventCorrection && !seen[ev.CorrectsID] {
			return nil, fmt.Errorf("backtest: correction %q targets unknown event %q", ev.ID, ev.CorrectsID)
		}
	}

	st := newStore()
	audit := &AuditLog{}
	q := newEventQueue(numbered)

	for {
		ev, ok := q.next()
		if !ok {
			break
		}
		now := ev.ArrivalTime
		st.apply(ev, now)
		ctx := &Context{now: now, trigger: ev, store: st, audit: audit}
		e.strategy.OnEvent(ctx, ev)
	}

	if err := audit.ValidateReadSet(); err != nil {
		return nil, err
	}
	return &Result{Audit: audit}, nil
}

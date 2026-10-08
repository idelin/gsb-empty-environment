package backtest

import (
	"fmt"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 9, 30, 0, 0, time.UTC)

func at(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

func md(id string, eventSec, arrivalSec int, payload interface{}) Event {
	return Event{
		ID:          id,
		Type:        EventMarketData,
		EventTime:   at(eventSec),
		ArrivalTime: at(arrivalSec),
		Payload:     payload,
	}
}

func TestDispatchOrderArrivalThenSeq(t *testing.T) {
	events := []Event{
		md("c", 30, 30, nil), // arrives at 30, seq 2
		md("a", 10, 10, nil), // arrives at 10, seq 1
		md("b", 20, 30, nil), // arrives at 30, seq 2... but input order c,a,b
	}
	var got []string
	eng := NewEngine(StrategyFunc(func(ctx *Context, ev Event) {
		got = append(got, ev.ID)
	}))
	if _, err := eng.Run(events); err != nil {
		t.Fatal(err)
	}
	// arrival 10 first; the two arrival-30 events in input order (c before b)
	want := []string{"a", "c", "b"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("dispatch order = %v, want %v", got, want)
	}
}

func TestStrategyCannotReadFuture(t *testing.T) {
	events := []Event{
		md("early", 10, 10, 1.0),
		md("late", 20, 100, 2.0), // exists in input but arrives much later
	}
	var earlyReadOK bool
	eng := NewEngine(StrategyFunc(func(ctx *Context, ev Event) {
		if ev.ID == "early" {
			_, earlyReadOK = ctx.Read("late") // must not be visible yet
		}
	}))
	res, err := eng.Run(events)
	if err != nil {
		t.Fatal(err)
	}
	if earlyReadOK {
		t.Fatal("strategy read an event before its arrival time")
	}
	// the missed read is still audited, with Found=false
	var foundMiss bool
	for _, r := range res.Audit.Reads {
		if r.EventID == "late" && !r.Found {
			foundMiss = true
		}
	}
	if !foundMiss {
		t.Fatal("expected an audited not-found read of the future event")
	}
}

func TestLateCorrectionAffectsFutureNotHistory(t *testing.T) {
	events := []Event{
		md("px", 10, 10, 100.0),
		md("other", 20, 20, nil),
		Event{
			ID:          "fix1",
			Type:        EventCorrection,
			CorrectsID:  "px",
			EventTime:   at(10), // corrects a fact from t=10...
			ArrivalTime: at(50), // ...but only arrives at t=50
			Payload:     105.0,
		},
		md("later", 60, 60, nil),
	}
	eng := NewEngine(StrategyFunc(func(ctx *Context, ev Event) {
		switch ev.ID {
		case "other":
			px, _ := ctx.Read("px")
			ctx.Act("quote", fmt.Sprint(px.Payload))
		case "later":
			px, _ := ctx.Read("px")
			ctx.Act("quote", fmt.Sprint(px.Payload))
		}
	}))
	res, err := eng.Run(events)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Audit.Actions) != 2 {
		t.Fatalf("actions = %v, want 2", res.Audit.Actions)
	}
	before, after := res.Audit.Actions[0], res.Audit.Actions[1]
	if before.Detail != "100" {
		t.Fatalf("pre-correction action = %q, want 100 (original value)", before.Detail)
	}
	if after.Detail != "105" {
		t.Fatalf("post-correction action = %q, want 105 (corrected value)", after.Detail)
	}
	// history is immutable: the pre-correction action stays at its time
	if !before.At.Equal(at(20)) || before.Kind != "quote" {
		t.Fatalf("historical action was rewritten: %+v", before)
	}
}

func TestReplayDeterministic(t *testing.T) {
	events := []Event{
		md("e3", 5, 30, 3),
		md("e1", 1, 10, 1),
		md("e2", 2, 30, 2),
		Event{ID: "c1", Type: EventCorrection, CorrectsID: "e1",
			EventTime: at(1), ArrivalTime: at(40), Payload: 11},
		md("e4", 4, 50, 4),
	}
	strat := func() Strategy {
		return StrategyFunc(func(ctx *Context, ev Event) {
			for _, id := range []string{"e1", "e2", "e3", "e4"} {
				if cur, ok := ctx.Read(id); ok {
					ctx.Act("observe", fmt.Sprintf("%s=%v", id, cur.Payload))
				}
			}
		})
	}
	base, err := NewEngine(strat()).Run(events)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Replay(events, base.Audit.Actions, strat()); err != nil {
		t.Fatal(err)
	}
	if err := VerifyDeterministic(events, strat, 5); err != nil {
		t.Fatal(err)
	}
}

func TestValidateReadSetCatchesTampering(t *testing.T) {
	log := &AuditLog{Reads: []ReadRecord{{
		EventID:        "x",
		RevArrivalTime: at(100),
		ReadAt:         at(50), // read before the revision arrived: impossible
		Found:          true,
	}}}
	if err := log.ValidateReadSet(); err == nil {
		t.Fatal("expected read-set validation to reject a future read")
	}
}

func TestInputValidation(t *testing.T) {
	if _, err := NewEngine(nil).Run(nil); err == nil {
		t.Fatal("expected error for nil strategy")
	}
	dup := []Event{md("x", 1, 1, nil), md("x", 2, 2, nil)}
	if _, err := NewEngine(StrategyFunc(func(*Context, Event) {})).Run(dup); err == nil {
		t.Fatal("expected error for duplicate IDs")
	}
	bad := []Event{md("x", 1, 1, nil), {
		ID: "c", Type: EventCorrection, CorrectsID: "ghost",
		EventTime: at(1), ArrivalTime: at(2),
	}}
	if _, err := NewEngine(StrategyFunc(func(*Context, Event) {})).Run(bad); err == nil {
		t.Fatal("expected error for correction of unknown event")
	}
}

package shortcircuit

import (
	"context"
	"errors"
	"testing"
)

// recordingCheck counts every real invocation so tests can prove which checks
// actually ran. The validator never receives this object.
type recordingCheck struct {
	t        *testing.T
	id       string
	rule     RuleVersion
	decision bool
	err      error
	panicV   any
	calls    int
	cached   *CheckNode
}

func (c *recordingCheck) node() *CheckNode {
	if c.cached != nil {
		return c.cached
	}
	c.cached = &CheckNode{
		ID:   c.id,
		Rule: c.rule,
		Fn: func(ctx context.Context) (bool, Evidence, error) {
			c.calls++
			if c.panicV != nil {
				panic(c.panicV)
			}
			if c.err != nil {
				return false, Evidence{}, c.err
			}
			return c.decision, Evidence{Summary: "ev-" + c.id, Data: "payload"}, nil
		},
	}
	return c.cached
}

func rule(name, version string) RuleVersion {
	return RuleVersion{Name: name, Version: version}
}

func entryIDs(trace *Trace) []string {
	ids := make([]string, 0, len(trace.Entries))
	for _, e := range trace.Entries {
		ids = append(ids, e.NodeID)
	}
	return ids
}

func equalIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestShortCircuitAND(t *testing.T) {
	a := &recordingCheck{t: t, id: "a", rule: rule("ra", "1"), decision: true}
	b := &recordingCheck{t: t, id: "b", rule: rule("rb", "1"), decision: false}
	c := &recordingCheck{t: t, id: "c", rule: rule("rc", "1"), decision: true}
	root := &AndNode{ID: "and", Children: []Node{a.node(), b.node(), c.node()}}

	decision, trace, err := NewEngine().Evaluate(context.Background(), "run-and", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision {
		t.Fatalf("AND should be false, got true")
	}
	if a.calls != 1 || b.calls != 1 || c.calls != 0 {
		t.Fatalf("short-circuit broken: calls a=%d b=%d c=%d", a.calls, b.calls, c.calls)
	}
	if !equalIDs(entryIDs(trace), []string{"a", "b", "and"}) {
		t.Fatalf("unexpected entries: %v", entryIDs(trace))
	}
	if got := trace.Entries[1].Status; got != StatusFalse {
		t.Fatalf("b status = %s, want false", got)
	}
	if err := NewValidator().Validate(root, trace); err != nil {
		t.Fatalf("genuine trace rejected: %v", err)
	}
}

func TestShortCircuitOR(t *testing.T) {
	a := &recordingCheck{t: t, id: "a", rule: rule("ra", "1"), decision: false}
	b := &recordingCheck{t: t, id: "b", rule: rule("rb", "1"), decision: true}
	c := &recordingCheck{t: t, id: "c", rule: rule("rc", "1"), decision: false}
	root := &OrNode{ID: "or", Children: []Node{a.node(), b.node(), c.node()}}

	decision, trace, err := NewEngine().Evaluate(context.Background(), "run-or", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !decision {
		t.Fatalf("OR should be true, got false")
	}
	if a.calls != 1 || b.calls != 1 || c.calls != 0 {
		t.Fatalf("short-circuit broken: calls a=%d b=%d c=%d", a.calls, b.calls, c.calls)
	}
	if !equalIDs(entryIDs(trace), []string{"a", "b", "or"}) {
		t.Fatalf("unexpected entries: %v", entryIDs(trace))
	}
	if err := NewValidator().Validate(root, trace); err != nil {
		t.Fatalf("genuine trace rejected: %v", err)
	}
}

func TestNOT(t *testing.T) {
	a := &recordingCheck{t: t, id: "a", rule: rule("ra", "1"), decision: false}
	root := &NotNode{ID: "not", Child: a.node()}

	decision, trace, err := NewEngine().Evaluate(context.Background(), "run-not", root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !decision {
		t.Fatalf("NOT(false) should be true")
	}
	notEntry := trace.Entries[1]
	if notEntry.Kind != KindNot || !notEntry.Value {
		t.Fatalf("not entry wrong: %+v", notEntry)
	}
	if err := NewValidator().Validate(root, trace); err != nil {
		t.Fatalf("genuine trace rejected: %v", err)
	}
}

var sentinelCheckErr = errors.New("backend unavailable")

func TestCheckErrorAborts(t *testing.T) {
	a := &recordingCheck{t: t, id: "a", rule: rule("ra", "1"), decision: true}
	b := &recordingCheck{t: t, id: "b", rule: rule("rb", "1"), err: sentinelCheckErr}
	c := &recordingCheck{t: t, id: "c", rule: rule("rc", "1"), decision: true}
	root := &AndNode{ID: "and", Children: []Node{a.node(), b.node(), c.node()}}

	decision, trace, err := NewEngine().Evaluate(context.Background(), "run-err", root)
	if err == nil {
		t.Fatal("expected abort error")
	}
	var abort *AbortError
	if !errors.As(err, &abort) {
		t.Fatalf("expected *AbortError, got %T", err)
	}
	if !errors.Is(err, sentinelCheckErr) {
		t.Fatal("AbortError must unwrap to the check error")
	}
	if decision {
		t.Fatal("aborted run must not yield an allow decision")
	}
	if abort.Status != StatusError || abort.NodeID != "b" {
		t.Fatalf("unexpected abort: %+v", abort)
	}
	if c.calls != 0 {
		t.Fatal("nodes after the failing check must not execute")
	}
	last := trace.Entries[len(trace.Entries)-1]
	if last.NodeID != "b" || last.Status != StatusError || last.Evidence != nil {
		t.Fatalf("abort entry wrong: %+v", last)
	}
	if trace.Abort == nil || trace.Final {
		t.Fatal("trace must carry abort marker and no allow final value")
	}
	if err := NewValidator().Validate(root, trace); err != nil {
		t.Fatalf("genuine error trace rejected: %v", err)
	}
}

func TestPanicIsCapturedAsError(t *testing.T) {
	a := &recordingCheck{t: t, id: "a", rule: rule("ra", "1"), panicV: "boom"}
	root := a.node()

	decision, trace, err := NewEngine().Evaluate(context.Background(), "run-panic", root)
	if err == nil {
		t.Fatal("expected panic to surface as error")
	}
	var abort *AbortError
	if !errors.As(err, &abort) || abort.Status != StatusError {
		t.Fatalf("panic must be classified as error, got %v", err)
	}
	if decision {
		t.Fatal("panicked run must not allow")
	}
	if err := NewValidator().Validate(root, trace); err != nil {
		t.Fatalf("genuine panic trace rejected: %v", err)
	}
}

func TestCancellation(t *testing.T) {
	a := &recordingCheck{t: t, id: "a", rule: rule("ra", "1"), decision: true}
	b := &recordingCheck{t: t, id: "b", rule: rule("rb", "1"), decision: true}
	root := &AndNode{ID: "and", Children: []Node{a.node(), b.node()}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // canceled before evaluation starts

	decision, trace, err := NewEngine().Evaluate(ctx, "run-cancel", root)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	var abort *AbortError
	if !errors.As(err, &abort) || abort.Status != StatusCanceled {
		t.Fatalf("cancel must be classified as canceled: %v", err)
	}
	if decision || a.calls != 0 || b.calls != 0 {
		t.Fatal("canceled run must not execute checks or allow")
	}
	last := trace.Entries[len(trace.Entries)-1]
	if last.NodeID != "and" || last.Status != StatusCanceled {
		t.Fatalf("cancel entry wrong: %+v", last)
	}
	if err := NewValidator().Validate(root, trace); err != nil {
		t.Fatalf("genuine cancel trace rejected: %v", err)
	}
}

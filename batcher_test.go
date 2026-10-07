package batcher

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
)

// scriptTransport is an orchestratable Transport: each Send consumes
// the next scripted handler, and every dispatched batch is recorded.
type scriptTransport struct {
	mu    sync.Mutex
	steps []func(reqs []Request) ([]Response, error)
	calls [][]Request
}

func (s *scriptTransport) Send(_ context.Context, reqs []Request) ([]Response, error) {
	s.mu.Lock()
	s.calls = append(s.calls, append([]Request(nil), reqs...))
	var h func([]Request) ([]Response, error)
	if len(s.steps) > 0 {
		h = s.steps[0]
		s.steps = s.steps[1:]
	}
	s.mu.Unlock()
	if h == nil {
		return nil, errors.New("script exhausted")
	}
	return h(reqs)
}

func ok(id string, v uint64, val string) Response {
	return Response{ID: id, Version: v, Value: val}
}

func fail(id string, v uint64, msg string) Response {
	return Response{ID: id, Version: v, Err: errors.New(msg)}
}

func callIDs(calls [][]Request) [][]string {
	out := make([][]string, len(calls))
	for i, c := range calls {
		for _, r := range c {
			out[i] = append(out[i], r.ID)
		}
	}
	return out
}

func resultIDs(res []Result) []string {
	out := make([]string, len(res))
	for i, r := range res {
		out[i] = r.Request.ID
	}
	return out
}

func reqs(ids ...string) []Request {
	out := make([]Request, len(ids))
	for i, id := range ids {
		out[i] = Request{ID: id, Data: "data-" + id}
	}
	return out
}

func TestEmptyBatchNeverDispatches(t *testing.T) {
	st := &scriptTransport{}
	e := New(st, 2, 3, nil)
	if err := e.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(st.calls) != 0 {
		t.Fatalf("transport called for empty input: %v", callIDs(st.calls))
	}
	if got := e.Results(); len(got) != 0 {
		t.Fatalf("expected no results, got %v", got)
	}
}

func TestOutOfOrderMissingAndRetryKeepsOriginalIDs(t *testing.T) {
	st := &scriptTransport{steps: []func([]Request) ([]Response, error){
		// batch [a b]: out of order, b missing
		func([]Request) ([]Response, error) { return []Response{ok("a", 1, "A")}, nil },
		// batch [c d]: reversed, both present
		func([]Request) ([]Response, error) { return []Response{ok("d", 1, "D"), ok("c", 1, "C")}, nil },
		// batch [e]: missing entirely
		func([]Request) ([]Response, error) { return nil, nil },
		// retry: only missing items, original IDs
		func(got []Request) ([]Response, error) {
			want := []Request{{ID: "b", Data: "data-b"}, {ID: "e", Data: "data-e"}}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("retry batch = %+v, want %+v", got, want)
			}
			return []Response{ok("e", 2, "E"), ok("b", 2, "B")}, nil
		},
	}}
	e := New(st, 2, 3, reqs("a", "b", "c", "d", "e"))
	if err := e.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	wantCalls := [][]string{{"a", "b"}, {"c", "d"}, {"e"}, {"b", "e"}}
	if got := callIDs(st.calls); !reflect.DeepEqual(got, wantCalls) {
		t.Fatalf("dispatched = %v, want %v", got, wantCalls)
	}
	res := e.Results()
	if got := resultIDs(res); !reflect.DeepEqual(got, []string{"a", "b", "c", "d", "e"}) {
		t.Fatalf("result order = %v", got)
	}
	for _, r := range res {
		if r.Status != StatusConfirmed {
			t.Fatalf("%s not confirmed: %+v", r.Request.ID, r)
		}
	}
	if res[1].Value != "B" || res[4].Value != "E" {
		t.Fatalf("retried values wrong: %+v", res)
	}
}

func TestDuplicateResponsesAndConflictIsolation(t *testing.T) {
	st := &scriptTransport{steps: []func([]Request) ([]Response, error){
		func([]Request) ([]Response, error) {
			return []Response{
				ok("a", 1, "A"),
				ok("a", 1, "A"), // exact duplicate: idempotent
				ok("b", 1, "B1"),
				ok("b", 1, "B2"), // same version, different value: conflict
			}, nil
		},
	}}
	e := New(st, 10, 3, reqs("a", "b"))
	if err := e.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	res := e.Results()
	if res[0].Status != StatusConfirmed || res[0].Value != "A" {
		t.Fatalf("a = %+v", res[0])
	}
	if res[1].Status != StatusConflict {
		t.Fatalf("b = %+v, want conflict", res[1])
	}
	if len(st.calls) != 1 {
		t.Fatalf("conflicted item must be isolated from retries, calls=%v", callIDs(st.calls))
	}
	cf := e.Conflicts()
	if len(cf) != 1 || cf[0].ID != "b" || cf[0].Existing != "B1" || cf[0].Incoming != "B2" {
		t.Fatalf("conflicts = %+v", cf)
	}
}

func TestBatchFailureDoesNotOverwriteConfirmed(t *testing.T) {
	batchErr := errors.New("connection reset")
	st := &scriptTransport{steps: []func([]Request) ([]Response, error){
		func([]Request) ([]Response, error) { return []Response{ok("a", 1, "A")}, nil },
		func([]Request) ([]Response, error) { return nil, batchErr }, // batch-level failure for [b]
		func([]Request) ([]Response, error) { return []Response{ok("b", 2, "B")}, nil },
	}}
	e := New(st, 1, 3, reqs("a", "b"))
	if err := e.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	res := e.Results()
	if res[0].Status != StatusConfirmed || res[0].Value != "A" || res[0].Version != 1 {
		t.Fatalf("confirmed item clobbered by batch failure: %+v", res[0])
	}
	if res[1].Status != StatusConfirmed || res[1].Value != "B" {
		t.Fatalf("b = %+v", res[1])
	}
}

func TestStaleFailureCannotOverwriteNewerSuccess(t *testing.T) {
	// Item a is in flight across two attempts: attempt 1 drops it,
	// attempt 2 re-sends it. During attempt 2 the late answer from
	// attempt 1 arrives: a stale success (v1) and a stale failure,
	// both older than the fresh commit (v2).
	st := &scriptTransport{steps: []func([]Request) ([]Response, error){
		func([]Request) ([]Response, error) { return nil, nil }, // a dropped
		func(got []Request) ([]Response, error) {
			if len(got) != 1 || got[0].ID != "a" {
				t.Errorf("retry = %+v, want [a]", got)
			}
			return []Response{
				ok("a", 2, "new"),       // fresh commit wins
				ok("a", 1, "old"),       // stale success: ignored
				fail("a", 1, "old err"), // stale failure: must not overwrite
			}, nil
		},
	}}
	e := New(st, 1, 3, reqs("a"))
	if err := e.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	res := e.Results()
	if res[0].Status != StatusConfirmed || res[0].Version != 2 || res[0].Value != "new" {
		t.Fatalf("a = %+v, want v2/new confirmed", res[0])
	}
	if len(e.Conflicts()) != 0 {
		t.Fatalf("stale responses must not create conflicts: %+v", e.Conflicts())
	}
}

func TestCancelStopsOnlyUndispatched(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	st := &scriptTransport{steps: []func([]Request) ([]Response, error){
		func([]Request) ([]Response, error) {
			cancel() // a in flight completes; b, c never dispatched
			return []Response{ok("a", 1, "A")}, nil
		},
	}}
	e := New(st, 1, 3, reqs("a", "b", "c"))
	if err := e.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run err = %v, want context.Canceled", err)
	}
	if got := callIDs(st.calls); !reflect.DeepEqual(got, [][]string{{"a"}}) {
		t.Fatalf("dispatched = %v, want only [a]", got)
	}
	res := e.Results()
	if res[0].Status != StatusConfirmed {
		t.Fatalf("in-flight item must complete: %+v", res[0])
	}
	if res[1].Status != StatusPending || res[2].Status != StatusPending {
		t.Fatalf("undispatched items must stay pending: %+v", res)
	}
}

func TestCheckpointRestoreWithFaultReplay(t *testing.T) {
	// First run: a confirmed at v2, b dropped. Checkpoint, then restore
	// against a faulty server that replays stale responses for a.
	st1 := &scriptTransport{steps: []func([]Request) ([]Response, error){
		func([]Request) ([]Response, error) { return []Response{ok("a", 2, "A")}, nil },
	}}
	e1 := New(st1, 2, 1, reqs("a", "b"))
	if err := e1.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	cp := e1.Save()
	if !reflect.DeepEqual(cp.Missing, []string{"b"}) {
		t.Fatalf("missing set = %v, want [b]", cp.Missing)
	}

	st2 := &scriptTransport{steps: []func([]Request) ([]Response, error){
		func(got []Request) ([]Response, error) {
			// Only the missing set is re-dispatched, original IDs kept.
			if want := reqs("b"); !reflect.DeepEqual(got, want) {
				t.Errorf("redispatch = %+v, want %+v", got, want)
			}
			return []Response{
				ok("a", 1, "replay-old"), // stale replay: ignored
				ok("a", 2, "A"),          // replay of same commit: idempotent
				ok("b", 3, "B"),
			}, nil
		},
	}}
	e2, err := Restore(st2, 2, 1, cp)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if err := e2.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	res := e2.Results()
	if got := resultIDs(res); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("identity/order lost across restore: %v", got)
	}
	if res[0].Status != StatusConfirmed || res[0].Version != 2 || res[0].Value != "A" {
		t.Fatalf("confirmed version not preserved: %+v", res[0])
	}
	if res[1].Status != StatusConfirmed || res[1].Version != 3 || res[1].Value != "B" {
		t.Fatalf("b = %+v", res[1])
	}
	if len(e2.Conflicts()) != 0 {
		t.Fatalf("replays must not create conflicts: %+v", e2.Conflicts())
	}
}

func TestRestoreRejectsInconsistentCheckpoint(t *testing.T) {
	cp := Checkpoint{
		Items: []ItemCheckpoint{
			{Request: Request{ID: "a"}, Confirmed: true, Version: 1, Value: "A"},
		},
		Missing: []string{"a"}, // confirmed yet missing: inconsistent
	}
	if _, err := Restore(&scriptTransport{}, 1, 1, cp); err == nil {
		t.Fatal("expected inconsistency error")
	}
	cp2 := Checkpoint{Missing: []string{"ghost"}}
	if _, err := Restore(&scriptTransport{}, 1, 1, cp2); err == nil {
		t.Fatal("expected error for unknown missing id")
	}
}

func TestUnknownResponseIDIgnored(t *testing.T) {
	st := &scriptTransport{steps: []func([]Request) ([]Response, error){
		func([]Request) ([]Response, error) {
			return []Response{ok("ghost", 9, "G"), ok("a", 1, "A")}, nil
		},
	}}
	e := New(st, 1, 1, reqs("a"))
	if err := e.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	res := e.Results()
	if len(res) != 1 || res[0].Status != StatusConfirmed || res[0].Value != "A" {
		t.Fatalf("res = %+v", res)
	}
}

func TestAttemptsExhaustedLeavesMissingPending(t *testing.T) {
	st := &scriptTransport{} // every Send fails with "script exhausted"
	e := New(st, 2, 2, reqs("a", "b"))
	if err := e.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := len(st.calls); got != 2 {
		t.Fatalf("calls = %d, want 2 attempts", got)
	}
	for _, r := range e.Results() {
		if r.Status != StatusPending || r.LastErr == nil {
			t.Fatalf("r = %+v, want pending with last error", r)
		}
	}
}

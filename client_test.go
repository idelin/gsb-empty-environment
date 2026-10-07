package batchclient

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func reqs(ids ...string) []Request {
	out := make([]Request, len(ids))
	for i, id := range ids {
		out[i] = Request{ID: id, Data: "data-" + id}
	}
	return out
}

func callIDs(calls [][]Request) [][]string {
	out := make([][]string, len(calls))
	for i, batch := range calls {
		for _, req := range batch {
			out[i] = append(out[i], req.ID)
		}
	}
	return out
}

func outcomeByID(t *testing.T, outcomes []Outcome, id string) Outcome {
	t.Helper()
	for _, o := range outcomes {
		if o.Request.ID == id {
			return o
		}
	}
	t.Fatalf("no outcome for %q", id)
	return Outcome{}
}

func TestEmptyBatchMakesNoTransportCalls(t *testing.T) {
	st := NewScriptTransport()
	c := New(st)
	out := c.Submit(context.Background(), nil)
	if len(out) != 0 {
		t.Fatalf("expected no outcomes, got %d", len(out))
	}
	if got := len(st.Calls()); got != 0 {
		t.Fatalf("expected no transport calls, got %d", got)
	}
	res := c.Run(context.Background(), []Request{})
	if !res.Complete() {
		t.Fatal("empty run must be complete")
	}
}

func TestOutOfOrderDuplicatesAndInputOrder(t *testing.T) {
	st := NewScriptTransport(Respond(
		Response{ID: "c", Version: 1, Value: "C"},
		Response{ID: "a", Version: 1, Value: "A"},
		Response{ID: "b", Version: 1, Value: "B"},
		Response{ID: "a", Version: 1, Value: "A"},     // exact duplicate
		Response{ID: "ghost", Version: 9, Value: "?"}, // unknown ID ignored
	))
	c := New(st, WithBatchSize(10))
	out := c.Submit(context.Background(), reqs("a", "b", "c"))

	if len(out) != 3 {
		t.Fatalf("expected 3 outcomes, got %d", len(out))
	}
	wantIDs := []string{"a", "b", "c"}
	wantVals := []string{"A", "B", "C"}
	for i, o := range out {
		if o.Request.ID != wantIDs[i] {
			t.Fatalf("outcome %d: expected id %q, got %q", i, wantIDs[i], o.Request.ID)
		}
		if o.Status != StatusOK || o.Value != wantVals[i] {
			t.Fatalf("outcome %d: got %v %q", i, o.Status, o.Value)
		}
	}
}

func TestRetryOnlyMissingItemsKeepsOriginalIDs(t *testing.T) {
	st := NewScriptTransport(
		CommitExcept("b", "c"), // first batch drops b and c
	)
	c := New(st, WithBatchSize(3), WithMaxAttempts(3), WithParallelism(1))
	out := c.Submit(context.Background(), reqs("a", "b", "c"))

	for _, o := range out {
		if o.Status != StatusOK {
			t.Fatalf("expected all OK, got %v for %q", o.Status, o.Request.ID)
		}
	}
	calls := callIDs(st.Calls())
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %v", calls)
	}
	if !reflect.DeepEqual(calls[1], []string{"b", "c"}) {
		t.Fatalf("retry must carry only missing items with original ids, got %v", calls[1])
	}
}

func TestBatchFailureDoesNotOverrideConfirmedItems(t *testing.T) {
	st := NewScriptTransport(
		RespondErr(errors.New("connection reset"), Response{ID: "a", Version: 1, Value: "A"}),
	)
	c := New(st, WithBatchSize(2), WithMaxAttempts(2), WithParallelism(1))
	out := c.Submit(context.Background(), reqs("a", "b"))

	a := outcomeByID(t, out, "a")
	if a.Status != StatusOK || a.Value != "A" || a.Version != 1 {
		t.Fatalf("confirmed item must survive batch-level failure, got %+v", a)
	}
	b := outcomeByID(t, out, "b")
	if b.Status != StatusOK {
		t.Fatalf("missing item must be retried, got %+v", b)
	}
	calls := callIDs(st.Calls())
	if !reflect.DeepEqual(calls[1], []string{"b"}) {
		t.Fatalf("only the missing item may be retried, got %v", calls)
	}
}

func TestStaleFailureCannotOverrideNewerSuccess(t *testing.T) {
	gate := make(chan struct{})
	step2Called := make(chan struct{})
	st := NewScriptTransport(
		// Attempt 1 hangs in flight, then delivers an old committed failure.
		func(tr *ScriptTransport, batch []Request) ([]Response, error) {
			<-gate
			return []Response{{ID: "x", Version: 1, Err: "old failure"}}, nil
		},
		// Attempt 2 (retry after timeout) commits a newer success.
		func(tr *ScriptTransport, batch []Request) ([]Response, error) {
			close(step2Called)
			return []Response{{ID: "x", Version: 2, Value: "new"}}, nil
		},
	)
	c := New(st, WithBatchSize(1), WithMaxAttempts(2), WithAttemptTimeout(20*time.Millisecond))

	type res struct{ out []Outcome }
	done := make(chan res, 1)
	go func() {
		done <- res{c.Submit(context.Background(), reqs("x"))}
	}()

	<-step2Called // retry dispatched while attempt 1 is still in flight
	close(gate)   // now the stale attempt 1 delivers its old failure
	out := (<-done).out

	x := outcomeByID(t, out, "x")
	if x.Status != StatusOK || x.Version != 2 || x.Value != "new" {
		t.Fatalf("stale failure must not override newer success, got %+v", x)
	}
}

func TestSameVersionDifferentResultIsConflict(t *testing.T) {
	st := NewScriptTransport(Respond(
		Response{ID: "a", Version: 1, Value: "X"},
		Response{ID: "b", Version: 1, Value: "B"},
		Response{ID: "a", Version: 1, Value: "Y"}, // same version, different result
	))
	c := New(st, WithBatchSize(10))
	out := c.Submit(context.Background(), reqs("a", "b"))

	a := outcomeByID(t, out, "a")
	if a.Status != StatusConflict {
		t.Fatalf("expected conflict, got %+v", a)
	}
	b := outcomeByID(t, out, "b")
	if b.Status != StatusOK || b.Value != "B" {
		t.Fatalf("conflict must be isolated, got %+v", b)
	}
}

func TestHigherVersionResolvesEarlierConflict(t *testing.T) {
	st := NewScriptTransport(Respond(
		Response{ID: "a", Version: 1, Value: "X"},
		Response{ID: "a", Version: 1, Value: "Y"},
		Response{ID: "a", Version: 2, Value: "Z"},
	))
	c := New(st, WithBatchSize(10))
	out := c.Submit(context.Background(), reqs("a"))
	if out[0].Status != StatusOK || out[0].Version != 2 || out[0].Value != "Z" {
		t.Fatalf("higher version must win, got %+v", out[0])
	}
}

func TestCancellationStopsOnlyUndispatchedItems(t *testing.T) {
	gate := make(chan struct{})
	started := make(chan struct{})
	st := NewScriptTransport(
		func(tr *ScriptTransport, batch []Request) ([]Response, error) {
			close(started)
			<-gate
			return tr.CommitAll(batch), nil
		},
	)
	c := New(st, WithBatchSize(1), WithParallelism(1))
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan []Outcome, 1)
	go func() {
		done <- c.Submit(ctx, reqs("a", "b", "c"))
	}()

	<-started // first batch in flight
	cancel()  // must not kill the in-flight batch
	close(gate)
	out := <-done

	a := outcomeByID(t, out, "a")
	if a.Status != StatusOK {
		t.Fatalf("in-flight item must complete, got %+v", a)
	}
	for _, id := range []string{"b", "c"} {
		if o := outcomeByID(t, out, id); o.Status != StatusCancelled {
			t.Fatalf("un-dispatched item %q must be cancelled, got %+v", id, o)
		}
	}
	if got := len(st.Calls()); got != 1 {
		t.Fatalf("no further batches may be dispatched after cancel, got %d calls", got)
	}
}

func TestMaxAttemptsExhaustedYieldsMissing(t *testing.T) {
	boom := errors.New("boom")
	st := NewScriptTransport(FailBatch(boom), FailBatch(boom))
	c := New(st, WithBatchSize(1), WithMaxAttempts(2), WithParallelism(1))
	out := c.Submit(context.Background(), reqs("a"))

	if out[0].Status != StatusMissing {
		t.Fatalf("expected missing, got %+v", out[0])
	}
	if got := len(st.Calls()); got != 2 {
		t.Fatalf("expected exactly 2 attempts, got %d", got)
	}
}

func TestCheckpointResumeWithFaultReplay(t *testing.T) {
	// Run 1: fault injection — batch [c,d] fails at batch level, f is dropped.
	steps1 := []StepFunc{
		func(tr *ScriptTransport, batch []Request) ([]Response, error) {
			return tr.CommitAll(batch), nil // a,b -> v1
		},
		FailBatch(errors.New("connection reset")), // c,d lost entirely
		CommitExcept("f"),                         // e commits, f dropped
	}
	tr1 := NewScriptTransport(steps1...)
	c1 := New(tr1, WithBatchSize(2), WithMaxAttempts(1), WithParallelism(1))
	input := reqs("a", "b", "c", "d", "e", "f")
	res1 := c1.Run(context.Background(), input)

	if res1.Complete() {
		t.Fatal("run 1 must be incomplete after faults")
	}
	cp := res1.Checkpoint
	if !reflect.DeepEqual(cp.Missing, []string{"c", "d", "f"}) {
		t.Fatalf("missing set mismatch: %v", cp.Missing)
	}
	for _, id := range []string{"a", "b", "e"} {
		conf, ok := cp.Confirmed[id]
		if !ok || conf.Version != 1 {
			t.Fatalf("confirmed version for %q not preserved: %+v", id, cp.Confirmed)
		}
	}
	if got := len(cp.Requests); got != 6 {
		t.Fatalf("request identity not preserved: %d requests", got)
	}

	// Run 2: replay after recovery — only the missing set is resubmitted.
	tr2 := NewScriptTransport()
	c2 := New(tr2, WithBatchSize(2), WithMaxAttempts(1), WithParallelism(1))
	res2 := c2.Resume(context.Background(), cp)

	if !res2.Complete() {
		t.Fatalf("resumed run must complete, missing: %v", res2.Checkpoint.Missing)
	}
	calls := callIDs(tr2.Calls())
	if !reflect.DeepEqual(calls, [][]string{{"c", "d"}, {"f"}}) {
		t.Fatalf("resume must redispatch only missing items with original ids, got %v", calls)
	}
	// Final results align to the original input order.
	wantIDs := []string{"a", "b", "c", "d", "e", "f"}
	for i, o := range res2.Outcomes {
		if o.Request.ID != wantIDs[i] || o.Status != StatusOK {
			t.Fatalf("outcome %d mismatch: %+v", i, o)
		}
	}
	// Confirmed versions from run 1 survive recovery untouched.
	for _, id := range []string{"a", "b", "e"} {
		if o := outcomeByID(t, res2.Outcomes, id); o.Version != 1 || o.Value != "data-"+id {
			t.Fatalf("confirmed state for %q changed across recovery: %+v", id, o)
		}
	}
	// Recovered items carry fresh monotonic versions from the replayed server.
	for _, id := range []string{"c", "d", "f"} {
		if o := outcomeByID(t, res2.Outcomes, id); o.Version != 1 {
			t.Fatalf("recovered item %q: %+v", id, o)
		}
	}
}

func TestResumeIgnoresStaleReplayOfConfirmedItems(t *testing.T) {
	tr1 := NewScriptTransport(CommitExcept("b"))
	c1 := New(tr1, WithBatchSize(2), WithMaxAttempts(1), WithParallelism(1))
	res1 := c1.Run(context.Background(), reqs("a", "b"))

	// Replay: server still has an old committed failure for a (v1 loses to
	// the confirmed v1 success? no — equal version, different result is a
	// conflict; use a lower stale version instead) and commits b fresh.
	tr2 := NewScriptTransport(
		Respond(
			Response{ID: "a", Version: 0, Err: "stale failure"}, // older than confirmed v1
			Response{ID: "b", Version: 1, Value: "B"},
		),
	)
	c2 := New(tr2, WithBatchSize(2), WithMaxAttempts(1), WithParallelism(1))
	res2 := c2.Resume(context.Background(), res1.Checkpoint)

	a := outcomeByID(t, res2.Outcomes, "a")
	if a.Status != StatusOK || a.Value != "data-a" || a.Version != 1 {
		t.Fatalf("stale replay must not touch confirmed item, got %+v", a)
	}
	if b := outcomeByID(t, res2.Outcomes, "b"); b.Status != StatusOK || b.Value != "B" {
		t.Fatalf("missing item must recover, got %+v", b)
	}
}

func TestDuplicateIDsInInputShareResolution(t *testing.T) {
	st := NewScriptTransport()
	c := New(st, WithBatchSize(10))
	out := c.Submit(context.Background(), reqs("a", "a", "b"))
	if len(out) != 3 {
		t.Fatalf("expected 3 outcomes, got %d", len(out))
	}
	for _, o := range out {
		if o.Status != StatusOK {
			t.Fatalf("expected OK, got %+v", o)
		}
	}
	// ID "a" dispatched once despite appearing twice in the input.
	calls := callIDs(st.Calls())
	if !reflect.DeepEqual(calls, [][]string{{"a", "b"}}) {
		t.Fatalf("duplicate ids must share one dispatch, got %v", calls)
	}
}

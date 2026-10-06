package replicaread

import (
	"errors"
	"reflect"
	"testing"
)

// repairLog records repairs issued by the reader under test.
type repairLog struct {
	calls []repairCall
}

type repairCall struct {
	replica string
	version Version
	payload Payload
}

func (l *repairLog) send(replica string, v Version, p Payload) {
	l.calls = append(l.calls, repairCall{replica, v, p})
}

// deliver feeds responses to the reader one at a time, in the exact
// scripted order, so every assertion runs against a controlled
// arrival sequence.
func deliver(r *Reader, responses ...Response) {
	for _, resp := range responses {
		r.HandleResponse(resp)
	}
}

func ok(replica string, term, seq uint64, p Payload) Response {
	return Response{Replica: replica, Version: Version{Term: term, Seq: seq}, Payload: p}
}

func fail(replica string) Response {
	return Response{Replica: replica, Err: errors.New("timeout")}
}

func TestQuorumSelectsHighestVersion(t *testing.T) {
	repairs := &repairLog{}
	r := NewReader(2, repairs.send)

	deliver(r,
		ok("a", 3, 1, Value([]byte("old"))),
		ok("b", 3, 7, Value([]byte("new"))),
	)

	results := r.Results()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	res := results[0]
	if res.Version != (Version{Term: 3, Seq: 7}) {
		t.Fatalf("expected version 3.7, got %v", res.Version)
	}
	if string(res.Payload.Data) != "new" {
		t.Fatalf("expected payload %q, got %q", "new", res.Payload.Data)
	}
	if res.Conflict {
		t.Fatal("unexpected conflict")
	}
}

func TestRepairOnlyConfirmedStaleReplicas(t *testing.T) {
	repairs := &repairLog{}
	r := NewReader(3, repairs.send)

	// "c" never answers: quorum is 3 out of 4 attempted replicas.
	deliver(r,
		ok("a", 1, 1, Value([]byte("stale-a"))),
		ok("b", 2, 5, Value([]byte("fresh"))),
		ok("d", 1, 9, Value([]byte("stale-d"))),
	)

	res := r.Results()[0]
	wantRepaired := []string{"a", "d"}
	if !reflect.DeepEqual(res.Repaired, wantRepaired) {
		t.Fatalf("expected repairs to %v, got %v", wantRepaired, res.Repaired)
	}
	for _, call := range repairs.calls {
		if call.replica == "c" {
			t.Fatal("repair sent to a replica that never answered")
		}
		if call.version != (Version{Term: 2, Seq: 5}) {
			t.Fatalf("repair carried wrong version %v", call.version)
		}
		if string(call.payload.Data) != "fresh" {
			t.Fatalf("repair carried wrong payload %q", call.payload.Data)
		}
	}
}

func TestSameVersionDifferentContentIsConflict(t *testing.T) {
	repairs := &repairLog{}
	r := NewReader(3, repairs.send)

	deliver(r,
		ok("a", 4, 2, Value([]byte("x"))),
		ok("b", 4, 2, Value([]byte("y"))),
		ok("c", 4, 2, Value([]byte("x"))),
	)

	res := r.Results()[0]
	if !res.Conflict {
		t.Fatal("expected conflict for divergent content at one version")
	}
	if len(res.Conflicts) != 2 {
		t.Fatalf("expected 2 distinct conflicting payloads, got %d", len(res.Conflicts))
	}
	if len(repairs.calls) != 0 {
		t.Fatalf("no repairs may be issued on conflict, got %v", repairs.calls)
	}
}

func TestTombstoneParticipatesInVersionOrdering(t *testing.T) {
	r := NewReader(2, nil)

	deliver(r,
		ok("a", 5, 1, Value([]byte("resurrected?"))),
		ok("b", 5, 2, Tombstone()),
	)

	res := r.Results()[0]
	if res.Payload.Kind != TombstonePayload {
		t.Fatalf("expected tombstone to win at higher version, got %+v", res.Payload)
	}
	if res.Version != (Version{Term: 5, Seq: 2}) {
		t.Fatalf("expected version 5.2, got %v", res.Version)
	}
}

func TestLateHigherVersionOpensNewGeneration(t *testing.T) {
	repairs := &repairLog{}
	r := NewReader(2, repairs.send)

	deliver(r,
		ok("a", 1, 1, Value([]byte("v1"))),
		ok("b", 1, 1, Value([]byte("v1"))),
	)
	first := r.Results()[0]

	// Late answer with a higher version: generation 2 begins.
	deliver(r, ok("c", 2, 1, Value([]byte("v2"))))
	if len(r.Results()) != 1 {
		t.Fatal("generation 2 must not complete before its own quorum")
	}
	if !r.Pending() {
		t.Fatal("reader should be pending on generation 2")
	}

	deliver(r, ok("d", 2, 1, Value([]byte("v2"))))
	results := r.Results()
	if len(results) != 2 {
		t.Fatalf("expected 2 generations, got %d", len(results))
	}

	// The original receipt is untouched.
	if r.Results()[0] != first {
		t.Fatal("first-generation result was replaced")
	}
	if first.Version != (Version{Term: 1, Seq: 1}) || string(first.Payload.Data) != "v1" {
		t.Fatalf("first-generation result mutated: %+v", first)
	}

	second := results[1]
	if second.Generation != 2 {
		t.Fatalf("expected generation 2, got %d", second.Generation)
	}
	if second.Version != (Version{Term: 2, Seq: 1}) || string(second.Payload.Data) != "v2" {
		t.Fatalf("unexpected second-generation result: %+v", second)
	}
	if !reflect.DeepEqual(second.Responded, []string{"c", "d"}) {
		t.Fatalf("generation 2 must only contain its own answers, got %v", second.Responded)
	}
}

func TestLateStaleOrDuplicateAnswersAreDropped(t *testing.T) {
	r := NewReader(2, nil)

	deliver(r,
		ok("a", 3, 3, Value([]byte("final"))),
		ok("b", 3, 3, Value([]byte("final"))),
	)
	deliver(r,
		ok("c", 3, 1, Value([]byte("ancient"))), // stale: dropped
		ok("a", 3, 3, Value([]byte("final"))),   // duplicate: dropped
		fail("d"),                               // late failure: dropped
	)

	if len(r.Results()) != 1 {
		t.Fatalf("late stale answers must not open generations, got %d", len(r.Results()))
	}
}

func TestFailedAnswersCountTowardQuorum(t *testing.T) {
	repairs := &repairLog{}
	r := NewReader(3, repairs.send)

	deliver(r,
		fail("a"),
		ok("b", 7, 1, Value([]byte("only"))),
		fail("c"),
	)

	res := r.Results()[0]
	if res.Err != nil {
		t.Fatalf("unexpected error: %v", res.Err)
	}
	if string(res.Payload.Data) != "only" {
		t.Fatalf("expected the single successful answer to win, got %q", res.Payload.Data)
	}
	if len(res.Repaired) != 0 {
		t.Fatalf("failed replicas are not repaired, got %v", res.Repaired)
	}
}

func TestAllFailuresYieldQuorumError(t *testing.T) {
	r := NewReader(2, nil)
	deliver(r, fail("a"), fail("b"))

	res := r.Results()[0]
	if res.Err != ErrQuorumFailed {
		t.Fatalf("expected ErrQuorumFailed, got %v", res.Err)
	}
}

func TestTermDominatesSeq(t *testing.T) {
	r := NewReader(2, nil)
	deliver(r,
		ok("a", 9, 1, Value([]byte("high-term"))),
		ok("b", 8, 999, Value([]byte("high-seq"))),
	)

	res := r.Results()[0]
	if string(res.Payload.Data) != "high-term" {
		t.Fatalf("term must dominate seq, got %q", res.Payload.Data)
	}
}

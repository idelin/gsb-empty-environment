package replicaread

import (
	"reflect"
	"strings"
	"testing"
)

// step is one scripted response delivery plus the effects it must
// produce. Tests replay steps in a fully controlled order.
type step struct {
	resp        Response
	wantDecided *Receipt
	wantRepairs []Repair
	wantErr     string // substring of the expected error; "" means none
}

// play feeds the steps into a fresh session in the given order and
// checks the effects of every single delivery.
func play(t *testing.T, quorum int, steps []step) *Session {
	t.Helper()
	s, err := NewSession(quorum)
	if err != nil {
		t.Fatalf("NewSession(%d): %v", quorum, err)
	}
	for i, st := range steps {
		fx, err := s.Handle(st.resp)
		prefix := "step " + st.resp.Replica
		if st.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), st.wantErr) {
				t.Fatalf("%s: want error containing %q, got %v", prefix, st.wantErr, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", prefix, err)
		}
		if !reflect.DeepEqual(fx.Decided, st.wantDecided) {
			t.Errorf("%s: decided = %+v, want %+v", prefix, fx.Decided, st.wantDecided)
		}
		if !reflect.DeepEqual(fx.Repairs, st.wantRepairs) {
			t.Errorf("%s: repairs = %+v, want %+v", prefix, fx.Repairs, st.wantRepairs)
		}
		_ = i
	}
	return s
}

func val(s string) Content    { return Content{Value: []byte(s)} }
func tomb() Content           { return Content{Tombstone: true} }
func ver(t, q uint64) Version { return Version{Term: t, Seq: q} }

func resp(id string, v Version, c Content) Response {
	return Response{Replica: id, Version: v, Content: c}
}

func TestDecidesMaxVersionOnceQuorumArrives(t *testing.T) {
	s := play(t, 2, []step{
		{resp: resp("r1", ver(1, 4), val("old"))}, // quorum not reached yet
		{
			resp:        resp("r2", ver(2, 1), val("new")),
			wantDecided: &Receipt{Generation: 1, Version: ver(2, 1), Content: val("new"), Acks: []string{"r2"}},
			wantRepairs: []Repair{{Replica: "r1", Version: ver(2, 1), Content: val("new")}},
		},
	})
	if got := s.Generation(); got != 1 {
		t.Errorf("Generation() = %d, want 1", got)
	}
}

func TestSameVersionDifferentContentIsConflict(t *testing.T) {
	play(t, 2, []step{
		{resp: resp("r1", ver(3, 7), val("a"))},
		{resp: resp("r2", ver(3, 7), val("b")), wantErr: "conflicting content for version 3.7"},
	})
}

func TestConflictAlsoReportedAfterDecision(t *testing.T) {
	play(t, 2, []step{
		{resp: resp("r1", ver(1, 1), val("a"))},
		{
			resp:        resp("r2", ver(1, 1), val("a")),
			wantDecided: &Receipt{Generation: 1, Version: ver(1, 1), Content: val("a"), Acks: []string{"r1", "r2"}},
		},
		{resp: resp("r3", ver(1, 1), val("different")), wantErr: "conflicting content"},
	})
}

func TestRepairOnlyGoesToConfirmedOlderReplicas(t *testing.T) {
	// r3 never answers: it must not receive a repair, because its
	// state is unconfirmed. When it finally answers with an older
	// version, only then is it repaired.
	play(t, 2, []step{
		{resp: resp("r1", ver(1, 1), val("stale"))},
		{
			resp:        resp("r2", ver(1, 2), val("fresh")),
			wantDecided: &Receipt{Generation: 1, Version: ver(1, 2), Content: val("fresh"), Acks: []string{"r2"}},
			wantRepairs: []Repair{{Replica: "r1", Version: ver(1, 2), Content: val("fresh")}},
		},
		{
			resp:        resp("r3", ver(1, 1), val("stale")),
			wantRepairs: []Repair{{Replica: "r3", Version: ver(1, 2), Content: val("fresh")}},
		},
	})
}

func TestLateHigherVersionOpensNewGenerationWithoutTouchingOldReceipt(t *testing.T) {
	s := play(t, 2, []step{
		{resp: resp("r1", ver(1, 1), val("v1"))},
		{
			resp:        resp("r2", ver(1, 1), val("v1")),
			wantDecided: &Receipt{Generation: 1, Version: ver(1, 1), Content: val("v1"), Acks: []string{"r1", "r2"}},
		},
		{
			resp:        resp("r3", ver(2, 5), val("v2")),
			wantDecided: &Receipt{Generation: 2, Version: ver(2, 5), Content: val("v2"), Acks: []string{"r3"}},
			wantRepairs: []Repair{
				{Replica: "r1", Version: ver(2, 5), Content: val("v2")},
				{Replica: "r2", Version: ver(2, 5), Content: val("v2")},
			},
		},
	})

	history := s.History()
	if len(history) != 2 {
		t.Fatalf("History() has %d receipts, want 2", len(history))
	}
	wantOld := Receipt{Generation: 1, Version: ver(1, 1), Content: val("v1"), Acks: []string{"r1", "r2"}}
	if !reflect.DeepEqual(history[0], wantOld) {
		t.Errorf("old receipt mutated: got %+v, want %+v", history[0], wantOld)
	}
	if got := s.Generation(); got != 2 {
		t.Errorf("Generation() = %d, want 2", got)
	}
}

func TestTombstoneIsDecidedAndRepaired(t *testing.T) {
	play(t, 2, []step{
		{resp: resp("r1", ver(1, 9), val("lingering"))},
		{
			resp:        resp("r2", ver(2, 0), tomb()),
			wantDecided: &Receipt{Generation: 1, Version: ver(2, 0), Content: tomb(), Acks: []string{"r2"}},
			wantRepairs: []Repair{{Replica: "r1", Version: ver(2, 0), Content: tomb()}},
		},
	})
}

func TestDuplicateResponseDoesNotCountTwice(t *testing.T) {
	play(t, 2, []step{
		{resp: resp("r1", ver(1, 1), val("x"))},
		{resp: resp("r1", ver(9, 9), val("forged"))}, // ignored: first answer stands
		{
			resp:        resp("r2", ver(1, 1), val("x")),
			wantDecided: &Receipt{Generation: 1, Version: ver(1, 1), Content: val("x"), Acks: []string{"r1", "r2"}},
		},
	})
}

func TestEqualVersionSameContentMergesAcks(t *testing.T) {
	play(t, 3, []step{
		{resp: resp("r1", ver(4, 2), val("same"))},
		{resp: resp("r2", ver(4, 2), val("same"))},
		{
			resp:        resp("r3", ver(4, 2), val("same")),
			wantDecided: &Receipt{Generation: 1, Version: ver(4, 2), Content: val("same"), Acks: []string{"r1", "r2", "r3"}},
		},
	})
}

func TestNewSessionRejectsZeroQuorum(t *testing.T) {
	if _, err := NewSession(0); err == nil {
		t.Fatal("NewSession(0) succeeded, want error")
	}
}

func TestReceiptsAreImmutableSnapshots(t *testing.T) {
	s, err := NewSession(1)
	if err != nil {
		t.Fatal(err)
	}
	fx, err := s.Handle(resp("r1", ver(1, 1), val("x")))
	if err != nil {
		t.Fatal(err)
	}
	// Mutating the returned receipt must not corrupt the session.
	fx.Decided.Acks[0] = "hacked"
	fx.Decided.Content.Value[0] = 'X'
	cur := s.Current()
	if cur.Acks[0] != "r1" || string(cur.Content.Value) != "x" {
		t.Errorf("session state corrupted through returned receipt: %+v", cur)
	}
}

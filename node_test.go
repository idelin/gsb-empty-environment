package replsim

import (
	"reflect"
	"testing"
)

func entries(term uint64, data ...string) []Entry {
	out := make([]Entry, len(data))
	for i, d := range data {
		out[i] = Entry{Term: term, Data: d}
	}
	return out
}

func mustNode(t *testing.T, s Storage) *Node {
	n, err := NewNode(s)
	if err != nil {
		t.Fatalf("NewNode: %v", err)
	}
	return n
}

func TestAppendAndCommitAdvance(t *testing.T) {
	n := mustNode(t, NewMemStorage())
	resp := n.HandleAppend(AppendRequest{Term: 1, Entries: entries(1, "a", "b", "c")})
	if !resp.Success || resp.MatchIndex != 3 {
		t.Fatalf("append: %+v", resp)
	}
	resp = n.HandleAppend(AppendRequest{Term: 1, PrevIndex: 3, PrevTerm: 1, LeaderCommit: 100})
	if !resp.Success {
		t.Fatalf("commit: %+v", resp)
	}
	if got := n.CommitIndex(); got != 3 {
		t.Fatalf("commit index = %d, want 3 (capped at last index)", got)
	}
}

func TestStaleTermRejected(t *testing.T) {
	n := mustNode(t, NewMemStorage())
	n.HandleAppend(AppendRequest{Term: 5, Entries: entries(5, "a")})
	resp := n.HandleAppend(AppendRequest{Term: 4, Entries: entries(4, "x")})
	if resp.Success || resp.Reason != RejectStaleTerm {
		t.Fatalf("got %+v", resp)
	}
	if got := n.Log(); !reflect.DeepEqual(got, entries(5, "a")) {
		t.Fatalf("log mutated by stale request: %v", got)
	}
}

func TestMissingPredecessorRejected(t *testing.T) {
	n := mustNode(t, NewMemStorage())
	n.HandleAppend(AppendRequest{Term: 1, Entries: entries(1, "a")})
	resp := n.HandleAppend(AppendRequest{Term: 1, PrevIndex: 3, PrevTerm: 1, Entries: entries(1, "b")})
	if resp != (AppendResponse{Term: 1, Reason: RejectMissingPredecessor}) {
		t.Fatalf("got %+v", resp)
	}
}

func TestPrevTermMismatchRejected(t *testing.T) {
	n := mustNode(t, NewMemStorage())
	n.HandleAppend(AppendRequest{Term: 2, Entries: entries(2, "a")})
	resp := n.HandleAppend(AppendRequest{Term: 2, PrevIndex: 1, PrevTerm: 1, Entries: entries(2, "b")})
	if resp.Reason != RejectPrevTermMismatch {
		t.Fatalf("got %+v", resp)
	}
}

func TestConflictingSuffixTruncated(t *testing.T) {
	n := mustNode(t, NewMemStorage())
	n.HandleAppend(AppendRequest{Term: 1, Entries: entries(1, "a", "b", "c")})
	resp := n.HandleAppend(AppendRequest{Term: 2, PrevIndex: 1, PrevTerm: 1, Entries: entries(2, "B", "C")})
	if !resp.Success {
		t.Fatalf("append: %+v", resp)
	}
	want := []Entry{{1, "a"}, {2, "B"}, {2, "C"}}
	if got := n.Log(); !reflect.DeepEqual(got, want) {
		t.Fatalf("log = %v, want %v", got, want)
	}
}

func TestCommittedPrefixNotTruncated(t *testing.T) {
	n := mustNode(t, NewMemStorage())
	n.HandleAppend(AppendRequest{Term: 1, Entries: entries(1, "a", "b"), LeaderCommit: 2})
	resp := n.HandleAppend(AppendRequest{Term: 2, PrevIndex: 0, Entries: entries(2, "A")})
	if resp.Reason != RejectCommittedConflict {
		t.Fatalf("got %+v", resp)
	}
	if got, want := n.Log(), entries(1, "a", "b"); !reflect.DeepEqual(got, want) {
		t.Fatalf("log = %v, want %v", got, want)
	}
	if got := n.CommitIndex(); got != 2 {
		t.Fatalf("commit index = %d, want 2", got)
	}
}

func TestSameIndexSameTermDifferentContentIsCorruption(t *testing.T) {
	n := mustNode(t, NewMemStorage())
	n.HandleAppend(AppendRequest{Term: 2, Entries: entries(2, "a")})
	resp := n.HandleAppend(AppendRequest{Term: 3, PrevIndex: 0, Entries: entries(2, "tampered")})
	if resp.Reason != RejectCorruption {
		t.Fatalf("got %+v", resp)
	}
	if got := n.Log(); !reflect.DeepEqual(got, entries(2, "a")) {
		t.Fatalf("log mutated: %v", got)
	}
}

func TestDuplicateAppendIdempotent(t *testing.T) {
	s := NewMemStorage()
	n := mustNode(t, s)
	req := AppendRequest{Term: 1, Entries: entries(1, "a", "b"), LeaderCommit: 1}
	for i := 0; i < 3; i++ {
		if resp := n.HandleAppend(req); !resp.Success {
			t.Fatalf("delivery %d: %+v", i, resp)
		}
	}
	if got, want := n.Log(), entries(1, "a", "b"); !reflect.DeepEqual(got, want) {
		t.Fatalf("log = %v, want %v", got, want)
	}
	if got := n.CommitIndex(); got != 1 {
		t.Fatalf("commit index = %d, want 1", got)
	}
}

func TestNoAckBeforePersist(t *testing.T) {
	s := NewMemStorage()
	n := mustNode(t, s)
	s.FailNext = true
	resp := n.HandleAppend(AppendRequest{Term: 1, Entries: entries(1, "a")})
	if resp.Success || resp.Reason != RejectStorageFailure {
		t.Fatalf("got %+v", resp)
	}
	durable, _ := s.Load()
	if len(durable.Log) != 0 {
		t.Fatalf("unacked entry persisted: %v", durable.Log)
	}
	if resp := n.HandleAppend(AppendRequest{Term: 1, Entries: entries(1, "a")}); !resp.Success {
		t.Fatalf("retry: %+v", resp)
	}
	durable, _ = s.Load()
	if !reflect.DeepEqual(durable.Log, entries(1, "a")) {
		t.Fatalf("durable log = %v", durable.Log)
	}
}

func TestTermBumpPersistedEvenWhenAppendRejected(t *testing.T) {
	s := NewMemStorage()
	n := mustNode(t, s)
	resp := n.HandleAppend(AppendRequest{Term: 7, PrevIndex: 9, PrevTerm: 9})
	if resp.Reason != RejectMissingPredecessor {
		t.Fatalf("got %+v", resp)
	}
	durable, _ := s.Load()
	if durable.CurrentTerm != 7 {
		t.Fatalf("durable term = %d, want 7", durable.CurrentTerm)
	}
}

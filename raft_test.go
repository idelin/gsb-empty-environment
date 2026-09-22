package readindexsim

import "testing"

func contains(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func waitLeader(t *testing.T, s *Sim) int {
	t.Helper()
	for tick := 0; tick < 100; tick++ {
		s.RunFor(1)
		for id := 0; id < len(s.nodes); id++ {
			if s.IsConfirmedLeader(id) {
				return id
			}
		}
	}
	t.Fatal("no leader elected")
	return -1
}

func waitResult(t *testing.T, s *Sim, id int) ReadResult {
	t.Helper()
	for tick := 0; tick < 100; tick++ {
		if result, ok := s.Result(id); ok {
			return result
		}
		s.RunFor(1)
	}
	t.Fatalf("read %d did not complete", id)
	return ReadResult{}
}

func waitNoResult(t *testing.T, s *Sim, id int, ticks int) {
	t.Helper()
	before := len(s.Results())
	s.RunFor(ticks)
	if len(s.Results()) != before {
		t.Fatalf("read %d completed without evidence", id)
	}
}

func waitCommitAtLeast(t *testing.T, s *Sim, leader, index int) {
	t.Helper()
	for tick := 0; tick < 100; tick++ {
		if s.CommitIndex(leader) >= index {
			return
		}
		s.RunFor(1)
	}
	t.Fatalf("leader commit index %d never reached %d", s.CommitIndex(leader), index)
}

func TestReadWaitsForCurrentTermMajorityConfirmation(t *testing.T) {
	s := NewCluster(false)
	leader := waitLeader(t, s)
	if !s.Propose(leader, "k", "v1") {
		t.Fatal("initial proposal failed")
	}
	waitCommitAtLeast(t, s, leader, 2)

	s.Isolate(leader)
	readBefore := s.Read(leader, "k")
	s.RunFor(1)
	waitNoResult(t, s, readBefore, 40)

	newLeader := -1
	for id := range s.nodes {
		if id != leader {
			newLeader = id
			break
		}
	}
	s.RunFor(1)
	for attempts := 0; attempts < 100 && !s.IsConfirmedLeader(newLeader); attempts++ {
		s.RunFor(1)
	}
	if !s.IsConfirmedLeader(newLeader) {
		t.Fatalf("node %d did not become leader", newLeader)
	}

	readAfter := s.Read(newLeader, "k")
	if _, ok := s.Result(readAfter); ok {
		t.Fatal("new leader returned a read before its read confirmation was processed")
	}

	result := waitResult(t, s, readAfter)
	if result.Value != "v1" {
		t.Fatalf("read value = %q, want v1", result.Value)
	}
	if !contains(result.Evidence.Acks, newLeader) || len(result.Evidence.Acks) != 2 {
		t.Fatalf("acks = %v, want quorum containing leader n%d", result.Evidence.Acks, newLeader)
	}
	if result.Evidence.Term != s.Term(newLeader) {
		t.Fatalf("evidence term = %d, want %d", result.Evidence.Term, s.Term(newLeader))
	}
	if result.Evidence.LeaderID != newLeader || !contains(result.Evidence.Acks, newLeader) || len(result.Evidence.Acks) != 2 {
		t.Fatalf("evidence = %+v, want majority acknowledged by leader n%d", result.Evidence, newLeader)
	}
	if result.Evidence.AppliedIndex < result.Evidence.ReadIndex {
		t.Fatalf("applied %d precedes read index %d", result.Evidence.AppliedIndex, result.Evidence.ReadIndex)
	}

	waitNoResult(t, s, readBefore, 30)
}

func TestReadWaitsForCommittedEntryToBeApplied(t *testing.T) {
	s := NewCluster(false)
	leader := waitLeader(t, s)

	s.PauseApply(leader)
	if !s.Propose(leader, "k", "committed-not-applied") {
		t.Fatal("proposal failed")
	}
	waitCommitAtLeast(t, s, leader, 2)

	readID := s.Read(leader, "k")
	s.RunFor(3)
	if _, ok := s.Result(readID); ok {
		t.Fatal("read completed while local state machine was behind read index")
	}
	if s.AppliedIndex(leader) >= s.CommitIndex(leader) {
		t.Fatalf("test setup invalid: applied=%d commit=%d", s.AppliedIndex(leader), s.CommitIndex(leader))
	}

	s.ResumeApply(leader)
	result := waitResult(t, s, readID)
	if result.Value != "committed-not-applied" {
		t.Fatalf("value = %q, want committed-not-applied", result.Value)
	}
	if result.Evidence.AppliedIndex < result.Evidence.ReadIndex {
		t.Fatalf("applied %d precedes read index %d", result.Evidence.AppliedIndex, result.Evidence.ReadIndex)
	}
}

func TestIsolatedOldLeaderCannotObtainReadEvidence(t *testing.T) {
	s := NewCluster(false)
	oldLeader := waitLeader(t, s)
	if !s.Propose(oldLeader, "k", "old-value") {
		t.Fatal("initial proposal failed")
	}
	waitCommitAtLeast(t, s, oldLeader, 2)

	s.Isolate(oldLeader)
	staleRead := s.Read(oldLeader, "k")
	s.RunFor(1)
	waitNoResult(t, s, staleRead, 60)

	newLeader := -1
	for tick := 0; tick < 100; tick++ {
		for id := 0; id < len(s.nodes); id++ {
			if id != oldLeader && s.IsConfirmedLeader(id) {
				newLeader = id
			}
		}
		if newLeader >= 0 {
			break
		}
		s.RunFor(1)
	}
	if newLeader < 0 {
		t.Fatal("majority did not elect a replacement")
	}

	if !s.Propose(newLeader, "k", "new-value") {
		t.Fatal("new leader proposal failed")
	}
	waitCommitAtLeast(t, s, newLeader, 3)
	healthyRead := s.Read(newLeader, "k")
	result := waitResult(t, s, healthyRead)

	if result.Value != "new-value" {
		t.Fatalf("healthy read = %q, want new-value", result.Value)
	}
	if result.Evidence.LeaderID != newLeader || !contains(result.Evidence.Acks, newLeader) || len(result.Evidence.Acks) != 2 {
		t.Fatalf("healthy read evidence acks = %v", result.Evidence.Acks)
	}
	waitNoResult(t, s, staleRead, 60)
}

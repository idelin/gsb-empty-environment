package main

import "testing"

func mustConverge(t *testing.T, leader *Leader, follower *Follower, cfg NetConfig) SimResult {
	t.Helper()
	res := simulate(leader, follower, cfg)
	if !res.Converged {
		t.Fatalf("did not converge")
	}
	if err := verifyPrefix(leader, follower); err != nil {
		t.Fatalf("prefix verification failed: %v", err)
	}
	if follower.commitIndex != len(leader.log) {
		t.Fatalf("commitIndex=%d, want %d", follower.commitIndex, len(leader.log))
	}
	return res
}

func TestEmptyLog(t *testing.T) {
	leader := NewLeader(3, makeLog(1, 1, 2, 2, 3))
	follower := NewFollower("F", nil, 0)
	res := mustConverge(t, leader, follower, NetConfig{Seed: 1})
	if res.Rejections != 1 {
		t.Fatalf("rejections=%d, want 1 (initial probe past end of empty log)", res.Rejections)
	}
}

func TestSameTermSegmentFastBackup(t *testing.T) {
	leader := NewLeader(2, makeLog(1, 1, 1, 2, 2, 2, 2, 2))
	follower := NewFollower("F", makeLog(1, 1, 1, 1, 1, 1, 1, 1), 0)
	res := mustConverge(t, leader, follower, NetConfig{Seed: 2})
	// 冲突任期优化：一次拒绝即定位到任期 1 的末尾，无需逐条回退。
	if res.Rejections != 1 {
		t.Fatalf("rejections=%d, want 1 (fast backup)", res.Rejections)
	}
}

func TestConflictReplyFields(t *testing.T) {
	follower := NewFollower("F", makeLog(1, 1, 1, 1), 0)
	leader := NewLeader(2, makeLog(1, 1, 1, 2, 2))
	rep, _ := follower.handleAppend(leader.requestFor(5))
	if rep.Success || rep.ConflictTerm != 1 || rep.ConflictIndex != 1 {
		t.Fatalf("reply=%+v, want ConflictTerm=1 ConflictIndex=1", rep)
	}
	if next := leader.backup(rep); next != 4 {
		t.Fatalf("backup nextIndex=%d, want 4 (after leader's last term-1 entry)", next)
	}
}

func TestOutOfOrderAndDuplicates(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		leader := NewLeader(4, makeLog(1, 1, 2, 3, 3, 4, 4, 4, 4, 4))
		follower := NewFollower("F", makeLog(1, 1, 2, 2, 2, 2, 2), 3)
		mustConverge(t, leader, follower, NetConfig{Reorder: true, Duplicate: true, Seed: seed})
	}
}

func TestDuplicateRetryIdempotent(t *testing.T) {
	leader := NewLeader(1, makeLog(1, 1, 1))
	follower := NewFollower("F", nil, 0)
	req := leader.requestFor(1)
	if _, events := follower.handleAppend(req); len(events) != 1 {
		t.Fatalf("first delivery events=%v, want 1 append", events)
	}
	rep, events := follower.handleAppend(req) // 重试重复投递
	if !rep.Success || rep.MatchIndex != 3 {
		t.Fatalf("retry reply=%+v", rep)
	}
	if len(events) != 0 {
		t.Fatalf("duplicate delivery produced events=%v, want none", events)
	}
	if len(follower.log) != 3 {
		t.Fatalf("log len=%d after duplicate, want 3", len(follower.log))
	}
}

func TestCommittedEntriesNeverTruncated(t *testing.T) {
	follower := NewFollower("F", makeLog(1, 1, 2, 2), 3) // 前 3 条已提交
	// 恶意/异常请求试图用不同任期覆盖已提交的索引 2。
	req := AppendRequest{
		Term:         5,
		PrevLogIndex: 1,
		PrevLogTerm:  1,
		Entries:      []Entry{{Term: 5, Command: "x"}, {Term: 5, Command: "y"}},
		LeaderCommit: 0,
	}
	rep, events := follower.handleAppend(req)
	if !rep.Success {
		t.Fatalf("reply=%+v", rep)
	}
	if len(events) != 1 || events[0] == "" {
		t.Fatalf("events=%v, want a reject-truncate event", events)
	}
	for i := 0; i < 3; i++ {
		if follower.log[i] != makeLog(1, 1, 2)[i] {
			t.Fatalf("committed entry %d changed: %v", i+1, follower.log[i])
		}
	}
}

func TestStaleTermRejected(t *testing.T) {
	follower := NewFollower("F", makeLog(1, 1), 0)
	follower.currentTerm = 5
	late := AppendRequest{Term: 3, PrevLogIndex: 0, Entries: []Entry{{Term: 3, Command: "old"}}}
	rep, _ := follower.handleAppend(late)
	if rep.Success {
		t.Fatalf("stale-term request must be rejected")
	}
	if len(follower.log) != 2 {
		t.Fatalf("log mutated by stale request: %v", follower.log)
	}
}

package reclaim

import (
	"reflect"
	"testing"
)

func mustHeartbeat(t *testing.T, s *Simulator, id ReplicaID, gen, pos uint64) {
	t.Helper()
	if err := s.Heartbeat(id, gen, pos); err != nil {
		t.Fatalf("heartbeat %s gen=%d pos=%d: %v", id, gen, pos, err)
	}
}

func genOf(t *testing.T, s *Simulator, id ReplicaID) uint64 {
	t.Helper()
	r, ok := s.Replica(id)
	if !ok {
		t.Fatalf("replica %s not active", id)
	}
	return r.Generation
}

// 所有活跃副本都越过水位后，前缀才可回收。
func TestReclaimOnlyAfterAllActivePass(t *testing.T) {
	s := New()
	s.Append(100)
	for _, id := range []ReplicaID{"a", "b", "c"} {
		if err := s.AddReplica(id); err != nil {
			t.Fatal(err)
		}
	}

	mustHeartbeat(t, s, "a", genOf(t, s, "a"), 80)
	mustHeartbeat(t, s, "b", genOf(t, s, "b"), 90)

	if _, end := s.ReclaimablePrefix(); end != 0 {
		t.Fatalf("prefix end = %d, want 0 (c still at 0)", end)
	}

	mustHeartbeat(t, s, "c", genOf(t, s, "c"), 50)
	if _, end := s.ReclaimablePrefix(); end != 50 {
		t.Fatalf("prefix end = %d, want 50", end)
	}

	mustHeartbeat(t, s, "c", genOf(t, s, "c"), 80)
	if _, end := s.ReclaimablePrefix(); end != 80 {
		t.Fatalf("prefix end = %d, want 80", end)
	}
}

// 乱序心跳：位置回退被忽略，水位单调不降。
func TestOutOfOrderHeartbeats(t *testing.T) {
	s := New()
	s.Append(100)
	s.AddReplica("a")
	s.AddReplica("b")

	ga, gb := genOf(t, s, "a"), genOf(t, s, "b")
	mustHeartbeat(t, s, "a", ga, 70)
	mustHeartbeat(t, s, "b", gb, 70)
	if wm := s.Watermark(); wm != 70 {
		t.Fatalf("watermark = %d, want 70", wm)
	}

	// 迟到的心跳携带更小位置：不得回退。
	mustHeartbeat(t, s, "a", ga, 30)
	mustHeartbeat(t, s, "b", gb, 10)
	if wm := s.Watermark(); wm != 70 {
		t.Fatalf("watermark regressed to %d, want 70", wm)
	}

	// 乱序到达：b 先到 90 再到 80，a 仍在 70。
	mustHeartbeat(t, s, "b", gb, 90)
	mustHeartbeat(t, s, "b", gb, 80)
	if wm := s.Watermark(); wm != 70 {
		t.Fatalf("watermark = %d, want 70", wm)
	}
	mustHeartbeat(t, s, "a", ga, 95)
	if wm := s.Watermark(); wm != 90 {
		t.Fatalf("watermark = %d, want 90 (b at 90)", wm)
	}
}

// 剔除成员在确认前仍阻挡回收，确认后放行。
func TestRemovalRequiresConfirmation(t *testing.T) {
	s := New()
	s.Append(100)
	s.AddReplica("a")
	s.AddReplica("b")

	mustHeartbeat(t, s, "a", genOf(t, s, "a"), 60)
	mustHeartbeat(t, s, "b", genOf(t, s, "b"), 20)

	if err := s.RemoveReplica("b"); err != nil {
		t.Fatal(err)
	}
	// 未确认：b 仍阻挡在 20。
	if wm := s.Watermark(); wm != 20 {
		t.Fatalf("watermark = %d, want 20 (b pending removal)", wm)
	}
	if got := s.PendingRemoval(); !reflect.DeepEqual(got, []ReplicaID{"b"}) {
		t.Fatalf("pending = %v, want [b]", got)
	}
	// 待确认成员不再接受心跳。
	if err := s.Heartbeat("b", genOf(t, s, "a"), 99); err != ErrUnknownReplica {
		t.Fatalf("heartbeat to pending replica err = %v, want ErrUnknownReplica", err)
	}

	if err := s.ConfirmRemoval("b"); err != nil {
		t.Fatal(err)
	}
	if wm := s.Watermark(); wm != 60 {
		t.Fatalf("watermark = %d, want 60 after confirm", wm)
	}
	// 重复确认报错。
	if err := s.ConfirmRemoval("b"); err != ErrNotPending {
		t.Fatalf("re-confirm err = %v, want ErrNotPending", err)
	}
}

// 重加入分配新代次，旧代次心跳不能提升水位。
func TestRejoinRejectsStaleGeneration(t *testing.T) {
	s := New()
	s.Append(100)
	s.AddReplica("a")
	s.AddReplica("b")

	ga := genOf(t, s, "a")
	oldGenB := genOf(t, s, "b")
	mustHeartbeat(t, s, "a", ga, 90)
	mustHeartbeat(t, s, "b", oldGenB, 40)

	// b 崩溃重加入：新代次，从当前水位(40)恢复。
	if err := s.Rejoin("b"); err != nil {
		t.Fatal(err)
	}
	newGenB := genOf(t, s, "b")
	if newGenB == oldGenB {
		t.Fatal("rejoin did not allocate a new generation")
	}

	// 旧代次心跳（即使位置很高）必须拒绝，水位不动。
	if err := s.Heartbeat("b", oldGenB, 95); err != ErrStaleGeneration {
		t.Fatalf("stale heartbeat err = %v, want ErrStaleGeneration", err)
	}
	if wm := s.Watermark(); wm != 40 {
		t.Fatalf("watermark = %d, want 40 (stale heartbeat must not advance)", wm)
	}

	// 新代次正常推进。
	mustHeartbeat(t, s, "b", newGenB, 90)
	if wm := s.Watermark(); wm != 90 {
		t.Fatalf("watermark = %d, want 90", wm)
	}
}

// 乱序心跳与成员变更交错。
func TestInterleavedHeartbeatsAndMembership(t *testing.T) {
	s := New()
	s.Append(200)
	s.AddReplica("a")
	s.AddReplica("b")
	s.AddReplica("c")

	ga := genOf(t, s, "a")
	gb := genOf(t, s, "b")
	gc := genOf(t, s, "c")

	// 追加日志与心跳交错，含乱序。
	s.Append(0) // no-op，模拟事件流
	mustHeartbeat(t, s, "a", ga, 120)
	mustHeartbeat(t, s, "c", gc, 100)
	mustHeartbeat(t, s, "b", gb, 110)
	mustHeartbeat(t, s, "a", ga, 80) // 乱序迟到，忽略
	if wm := s.Watermark(); wm != 100 {
		t.Fatalf("watermark = %d, want 100", wm)
	}

	// 剔除最慢的 c，但未确认前仍阻挡。
	if err := s.RemoveReplica("c"); err != nil {
		t.Fatal(err)
	}
	mustHeartbeat(t, s, "a", ga, 150)
	mustHeartbeat(t, s, "b", gb, 140)
	if wm := s.Watermark(); wm != 100 {
		t.Fatalf("watermark = %d, want 100 (c pending)", wm)
	}

	// 确认剔除 c，水位推进到 min(a,b)=140。
	if err := s.ConfirmRemoval("c"); err != nil {
		t.Fatal(err)
	}
	if wm := s.Watermark(); wm != 140 {
		t.Fatalf("watermark = %d, want 140", wm)
	}

	// c 重加入：新代次，从水位 140 恢复；旧心跳无效。
	if err := s.Rejoin("c"); err != nil {
		t.Fatal(err)
	}
	if err := s.Heartbeat("c", gc, 200); err != ErrStaleGeneration {
		t.Fatalf("stale c heartbeat err = %v, want ErrStaleGeneration", err)
	}
	if wm := s.Watermark(); wm != 140 {
		t.Fatalf("watermark = %d, want 140", wm)
	}

	// c 用新代次追赶，期间新成员 d 加入（从水位恢复）。
	if err := s.AddReplica("d"); err != nil {
		t.Fatal(err)
	}
	mustHeartbeat(t, s, "c", genOf(t, s, "c"), 160)
	mustHeartbeat(t, s, "d", genOf(t, s, "d"), 150)
	mustHeartbeat(t, s, "a", ga, 170)
	mustHeartbeat(t, s, "b", gb, 180)
	if wm := s.Watermark(); wm != 150 {
		t.Fatalf("watermark = %d, want 150 (d at 150)", wm)
	}

	// 越界心跳报错。
	if err := s.Heartbeat("a", ga, 500); err != ErrPositionBeyond {
		t.Fatalf("beyond-log heartbeat err = %v, want ErrPositionBeyond", err)
	}
}

// 阻挡节点输出：位于水位的全部成员，含待确认剔除者。
func TestBlockingNodes(t *testing.T) {
	s := New()
	s.Append(100)
	s.AddReplica("a")
	s.AddReplica("b")
	s.AddReplica("c")

	mustHeartbeat(t, s, "a", genOf(t, s, "a"), 30)
	mustHeartbeat(t, s, "b", genOf(t, s, "b"), 30)
	mustHeartbeat(t, s, "c", genOf(t, s, "c"), 70)
	if err := s.RemoveReplica("b"); err != nil {
		t.Fatal(err)
	}

	blockers := s.BlockingNodes()
	if len(blockers) != 2 {
		t.Fatalf("blockers = %v, want 2 nodes", blockers)
	}
	if blockers[0].ID != "a" || blockers[0].PendingRemoval {
		t.Fatalf("blocker[0] = %v, want active a", blockers[0])
	}
	if blockers[1].ID != "b" || !blockers[1].PendingRemoval {
		t.Fatalf("blocker[1] = %v, want pending-removal b", blockers[1])
	}

	if err := s.ConfirmRemoval("b"); err != nil {
		t.Fatal(err)
	}
	blockers = s.BlockingNodes()
	if len(blockers) != 1 || blockers[0].ID != "a" {
		t.Fatalf("blockers after confirm = %v, want [a]", blockers)
	}
}

// 无成员时没有任何副本确认进度，水位保持不动。
func TestNoMembersReclaimsNothing(t *testing.T) {
	s := New()
	s.Append(42)
	if _, end := s.ReclaimablePrefix(); end != 0 {
		t.Fatalf("prefix end = %d, want 0 with no members", end)
	}
}

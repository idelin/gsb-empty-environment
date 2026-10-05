package reclaimsim

import (
	"reflect"
	"strings"
	"testing"
)

func mustHeartbeat(t *testing.T, s *Simulator, id string, gen, matched uint64) {
	t.Helper()
	if err := s.Heartbeat(id, gen, matched); err != nil {
		t.Fatalf("Heartbeat(%s, gen=%d, matched=%d) failed: %v", id, gen, matched, err)
	}
}

func expectReclaimable(t *testing.T, s *Simulator, wantPrefix uint64, wantBlockers ...string) {
	t.Helper()
	prefix, blockers := s.Reclaimable()
	if prefix != wantPrefix || !reflect.DeepEqual(blockers, wantBlockers) {
		t.Fatalf("Reclaimable() = (%d, %v), want (%d, %v)", prefix, blockers, wantPrefix, wantBlockers)
	}
}

// 所有活跃副本越过水位后才可回收，最慢者即阻挡节点。
func TestReclaimRequiresAllActiveMembers(t *testing.T) {
	s := New("a", "b", "c")
	s.Append(100)

	expectReclaimable(t, s, 0, "a", "b", "c")

	mustHeartbeat(t, s, "a", 1, 80)
	mustHeartbeat(t, s, "b", 1, 50)
	mustHeartbeat(t, s, "c", 1, 90)
	expectReclaimable(t, s, 50, "b")

	mustHeartbeat(t, s, "b", 1, 80)
	expectReclaimable(t, s, 80, "a", "b")

	mustHeartbeat(t, s, "a", 1, 100)
	mustHeartbeat(t, s, "b", 1, 100)
	mustHeartbeat(t, s, "c", 1, 100)
	expectReclaimable(t, s, 100, "a", "b", "c")

	got, err := s.Reclaim()
	if err != nil || got != 100 {
		t.Fatalf("Reclaim() = (%d, %v), want (100, nil)", got, err)
	}
	if s.BaseIndex() != 100 || s.LastIndex() != 100 {
		t.Fatalf("after reclaim: base=%d last=%d, want 100/100", s.BaseIndex(), s.LastIndex())
	}
}

// 乱序心跳：matched 回退被忽略，水位只增不减。
func TestOutOfOrderHeartbeats(t *testing.T) {
	s := New("a", "b")
	s.Append(50)

	mustHeartbeat(t, s, "a", 1, 40)
	if err := s.Heartbeat("a", 1, 10); err == nil {
		t.Fatal("regressing heartbeat should be rejected")
	}
	if err := s.Heartbeat("a", 1, 40); err == nil {
		t.Fatal("duplicate heartbeat should be rejected")
	}
	m, _ := s.Member("a")
	if m.Matched != 40 {
		t.Fatalf("matched = %d, want 40 (watermark must not regress)", m.Matched)
	}

	if err := s.Heartbeat("a", 1, 51); err == nil {
		t.Fatal("matched beyond last index should be rejected")
	}
	if err := s.Heartbeat("ghost", 1, 1); err == nil {
		t.Fatal("unknown member heartbeat should be rejected")
	}
	expectReclaimable(t, s, 0, "b")
}

// 重加入副本从新代次恢复，旧代次心跳不能提升水位。
func TestRejoinWithNewGeneration(t *testing.T) {
	s := New("a", "b")
	s.Append(100)
	mustHeartbeat(t, s, "a", 1, 100)
	mustHeartbeat(t, s, "b", 1, 100)
	if _, err := s.Reclaim(); err != nil {
		t.Fatal(err)
	}
	s.Append(50) // 日志 (100, 150]

	// b 掉线后重加入：代次提升，水位回落到已回收前缀 100。
	if err := s.Rejoin("b", 1); err == nil {
		t.Fatal("rejoin with non-increasing generation should be rejected")
	}
	if err := s.Rejoin("b", 2); err != nil {
		t.Fatal(err)
	}
	m, _ := s.Member("b")
	if m.Generation != 2 || m.Matched != 100 {
		t.Fatalf("after rejoin: gen=%d matched=%d, want 2/100", m.Generation, m.Matched)
	}

	// 旧代次心跳被拒绝，水位不变。
	if err := s.Heartbeat("b", 1, 150); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("old-generation heartbeat should be rejected as stale, got %v", err)
	}
	m, _ = s.Member("b")
	if m.Matched != 100 {
		t.Fatalf("stale heartbeat raised watermark to %d", m.Matched)
	}
	// a 的水位同样停在 100，与 b 同为阻挡节点。
	expectReclaimable(t, s, 100, "a", "b")

	// 新代次心跳正常推进。
	mustHeartbeat(t, s, "b", 2, 150)
	mustHeartbeat(t, s, "a", 1, 150)
	expectReclaimable(t, s, 150, "a", "b")
}

// 剔除成员在确认前仍阻挡回收，确认后退出水位计算。
func TestRemovalRequiresConfirmation(t *testing.T) {
	s := New("a", "b", "c")
	s.Append(100)
	mustHeartbeat(t, s, "a", 1, 100)
	mustHeartbeat(t, s, "b", 1, 100)
	mustHeartbeat(t, s, "c", 1, 30)

	if err := s.RemoveMember("c"); err != nil {
		t.Fatal(err)
	}
	// 未确认前 c 仍阻挡回收。
	expectReclaimable(t, s, 30, "c")
	if got, _ := s.Reclaim(); got != 30 {
		t.Fatalf("Reclaim() = %d, want 30 while removal unconfirmed", got)
	}

	// 确认后 c 退出，前缀推进到 100。
	if err := s.ConfirmRemoval("c"); err != nil {
		t.Fatal(err)
	}
	expectReclaimable(t, s, 100, "a", "b")
	if err := s.Heartbeat("c", 1, 100); err == nil {
		t.Fatal("heartbeat from removed member should be rejected")
	}
	if err := s.ConfirmRemoval("c"); err == nil {
		t.Fatal("double confirm should be rejected")
	}
}

// 乱序心跳与成员变更交错：剔除-确认-重加入全链路。
func TestInterleavedHeartbeatsAndMembershipChanges(t *testing.T) {
	s := New("a", "b", "c")
	s.Append(200)

	// 一轮乱序心跳：c 的 120 先于 b 的 150 到达，a 的回退心跳被忽略。
	mustHeartbeat(t, s, "a", 1, 180)
	mustHeartbeat(t, s, "c", 1, 120)
	mustHeartbeat(t, s, "b", 1, 150)
	if err := s.Heartbeat("a", 1, 90); err == nil {
		t.Fatal("out-of-order heartbeat for a should be rejected")
	}
	expectReclaimable(t, s, 120, "c")

	// 发起剔除 c，但确认前 c 的心跳仍然有效并推进水位。
	if err := s.RemoveMember("c"); err != nil {
		t.Fatal(err)
	}
	mustHeartbeat(t, s, "c", 1, 160)
	expectReclaimable(t, s, 150, "b")

	// 确认剔除 c，回收推进到 b 的水位。
	if err := s.ConfirmRemoval("c"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Reclaim(); got != 150 {
		t.Fatalf("Reclaim() = %d, want 150", got)
	}

	// 现存成员不能重复加入；c 已确认剔除，可作为全新成员重新加入。
	if err := s.AddMember("a"); err == nil {
		t.Fatal("re-adding existing member should fail")
	}
	if err := s.AddMember("d"); err != nil {
		t.Fatal(err)
	}
	expectReclaimable(t, s, 150, "b", "d")

	// d 的乱序心跳不能越过自身水位，逐步推进后回收继续。
	mustHeartbeat(t, s, "d", 1, 170)
	if err := s.Heartbeat("d", 1, 160); err == nil {
		t.Fatal("out-of-order heartbeat for d should be rejected")
	}
	mustHeartbeat(t, s, "a", 1, 200)
	mustHeartbeat(t, s, "b", 1, 200)
	expectReclaimable(t, s, 170, "d")
	mustHeartbeat(t, s, "d", 1, 200)
	if got, _ := s.Reclaim(); got != 200 {
		t.Fatalf("Reclaim() = %d, want 200", got)
	}
	if s.BaseIndex() != 200 || s.LastIndex() != 200 {
		t.Fatalf("final state: base=%d last=%d, want 200/200", s.BaseIndex(), s.LastIndex())
	}
}

// 回收不会越过任何成员水位，也不会回退。
func TestReclaimNeverRegresses(t *testing.T) {
	s := New("a", "b")
	s.Append(10)
	mustHeartbeat(t, s, "a", 1, 10)
	mustHeartbeat(t, s, "b", 1, 4)

	if got, _ := s.Reclaim(); got != 4 {
		t.Fatalf("Reclaim() = %d, want 4", got)
	}
	if got, _ := s.Reclaim(); got != 4 {
		t.Fatalf("second Reclaim() = %d, want 4 (no regression)", got)
	}
	s.Append(6) // 日志 (4, 10]
	mustHeartbeat(t, s, "b", 1, 10)
	if got, _ := s.Reclaim(); got != 10 {
		t.Fatalf("Reclaim() = %d, want 10", got)
	}
}

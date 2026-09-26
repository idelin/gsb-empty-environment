package session

import (
	"errors"
	"testing"
)

// newCluster 构建 3 节点全连接异步复制集群，复制延迟 delay。
func newCluster(delay int64) (*Simulator, *Node, *Node, *Node) {
	sim := NewSimulator()
	a := NewNode(sim, "A", NewMemDisk(), delay)
	b := NewNode(sim, "B", NewMemDisk(), delay)
	c := NewNode(sim, "C", NewMemDisk(), delay)
	a.SetPeers("B", "C")
	b.SetPeers("A", "C")
	c.SetPeers("A", "B")
	return sim, a, b, c
}

// TestWriteAckUpdatesToken 枚举写确认：每次写确认都把新版本并入令牌。
func TestWriteAckUpdatesToken(t *testing.T) {
	sim, _, _, _ := newCluster(10)
	s := NewSession(sim, "s1")

	vv, err := s.Write("A", "k", "v1")
	if err != nil {
		t.Fatalf("write1: %v", err)
	}
	if !vv.Dominates(VersionVector{"A": 1}) {
		t.Fatalf("ack version = %v, want >= {A:1}", vv)
	}
	if !s.Token.Versions["k"].Dominates(VersionVector{"A": 1}) {
		t.Fatalf("token not updated by ack: %v", s.Token.Versions["k"])
	}

	vv2, err := s.Write("A", "k", "v2")
	if err != nil {
		t.Fatalf("write2: %v", err)
	}
	if !vv2.Dominates(VersionVector{"A": 2}) {
		t.Fatalf("ack2 version = %v, want >= {A:2}", vv2)
	}
}

// TestReadYourWritesAfterMigration 会话迁移：A 写成功后转 B 读，
// B 尚未收到复制时读挂起等待，复制到达后返回新值，绝不返回旧值（活性证据）。
func TestReadYourWritesAfterMigration(t *testing.T) {
	sim, _, _, _ := newCluster(10)
	s := NewSession(sim, "s1")
	if _, err := s.Write("A", "k", "v1"); err != nil {
		t.Fatalf("write: %v", err)
	}

	moved := s.Migrate() // 会话迁移到另一客户端侧，令牌随行
	start := sim.Now
	got, err := moved.Read("B", "k", 50)
	if err != nil {
		t.Fatalf("read after migration: %v", err)
	}
	if got != "v1" {
		t.Fatalf("read returned %q, want %q (must not return old value)", got, "v1")
	}
	if sim.Now-start < 10 {
		t.Fatalf("read did not wait for replication (elapsed=%d)", sim.Now-start)
	}
}

// TestReadTimeoutMarkedUnmet 读超时：复制消息被故障丢弃时，
// 读必须以 ErrConsistencyTimeout 显式标记一致性未满足，不得静默返回旧值。
func TestReadTimeoutMarkedUnmet(t *testing.T) {
	sim, _, _, _ := newCluster(10)
	sim.Fault = func(mc MsgContext) (bool, int64) {
		if mc.From == "A" && mc.To == "B" {
			return true, 0 // 丢弃 A->B 复制
		}
		return false, 10
	}
	s := NewSession(sim, "s1")
	if _, err := s.Write("A", "k", "v1"); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := s.Migrate().Read("B", "k", 50)
	if !errors.Is(err, ErrConsistencyTimeout) {
		t.Fatalf("err = %v, want ErrConsistencyTimeout", err)
	}
	if got != "" {
		t.Fatalf("timeout read returned value %q, want none", got)
	}
}

// TestCrossKeyDependency 令牌是跨键因果向量：读 k1 时若副本缺少
// 令牌中 k2 的依赖版本，即使本键版本已满足也不得返回。
func TestCrossKeyDependency(t *testing.T) {
	sim, a, _, _ := newCluster(10)
	sim.Fault = func(mc MsgContext) (bool, int64) {
		if mc.To == "B" && mc.Key == "k2" {
			return true, 0 // 只丢 k2 到 B 的复制
		}
		return false, 10
	}
	s := NewSession(sim, "s1")
	if _, err := s.Write("A", "k1", "x"); err != nil {
		t.Fatalf("write k1: %v", err)
	}
	if _, err := s.Write("A", "k2", "y"); err != nil {
		t.Fatalf("write k2: %v", err)
	}

	// B 已有 k1 的新版本，但缺少因果依赖 k2：读必须等待并最终超时。
	if _, err := s.Migrate().Read("B", "k1", 50); !errors.Is(err, ErrConsistencyTimeout) {
		t.Fatalf("read k1 err = %v, want ErrConsistencyTimeout (missing dependency k2)", err)
	}

	// 反熵补齐后，同一令牌再次读取成功（令牌在部分失败后继续有效）。
	sim.Fault = nil
	a.ReplicateAll()
	got, err := s.Migrate().Read("B", "k1", 50)
	if err != nil {
		t.Fatalf("read after anti-entropy: %v", err)
	}
	if got != "x" {
		t.Fatalf("got %q, want %q", got, "x")
	}
}

// TestMonotonicReads 单调读：同一会话跨节点连续读，版本不得回退。
func TestMonotonicReads(t *testing.T) {
	sim, _, _, _ := newCluster(10)
	s := NewSession(sim, "s1")

	if _, err := s.Write("A", "k", "v1"); err != nil {
		t.Fatalf("write1: %v", err)
	}
	got1, err := s.Migrate().Read("B", "k", 50)
	if err != nil || got1 != "v1" {
		t.Fatalf("read1 = %q, %v", got1, err)
	}
	ver1 := s.Token.Versions["k"].Copy()

	if _, err := s.Write("B", "k", "v2"); err != nil {
		t.Fatalf("write2: %v", err)
	}
	got2, err := s.Migrate().Read("C", "k", 50)
	if err != nil {
		t.Fatalf("read2: %v", err)
	}
	if got2 != "v2" {
		t.Fatalf("read2 = %q, want v2 (monotonic reads violated)", got2)
	}
	if !s.Token.Versions["k"].Dominates(ver1) {
		t.Fatalf("token regressed: %v !>= %v", s.Token.Versions["k"], ver1)
	}
}

// TestRestartKeepsTokenValid 节点重启后令牌继续有效：版本持久化，无降级。
func TestRestartKeepsTokenValid(t *testing.T) {
	sim, a, _, _ := newCluster(10)
	s := NewSession(sim, "s1")
	if _, err := s.Write("A", "k", "v1"); err != nil {
		t.Fatalf("write: %v", err)
	}

	a.Crash()
	a.Restart()

	got, err := s.Migrate().Read("A", "k", 50)
	if err != nil {
		t.Fatalf("read after restart: %v (token must remain valid)", err)
	}
	if got != "v1" {
		t.Fatalf("got %q, want v1 (silent downgrade!)", got)
	}
}

// TestSnapshotInstallNoDowngrade 快照安装：过期快照不得降低本地版本；
// 新节点可通过快照达到令牌要求。
func TestSnapshotInstallNoDowngrade(t *testing.T) {
	sim, a, b, c := newCluster(10)
	s := NewSession(sim, "s1")
	if _, err := s.Write("A", "k", "v1"); err != nil {
		t.Fatalf("write1: %v", err)
	}
	stale := a.Snapshot() // v1 时期的快照
	if _, err := s.Write("A", "k", "v2"); err != nil {
		t.Fatalf("write2: %v", err)
	}
	sim.RunUntil(func() bool { return false }, sim.Now+20) // 让 v2 复制到 B

	b.InstallSnapshot("A", stale) // 过期快照：不得降级
	got, err := s.Migrate().Read("B", "k", 50)
	if err != nil {
		t.Fatalf("read after stale snapshot: %v", err)
	}
	if got != "v2" {
		t.Fatalf("stale snapshot downgraded B: got %q, want v2", got)
	}

	c.InstallSnapshot("A", a.Snapshot()) // 全新节点经快照满足令牌
	got, err = s.Migrate().Read("C", "k", 50)
	if err != nil || got != "v2" {
		t.Fatalf("read from snapshot-installed node = %q, %v", got, err)
	}
}

// TestTokenMixupRejected 不同会话的令牌不得混用。
func TestTokenMixupRejected(t *testing.T) {
	sim, _, _, _ := newCluster(10)
	s1 := NewSession(sim, "s1")
	if _, err := s1.Write("A", "k", "v1"); err != nil {
		t.Fatalf("write: %v", err)
	}

	s2 := NewSession(sim, "s2")
	s2.Token = s1.Token // 混用 s1 的令牌
	if _, err := s2.Read("A", "k", 10); !errors.Is(err, ErrTokenMismatch) {
		t.Fatalf("read err = %v, want ErrTokenMismatch", err)
	}
	if _, err := s2.Write("A", "k", "evil"); !errors.Is(err, ErrTokenMismatch) {
		t.Fatalf("write err = %v, want ErrTokenMismatch", err)
	}
}

// TestEnumerationSafetyAndLiveness 有限消息模拟枚举：
// 对 读目标 x 丢包掩码 x 超时 的全部 16 条轨迹枚举会话迁移、写确认与读超时，
// 安全性：每条轨迹要么返回新值要么显式超时，绝不返回旧值；
// 活性：至少一条轨迹成功、至少一条轨迹显式超时。
func TestEnumerationSafetyAndLiveness(t *testing.T) {
	targets := []string{"B", "C"}
	timeouts := []int64{5, 50} // 5 < 复制延迟 10，必超时；50 视丢包而定
	successes, explicitTimeouts := 0, 0

	for _, target := range targets {
		for mask := 0; mask < 4; mask++ {
			for _, timeout := range timeouts {
				sim, _, _, _ := newCluster(10)
				m := mask
				sim.Fault = func(mc MsgContext) (bool, int64) {
					if mc.From == "A" && mc.To == "B" && m&1 != 0 {
						return true, 0
					}
					if mc.From == "A" && mc.To == "C" && m&2 != 0 {
						return true, 0
					}
					return false, 10
				}
				s := NewSession(sim, "s1")
				if _, err := s.Write("A", "k", "v1"); err != nil { // 写确认
					t.Fatalf("write: %v", err)
				}
				got, err := s.Migrate().Read(target, "k", timeout) // 会话迁移 + 读
				switch {
				case errors.Is(err, ErrConsistencyTimeout):
					explicitTimeouts++
					if got != "" {
						t.Fatalf("target=%s mask=%d timeout=%d: timeout returned value %q", target, m, timeout, got)
					}
				case err != nil:
					t.Fatalf("target=%s mask=%d timeout=%d: unexpected err %v", target, m, timeout, err)
				default:
					successes++
					if got != "v1" {
						t.Fatalf("SAFETY VIOLATION target=%s mask=%d timeout=%d: stale value %q", target, m, timeout, got)
					}
				}
			}
		}
	}
	if successes == 0 || explicitTimeouts == 0 {
		t.Fatalf("enumeration must cover both outcomes: successes=%d timeouts=%d", successes, explicitTimeouts)
	}
	t.Logf("enumerated 16 traces: %d succeeded, %d explicit consistency timeouts", successes, explicitTimeouts)
}

// TestNoGlobalLinearizabilityPromise 明确不承诺全局线性一致：
// 无令牌约束的会话允许读到旧值；会话保证只由令牌提供。
func TestNoGlobalLinearizabilityPromise(t *testing.T) {
	sim, _, _, _ := newCluster(10)
	w := NewSession(sim, "writer")
	if _, err := w.Write("A", "k", "v1"); err != nil {
		t.Fatalf("write: %v", err)
	}

	other := NewSession(sim, "other") // 无令牌要求的独立会话
	got, err := other.Read("B", "k", 5)
	if err != nil {
		t.Fatalf("unconstrained read should not fail: %v", err)
	}
	if got != "" {
		t.Fatalf("got %q, want empty (stale allowed without token)", got)
	}
}

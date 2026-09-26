package sessionkv

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// newCluster 构建全互联节点集群，链路延迟均为 delay。
func newCluster(sim *Simulator, delay int64, ids ...string) map[string]*Node {
	nodes := map[string]*Node{}
	for _, id := range ids {
		nodes[id] = NewNode(sim, id)
	}
	for _, a := range ids {
		for _, b := range ids {
			if a == b {
				continue
			}
			nodes[a].AddPeer(nodes[b])
			sim.SetLink(a, b, true, delay)
		}
	}
	return nodes
}

// TestReadYourWritesAfterMigration 安全性核心场景：
// 写在 A 成功后，会话迁移到 B 读取，绝不能返回旧值。
func TestReadYourWritesAfterMigration(t *testing.T) {
	sim := NewSimulator()
	nodes := newCluster(sim, 10, "A", "B")
	A, B := nodes["A"], nodes["B"]

	s := NewClient(sim, "s1")
	w0 := s.Write(B, "k", "v0", 100) // 旧值写在 B
	w1 := s.Write(A, "k", "v1", 100) // 新值写在 A（依赖 {B:1}，等复制到达后应用）
	var r *ReadResult
	sim.After(15, func() { // w1 在 t=10 确认（B->A 复制到达后），令牌已含 {A:1,B:1}
		if !w1.Done || w1.Err != nil {
			return
		}
		migrated := s.Migrate(sim)     // 会话迁移到 B
		r = migrated.Read(B, "k", 500) // 必须等到 B 覆盖令牌，禁止返回 v0
	})
	sim.Run()

	if w0.Err != nil || w1.Err != nil {
		t.Fatalf("writes failed: w0=%v w1=%v", w0.Err, w1.Err)
	}
	if r.Err != nil {
		t.Fatalf("read after migration failed: %v", r.Err)
	}
	if r.Value != "v1" {
		t.Fatalf("read-your-writes violated after migration: got %q, want %q", r.Value, "v1")
	}
	if !r.Version.Covers(w1.Version) {
		t.Fatalf("read version %s does not cover write ack %s", r.Version, w1.Version)
	}
}

// TestReadTimeoutMarkedUnsatisfied 分区下读无法在期限内达到令牌：
// 必须超时并显式标记 ErrNotSatisfied，禁止静默返回旧值。
func TestReadTimeoutMarkedUnsatisfied(t *testing.T) {
	sim := NewSimulator()
	nodes := newCluster(sim, 10, "A", "B")
	A, B := nodes["A"], nodes["B"]

	other := NewClient(sim, "seed")
	other.Write(B, "k", "stale", 100) // B 上有旧值

	sim.SetLink("A", "B", false, 0) // 分区：A 的复制永远到不了 B

	s := NewClient(sim, "s1")
	w := s.Write(A, "k", "fresh", 100)
	r := s.Read(B, "k", 50) // B 有旧值但不满足令牌，必须等待并最终超时

	sim.Run()

	if w.Err != nil {
		t.Fatalf("write failed: %v", w.Err)
	}
	if !r.Done || !errors.Is(r.Err, ErrNotSatisfied) {
		t.Fatalf("read should time out with ErrNotSatisfied, got done=%v err=%v", r.Done, r.Err)
	}
	if r.Found || r.Value == "stale" {
		t.Fatalf("silent degradation: stale value %q returned", r.Value)
	}
}

// TestMonotonicReads 同一会话的连续读版本单调不回退。
func TestMonotonicReads(t *testing.T) {
	sim := NewSimulator()
	nodes := newCluster(sim, 10, "A", "B")
	A, B := nodes["A"], nodes["B"]

	s := NewClient(sim, "s1")
	s.Write(A, "k", "a", 500)
	r1 := s.Read(B, "k", 500)

	sim.After(30, func() { // 等第一次读完成、令牌前进后再写
		s.Write(A, "k", "b", 500)
		s.Read(B, "k", 500)
	})
	sim.Run()

	if r1.Err != nil || r1.Value != "a" {
		t.Fatalf("first read: value=%q err=%v", r1.Value, r1.Err)
	}
	if len(s.Reads) != 2 {
		t.Fatalf("expected 2 reads, got %d", len(s.Reads))
	}
	r2 := s.Reads[1].Res
	if r2.Err != nil || r2.Value != "b" {
		t.Fatalf("second read: value=%q err=%v", r2.Value, r2.Err)
	}
	if !r2.Version.Covers(r1.Version) {
		t.Fatalf("monotonic read violated: %s does not cover %s", r2.Version, r1.Version)
	}
}

// TestCrossKeyCausalDependency 读请求不得返回满足本键版本却缺少其依赖的快照：
// k2 依赖 k1；k2 的复制消息先到时必须被因果缓冲，读 k1/k2 都要等 k1 到达。
func TestCrossKeyCausalDependency(t *testing.T) {
	sim := NewSimulator()
	nodes := newCluster(sim, 10, "A", "B")
	A, B := nodes["A"], nodes["B"]

	sim.SetLink("A", "B", false, 0) // 先断链路，确保复制消息不泄漏给 B

	w1 := NewClient(sim, "w")
	w1.Write(A, "k1", "v1", 100) // {A:1}
	w1.Write(A, "k2", "v2", 100) // {A:2}，依赖 {A:1}

	observer := NewClient(sim, "s2")
	observer.Read(A, "k2", 100) // 观察到 k2，令牌变为 {A:2}

	k1Msg := WriteMsg{Key: "k1", Value: "v1", Version: Vector{"A": 1}, Deps: Vector{}}
	k2Msg := WriteMsg{Key: "k2", Value: "v2", Version: Vector{"A": 2}, Deps: Vector{"A": 1}}

	var readK1, readK2 *ReadResult
	var doneAt49 bool
	sim.After(5, func() { B.ApplyRemote(k2Msg) }) // 乱序：依赖未满足的 k2 先到
	sim.After(6, func() {
		readK1 = observer.Read(B, "k1", 500)
		readK2 = observer.Read(B, "k2", 500)
	})
	sim.After(49, func() { // 依赖 k1 尚未到达：两个读都必须仍在等待
		doneAt49 = readK1.Done || readK2.Done
	})
	sim.After(50, func() { B.ApplyRemote(k1Msg) }) // 依赖后到
	sim.Run()

	if doneAt49 {
		t.Fatalf("read served before causal dependency k1 arrived (snapshot missing deps)")
	}
	if readK1.Err != nil || readK1.Value != "v1" {
		t.Fatalf("read k1: value=%q err=%v", readK1.Value, readK1.Err)
	}
	if readK2.Err != nil || readK2.Value != "v2" {
		t.Fatalf("read k2: value=%q err=%v", readK2.Value, readK2.Err)
	}
}

// TestTokenSessionIsolation 不同会话的令牌禁止混用。
func TestTokenSessionIsolation(t *testing.T) {
	sim := NewSimulator()
	nodes := newCluster(sim, 10, "A")
	A := nodes["A"]

	s1 := NewClient(sim, "s1")
	s1.Write(A, "k", "v", 100)

	s2 := NewClient(sim, "s2")
	r := s2.ReadWithToken(A, "k", s1.Token(), 100) // 盗用 s1 的令牌

	sim.Run()

	if !errors.Is(r.Err, ErrTokenSessionMismatch) {
		t.Fatalf("expected ErrTokenSessionMismatch, got %v", r.Err)
	}
}

// TestRestartKeepsTokenValid 节点重启后令牌继续有效：
// 崩溃期间发起的读在重启反熵补齐状态后被服务，而不是失败或降级。
func TestRestartKeepsTokenValid(t *testing.T) {
	sim := NewSimulator()
	nodes := newCluster(sim, 10, "A", "B")
	A, B := nodes["A"], nodes["B"]

	s := NewClient(sim, "s1")
	s.Write(A, "k", "v1", 500)

	sim.After(1, func() { B.Crash() })
	var r *ReadResult
	sim.After(2, func() { r = s.Read(B, "k", 500) }) // B 宕机中，读挂起
	sim.After(5, func() { B.Restart() })             // 重启后反熵从 A 补齐
	sim.Run()

	if r.Err != nil {
		t.Fatalf("read after restart failed: %v", r.Err)
	}
	if r.Value != "v1" {
		t.Fatalf("got %q, want %q", r.Value, "v1")
	}
}

// TestSnapshotInstallSatisfiesToken 新节点通过快照安装达到令牌要求。
func TestSnapshotInstallSatisfiesToken(t *testing.T) {
	sim := NewSimulator()
	nodes := newCluster(sim, 10, "A", "C")
	A, C := nodes["A"], nodes["C"]

	s := NewClient(sim, "s1")
	s.Write(A, "k", "v1", 500)

	sim.SetLink("A", "C", false, 0) // 常规复制断开
	r := s.Read(C, "k", 500)        // C 没有状态，读挂起

	sim.After(20, func() {
		sim.SetLink("A", "C", true, 5)
		A.SendStateTo(C) // 快照安装
	})
	sim.Run()

	if r.Err != nil || r.Value != "v1" {
		t.Fatalf("read after snapshot: value=%q err=%v", r.Value, r.Err)
	}
}

// TestRegressiveSnapshotNoSilentDegrade 回退式快照安装后，
// 令牌要求超出新前沿的读必须显式超时失败，禁止静默降级为现有旧值。
func TestRegressiveSnapshotNoSilentDegrade(t *testing.T) {
	sim := NewSimulator()
	nodes := newCluster(sim, 10, "A", "B")
	A, B := nodes["A"], nodes["B"]

	s := NewClient(sim, "s1")
	seed := NewClient(sim, "seed")

	sim.SetLink("A", "B", false, 0) // 先断链路：A 的复制永远到不了 B
	s.Write(A, "k", "fresh", 500)   // 令牌 {A:1}
	seed.Write(B, "k", "stale", 500)

	sim.After(1, func() {
		B.ResetSnapshot(B.Snapshot()) // 回退式安装：状态被替换，仍不含 A 的写
	})
	var r *ReadResult
	sim.After(2, func() {
		r = s.Read(B, "k", 50)
	})
	sim.Run()

	if !errors.Is(r.Err, ErrNotSatisfied) {
		t.Fatalf("expected ErrNotSatisfied, got %v", r.Err)
	}
	if r.Found || r.Value == "stale" {
		t.Fatalf("silent degradation after regressive snapshot: %q", r.Value)
	}
}

// TestRandomizedConsistencyChecker 有限消息模拟中的独立检查器：
// 随机会话迁移、写确认与读超时场景下，独立验证读己之写与单调读；
// 有界延迟下所有操作在期限内完成（活性），不承诺全局线性一致。
func TestRandomizedConsistencyChecker(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	sim := NewSimulator()
	ids := []string{"N1", "N2", "N3"}
	nodes := map[string]*Node{}
	for _, id := range ids {
		nodes[id] = NewNode(sim, id)
	}
	for _, a := range ids {
		for _, b := range ids {
			if a != b {
				nodes[a].AddPeer(nodes[b])
				sim.SetLink(a, b, true, 1+rng.Int63n(8)) // 有界随机延迟
			}
		}
	}

	const sessions = 4
	const opsPerSession = 30
	clients := make([]*Client, sessions)
	for i := range clients {
		clients[i] = NewClient(sim, fmt.Sprintf("s%d", i))
	}

	// 每个会话在随机节点上交替写/读自己的键（会话在节点间随机迁移）。
	for i, c := range clients {
		key := fmt.Sprintf("key-s%d", i)
		for op := 0; op < opsPerSession; op++ {
			at := int64(op)*40 + rng.Int63n(10)
			node := nodes[ids[rng.Int63n(int64(len(ids)))]]
			value := fmt.Sprintf("val-s%d-op%d", i, op)
			sim.After(at, func() {
				c.Write(node, key, value, 10000)
				c.Read(nodes[ids[rng.Int63n(int64(len(ids)))]], key, 10000)
			})
		}
	}
	sim.Run()

	// ---- 独立检查器（不依赖客户端令牌实现，只看病历记录）----
	totalReads, totalWrites := 0, 0
	for i, c := range clients {
		// 活性：有界延迟下所有操作必须在期限内完成，无超时。
		for _, w := range c.Writes {
			totalWrites++
			if w.Res.Err != nil {
				t.Fatalf("session s%d write not satisfied: %v", i, w.Res.Err)
			}
		}
		lastReadVersion := Vector{}
		writeByValue := map[string]Vector{}
		for _, w := range c.Writes {
			writeByValue[w.Value] = w.Res.Version
		}
		for _, r := range c.Reads {
			totalReads++
			if r.Res.Err != nil {
				t.Fatalf("session s%d read not satisfied: %v", i, r.Res.Err)
			}
			if !r.Res.Found {
				t.Fatalf("session s%d read missing key %q after writing it", i, r.Key)
			}
			// 读己之写：返回值必须是本会话某次已确认写写入的值，
			// 且其版本覆盖该次写的确认。
			ack, ok := writeByValue[r.Res.Value]
			if !ok {
				t.Fatalf("session s%d read returned foreign value %q", i, r.Res.Value)
			}
			if !r.Res.Version.Equal(ack) {
				t.Fatalf("session s%d read version %s != write ack %s", i, r.Res.Version, ack)
			}
			// 单调读：同一键的连续读版本不得回退。
			if !r.Res.Version.Covers(lastReadVersion) {
				t.Fatalf("session s%d monotonic read violated: %s after %s", i, r.Res.Version, lastReadVersion)
			}
			lastReadVersion = r.Res.Version
		}
	}
	t.Logf("checked %d writes and %d reads across %d migrating sessions: RYW + monotonic reads hold, no timeouts", totalWrites, totalReads, sessions)
}

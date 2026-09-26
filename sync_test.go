package main

import (
	"fmt"
	"math/rand"
	"testing"
)

// ---------- 安全性证据 ----------

// 显式冲突规则：全序、确定性、与到达顺序无关。
func TestExplicitConflictRule(t *testing.T) {
	// 同版本并发写：Origin 大者胜，两个方向合并结果一致
	rA := Record{Key: "k", Value: "fromA", Version: 7, Origin: "A"}
	rB := Record{Key: "k", Value: "fromB", Version: 7, Origin: "B"}
	s1 := NewStore()
	s1.Merge(rA)
	s1.Merge(rB)
	s2 := NewStore()
	s2.Merge(rB)
	s2.Merge(rA)
	if got, _ := s1.Get("k"); got != rB {
		t.Fatalf("同版本冲突应裁定 Origin 大者胜, 得到 %v", got)
	}
	if !s1.Equal(s2) {
		t.Fatal("合并顺序不应影响结果")
	}

	// 同版本同 Origin：墓碑胜活值（删除不会被并发写悄悄抵消）
	tomb := Record{Key: "k", Deleted: true, Version: 7, Origin: "A"}
	live := Record{Key: "k", Value: "x", Version: 7, Origin: "A"}
	s3 := NewStore()
	s3.Merge(live)
	s3.Merge(tomb)
	if got, _ := s3.Get("k"); !got.Deleted {
		t.Fatal("同版本时墓碑应胜活值")
	}

	// 只有更高版本的写才能取代墓碑（显式“复活”必须带新版本）
	newer := Record{Key: "k", Value: "y", Version: 8, Origin: "A"}
	s3.Merge(newer)
	if got, _ := s3.Get("k"); got.Deleted || got.Value != "y" {
		t.Fatal("更高版本的写应取代墓碑")
	}
	stale := Record{Key: "k", Value: "z", Version: 6, Origin: "A"}
	if s3.Merge(stale) {
		t.Fatal("更低版本的写不应生效")
	}
}

// Merge 是交换、结合、幂等的半格 join：任意顺序/重复合并殊途同归。
func TestMergeOrderIndependence(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	var recs []Record
	for i := 0; i < 200; i++ {
		recs = append(recs, Record{
			Key:     fmt.Sprintf("k%02d", rng.Intn(20)),
			Value:   fmt.Sprintf("v%d", i),
			Version: uint64(rng.Intn(50)),
			Origin:  string(rune('A' + rng.Intn(3))),
			Deleted: rng.Intn(4) == 0,
		})
	}
	ref := NewStore()
	for _, r := range recs {
		ref.Merge(r)
	}
	for trial := 0; trial < 5; trial++ {
		perm := rng.Perm(len(recs))
		s := NewStore()
		for _, i := range perm {
			s.Merge(recs[i])
			s.Merge(recs[i]) // 重复合并：幂等
		}
		if !s.Equal(ref) {
			t.Fatalf("第 %d 次乱序合并结果与参考不一致", trial)
		}
	}
}

// 删除必须传播，且迟到的旧值不能复活已删除的键。
func TestTombstonePropagatesAndNoResurrection(t *testing.T) {
	sim := NewSim(Config{MinDelay: 1, MaxDelay: 2}, 1)
	a := NewNode(sim, "A", "B")
	b := NewNode(sim, "B", "A")

	a.Put("k", "v1")
	a.SyncRound()
	sim.RunUntilIdle()
	if r, ok := b.store.Get("k"); !ok || r.Deleted {
		t.Fatal("前置条件：put 应先同步到 B")
	}

	a.Delete("k") // A 上产生 v2 墓碑

	// 故障场景：B 的旧值消息在 A 删除之后才迟到（模拟乱序投递）
	sim.Send(Msg{Kind: MsgRecords, From: "B", To: "A",
		Records: []Record{{Key: "k", Value: "v1", Version: 1, Origin: "A"}}})
	a.SyncRound()
	b.SyncRound()
	sim.RunUntilIdle()

	for _, id := range []string{"A", "B"} {
		r, ok := sim.nodes[id].store.Get("k")
		if !ok || !r.Deleted || r.Version != 2 {
			t.Fatalf("节点 %s 上 k 应为 v2 墓碑, 得到 %v (ok=%v)", id, r, ok)
		}
	}
}

// 相同子树不传全量：300 个相同键 + 1 个差异键，
// 被传输的键必须全部落在差异叶桶内。
func TestIdenticalSubtreeNotTransmitted(t *testing.T) {
	sim := NewSim(Config{MinDelay: 1, MaxDelay: 1}, 2)
	a := NewNode(sim, "A", "B")
	b := NewNode(sim, "B", "A")
	for i := 0; i < 300; i++ {
		rec := Record{Key: fmt.Sprintf("k%04d", i), Value: "v", Version: 1, Origin: "A"}
		a.store.Merge(rec)
		b.store.Merge(rec)
	}
	// 唯一差异：更高版本的写（显式规则：版本大者胜）
	a.store.Merge(Record{Key: "k0000", Value: "updated", Version: 2, Origin: "A"})

	a.SyncRound()
	b.SyncRound()
	sim.RunUntilIdle()

	if !a.store.Equal(b.store) {
		t.Fatal("同步后应收敛")
	}
	diffBucket := keyPath("k0000")
	inBucket := map[string]bool{}
	for i := 0; i < 300; i++ {
		k := fmt.Sprintf("k%04d", i)
		if hasPathPrefix(keyPath(k), diffBucket) {
			inBucket[k] = true
		}
	}
	for k := range sim.Metrics.SentKeys {
		if !inBucket[k] {
			t.Fatalf("键 %s 位于相同子树却被传输", k)
		}
	}
	if len(sim.Metrics.SentKeys) >= 300 {
		t.Fatalf("传输了 %d 个不同的键, 违反“相同子树不传全量”", len(sim.Metrics.SentKeys))
	}
	t.Logf("300 键中仅传输 %d 个（差异叶桶大小）", len(sim.Metrics.SentKeys))
}

// 摘要剪枝：内容相同则根摘要相同；改动一个键则根摘要不同。
func TestDigestPruning(t *testing.T) {
	sim := NewSim(Config{}, 3)
	a := NewNode(sim, "A")
	b := NewNode(sim, "B")
	for i := 0; i < 50; i++ {
		rec := Record{Key: fmt.Sprintf("k%03d", i), Value: "v", Version: 1, Origin: "A"}
		a.store.Merge(rec)
		b.store.Merge(rec)
	}
	if a.digestAt(nil) != b.digestAt(nil) {
		t.Fatal("相同内容的根摘要应相同")
	}
	b.Put("k000", "changed")
	if a.digestAt(nil) == b.digestAt(nil) {
		t.Fatal("内容不同则根摘要应不同")
	}
}

// ---------- 复核路径（摘要碰撞） ----------

// 强制所有摘要相同：朴素协议会被骗过而停滞不前；
// 开启复核轮后，协议下钻到叶并直接比对记录，仍然收敛。
func TestForcedCollisionReviewPath(t *testing.T) {
	cfg := Config{MinDelay: 1, MaxDelay: 1, ForceCollision: true}
	sim := NewSim(cfg, 4)
	a := NewNode(sim, "A", "B")
	b := NewNode(sim, "B", "A")
	a.Put("only-a", "1")
	b.Put("only-b", "2")

	// 无复核：摘要“相同”，协议被碰撞欺骗，分歧保留
	a.SyncRound()
	b.SyncRound()
	sim.RunUntilIdle()
	if _, ok := a.store.Get("only-b"); ok {
		t.Fatal("无复核时不应收敛（碰撞欺骗了协议）")
	}
	if _, ok := b.store.Get("only-a"); ok {
		t.Fatal("无复核时不应收敛（碰撞欺骗了协议）")
	}

	// 开启复核路径：不再信任摘要，全树下钻到叶比对记录
	sim.cfg.VerifyEvery = 1
	a.SyncRound()
	b.SyncRound()
	sim.RunUntilIdle()
	if !a.store.Equal(b.store) {
		t.Fatal("复核路径应在摘要全部碰撞时仍收敛")
	}
	if _, ok := a.store.Get("only-b"); !ok {
		t.Fatal("复核后 A 应获得 only-b")
	}
}

// ---------- 活性证据 ----------

// 丢包/重复/随机延迟下，重复同步最终收敛到参考状态（不动点），
// 且收敛后再同步不再传输任何记录。
func TestLivenessUnderFaults(t *testing.T) {
	cfg := Config{
		MinDelay: 1, MaxDelay: 8,
		DropRate: 0.25, DupRate: 0.15,
		SyncInterval: 15,
	}
	sim := NewSim(cfg, 42)
	nodes := []*Node{
		NewNode(sim, "A", "B"),
		NewNode(sim, "B", "A", "C"),
		NewNode(sim, "C", "B"),
	}

	// 参考存储：合并是半格 join，与顺序无关，
	// 把所有操作记录合并即得“应然”最终状态。
	ref := NewStore()
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 120; i++ {
		n := nodes[rng.Intn(len(nodes))]
		key := fmt.Sprintf("key-%02d", rng.Intn(40))
		var r Record
		if rng.Intn(4) == 0 {
			r = n.Delete(key)
		} else {
			r = n.Put(key, fmt.Sprintf("val-%d", i))
		}
		ref.Merge(r)
	}

	sim.StartTimers()
	sim.Run(4000) // 约 260 轮同步，足以吸收 25% 丢包
	sim.StopTimers()
	sim.RunUntilIdle()

	for _, n := range nodes {
		if !n.store.Equal(ref) {
			t.Fatalf("节点 %s 未收敛到参考状态", n.id)
		}
	}
	t.Logf("t=%d 收敛, 丢包=%d 重复=%d, 传输记录=%d 条",
		sim.Now(), sim.Metrics.Dropped, sim.Metrics.Duplicated, sim.Metrics.RecordsSent)

	// 不动点：收敛后再做完整一轮，记录传输必须为 0，状态必须不变
	snapshot := make([]*Store, len(nodes))
	for i, n := range nodes {
		snapshot[i] = n.store.Clone()
	}
	recordsBefore := sim.Metrics.RecordsSent
	for _, n := range nodes {
		n.SyncRound()
	}
	sim.RunUntilIdle()
	if got := sim.Metrics.RecordsSent - recordsBefore; got != 0 {
		t.Fatalf("不动点违反：收敛后一轮同步仍传输 %d 条记录", got)
	}
	for i, n := range nodes {
		if !n.store.Equal(snapshot[i]) {
			t.Fatalf("节点 %s 状态在不动点轮后发生变化", n.id)
		}
	}
}

// 重复同步达到不动点：无故障下逐轮推进，每轮传输量单调降为 0。
func TestRepeatedSyncReachesFixpoint(t *testing.T) {
	sim := NewSim(Config{MinDelay: 1, MaxDelay: 2, SyncInterval: 10}, 5)
	a := NewNode(sim, "A", "B")
	b := NewNode(sim, "B", "A")
	for i := 0; i < 30; i++ {
		a.Put(fmt.Sprintf("k%02d", i), "x")
	}
	b.Put("k00", "old") // B 先写再删：v2 墓碑 > A 的 v1 活值
	b.Delete("k00")

	var prev int
	for round := 1; round <= 6; round++ {
		before := sim.Metrics.RecordsSent
		a.SyncRound()
		b.SyncRound()
		sim.RunUntilIdle()
		sent := sim.Metrics.RecordsSent - before
		t.Logf("第 %d 轮传输记录 %d 条", round, sent)
		if round > 1 && sent > prev {
			t.Fatalf("传输量应单调不增: 第 %d 轮 %d > 上一轮 %d", round, sent, prev)
		}
		prev = sent
	}
	if prev != 0 {
		t.Fatalf("应达到不动点（最后一轮传输 0 条）, 实际 %d", prev)
	}
	if !a.store.Equal(b.store) {
		t.Fatal("不动点处两节点状态应一致")
	}
	// B 的墓碑版本高于 A 的 put，最终应为墓碑
	if r, _ := a.store.Get("k00"); !r.Deleted {
		t.Fatal("k00 最终应为墓碑（高版本删除获胜）")
	}
}

package sim

import (
	"strings"
	"testing"
)

func testNodes() []Node {
	return []Node{
		{ID: "n1", Domain: "d1"}, {ID: "n2", Domain: "d1"},
		{ID: "n3", Domain: "d2"}, {ID: "n4", Domain: "d2"},
	}
}

func TestPlacementPermutation(t *testing.T) {
	M := 7
	for _, id := range []string{"n1", "n2", "n3", "n4", "zzz"} {
		perm := Perm(id, M)
		if len(perm) != M {
			t.Fatalf("%s 排列长度=%d", id, len(perm))
		}
		seen := map[int]bool{}
		for _, s := range perm {
			if seen[s] {
				t.Fatalf("%s 排列重复槽 %d", id, s)
			}
			seen[s] = true
		}
	}
}

func TestAssignAndReplicaDomains(t *testing.T) {
	M := 7
	primary, err := Assign(testNodes(), M)
	if err != nil {
		t.Fatal(err)
	}
	for s, p := range primary {
		if p == "" {
			t.Fatalf("槽 %d 无主", s)
		}
	}
	replica, err := PickReplica(testNodes(), M, primary)
	if err != nil {
		t.Fatal(err)
	}
	dom := map[string]string{}
	for _, n := range testNodes() {
		dom[n.ID] = n.Domain
	}
	for s := 0; s < M; s++ {
		if replica[s] == primary[s] || dom[replica[s]] == dom[primary[s]] {
			t.Fatalf("槽 %d 备节点 %s 与主 %s 未隔离故障域", s, replica[s], primary[s])
		}
	}
}

func TestReplicaMissingReported(t *testing.T) {
	nodes := []Node{{ID: "a", Domain: "d1"}, {ID: "b", Domain: "d1"}}
	primary, err := Assign(nodes, 3)
	if err != nil {
		t.Fatal(err)
	}
	_, err = PickReplica(nodes, 3, primary)
	if err == nil || !strings.Contains(err.Error(), "无合法备节点") {
		t.Fatalf("缺少合法备节点应显式报告, got %v", err)
	}
}

func runScenario(t *testing.T, name string, M, B int) *Runner {
	t.Helper()
	gen := Scenarios()[name]
	ops, err := gen(M, B)
	if err != nil {
		t.Fatalf("%s 生成失败: %v", name, err)
	}
	r, err := NewRunner(M, B)
	if err != nil {
		t.Fatal(err)
	}
	r.Run(ops)
	return r
}

func mustClean(t *testing.T, r *Runner) {
	t.Helper()
	if vs := r.Check(); len(vs) > 0 {
		t.Fatalf("不变量违规:\n%s", strings.Join(vs, "\n"))
	}
}

// 综合场景：基线期间改同键、乱序补缺、删除重建、跨切换重复、目标崩溃、fence。
func TestScenarioFull(t *testing.T) {
	r := runScenario(t, "full", 7, 1<<20)
	mustClean(t, r)

	var sawRetryDuringFreeze, sawDupAcrossSwitch, sawNewEpochOK bool
	freezeSeen := false
	for i, e := range r.C.Ev {
		if e.Kind == "fence" {
			freezeSeen = true
		}
		if e.Kind == "req" && e.Status == "Retry" && freezeSeen && e.Idem == "w6" {
			sawRetryDuringFreeze = true
		}
		if e.Kind == "req" && e.Status == "Dup" && e.Idem == "w5" {
			sawDupAcrossSwitch = true
			if e.Seq == 0 {
				t.Fatalf("Dup 应返回原序号, ev=%+v", e)
			}
		}
		if e.Kind == "req" && e.Status == "OK" && e.Epoch == 2 && e.Idem == "w7" {
			sawNewEpochOK = true
		}
		_ = i
	}
	if !sawRetryDuringFreeze {
		t.Fatal("冻结期间新写必须返回 Retry 而非确认")
	}
	if !sawDupAcrossSwitch {
		t.Fatal("重复请求跨切换应幂等命中")
	}
	if !sawNewEpochOK {
		t.Fatal("新主应以新 epoch 接受写")
	}
	// 删除后重建 + 基线期间同键修改的最终效果
	dump := r.C.Dump()
	for _, sd := range dump.Slots {
		for k, v := range sd.KV {
			if strings.HasPrefix(k, "alpha") && v != "v4" {
				t.Fatalf("同键多次修改最终值错误: %s=%s", k, v)
			}
			if strings.HasPrefix(k, "beta") && v != "x2" {
				t.Fatalf("删除后重建最终值错误: %s=%s", k, v)
			}
		}
	}
}

// 乱序补缺：目标只能确认连续前缀。
func TestOutOfOrderGapFill(t *testing.T) {
	c, err := NewCluster(7, 1<<20, testNodes())
	if err != nil {
		t.Fatal(err)
	}
	slot := 0
	for s := 0; s < c.M; s++ {
		if c.Nodes[c.Cfg.Primary[s]].Domain == "d1" {
			slot = s
			break
		}
	}
	tgt := targetFor(c, slot)
	k := keyOn(c, slot, "oo")
	for i, idem := range []string{"a", "b", "c", "d"} {
		c.Handle(Request{Op: OpSet, Key: k, Val: string(rune('1' + i)), Idem: idem, CfgVer: 1})
	}
	if err := c.StartMigration(slot, tgt); err != nil {
		t.Fatal(err)
	}
	c.Pump(slot, 10)
	c.Deliver(1, "lifo") // seq4 先到
	ts := c.Nodes[tgt].Disk.Slots[slot]
	if ts.ConfirmedSeq != 0 {
		t.Fatalf("乱序到达不得确认: confirmed=%d", ts.ConfirmedSeq)
	}
	c.Deliver(1, "fifo") // seq1
	if ts.ConfirmedSeq != 1 {
		t.Fatalf("confirmed=%d, 期望 1", ts.ConfirmedSeq)
	}
	c.Deliver(2, "fifo") // seq2,seq3 -> 冲刷暂存的 seq4
	if ts.ConfirmedSeq != 4 {
		t.Fatalf("补缺后 confirmed=%d, 期望 4", ts.ConfirmedSeq)
	}
}

// 容量耗尽：背压出现且不丢记录。
func TestCapacityExhaustion(t *testing.T) {
	r := runScenario(t, "capacity", 7, 100)
	mustClean(t, r)
	backpressure := 0
	for _, e := range r.C.Ev {
		if e.Kind == "backpressure" {
			backpressure++
		}
	}
	if backpressure == 0 {
		t.Fatal("B=100 应触发背压")
	}
}

// 目标崩溃后从已确认进度继续，不重头复制。
func TestTargetCrashResume(t *testing.T) {
	c, err := NewCluster(7, 1<<20, testNodes())
	if err != nil {
		t.Fatal(err)
	}
	slot := 0
	for s := 0; s < c.M; s++ {
		if c.Nodes[c.Cfg.Primary[s]].Domain == "d1" {
			slot = s
			break
		}
	}
	tgt := targetFor(c, slot)
	k := keyOn(c, slot, "tc")
	for i := 0; i < 5; i++ {
		c.Handle(Request{Op: OpSet, Key: k, Val: string(rune('a' + i)), Idem: string(rune('A' + i)), CfgVer: 1})
	}
	if err := c.StartMigration(slot, tgt); err != nil {
		t.Fatal(err)
	}
	c.Pump(slot, 3)
	c.Deliver(3, "fifo")
	if got := c.Nodes[tgt].Disk.Slots[slot].ConfirmedSeq; got != 3 {
		t.Fatalf("confirmed=%d", got)
	}
	c.CrashAll()
	c.Recover()
	mig := c.Migs[slot]
	if mig.SentUpTo != 3 {
		t.Fatalf("恢复后应从已确认进度继续: SentUpTo=%d, 期望 3", mig.SentUpTo)
	}
	c.Pump(slot, 10)
	c.Deliver(10, "fifo")
	if got := c.Nodes[tgt].Disk.Slots[slot].ConfirmedSeq; got != 5 {
		t.Fatalf("续传后 confirmed=%d, 期望 5", got)
	}
}

// 同序号不同内容必须报错并被检查器检出。
func TestConflictDetected(t *testing.T) {
	r := runScenario(t, "conflict", 7, 1<<20)
	sawConflict := false
	for _, e := range r.C.Ev {
		if e.Kind == "conflict" {
			sawConflict = true
		}
	}
	if !sawConflict {
		t.Fatal("应产生冲突事件")
	}
	if vs := r.Check(); len(vs) == 0 {
		t.Fatal("检查器应检出同序号不同内容")
	}
}

// 每个持久化边界都重启，结果仍须满足全部不变量。
func TestRestartEveryBoundary(t *testing.T) {
	r := runScenario(t, "restart-boundary", 7, 1<<20)
	mustClean(t, r)
}

// 多槽独立推进。
func TestMultiSlot(t *testing.T) {
	r := runScenario(t, "multi-slot", 7, 1<<20)
	mustClean(t, r)
	done := 0
	for _, m := range r.C.Migs {
		if m.Phase == "Done" {
			done++
		}
	}
	if done != 2 {
		t.Fatalf("应有 2 个槽完成迁移, got %d", done)
	}
}

// fence 持久化：旧主恢复后仍拒绝写；决议不得回退。
func TestFenceSurvivesRecovery(t *testing.T) {
	r := runScenario(t, "full", 7, 1<<20)
	c := r.C
	for slot, dec := range c.Decisions {
		old := c.Nodes[dec.OldPrimary]
		if !old.Disk.Slots[slot].Fenced {
			t.Fatalf("槽 %d 旧主 fence 未持久化", slot)
		}
		res := old.applyWrite(slot, 1, OpSet, "k", "v", "late-write")
		if res.Status == "OK" {
			t.Fatalf("槽 %d 旧主在决议后接受写（回退）", slot)
		}
	}
}

// 重放确定性：同一脚本两次执行证据摘要一致。
func TestReplayDeterminism(t *testing.T) {
	ops, err := ScenarioFull(7, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	r1, _ := NewRunner(7, 1<<20)
	r1.Run(ops)
	r2, _ := NewRunner(7, 1<<20)
	r2.Run(ops)
	if r1.Digest() != r2.Digest() {
		t.Fatal("同一脚本两次执行证据不一致")
	}
}

// 决议前目标不得接受写；决议后新主才能以新 epoch 接受写。
func TestNoDualPrimary(t *testing.T) {
	c, err := NewCluster(7, 1<<20, testNodes())
	if err != nil {
		t.Fatal(err)
	}
	slot := 0
	for s := 0; s < c.M; s++ {
		if c.Nodes[c.Cfg.Primary[s]].Domain == "d1" {
			slot = s
			break
		}
	}
	tgt := targetFor(c, slot)
	k := keyOn(c, slot, "dp")
	c.Handle(Request{Op: OpSet, Key: k, Val: "1", Idem: "p1", CfgVer: 1})
	if err := c.StartMigration(slot, tgt); err != nil {
		t.Fatal(err)
	}
	// 目标在决议前直接写（模拟路由错误）必须被拒绝
	res := c.Nodes[tgt].applyWrite(slot, 1, OpSet, k, "x", "rogue")
	if res.Status == "OK" {
		t.Fatal("决议前目标不得接受写")
	}
	c.Pump(slot, 10)
	c.Deliver(10, "fifo")
	if err := c.Freeze(slot); err != nil {
		t.Fatal(err)
	}
	c.Pump(slot, 10)
	c.Deliver(10, "fifo")
	if err := c.Decide(slot); err != nil {
		t.Fatal(err)
	}
	res = c.Handle(Request{Op: OpSet, Key: k, Val: "2", Idem: "p2", CfgVer: 0})
	if res.Status != "OK" || res.Epoch != 2 {
		t.Fatalf("决议后新主应以 epoch=2 接受写, got %+v", res)
	}
}

// 前缀不完整时禁止决议。
func TestDecideRequiresFullPrefix(t *testing.T) {
	c, err := NewCluster(7, 1<<20, testNodes())
	if err != nil {
		t.Fatal(err)
	}
	slot := 0
	for s := 0; s < c.M; s++ {
		if c.Nodes[c.Cfg.Primary[s]].Domain == "d1" {
			slot = s
			break
		}
	}
	tgt := targetFor(c, slot)
	k := keyOn(c, slot, "pf")
	c.Handle(Request{Op: OpSet, Key: k, Val: "1", Idem: "q1", CfgVer: 1})
	c.Handle(Request{Op: OpSet, Key: k, Val: "2", Idem: "q2", CfgVer: 1})
	if err := c.StartMigration(slot, tgt); err != nil {
		t.Fatal(err)
	}
	c.Pump(slot, 1) // 只复制 seq1
	c.Deliver(1, "fifo")
	if err := c.Freeze(slot); err != nil {
		t.Fatal(err)
	}
	if err := c.Decide(slot); err == nil {
		t.Fatal("目标未持久化完整前缀时决议必须失败")
	}
	c.Pump(slot, 10)
	c.Deliver(10, "fifo")
	if err := c.Decide(slot); err != nil {
		t.Fatalf("补齐后决议应成功: %v", err)
	}
}

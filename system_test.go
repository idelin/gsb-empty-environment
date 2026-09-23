package main

import (
	"fmt"
	"math/rand"
	"testing"
)

// 恒定延迟 10 的确定性测试环境：读应答 t=20 到齐，修复 t=30 到达、t=40 确认。
func newEnv(maxInflight, maxAttempts int) (*Sim, *Network, []*Replica, *RepairManager, *Coordinator) {
	sim := NewSim()
	net := NewNetwork(sim, 10, 10, 1)
	reps := []*Replica{NewReplica(0), NewReplica(1), NewReplica(2)}
	rm := NewRepairManager(sim, net, reps, maxInflight, maxAttempts, 50)
	c := NewCoordinator(sim, net, reps, rm, 1000)
	return sim, net, reps, rm, c
}

func seedStale(reps []*Replica) {
	for _, r := range reps {
		r.Apply("k", VersionedValue{"v1", 1})
	}
	reps[0].Apply("k", VersionedValue{"v2", 2}) // 副本1、2 落后
}

// ---------- 合并 ----------

// 同键 100 次读只产生必要的修复：每个落后副本恰好 1 条修复 RPC。
func TestHundredReadsMergeRepairs(t *testing.T) {
	sim, net, reps, rm, c := newEnv(4, 3)
	seedStale(reps)

	done := 0
	for i := 0; i < 100; i++ {
		c.Read("k", func(best VersionedValue, found bool) {
			done++
			if !found || best.Val != "v2" {
				t.Errorf("读结果错误: best=%v found=%v", best, found)
			}
		})
	}
	sim.Run()

	if done != 100 {
		t.Fatalf("完成读数 = %d, 期望 100", done)
	}
	if got := net.Sent[MsgRepairReq]; got != 2 {
		t.Fatalf("修复 RPC = %d, 期望 2（100 次读的承诺被按键合并）", got)
	}
	if len(rm.Outcomes) != 2 {
		t.Fatalf("修复结果数 = %d, 期望 2", len(rm.Outcomes))
	}
	for i, r := range reps {
		if v := r.Get("k"); v.Ver != 2 {
			t.Fatalf("副本%d 未收敛: %v", i, v)
		}
	}
}

// ---------- 安全性 ----------

// 修复在途期间出现更新版本：旧修复到达时必须被拒绝，新版本保留。
func TestOldRepairDoesNotOverwriteNewerWrite(t *testing.T) {
	sim, _, reps, _, c := newEnv(4, 3)
	seedStale(reps)

	c.Read("k", nil) // t=20 承诺修复 v2，t=30 到达副本
	sim.After(25, func() {
		reps[1].Apply("k", VersionedValue{"v3", 3}) // 修复在途时的更新写
	})
	sim.Run()

	if v := reps[1].Get("k"); v.Val != "v3" || v.Ver != 3 {
		t.Fatalf("旧修复覆盖了新写! 副本1 = %v, 期望 (v3,ver3)", v)
	}
	if reps[1].RejectedRepairs != 1 {
		t.Fatalf("拒绝旧修复次数 = %d, 期望 1", reps[1].RejectedRepairs)
	}
	if v := reps[2].Get("k"); v.Ver != 2 { // 无新写的副本正常被修复
		t.Fatalf("副本2 = %v, 期望 (v2,ver2)", v)
	}
}

// 取消读不丢已承诺修复：承诺之后取消，修复照样完成。
func TestCancelReadKeepsPromisedRepairs(t *testing.T) {
	sim, _, reps, rm, c := newEnv(4, 3)
	seedStale(reps)

	h := c.Read("k", nil)
	sim.After(25, h.Cancel) // t=20 承诺已产生，t=25 取消
	sim.Run()

	if len(rm.Outcomes) != 2 {
		t.Fatalf("取消后丢失已承诺修复: 结果数 = %d, 期望 2", len(rm.Outcomes))
	}
	for i, r := range reps {
		if v := r.Get("k"); v.Ver != 2 {
			t.Fatalf("副本%d 未收敛: %v", i, v)
		}
	}
}

// 对照：应答到达前取消，则不产生任何修复承诺。
func TestCancelBeforeResponsesPromisesNothing(t *testing.T) {
	sim, net, reps, _, c := newEnv(4, 3)
	seedStale(reps)

	h := c.Read("k", nil)
	sim.After(5, h.Cancel) // 应答 t=20 才到
	sim.Run()

	if got := net.Sent[MsgRepairReq]; got != 0 {
		t.Fatalf("取消后仍承诺修复: RPC = %d, 期望 0", got)
	}
	if v := reps[1].Get("k"); v.Ver != 1 {
		t.Fatalf("副本1 不应被修复: %v", v)
	}
}

// 全局并发上限：任意时刻在途修复不超过配额。
func TestGlobalConcurrencyLimit(t *testing.T) {
	sim := NewSim()
	net := NewNetwork(sim, 10, 10, 1)
	reps := []*Replica{NewReplica(0), NewReplica(1)}
	rm := NewRepairManager(sim, net, reps, 2, 3, 50)

	for k := 0; k < 6; k++ {
		key := fmt.Sprintf("key%d", k)
		reps[0].Apply(key, VersionedValue{"new", 2})
		reps[1].Apply(key, VersionedValue{"old", 1})
		rm.Schedule(key, 1, VersionedValue{"new", 2})
	}
	if rm.Inflight() != 2 {
		t.Fatalf("调度后在途 = %d, 期望 2", rm.Inflight())
	}
	sim.Run()
	if rm.MaxInflightObserved != 2 {
		t.Fatalf("最大在途观测 = %d, 期望 2（不得超过全局上限）", rm.MaxInflightObserved)
	}
	if len(rm.Outcomes) != 6 {
		t.Fatalf("完成修复 = %d, 期望 6", len(rm.Outcomes))
	}
	for k := 0; k < 6; k++ {
		if v := reps[1].Get(fmt.Sprintf("key%d", k)); v.Ver != 2 {
			t.Fatalf("key%d 未收敛: %v", k, v)
		}
	}
}

// ---------- 活性 ----------

// 网络抖动（前 2 个修复请求丢失）：有界重试后收敛。
func TestEventualConvergenceWithFlakyNetwork(t *testing.T) {
	sim, net, reps, rm, c := newEnv(4, 3)
	seedStale(reps)

	dropsLeft := 2
	net.DropHook = func(kind MsgKind, from, to int) bool {
		if kind == MsgRepairReq && dropsLeft > 0 {
			dropsLeft--
			return true
		}
		return false
	}
	c.Read("k", nil)
	sim.Run()

	for _, o := range rm.Outcomes {
		if !o.Success || o.Attempts != 2 {
			t.Fatalf("修复结果异常: %+v, 期望第 2 次尝试成功", o)
		}
	}
	for i, r := range reps {
		if v := r.Get("k"); v.Ver != 2 {
			t.Fatalf("副本%d 未收敛: %v", i, v)
		}
	}
}

// 持续故障：重试有界（恰好 maxAttempts 次），明确失败，模拟器终止。
func TestBoundedRetriesUnderTotalFailure(t *testing.T) {
	sim, net, reps, rm, c := newEnv(4, 3)
	seedStale(reps)

	net.DropHook = func(kind MsgKind, from, to int) bool { return kind == MsgRepairReq }
	c.Read("k", nil)
	sim.Run() // 若重试无界，这里永不返回

	if got := net.Sent[MsgRepairReq]; got != 6 { // 2 个目标 × 3 次尝试
		t.Fatalf("修复 RPC = %d, 期望 6（2 目标 × 3 次上限）", got)
	}
	if len(rm.Outcomes) != 2 {
		t.Fatalf("结果数 = %d, 期望 2", len(rm.Outcomes))
	}
	for _, o := range rm.Outcomes {
		if o.Success || o.Attempts != 3 {
			t.Fatalf("结果异常: %+v, 期望 3 次尝试后失败", o)
		}
	}
	if rm.Inflight() != 0 {
		t.Fatalf("残留在途 = %d, 期望 0", rm.Inflight())
	}
	if sim.Pending() != 0 {
		t.Fatalf("事件队列未清空: %d", sim.Pending())
	}
}

// 随机化压力测试：随机写子集、随机读、随机丢包。
// 安全性不变式：任何副本上任何键的版本永不回退；
// 活性：故障停止后，最终一轮读使全部副本收敛到每键最大版本。
func TestRandomizedSafetyAndConvergence(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	sim := NewSim()
	net := NewNetwork(sim, 1, 5, 99)
	reps := []*Replica{NewReplica(0), NewReplica(1), NewReplica(2)}
	rm := NewRepairManager(sim, net, reps, 3, 5, 30)
	c := NewCoordinator(sim, net, reps, rm, 2000)

	// 安全性不变式：落盘版本必须单调递增。
	maxStored := map[int]map[string]int64{}
	rejectedBefore := 0
	for _, r := range reps {
		rep := r
		rep.OnStore = func(key string, v VersionedValue) {
			m := maxStored[rep.ID]
			if m == nil {
				m = map[string]int64{}
				maxStored[rep.ID] = m
			}
			if prev, ok := m[key]; ok && v.Ver < prev {
				t.Errorf("安全性违反: 副本%d 键 %s 版本回退 %d -> %d", rep.ID, key, prev, v.Ver)
			}
			m[key] = v.Ver
		}
	}

	// t<600 期间 30% 修复请求丢失；之后网络恢复。
	net.DropHook = func(kind MsgKind, from, to int) bool {
		return kind == MsgRepairReq && sim.Now() < 600 && rng.Float64() < 0.3
	}

	// 随机写（子集）与随机读。
	maxVer := map[string]int64{}
	ver := int64(0)
	keys := []string{"a", "b", "c", "d"}
	for i := 0; i < 120; i++ {
		at := int64(rng.Intn(800))
		if rng.Float64() < 0.5 {
			key := keys[rng.Intn(len(keys))]
			var subset []int
			for id := range reps {
				if rng.Float64() < 0.7 {
					subset = append(subset, id)
				}
			}
			if len(subset) == 0 {
				subset = []int{0}
			}
			sim.After(at, func() {
				ver++
				v := VersionedValue{Val: fmt.Sprintf("w%d", ver), Ver: ver}
				for _, id := range subset {
					reps[id].Apply(key, v)
				}
				if ver > maxVer[key] {
					maxVer[key] = ver
				}
			})
		} else {
			key := keys[rng.Intn(len(keys))]
			sim.After(at, func() { c.Read(key, nil) })
		}
	}
	// 故障恢复后的最终一轮读：触发收敛所需的全部修复。
	for _, key := range keys {
		k := key
		sim.After(1000, func() { c.Read(k, nil) })
	}
	sim.Run()

	for _, r := range reps {
		rejectedBefore += r.RejectedRepairs
	}
	if rejectedBefore == 0 {
		t.Fatalf("测试未覆盖到“旧修复遭遇新写”的竞态（拒绝次数为 0）")
	}
	for _, key := range keys {
		for id, r := range reps {
			if got := r.Get(key).Ver; got != maxVer[key] {
				t.Fatalf("活性违反: 副本%d 键 %s 版本 = %d, 期望收敛到 %d", id, key, got, maxVer[key])
			}
		}
	}
}

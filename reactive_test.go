package reactive

import "testing"

// 钻石图：A -> B, A -> C, (B, C) -> D
// 更新 A 后：B、C 各重算一次，D 只重算一次且读到一致的 B、C 最新值。
func TestDiamondUpdatesOnceAndConsistent(t *testing.T) {
	a := NewSignal(1)

	var bRuns, cRuns, dRuns int
	var dSeenB, dSeenC int // D 每次重算时实际读到的 B、C

	b := NewComputed(func() interface{} {
		bRuns++
		return a.Get().(int) + 1
	})
	c := NewComputed(func() interface{} {
		cRuns++
		return a.Get().(int) * 2
	})
	d := NewComputed(func() interface{} {
		dRuns++
		dSeenB = b.Get().(int)
		dSeenC = c.Get().(int)
		return dSeenB + dSeenC
	})

	// 初始各算一次
	if bRuns != 1 || cRuns != 1 || dRuns != 1 {
		t.Fatalf("initial runs: b=%d c=%d d=%d, want 1/1/1", bRuns, cRuns, dRuns)
	}

	a.Set(10)

	// B、C 按需各更新一次；D 只计算 1 次（总计 2 次含初始化）
	if bRuns != 2 || cRuns != 2 {
		t.Fatalf("after Set: bRuns=%d cRuns=%d, want 2/2", bRuns, cRuns)
	}
	if dRuns != 2 {
		t.Fatalf("D recomputed %d times for one A update, want exactly 1 (total 2)", dRuns-1)
	}
	// D 读到的 B、C 必须都是最新版本：B=11, C=20
	if dSeenB != 11 || dSeenC != 20 {
		t.Fatalf("D saw stale/inconsistent values: B=%d C=%d, want 11/20", dSeenB, dSeenC)
	}
	if got := d.Get().(int); got != 31 {
		t.Fatalf("D = %d, want 31", got)
	}
}

// Effect 观测钻石图：每次 A 更新，Effect 只触发一次，
// 且所有观测快照都满足一致性约束（不存在中间瞬态脏值）。
func TestDiamondEffectSeesNoTransient(t *testing.T) {
	a := NewSignal(0)
	b := NewComputed(func() interface{} { return a.Get().(int) + 1 })
	c := NewComputed(func() interface{} { return a.Get().(int) * 2 })
	d := NewComputed(func() interface{} { return b.Get().(int) + c.Get().(int) })

	type snapshot struct{ b, c, sum int }
	var snaps []snapshot
	NewEffect(func() {
		snaps = append(snaps, snapshot{b.Get().(int), c.Get().(int), d.Get().(int)})
	})

	a.Set(3)
	a.Set(7)

	if len(snaps) != 3 { // 初始 + 两次更新，各一次
		t.Fatalf("effect ran %d times, want 3 (no duplicate/glitch runs to be glitch-free)", len(snaps))
	}
	want := []snapshot{{1, 0, 1}, {4, 6, 10}, {8, 14, 22}}
	for i, w := range want {
		if snaps[i] != w {
			t.Fatalf("snapshot %d = %+v, want %+v (inconsistent transient observed)", i, snaps[i], w)
		}
		// 一致性不变式：sum == b + c 且 b、c 同源同版本
		if snaps[i].sum != snaps[i].b+snaps[i].c {
			t.Fatalf("snapshot %d violates invariant: %+v", i, snaps[i])
		}
	}
}

// 更深的钻石：rank 染色保证多层拓扑序依然正确。
func TestDeepDiamond(t *testing.T) {
	a := NewSignal(1)
	b := NewComputed(func() interface{} { return a.Get().(int) + 1 })  // rank 1
	c := NewComputed(func() interface{} { return b.Get().(int) * 10 }) // rank 2
	e := NewComputed(func() interface{} { return a.Get().(int) - 1 })  // rank 1
	runs := 0
	d := NewComputed(func() interface{} { // 依赖 c(rank2) 与 e(rank1) -> rank 3
		runs++
		return c.Get().(int) + e.Get().(int)
	})

	a.Set(5)
	if runs != 2 {
		t.Fatalf("D recomputed %d times total, want 2", runs)
	}
	if got, want := d.Get().(int), 60+4; got != want {
		t.Fatalf("D = %d, want %d", got, want)
	}
}

// 基本行为：未依赖的 Signal 变化不触发重算（细粒度）。
func TestFineGrainedNoSpuriousRecompute(t *testing.T) {
	a := NewSignal(1)
	unrelated := NewSignal(100)
	runs := 0
	b := NewComputed(func() interface{} {
		runs++
		return a.Get().(int) * 2
	})
	unrelated.Set(200)
	if runs != 1 {
		t.Fatalf("B recomputed %d times on unrelated change, want 0 extra (total 1)", runs)
	}
	if b.Get().(int) != 2 {
		t.Fatalf("B = %v, want 2", b.Get())
	}
}

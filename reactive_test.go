package reactive

import "testing"

// 验收边界：钻石图 A->B, A->C, (B,C)->D。
// 更新 A 后：B、C 各更新一次，D 仅计算一次，
// 且 D 读到的 B、C 始终是属于同一版本 A 的一致快照。
func TestDiamondGlitchFree(t *testing.T) {
	a := NewSignal(1)

	var bCalls, cCalls, dCalls int
	type snapshot struct{ b, c int }
	var dSaw []snapshot

	b := NewComputed(func() interface{} {
		bCalls++
		return a.Get().(int) + 1
	})
	c := NewComputed(func() interface{} {
		cCalls++
		return a.Get().(int) * 2
	})
	d := NewComputed(func() interface{} {
		dCalls++
		bv := b.Get().(int)
		cv := c.Get().(int)
		dSaw = append(dSaw, snapshot{bv, cv})
		return bv + cv
	})

	effectRuns := 0
	var effectSaw []int
	NewEffect(func() {
		effectRuns++
		effectSaw = append(effectSaw, d.Get().(int))
	})

	if bCalls != 1 || cCalls != 1 || dCalls != 1 || effectRuns != 1 {
		t.Fatalf("initial: b=%d c=%d d=%d effect=%d, want all 1", bCalls, cCalls, dCalls, effectRuns)
	}

	a.Set(2)

	if bCalls != 2 || cCalls != 2 {
		t.Fatalf("B and C should each update exactly once, got b=%d c=%d", bCalls, cCalls)
	}
	if dCalls != 2 {
		t.Fatalf("D must compute exactly once per A change, got dCalls=%d", dCalls)
	}
	if effectRuns != 2 {
		t.Fatalf("effect must run exactly once per A change, got %d", effectRuns)
	}
	// D 从未观察到中间瞬态：c 必须始终等于 2*(b-1)（同一版本 A 派生）。
	for i, s := range dSaw {
		if s.c != 2*(s.b-1) {
			t.Fatalf("D observed inconsistent snapshot #%d: %+v", i, s)
		}
	}
	if got := d.Get().(int); got != 3+4 {
		t.Fatalf("final D = %d, want 7", got)
	}
	if effectSaw[len(effectSaw)-1] != 7 {
		t.Fatalf("effect saw %v, want final 7", effectSaw)
	}
}

// check 染色：间接依赖重算后值未变化时，下游节点不应重算。
func TestCheckNodeSkippedWhenDepValueUnchanged(t *testing.T) {
	a := NewSignal(1)
	b := NewComputed(func() interface{} { return a.Get().(int) % 2 }) // 奇偶性

	dCalls := 0
	d := NewComputed(func() interface{} {
		dCalls++
		return b.Get().(int) * 10
	})
	NewEffect(func() { d.Get() })

	a.Set(3) // 奇偶性不变：B 重算但版本号不变
	if dCalls != 1 {
		t.Fatalf("D must not recompute when B's value is unchanged, got %d", dCalls)
	}
	a.Set(4) // 奇偶性变化
	if dCalls != 2 {
		t.Fatalf("D must recompute when B's value changes, got %d", dCalls)
	}
}

// 按需更新：没有任何订阅者的 computed 在源变更时不急于重算，Get 时才惰性结算。
func TestUnobservedComputedIsLazy(t *testing.T) {
	a := NewSignal(1)
	calls := 0
	e := NewComputed(func() interface{} {
		calls++
		return a.Get().(int) * 10
	})
	if calls != 1 {
		t.Fatalf("initial compute, got %d", calls)
	}
	a.Set(2)
	if calls != 1 {
		t.Fatalf("unobserved computed must not recompute eagerly, got %d", calls)
	}
	if got := e.Get().(int); got != 20 {
		t.Fatalf("lazy Get = %d, want 20", got)
	}
	if calls != 2 {
		t.Fatalf("lazy recompute on Get, got %d", calls)
	}
}

// 相同值的 Set 是 no-op，不触发任何下游更新。
func TestSetSameValueIsNoop(t *testing.T) {
	a := NewSignal(1)
	runs := 0
	NewEffect(func() { runs++; a.Get() })
	a.Set(1)
	if runs != 1 {
		t.Fatalf("Set with equal value must be a no-op, runs=%d", runs)
	}
	a.Set(2)
	if runs != 2 {
		t.Fatalf("Set with new value must trigger effect, runs=%d", runs)
	}
}

// Dispose 后 Effect 不再被触发。
func TestEffectDispose(t *testing.T) {
	a := NewSignal(0)
	runs := 0
	e := NewEffect(func() { runs++; a.Get() })
	a.Set(1)
	if runs != 2 {
		t.Fatalf("runs=%d, want 2", runs)
	}
	e.Dispose()
	a.Set(2)
	if runs != 2 {
		t.Fatalf("disposed effect must not run, runs=%d", runs)
	}
}

// 多次连续 Set：每次都保证 D 只算一次（计数单调 +1）。
func TestRepeatedUpdates(t *testing.T) {
	a := NewSignal(0)
	b := NewComputed(func() interface{} { return a.Get().(int) + 1 })
	c := NewComputed(func() interface{} { return a.Get().(int) * 2 })
	dCalls := 0
	d := NewComputed(func() interface{} {
		dCalls++
		return b.Get().(int) + c.Get().(int)
	})
	NewEffect(func() { d.Get() })

	for i := 1; i <= 100; i++ {
		a.Set(i)
		if dCalls != i+1 {
			t.Fatalf("after %d sets: dCalls=%d, want %d", i, dCalls, i+1)
		}
		want := (i + 1) + (i * 2)
		if got := d.Get().(int); got != want {
			t.Fatalf("after set %d: D=%d, want %d", i, got, want)
		}
	}
}

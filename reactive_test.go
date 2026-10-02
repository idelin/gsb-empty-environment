package reactive

import "testing"

// 验收边界：Effect 同时监听 Signal X 和 Y；
// batch 内连续修改 X=10, Y=20，期间 Effect 零次触发；
// batch 结束时仅触发一次，且同时读到最新的 X 与 Y。
func TestBatchEffectFiresOnceWithLatestValues(t *testing.T) {
	x := NewSignal(0)
	y := NewSignal(0)

	var runs int
	var gotX, gotY int
	var duringBatchRuns int

	NewEffect(func() {
		runs++
		gotX = x.Get().(int)
		gotY = y.Get().(int)
	})
	if runs != 1 {
		t.Fatalf("创建时应立即执行 1 次, 实际 %d 次", runs)
	}

	Batch(func() {
		x.Set(10)
		y.Set(20)
		duringBatchRuns = runs // batch 尚未退出，记录此刻触发次数
	})

	if duringBatchRuns != 1 {
		t.Fatalf("batch 执行期间 Effect 应零次新增触发, 实际新增 %d 次", duringBatchRuns-1)
	}
	if runs != 2 {
		t.Fatalf("batch 结束后 Effect 应仅触发 1 次, 实际总触发 %d 次", runs)
	}
	if gotX != 10 || gotY != 20 {
		t.Fatalf("Effect 应同时读到最新值 X=10, Y=20, 实际 X=%d, Y=%d", gotX, gotY)
	}
}

func TestSignalSetGet(t *testing.T) {
	s := NewSignal(1)
	if s.Get().(int) != 1 {
		t.Fatal("初始值应为 1")
	}
	s.Set(42)
	if s.Get().(int) != 42 {
		t.Fatal("Set 后应读到 42")
	}
}

func TestEffectImmediateOutsideBatch(t *testing.T) {
	s := NewSignal(0)
	runs := 0
	NewEffect(func() {
		runs++
		s.Get()
	})
	s.Set(1)
	s.Set(2)
	if runs != 3 {
		t.Fatalf("batch 外每次 Set 应立即触发, 期望 3 次, 实际 %d 次", runs)
	}
}

func TestSameValueSetDoesNotNotify(t *testing.T) {
	s := NewSignal(5)
	runs := 0
	NewEffect(func() {
		runs++
		s.Get()
	})
	s.Set(5)
	if runs != 1 {
		t.Fatalf("写入相同值不应触发, 实际触发 %d 次", runs)
	}
}

func TestComputedLazyAndCached(t *testing.T) {
	s := NewSignal(2)
	computations := 0
	c := NewComputed(func() interface{} {
		computations++
		return s.Get().(int) * 10
	})
	if computations != 1 {
		t.Fatalf("创建时应求值 1 次, 实际 %d 次", computations)
	}
	if c.Get().(int) != 20 || c.Get().(int) != 20 {
		t.Fatal("Computed 值应为 20")
	}
	if computations != 1 {
		t.Fatalf("未变化时应命中缓存, 实际计算 %d 次", computations)
	}
	s.Set(3)
	if computations != 1 {
		t.Fatal("Set 后不应立即重算（惰性）")
	}
	if c.Get().(int) != 30 {
		t.Fatal("重算后应为 30")
	}
	if computations != 2 {
		t.Fatalf("应只重算 1 次, 实际 %d 次", computations)
	}
}

// batch 内多次写入，Computed 只重算一次，且 Effect 读到的是最新派生值。
func TestComputedAcrossBatch(t *testing.T) {
	a := NewSignal(1)
	b := NewSignal(2)
	computations := 0
	sum := NewComputed(func() interface{} {
		computations++
		return a.Get().(int) + b.Get().(int)
	})

	var runs int
	var got int
	NewEffect(func() {
		runs++
		got = sum.Get().(int)
	})

	Batch(func() {
		a.Set(10)
		b.Set(20)
		a.Set(30)
	})

	if runs != 2 {
		t.Fatalf("batch 后 Effect 应只触发 1 次, 实际 %d 次", runs-1)
	}
	if got != 50 {
		t.Fatalf("Effect 应读到最新派生值 50, 实际 %d", got)
	}
	if computations != 2 {
		t.Fatalf("batch 内多次写入应只重算 1 次, 实际 %d 次", computations-1)
	}
}

// 链式 Computed：C1 -> C2 -> Effect，batch 后一次性级联。
func TestChainedComputedCascade(t *testing.T) {
	s := NewSignal(1)
	double := NewComputed(func() interface{} { return s.Get().(int) * 2 })
	quad := NewComputed(func() interface{} { return double.Get().(int) * 2 })

	var runs int
	var got int
	NewEffect(func() {
		runs++
		got = quad.Get().(int)
	})

	Batch(func() {
		s.Set(5)
	})
	if runs != 2 || got != 20 {
		t.Fatalf("级联后应触发 1 次且读到 20, 实际触发 %d 次读到 %d", runs-1, got)
	}
}

// 同一 Effect 在 batch 内被多个源触发，也应只执行一次（去重）。
func TestEffectDeduplicatedInBatch(t *testing.T) {
	x := NewSignal(0)
	y := NewSignal(0)
	z := NewSignal(0)
	runs := 0
	NewEffect(func() {
		runs++
		x.Get()
		y.Get()
		z.Get()
	})
	Batch(func() {
		x.Set(1)
		y.Set(2)
		z.Set(3)
	})
	if runs != 2 {
		t.Fatalf("三个源同时变化应只触发 1 次, 实际 %d 次", runs-1)
	}
}

// 嵌套 batch：只有最外层退出时才刷新。
func TestNestedBatch(t *testing.T) {
	s := NewSignal(0)
	runs := 0
	NewEffect(func() {
		runs++
		s.Get()
	})
	Batch(func() {
		s.Set(1)
		Batch(func() {
			s.Set(2)
		})
		if runs != 1 {
			t.Fatalf("内层 batch 退出不应触发, 实际 %d 次", runs-1)
		}
	})
	if runs != 2 {
		t.Fatalf("最外层 batch 退出应触发 1 次, 实际 %d 次", runs-1)
	}
}

// batch 内 panic：深度正确恢复，挂起的更新不触发；后续系统仍可用。
func TestBatchPanicDoesNotFlush(t *testing.T) {
	s := NewSignal(0)
	runs := 0
	NewEffect(func() {
		runs++
		s.Get()
	})
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("应发生 panic")
			}
		}()
		Batch(func() {
			s.Set(99)
			panic("boom")
		})
	}()
	if runs != 1 {
		t.Fatalf("panic 的 batch 不应触发 Effect, 实际新增 %d 次", runs-1)
	}
	// 系统恢复正常：batch 外写入立即触发。
	s.Set(100)
	if runs != 2 {
		t.Fatalf("panic 后系统应恢复正常, 实际触发 %d 次", runs-1)
	}
}

// 动态依赖：Effect 读取的依赖集合随条件变化。
func TestDynamicDependencies(t *testing.T) {
	cond := NewSignal(true)
	a := NewSignal(1)
	b := NewSignal(2)
	runs := 0
	var got int
	NewEffect(func() {
		runs++
		if cond.Get().(bool) {
			got = a.Get().(int)
		} else {
			got = b.Get().(int)
		}
	})
	cond.Set(false)
	if got != 2 {
		t.Fatalf("切换后应读到 b=2, 实际 %d", got)
	}
	before := runs
	a.Set(100) // a 已不再是依赖
	if runs != before {
		t.Fatal("a 不再是依赖，不应触发")
	}
	b.Set(20)
	if got != 20 {
		t.Fatalf("b 变化应触发并读到 20, 实际 %d", got)
	}
}

// 钻石依赖：Effect 同时直接依赖 X、又经由 Computed 间接依赖 X。
// 单次 Set 只能触发一次，且读到一致的最新值（无 glitch）。
func TestDiamondDependencyNoGlitch(t *testing.T) {
	x := NewSignal(1)
	doubled := NewComputed(func() interface{} { return x.Get().(int) * 2 })

	runs := 0
	NewEffect(func() {
		runs++
		direct := x.Get().(int)
		viaComputed := doubled.Get().(int)
		if viaComputed != direct*2 {
			t.Fatalf("出现中间态: x=%d 但 doubled=%d", direct, viaComputed)
		}
	})
	x.Set(2)
	if runs != 2 {
		t.Fatalf("钻石依赖下单次 Set 应只触发 1 次, 实际 %d 次", runs-1)
	}
	Batch(func() {
		x.Set(3)
	})
	if runs != 3 {
		t.Fatalf("batch 内 Set 应只触发 1 次, 实际 %d 次", runs-2)
	}
}

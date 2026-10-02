package signalguard

import (
	"strings"
	"testing"
)

// 验收场景: Effect 内部读取 Signal A 同时写入 Signal A,
// 守护器必须在第 50 次迭代(默认阈值)后拦截, 抛出 CircularSignalUpdate 并指出触发源。
func TestSelfWriteLoopInterceptedAtIterationLimit(t *testing.T) {
	g := NewGraph() // default limit: 50
	a := NewSignal(g, "A", 0)
	runs := 0
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected CircularSignalUpdate panic, got none")
		}
		err, ok := r.(*CircularSignalUpdate)
		if !ok {
			t.Fatalf("expected *CircularSignalUpdate, got %T: %v", r, r)
		}
		if runs != 50 {
			t.Fatalf("effect ran %d times, want exactly 50 (guard blocks the 51st)", runs)
		}
		if err.Iterations != 51 || err.Limit != 50 {
			t.Fatalf("Iterations=%d Limit=%d, want 51/50", err.Iterations, err.Limit)
		}
		if !strings.Contains(err.Source, "Effect[inc]") {
			t.Fatalf("trigger source %q does not name Effect[inc]", err.Source)
		}
		report := err.Error()
		for _, want := range []string{"Effect[inc]", "Signal[A]", "self-loop", "causal chain"} {
			if !strings.Contains(report, want) {
				t.Fatalf("report missing %q:\n%s", want, report)
			}
		}
		t.Logf("guard report:\n%s", report)
	}()
	NewEffect(g, "inc", func() {
		runs++
		v := a.Get()
		a.Set(v + 1) // 恶意逻辑: 写入自身依赖
	})
}

// 间接环路: Effect[ab] 读 A 写 B, Effect[ba] 读 B 写 A, 互相无限触发。
func TestIndirectLoopAcrossEffects(t *testing.T) {
	g := NewGraph(WithMaxIterations(10))
	a := NewSignal(g, "A", 0)
	b := NewSignal(g, "B", 0)
	NewEffect(g, "ab", func() { b.Set(a.Get() + 1) })
	defer func() {
		r := recover()
		err, ok := r.(*CircularSignalUpdate)
		if !ok {
			t.Fatalf("expected *CircularSignalUpdate, got %T: %v", r, r)
		}
		report := err.Error()
		for _, want := range []string{"Effect[ab]", "Effect[ba]", "Signal[A]", "Signal[B]"} {
			if !strings.Contains(report, want) {
				t.Fatalf("report missing %q:\n%s", want, report)
			}
		}
		t.Logf("guard report:\n%s", report)
	}()
	NewEffect(g, "ba", func() { a.Set(b.Get() + 1) })
}

// 调用栈环路: 两个 Computed 同步互相读取, 必须立即被检测。
func TestComputedCallStackCycle(t *testing.T) {
	g := NewGraph()
	var c1, c2 *Computed[int]
	c1 = NewComputed(g, "c1", func() int { return c2.Get() + 1 })
	c2 = NewComputed(g, "c2", func() int { return c1.Get() + 1 })
	defer func() {
		r := recover()
		err, ok := r.(*CircularSignalUpdate)
		if !ok {
			t.Fatalf("expected *CircularSignalUpdate, got %T: %v", r, r)
		}
		if !strings.Contains(err.Reason, "call-stack") {
			t.Fatalf("unexpected reason: %s", err.Reason)
		}
		if !strings.Contains(err.Error(), "Computed[c1]") || !strings.Contains(err.Error(), "Computed[c2]") {
			t.Fatalf("chain missing computed nodes:\n%s", err.Error())
		}
		t.Logf("guard report:\n%s", err.Error())
	}()
	c1.Get()
}

// 健康用法不得误报: Effect 写入自己不依赖的 Signal, 且循环有终止条件。
func TestHealthyUsageNoFalsePositive(t *testing.T) {
	g := NewGraph()
	count := NewSignal(g, "count", 0)
	double := NewComputed(g, "double", func() int { return count.Get() * 2 })
	var seen []int
	NewEffect(g, "collect", func() { seen = append(seen, double.Get()) })
	for i := 1; i <= 100; i++ { // 远超阈值, 但每次 Set 都是独立的 flush 代
		count.Set(i)
	}
	if len(seen) != 101 || seen[100] != 200 {
		t.Fatalf("seen=%v... (len %d)", seen[:3], len(seen))
	}
}

// 有限自增(带终止条件)在阈值内不得被拦截。
func TestBoundedSelfWriteBelowLimit(t *testing.T) {
	g := NewGraph() // limit 50
	a := NewSignal(g, "A", 0)
	runs := 0
	NewEffect(g, "inc", func() {
		runs++
		if v := a.Get(); v < 10 {
			a.Set(v + 1)
		}
	})
	if runs != 11 {
		t.Fatalf("runs=%d, want 11", runs)
	}
	if a.Get() != 10 {
		t.Fatalf("A=%d, want 10", a.Get())
	}
}

// 自定义阈值应被遵守。
func TestCustomIterationLimit(t *testing.T) {
	g := NewGraph(WithMaxIterations(5))
	a := NewSignal(g, "A", 0)
	runs := 0
	defer func() {
		err, ok := recover().(*CircularSignalUpdate)
		if !ok {
			t.Fatal("expected CircularSignalUpdate")
		}
		if runs != 5 || err.Limit != 5 {
			t.Fatalf("runs=%d limit=%d, want 5/5", runs, err.Limit)
		}
	}()
	NewEffect(g, "inc", func() {
		runs++
		a.Set(a.Get() + 1)
	})
}

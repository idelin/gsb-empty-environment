package aimd

import (
	"context"
	"sync"
	"testing"
	"time"
)

func newTestLimiter(clk Clock) *Limiter {
	return NewLimiter(Options{
		InitialLimit: 4,
		MinLimit:     1,
		MaxLimit:     8,
		Additive:     1,
		Backoff:      0.5,
		Debounce:     200 * time.Millisecond,
		Clock:        clk,
	})
}

func okSamples(n int) []Sample {
	s := make([]Sample, n)
	for i := range s {
		s[i] = Sample{RTT: 10 * time.Millisecond}
	}
	return s
}

// 空样本不调整。
func TestEmptySamplesNoAdjust(t *testing.T) {
	l := newTestLimiter(NewManualClock(time.Now()))
	old, cur := l.Adjust(nil)
	if old != 4 || cur != 4 {
		t.Fatalf("empty samples changed limit: %d -> %d", old, cur)
	}
	old, cur = l.Adjust([]Sample{})
	if old != 4 || cur != 4 {
		t.Fatalf("empty slice changed limit: %d -> %d", old, cur)
	}
}

// 上限 4 遇超时乘性减为 2。
func TestTimeoutMultiplicativeDecrease(t *testing.T) {
	l := newTestLimiter(NewManualClock(time.Now()))
	old, cur := l.Adjust([]Sample{{RTT: 900 * time.Millisecond, Timeout: true}})
	if old != 4 || cur != 2 {
		t.Fatalf("want 4 -> 2, got %d -> %d", old, cur)
	}
	// 再次超时：2 * 0.5 = 1，触及下限 1。
	if _, cur = l.Adjust([]Sample{{Timeout: true}}); cur != 1 {
		t.Fatalf("want floor at min=1, got %d", cur)
	}
	if _, cur = l.Adjust([]Sample{{Timeout: true}}); cur != 1 {
		t.Fatalf("want clamp at min=1, got %d", cur)
	}
}

// 正常样本加性增大，封顶于 max。
func TestAdditiveIncreaseCappedAtMax(t *testing.T) {
	l := newTestLimiter(NewManualClock(time.Now()))
	for want := 5; want <= 8; want++ {
		if _, cur := l.Adjust(okSamples(3)); cur != want {
			t.Fatalf("want %d, got %d", want, cur)
		}
	}
	if _, cur := l.Adjust(okSamples(3)); cur != 8 {
		t.Fatalf("want cap at max=8, got %d", cur)
	}
}

// 恢复阶段防抖：减小后 Debounce 窗口内的成功样本不增大，窗口外才恢复。
func TestRecoveryDebounce(t *testing.T) {
	clk := NewManualClock(time.Now())
	l := newTestLimiter(clk)

	l.Adjust(okSamples(1)) // 4 -> 5
	l.Adjust(okSamples(1)) // 5 -> 6
	if _, cur := l.Adjust([]Sample{{Timeout: true}}); cur != 3 {
		t.Fatalf("want 6 -> 3, got %d", cur)
	}

	clk.Advance(50 * time.Millisecond) // 仍在防抖窗口内
	if _, cur := l.Adjust(okSamples(1)); cur != 3 {
		t.Fatalf("debounced increase should be suppressed, got %d", cur)
	}

	clk.Advance(200 * time.Millisecond) // 越过防抖窗口
	if _, cur := l.Adjust(okSamples(1)); cur != 4 {
		t.Fatalf("want increase after debounce, got %d", cur)
	}
}

// 手动时钟下的确定性变化序列。
func TestDeterministicSequence(t *testing.T) {
	clk := NewManualClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	l := newTestLimiter(clk)

	type step struct {
		advance time.Duration
		samples []Sample
		want    int
	}
	steps := []step{
		{0, okSamples(2), 5},                      // 加性增大
		{0, okSamples(2), 6},                      // 继续增大
		{0, nil, 6},                               // 空样本不调整
		{0, []Sample{{Timeout: true}}, 3},         // 乘性减小
		{50 * time.Millisecond, okSamples(1), 3},  // 防抖抑制
		{200 * time.Millisecond, okSamples(1), 4}, // 恢复增大
		{0, []Sample{{Timeout: true}}, 2},         // 再次减小
		{0, nil, 2},                               // 空样本不调整
		{time.Second, okSamples(1), 3},            // 恢复
	}
	for i, s := range steps {
		clk.Advance(s.advance)
		if _, cur := l.Adjust(s.samples); cur != s.want {
			t.Fatalf("step %d: want limit %d, got %d", i, s.want, cur)
		}
	}
}

// 上限 4 减为 2 时，已运行的 4 个任务可正常结束，但不补位：
// 只有 running 降回 2 以下才允许新任务进入。
func TestDecreaseDoesNotKillOrBackfill(t *testing.T) {
	l := newTestLimiter(NewManualClock(time.Now()))
	ctx := context.Background()

	releases := make([]func(), 4)
	for i := range releases {
		r, err := l.Acquire(ctx)
		if err != nil {
			t.Fatalf("acquire %d: %v", i, err)
		}
		releases[i] = r
	}

	l.Adjust([]Sample{{Timeout: true}}) // 4 -> 2，running 仍为 4

	releases[0]() // running: 4 -> 3，仍 >= 2，不允许补位
	assertBlocked(t, l, "running=3 >= limit=2 must block")

	releases[1]() // running: 3 -> 2，仍不补位
	assertBlocked(t, l, "running=2 >= limit=2 must block")

	releases[2]() // running: 2 -> 1 < 2，可以进入
	if _, err := l.Acquire(ctx); err != nil {
		t.Fatalf("expected acquire to succeed once running < limit: %v", err)
	}
	releases[3]()
}

func assertBlocked(t *testing.T, l *Limiter, msg string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := l.Acquire(ctx); err != context.DeadlineExceeded {
		t.Fatalf("%s: got err=%v", msg, err)
	}
}

// 并发压力：任务函数、耗时与失败均由测试注入；用 -race 运行。
func TestConcurrentStress(t *testing.T) {
	l := NewLimiter(Options{InitialLimit: 4, MaxLimit: 8, Clock: realClock{}})
	ctx := context.Background()

	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				err := l.Run(ctx, func(context.Context) error {
					time.Sleep(time.Millisecond) // 注入的任务耗时
					return nil
				})
				if err != nil {
					t.Errorf("run: %v", err)
					return
				}
				// 注入样本：每 10 个任务制造一次超时失败。
				if (g*25+i)%10 == 9 {
					l.Adjust([]Sample{{Timeout: true}})
				} else {
					l.Adjust(okSamples(1))
				}
			}
		}(g)
	}
	wg.Wait()
	if got := l.Running(); got != 0 {
		t.Fatalf("leaked running tasks: %d", got)
	}
	if got := l.Limit(); got < 1 || got > 8 {
		t.Fatalf("limit out of bounds: %d", got)
	}
}

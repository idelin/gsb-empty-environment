package ringlog

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"
)

// ---------- 无界历史截取模型 ----------
//
// 模型:维护一个无限增长的历史 history,环形日志的行为必须等价于
// 「只保留最后 capacity 条」的截取视图:
//   - abs >= len(history)            -> ErrEmpty
//   - abs <  len(history)-capacity   -> Lagged{From: c, To: oldest}
//   - 其余                            -> 返回 history[abs],游标前进一格

func runModel(t *testing.T, rng *rand.Rand, trials int) {
	t.Helper()
	for trial := 0; trial < trials; trial++ {
		capN := 1 + rng.Intn(8)
		l := New(capN)
		capU := uint64(capN)
		var history []Record

		steps := 30 + rng.Intn(60)
		for s := 0; s < steps; s++ {
			// 随机批量追加,批量经常超过容量以强制绕回与跨尾
			n := 1 + rng.Intn(3*capN+2)
			batch := make([]Record, n)
			for i := range batch {
				batch[i] = Record(fmt.Sprintf("rec-%d", len(history)+i))
			}
			l.Append(batch...)
			history = append(history, batch...)

			head := uint64(len(history))
			oldest := uint64(0)
			if head > capU {
				oldest = head - capU
			}

			// 探测点:边界(最旧-1/最旧/最旧+1/最新-1/最新/未来)与随机点
			probes := []uint64{0, head, head + 1}
			if oldest > 0 {
				probes = append(probes, oldest-1)
			}
			probes = append(probes, oldest)
			if oldest+1 < head {
				probes = append(probes, oldest+1)
			}
			if head > 0 {
				probes = append(probes, head-1)
			}
			probes = append(probes, uint64(rng.Intn(int(head+2))))

			for _, abs := range probes {
				c := Cursor{Gen: abs / capU, Seq: abs % capU}
				rec, next, err := l.TryNext(c)
				switch {
				case abs >= head:
					if !errors.Is(err, ErrEmpty) {
						t.Fatalf("abs=%d head=%d: 期望 ErrEmpty,得到 rec=%q err=%v", abs, head, rec, err)
					}
				case abs < oldest:
					var lag Lagged
					if !errors.As(err, &lag) {
						t.Fatalf("abs=%d oldest=%d: 期望 Lagged,得到 err=%v", abs, oldest, err)
					}
					if lag.From != c {
						t.Fatalf("abs=%d: Lagged.From=%v,期望 %v", abs, lag.From, c)
					}
					wantTo := Cursor{Gen: oldest / capU, Seq: oldest % capU}
					if lag.To != wantTo {
						t.Fatalf("abs=%d: Lagged.To=%v,期望 %v", abs, lag.To, wantTo)
					}
					// 恢复游标必须立即可读
					if _, _, err := l.TryNext(lag.To); err != nil {
						t.Fatalf("abs=%d: 从 To=%v 恢复读取失败: %v", abs, lag.To, err)
					}
				default:
					if err != nil {
						t.Fatalf("abs=%d: 意外错误 %v", abs, err)
					}
					if string(rec) != string(history[abs]) {
						t.Fatalf("abs=%d: 记录不符 rec=%q want=%q(覆盖了槽被当成旧记录?)", abs, rec, history[abs])
					}
					wantNext := Cursor{Gen: (abs + 1) / capU, Seq: (abs + 1) % capU}
					if next != wantNext {
						t.Fatalf("abs=%d: next=%v,期望 %v", abs, next, wantNext)
					}
				}
			}
		}
	}
}

func TestModelAgainstUnboundedHistory(t *testing.T) {
	runModel(t, rand.New(rand.NewSource(42)), 150)
}

func FuzzModel(f *testing.F) {
	f.Add(int64(1))
	f.Add(int64(99))
	f.Fuzz(func(t *testing.T, seed int64) {
		runModel(t, rand.New(rand.NewSource(seed)), 8)
	})
}

// ---------- 绕回与批次跨尾 ----------

func TestWrapAroundAndBatchCrossingTail(t *testing.T) {
	l := New(4)

	// 第一批:占满 0..2,head 停在槽 3
	l.Append(Record("a"), Record("b"), Record("c"))
	// 第二批 4 条:seq 3,4,5,6 -> 槽 3,0,1,2,批次跨越数组末尾
	l.Append(Record("d"), Record("e"), Record("f"), Record("g"))

	// 逐条追读,验证内容与游标代际推进
	want := []struct {
		c   Cursor
		rec string
		n   Cursor
	}{
		{Cursor{0, 3}, "d", Cursor{1, 0}}, // 跨代际:Gen 0 -> 1
		{Cursor{1, 0}, "e", Cursor{1, 1}},
		{Cursor{1, 1}, "f", Cursor{1, 2}},
		{Cursor{1, 2}, "g", Cursor{1, 3}},
	}
	for _, w := range want {
		rec, next, err := l.TryNext(w.c)
		if err != nil || string(rec) != w.rec || next != w.n {
			t.Fatalf("cursor=%v: got rec=%q next=%v err=%v; want %q %v", w.c, rec, next, err, w.rec, w.n)
		}
	}

	// 第三批 4 条:seq 7..10 -> 槽 3,0,1,2,再次跨尾,覆盖整个上一圈
	l.Append(Record("h"), Record("i"), Record("j"), Record("k"))

	// 被覆盖的旧记录必须报告准确缺失区间,而不是返回旧内容
	checkLagged := func(c Cursor, wantTo Cursor) {
		t.Helper()
		rec, resume, err := l.TryNext(c)
		var lag Lagged
		if !errors.As(err, &lag) {
			t.Fatalf("cursor=%v: 期望 Lagged,得到 rec=%q err=%v", c, rec, err)
		}
		if lag.From != c || lag.To != wantTo || resume != wantTo {
			t.Fatalf("cursor=%v: Lagged=%+v resume=%v; want From=%v To=%v", c, lag, resume, c, wantTo)
		}
	}
	// head=11, oldest=7 -> 游标 (1,1)=abs5 丢失区间 [5,7)
	checkLagged(Cursor{1, 1}, Cursor{1, 3})
	// 游标 (0,0)=abs0 丢失区间 [0,7)
	checkLagged(Cursor{0, 0}, Cursor{1, 3})

	// 绕回后的新记录可读且内容正确
	rec, next, err := l.TryNext(Cursor{1, 3})
	if err != nil || string(rec) != "h" || next != (Cursor{2, 0}) {
		t.Fatalf("got rec=%q next=%v err=%v; want h {2 0}", rec, next, err)
	}
	rec, _, err = l.TryNext(Cursor{2, 2})
	if err != nil || string(rec) != "k" {
		t.Fatalf("got rec=%q err=%v; want k", rec, err)
	}
}

// ---------- 整批原子发布 ----------

func TestBatchPublishIsAtomic(t *testing.T) {
	const capN = 8
	l := New(capN)
	stop := make(chan struct{})
	var wg sync.WaitGroup

	// 读者不变式:任何 abs < head 的游标,TryNext 绝不返回 ErrEmpty
	// (要么读到记录,要么 Lagged)——不存在「半批可见」。
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				head := l.Latest().Absolute(capN)
				if head == 0 {
					continue
				}
				abs := head - 1 // 最新一条必须可见
				c := Cursor{Gen: abs / capN, Seq: abs % capN}
				if _, _, err := l.TryNext(c); errors.Is(err, ErrEmpty) {
					t.Errorf("abs=%d < head=%d 却返回 ErrEmpty(半批发布?)", abs, head)
					return
				}
			}
		}()
	}

	for i := 0; i < 2000; i++ {
		batch := make([]Record, capN)
		for j := range batch {
			batch[j] = Record(fmt.Sprintf("b%d-%d", i, j))
		}
		l.Append(batch...)
	}
	close(stop)
	wg.Wait()
}

// ---------- 并发读取(对照无界历史) ----------

func TestConcurrentReaders(t *testing.T) {
	const capN = 16
	const batches = 400
	const readers = 6

	// 预生成全部记录,避免共享可变状态
	var flat []Record
	var allBatches [][]Record
	rng := rand.New(rand.NewSource(7))
	for len(flat) < batches*capN {
		n := 1 + rng.Intn(2*capN)
		b := make([]Record, n)
		for i := range b {
			b[i] = Record(fmt.Sprintf("rec-%d", len(flat)+i))
		}
		allBatches = append(allBatches, b)
		flat = append(flat, b...)
	}
	total := uint64(len(flat))

	l := New(capN)
	var wg sync.WaitGroup
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			c := Cursor{}
			var read, skipped uint64
			for c.Absolute(capN) < total {
				rec, next, err := l.Next(ctx, c)
				var lag Lagged
				switch {
				case err == nil:
					abs := c.Absolute(capN)
					if string(rec) != string(flat[abs]) {
						t.Errorf("reader %d: abs=%d 记录不符 rec=%q want=%q", id, abs, rec, flat[abs])
						return
					}
					read++
					c = next
				case errors.As(err, &lag):
					if lag.From != c {
						t.Errorf("reader %d: Lagged.From=%v != 游标 %v", id, lag.From, c)
						return
					}
					skipped += lag.To.Absolute(capN) - lag.From.Absolute(capN)
					c = lag.To
				default:
					t.Errorf("reader %d: 意外错误 %v", id, err)
					return
				}
			}
			// 读到的 + 确认丢失的必须恰好覆盖全部历史
			if read+skipped != total {
				t.Errorf("reader %d: read=%d skipped=%d 合计 %d != total %d", id, read, skipped, read+skipped, total)
			}
		}(r)
	}

	for _, b := range allBatches {
		l.Append(b...)
	}
	wg.Wait()
}

// ---------- 取消不消费游标 ----------

func TestCancelDoesNotConsumeCursor(t *testing.T) {
	l := New(4)
	ctx, cancel := context.WithCancel(context.Background())

	type result struct {
		next Cursor
		err  error
	}
	done := make(chan result, 1)
	go func() {
		_, next, err := l.Next(ctx, Cursor{})
		done <- result{next, err}
	}()

	// 等读者确实阻塞后再取消
	time.Sleep(20 * time.Millisecond)
	cancel()
	res := <-done
	if !errors.Is(res.err, context.Canceled) {
		t.Fatalf("期望 context.Canceled,得到 %v", res.err)
	}
	if res.next != (Cursor{}) {
		t.Fatalf("取消消费了游标:next=%v", res.next)
	}

	// 同一游标仍可读到之后追加的记录
	l.Append(Record("x"))
	rec, next, err := l.Next(context.Background(), Cursor{})
	if err != nil || string(rec) != "x" || next != (Cursor{0, 1}) {
		t.Fatalf("取消后重读失败:rec=%q next=%v err=%v", rec, next, err)
	}
}

func TestNextAlreadyCancelled(t *testing.T) {
	l := New(2)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, next, err := l.Next(ctx, Cursor{1, 1}); !errors.Is(err, context.Canceled) || next != (Cursor{1, 1}) {
		t.Fatalf("已取消的 ctx:next=%v err=%v", next, err)
	}
}

// ---------- 其他边界 ----------

func TestInvalidCursor(t *testing.T) {
	l := New(4)
	if _, _, err := l.TryNext(Cursor{0, 4}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("期望 ErrInvalidCursor,得到 %v", err)
	}
}

func TestBatchLargerThanCapacity(t *testing.T) {
	l := New(3)
	l.Append(Record("0"), Record("1"), Record("2"), Record("3"), Record("4"))
	if got := l.Oldest(); got != (Cursor{0, 2}) {
		t.Fatalf("Oldest=%v,期望 {0 2}", got)
	}
	if got := l.Latest(); got != (Cursor{1, 2}) {
		t.Fatalf("Latest=%v,期望 {1 2}", got)
	}
	// 只有最后 3 条可读
	for i, want := range []string{"2", "3", "4"} {
		rec, _, err := l.TryNext(Cursor{0, 2}.advance(uint64(i), 3))
		if err != nil || string(rec) != want {
			t.Fatalf("i=%d: rec=%q err=%v,期望 %q", i, rec, err, want)
		}
	}
}

func (c Cursor) advance(n, cap uint64) Cursor {
	abs := c.Absolute(cap) + n
	return Cursor{Gen: abs / cap, Seq: abs % cap}
}

func TestAppendCopiesInput(t *testing.T) {
	l := New(2)
	buf := Record("ab")
	l.Append(buf)
	buf[0] = 'X' // 追加后修改调用方缓冲区不应影响日志
	rec, _, err := l.TryNext(Cursor{})
	if err != nil || string(rec) != "ab" {
		t.Fatalf("rec=%q err=%v,期望 ab", rec, err)
	}
}

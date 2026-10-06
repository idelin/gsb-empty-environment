// Package ringlog 实现固定容量的循环日志(ring buffer)。
//
// 特性:
//   - 游标由「代际 + 序号」组成,可精确定位绕回后的记录;
//   - 追读落后超过容量时,返回准确的缺失区间 [From, To);
//   - 被覆盖的槽位绝不会被当作旧记录返回;
//   - 整批追加原子发布:读者要么看到整批,要么完全看不到;
//   - 取消读取不消费游标,同一游标可继续重试。
//
// Log 可安全地被多个 goroutine 并发使用(单写者串行化,多读者无锁)。
package ringlog

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
)

// Record 是一条日志记录。Append 会拷贝内容,返回给读者的也是拷贝,
// 因此双方都可以在返回后安全地复用自己的缓冲区。
type Record []byte

// Cursor 日志游标:Gen 为代际,Seq 为代内序号(即槽位下标)。
// 绝对序号 = Gen*Capacity + Seq;槽位下标 = Seq。
type Cursor struct {
	Gen uint64
	Seq uint64
}

// Absolute 返回游标对应的绝对序号。
func (c Cursor) Absolute(capacity uint64) uint64 { return c.Gen*capacity + c.Seq }

func (c Cursor) String() string { return fmt.Sprintf("%d:%d", c.Gen, c.Seq) }

// ErrEmpty 表示游标处暂时没有已发布的记录(非阻塞读取时返回)。
var ErrEmpty = errors.New("ringlog: 游标处暂无记录")

// ErrInvalidCursor 表示游标非法(序号超出容量)。
var ErrInvalidCursor = errors.New("ringlog: 非法游标")

// Lagged 表示追读落后超过容量,游标处的记录已被覆盖。
// [From, To) 区间内的记录已永久丢失,读者应从 To 处恢复读取。
type Lagged struct {
	From Cursor // 丢失区间的起点(即读者当时的游标)
	To   Cursor // 丢失区间的终点(不含),即当前最旧的可读记录
}

func (e Lagged) Error() string {
	return fmt.Sprintf("ringlog: 读取落后,区间 [%s, %s) 的记录已被覆盖", e.From, e.To)
}

// 槽位状态编码:0 = 空;偶数 2*(abs+1) = 绝对序号 abs 的记录已稳定发布;
// 奇数 2*(abs+1)+1 = 正在写入(覆盖)该槽。状态值单调递增。
func stableState(abs uint64) uint64  { return 2 * (abs + 1) }
func writingState(abs uint64) uint64 { return 2*(abs+1) + 1 }

type slot struct {
	state atomic.Uint64
	rec   atomic.Pointer[Record]
}

// Log 固定容量的循环日志。
type Log struct {
	cap   uint64
	slots []slot
	head  atomic.Uint64 // 下一条要写的绝对序号;[0, head) 已发布

	mu  sync.Mutex // 串行化追加
	sig atomic.Pointer[chan struct{}]
}

// New 创建容量为 capacity 的循环日志。capacity 必须 >= 1。
func New(capacity int) *Log {
	if capacity <= 0 {
		panic("ringlog: 容量必须为正数")
	}
	ch := make(chan struct{})
	l := &Log{cap: uint64(capacity), slots: make([]slot, capacity)}
	l.sig.Store(&ch)
	return l
}

// Capacity 返回日志容量。
func (l *Log) Capacity() int { return int(l.cap) }

func (l *Log) cursorOf(abs uint64) Cursor {
	return Cursor{Gen: abs / l.cap, Seq: abs % l.cap}
}

// Latest 返回最新游标(即已发布区间的终点,下一条记录的位置)。
func (l *Log) Latest() Cursor { return l.cursorOf(l.head.Load()) }

// Oldest 返回当前最旧的可读游标。落后于此游标的记录已被覆盖。
func (l *Log) Oldest() Cursor {
	h := l.head.Load()
	if h > l.cap {
		return l.cursorOf(h - l.cap)
	}
	return Cursor{}
}

// Append 原子地追加一批记录:整批对读者同时可见,绝不出现半批。
// 返回追加完成后的最新游标。批量超过容量时,只保留最后 capacity 条。
// 记录内容会被拷贝,调用方可在返回后复用缓冲区。
func (l *Log) Append(recs ...Record) Cursor {
	if len(recs) == 0 {
		return l.Latest()
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	base := l.head.Load()
	for i := range recs {
		abs := base + uint64(i)
		sl := &l.slots[abs%l.cap]
		sl.state.Store(writingState(abs)) // 标记写入中,使并发读者放弃旧代际
		buf := make(Record, len(recs[i]))
		copy(buf, recs[i])
		sl.rec.Store(&buf)
		sl.state.Store(stableState(abs)) // 发布该槽
	}
	head := base + uint64(len(recs))
	l.head.Store(head) // 整批原子发布:head 前移后整批立即可见

	// 唤醒所有等待中的读者
	newCh := make(chan struct{})
	close(*l.sig.Swap(&newCh))
	return l.cursorOf(head)
}

// TryNext 非阻塞地读取游标 c 处的记录。
//
// 成功时返回记录拷贝与下一条游标;游标处暂无记录时返回 ErrEmpty;
// 记录已被覆盖时返回 Lagged(含准确缺失区间),并附带可恢复读取的游标。
// 无论成功失败,返回的游标都可直接用于下一次调用。
func (l *Log) TryNext(c Cursor) (Record, Cursor, error) {
	if c.Seq >= l.cap {
		return nil, c, ErrInvalidCursor
	}
	abs := c.Absolute(l.cap)
	sl := &l.slots[abs%l.cap]
	want := stableState(abs)

	for {
		head := l.head.Load()
		if abs >= head {
			return nil, c, ErrEmpty
		}
		// abs < head 时,槽位状态必然 >= want(发布顺序保证),
		// 因此 v < want 的情况不存在。
		v := sl.state.Load()
		switch {
		case v == want:
			p := sl.rec.Load()
			if p != nil && sl.state.Load() == want {
				// 两次状态检查之间未发生覆盖,记录可信
				rec := make(Record, len(*p))
				copy(rec, *p)
				return rec, l.cursorOf(abs + 1), nil
			}
			// 与覆盖者竞争失败,重试以得到确定结论
		case v > want && v%2 == 0:
			// 槽位已稳定持有更新代际:abs 已被覆盖,计算缺失区间
			oldest := uint64(0)
			if h := l.head.Load(); h > l.cap {
				oldest = h - l.cap
			}
			if oldest <= abs {
				// 覆盖刚发生、head 尚未推进到下一圈的极端竞争:
				// abs 确实已丢失,报告最小区间以保证准确且可前进
				oldest = abs + 1
			}
			to := l.cursorOf(oldest)
			return nil, to, Lagged{From: c, To: to}
		default:
			// 奇数状态:覆盖写入进行中,等其落定后再判定
			runtime.Gosched()
		}
	}
}

// Next 阻塞读取游标 c 处的记录,语义与 TryNext 相同。
// 记录尚未发布时挂起等待;ctx 取消时返回 ctx.Err(),
// 且游标不被消费——同一游标可继续用于后续读取。
func (l *Log) Next(ctx context.Context, c Cursor) (Record, Cursor, error) {
	if err := ctx.Err(); err != nil {
		return nil, c, err
	}
	abs := c.Absolute(l.cap)
	for {
		rec, next, err := l.TryNext(c)
		if !errors.Is(err, ErrEmpty) {
			return rec, next, err
		}
		ch := *l.sig.Load()
		// 再查一次 head,避免错过在取 ch 之前完成的发布
		if l.head.Load() > abs {
			continue
		}
		select {
		case <-ctx.Done():
			return nil, c, ctx.Err()
		case <-ch:
		}
	}
}

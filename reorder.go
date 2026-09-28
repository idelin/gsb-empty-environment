// Package reorder 实现离散事件模型中的乱序包重排器。
//
// 时间单位为整数 tick，不进行任何真实网络 I/O。接收器只在 Handle 与
// Tick 两个离散事件点推进状态。释放顺序由 (事件时间 Tick, 序号 Seq)
// 决定：同一 tick 内按序号释放；缓存中缺失的包不会永久阻塞后续包，
// 超过等待上限 MaxWaitTicks 或缓存达到上限 CacheCap 时，缺失序号以
// Gap 事件形式跳过。迟到、陈旧（旧 epoch）与重复包进入旁路通道，
// 已释放的序列不会被修改。Reset 切换 epoch 并清空旧队列，旧 epoch
// 残留包之后到达时一律视为陈旧包，不会串入新流。
package reorder

import (
	"container/heap"
	"errors"
	"sync"
)

// GapReason 描述缺失序号被跳过的原因。
type GapReason int

const (
	// GapWaitTimeout：缺失序号等待时间达到 MaxWaitTicks 仍未到达。
	GapWaitTimeout GapReason = iota
	// GapCacheFull：缓存达到 CacheCap，必须跳过缺失序号才能接纳新包。
	GapCacheFull
)

func (r GapReason) String() string {
	switch r {
	case GapWaitTimeout:
		return "wait_timeout"
	case GapCacheFull:
		return "cache_full"
	default:
		return "unknown"
	}
}

// SideKind 描述旁路条目的类别。
type SideKind int

const (
	// SideLate：迟到包，其序号在之前的释放点已被缺口越过，不补回。
	SideLate SideKind = iota
	// SideStale：陈旧包，属于已被 Reset 结束的旧 epoch。
	SideStale
	// SideDuplicate：重复包，同一 (epoch, seq) 已经到达过一次。
	SideDuplicate
)

func (k SideKind) String() string {
	switch k {
	case SideLate:
		return "late"
	case SideStale:
		return "stale"
	case SideDuplicate:
		return "duplicate"
	default:
		return "unknown"
	}
}

// Packet 是链路上传递的不可变数据包。
type Packet struct {
	Epoch int64 // 纪元，Reset 后变化；接收器只接受当前 epoch
	Seq   int64 // epoch 内从 1 开始的连续序号
	Tick  int64 // 发送方事件时间（整数 tick，随序号单调非减）
	Body  string
}

// Item 是有序输出通道中的一个条目：包释放与缺口跳过严格按序交错。
type Item struct {
	// Pkg 非 nil 表示正常释放的包；此时 Gap 为 nil。
	Pkg *Packet
	// Gap 非 nil 表示被跳过、不会再释放的缺失序号区间；Pkg 为 nil。
	Gap *Gap
}

// Gap 表示一段被跳过的缺失序号。
type Gap struct {
	From   int64     // 缺失起始序号（含）
	To     int64     // 缺失结束序号（含）
	Tick   int64     // 做出跳过决定时的接收器 tick
	Reason GapReason // 跳过原因
}

// SideItem 是旁路通道中的一个条目。
type SideItem struct {
	Kind SideKind
	Pkg  Packet
	At   int64 // 进入旁路时的接收器 tick
}

// Config 是接收器配置。两个上限必须显式给出且为正。
type Config struct {
	// MaxWaitTicks：缺失序号的等待上限。设缺口首次被观察到的 tick 为
	// t0（即“缺口头部”形成的时刻），则在 now - t0 >= MaxWaitTicks
	// 的事件点跳过该缺口。一次超时只处理自 t0 起等待的连续缺口段，
	// 之后新形成的缺口从当前 tick 重新起算。
	MaxWaitTicks int64
	// CacheCap：乱序缓存中最多保留的包数。缓存已满且又有乱序包到达
	// 时，跳过最旧的缺失序号段并级联释放，为新包腾出位置。
	CacheCap int
}

// Stats 是接收器自创建以来的累计计数。
type Stats struct {
	Released   int64 // 正常释放的包数
	Gaps       int64 // 跳过的缺口段数
	MissingSeq int64 // 跳过的缺失序号总数
	Late       int64 // 迟到包数
	Stale      int64 // 陈旧包数
	Duplicate  int64 // 重复包数
}

// ErrInvalidConfig 在等待上限或缓存上限非正时返回。
var ErrInvalidConfig = errors.New("reorder: MaxWaitTicks and CacheCap must be positive")

// channelCap 是输出通道的固定缓冲大小。
const channelCap = 4096

// Receiver 是乱序包接收器。
type Receiver struct {
	mu sync.Mutex

	cfg       Config
	epoch     int64
	now       int64 // 接收器当前 tick（单调非减）
	nextSeq   int64 // 当前 epoch 中下一个待释放序号
	firstWait int64 // 缺口头部首次出现的 tick；无缺口时为 -1

	buf     map[int64]Packet   // 乱序缓存：序号 -> 包
	order   seqHeap            // buf 中序号的最小堆
	arrived map[int64]struct{} // 本 epoch 已实际到达过的序号
	skipped map[int64]struct{} // 本 epoch 已被缺口越过的序号

	outCh  chan Item
	sideCh chan SideItem
	closed bool

	stats Stats
}

// New 创建接收器。epoch 是初始纪元编号；接收器从序号 1 开始等待。
func New(cfg Config, epoch int64) (*Receiver, error) {
	if cfg.MaxWaitTicks <= 0 || cfg.CacheCap <= 0 {
		return nil, ErrInvalidConfig
	}
	return &Receiver{
		cfg:       cfg,
		epoch:     epoch,
		now:       0,
		nextSeq:   1,
		firstWait: -1,
		buf:       make(map[int64]Packet),
		order:     make(seqHeap, 0),
		arrived:   make(map[int64]struct{}),
		skipped:   make(map[int64]struct{}),
		outCh:     make(chan Item, channelCap),
		sideCh:    make(chan SideItem, channelCap),
	}, nil
}

// Out 返回有序输出通道。通道按 (事件时间, 序号) 投递 Item；Close 后
// 通道关闭，已在缓冲区中的条目仍可被读尽。
func (r *Receiver) Out() <-chan Item { return r.outCh }

// Side 返回旁路通道，投递迟到、陈旧、重复包。
func (r *Receiver) Side() <-chan SideItem { return r.sideCh }

// Epoch 返回当前纪元编号。
func (r *Receiver) Epoch() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.epoch
}

// Stats 返回累计计数的快照。
func (r *Receiver) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stats
}

// Handle 处理在 tick at 到达的包。返回错误仅表示非法调用参数；
// 所有“不被有序流接纳”的包都通过旁路通道给出而不报错。
func (r *Receiver) Handle(p Packet, at int64) error {
	if at < 0 {
		return errors.New("reorder: tick must be non-negative")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("reorder: receiver closed")
	}
	if p.Seq < 1 {
		return errors.New("reorder: packet seq must be >= 1")
	}
	r.advanceLocked(at)

	// 旧 epoch 的残留包：进旁路，绝不串入新流。
	if p.Epoch != r.epoch {
		r.stats.Stale++
		r.sideCh <- SideItem{Kind: SideStale, Pkg: p, At: r.now}
		return nil
	}

	// 序号已被缺口越过：迟到包，不能修改已释放序列。
	if _, late := r.skipped[p.Seq]; late {
		r.stats.Late++
		r.sideCh <- SideItem{Kind: SideLate, Pkg: p, At: r.now}
		return nil
	}
	// 序号已到达过（缓存中或已释放）：重复包。
	if _, dup := r.arrived[p.Seq]; dup {
		r.stats.Duplicate++
		r.sideCh <- SideItem{Kind: SideDuplicate, Pkg: p, At: r.now}
		return nil
	}

	switch {
	case p.Seq == r.nextSeq:
		// 连续包：直接释放，不占用缓存名额，再级联释放缓存。
		r.deliverNewLocked(p)
		r.flushLocked()

	case p.Seq > r.nextSeq:
		// 乱序包。缓存已满时跳过最旧缺口腾位；跳过的上界不超过
		// p.Seq-1，保证不会把正在到达的包自身越过。
		for len(r.buf) >= r.cfg.CacheCap && r.nextSeq < p.Seq {
			top := r.order[0]
			end := top - 1
			if p.Seq-1 < end {
				end = p.Seq - 1
			}
			if end < r.nextSeq {
				// 没有可跳过的缺口（理论上不可达）：让出循环保护。
				break
			}
			r.declareGapLocked(r.nextSeq, end, GapCacheFull)
			r.flushLocked()
		}
		if r.nextSeq == p.Seq {
			// 腾位后该包恰好补到头部。
			r.deliverNewLocked(p)
			r.flushLocked()
		} else {
			r.arrived[p.Seq] = struct{}{}
			r.buf[p.Seq] = p
			heap.Push(&r.order, p.Seq)
			if r.firstWait < 0 {
				r.firstWait = r.now
			}
		}

	default: // p.Seq < r.nextSeq 且不在 skipped/arrived 中：防御性分支
		r.stats.Late++
		r.sideCh <- SideItem{Kind: SideLate, Pkg: p, At: r.now}
	}

	r.processTimeoutsLocked()
	return nil
}

// Tick 在无包到达的 tick 推进接收器时钟，并处理等待超时。
// 时钟只允许单调前进。
func (r *Receiver) Tick(at int64) error {
	if at < 0 {
		return errors.New("reorder: tick must be non-negative")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("reorder: receiver closed")
	}
	if at < r.now {
		return errors.New("reorder: tick moved backwards")
	}
	r.now = at
	r.processTimeoutsLocked()
	return nil
}

// processTimeoutsLocked 按 MaxWaitTicks 跳过超时缺口。
// 前置不变量：firstWait >= 0 时必有 len(buf) > 0 且 order[0] > nextSeq，
// 即缺口头部之后确实缓存着等待释放的包，因此空流不会凭空产生缺口。
func (r *Receiver) processTimeoutsLocked() {
	for r.firstWait >= 0 && r.now-r.firstWait >= r.cfg.MaxWaitTicks {
		// [nextSeq, top-1] 自 firstWait 起持续缺失，整段一次跳过，
		// 随后级联释放；新缺口的等待起点由 flushLocked 重新记为 now。
		top := r.order[0]
		r.declareGapLocked(r.nextSeq, top-1, GapWaitTimeout)
		r.flushLocked()
	}
}

// deliverNewLocked 投递一个首次到达且恰为 nextSeq 的包，并推进游标。
func (r *Receiver) deliverNewLocked(p Packet) {
	r.arrived[p.Seq] = struct{}{}
	r.nextSeq = p.Seq + 1
	r.stats.Released++
	pc := p
	r.outCh <- Item{Pkg: &pc}
}

// flushLocked 从 nextSeq 起级联释放缓存中的连续包，并刷新等待状态。
func (r *Receiver) flushLocked() {
	for {
		p, ok := r.buf[r.nextSeq]
		if !ok {
			break
		}
		delete(r.buf, r.nextSeq)
		heap.Remove(&r.order, 0) // 堆顶即 nextSeq（键序与游标一致）
		r.nextSeq++
		r.stats.Released++
		pc := p
		r.outCh <- Item{Pkg: &pc}
	}
	r.refreshWaitLocked()
}

// refreshWaitLocked 重新计算缺口等待起点。
func (r *Receiver) refreshWaitLocked() {
	if len(r.buf) == 0 || r.order[0] == r.nextSeq {
		r.firstWait = -1
		return
	}
	// 缺口头部之后仍有缓存包。保留旧起点（缺口持续中）；若缺口是
	// 刚形成的，则从当前 tick 起算。
	if r.firstWait < 0 {
		r.firstWait = r.now
	}
}

// declareGapLocked 跳过 [from, to] 的缺失序号并投递一个 Gap 事件。
func (r *Receiver) declareGapLocked(from, to int64, reason GapReason) {
	if to < from {
		return
	}
	for seq := from; seq <= to; seq++ {
		r.skipped[seq] = struct{}{}
	}
	r.stats.MissingSeq += to - from + 1
	r.stats.Gaps++
	r.outCh <- Item{Gap: &Gap{From: from, To: to, Tick: r.now, Reason: reason}}
	r.nextSeq = to + 1
}

// advanceLocked 将接收器时钟推进到 at；时间不倒流（由调用方保证）。
func (r *Receiver) advanceLocked(at int64) {
	if at > r.now {
		r.now = at
	}
}

// Reset 切换到新 epoch：清空缓存、缺口集合与等待状态。已释放序列
// 不回溯、不修改；重置后到达的旧 epoch 包全部进入陈旧旁路，不串流。
func (r *Receiver) Reset(newEpoch int64, at int64) error {
	if at < 0 {
		return errors.New("reorder: tick must be non-negative")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return errors.New("reorder: receiver closed")
	}
	if newEpoch == r.epoch {
		return errors.New("reorder: new epoch must differ from current epoch")
	}
	r.advanceLocked(at)
	r.epoch = newEpoch
	r.nextSeq = 1
	r.firstWait = -1
	r.buf = make(map[int64]Packet)
	r.order = make(seqHeap, 0)
	r.arrived = make(map[int64]struct{})
	r.skipped = make(map[int64]struct{})
	return nil
}

// Close 关闭接收器及其输出通道。关闭不产生任何输出；已在通道缓冲
// 区中的条目仍可被消费者读尽。
func (r *Receiver) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	close(r.outCh)
	close(r.sideCh)
}

// seqHeap 是序号的最小堆。
type seqHeap []int64

func (h seqHeap) Len() int           { return len(h) }
func (h seqHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h seqHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *seqHeap) Push(x interface{}) { *h = append(*h, x.(int64)) }

func (h *seqHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

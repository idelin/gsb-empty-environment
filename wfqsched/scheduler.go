// Package wfqsched 实现由并行流体服务模型（GPS，通用处理器共享）驱动的
// 逐包加权公平队列（WFQ/PGPS）调度器，供串行发包的交换机模拟器调用。
//
// 模型与约定：
//   - 链路在每个整数时刻传送恰好 1 字节，一个包占用半开整数区间 [Start, End)，
//     区间长度等于包长，真实发包器不可抢占。
//   - 每个流有正整数权重；流体参照系统只在“有积压”的流之间按权重比例分配带宽。
//   - 包的虚拟完成标签 F = max(本流上一标签, 到达时刻的系统虚拟时间 V) + 包长/权重。
//   - 流体系统非空时，虚拟时间以 dV = dt / 活跃流总权重 的速率前进；流体系统
//     空闲时虚拟时间冻结。
//   - 真实发包器空闲时，在所有等待包中取标签最小者，标签相同按全局到达序号排序。
//
// 所有时间、长度、权重均为 64 位整数；标签与虚拟时间内部以精确有理数
// (math/big.Rat) 保存和比较，不使用浮点，累计使用任意精度整数，杜绝舍入
// 与溢出改变次序。时间推进按“流体完成事件 / 真实完成事件”逐个结算，
// 事件总数与包数同阶，绝不按字节循环。
package wfqsched

import (
	"container/heap"
	"errors"
	"math"
	"math/big"
)

// 调度器返回的参数校验与状态错误。
var (
	ErrDuplicateFlow       = errors.New("wfqsched: duplicate flow id")
	ErrInvalidWeight       = errors.New("wfqsched: weight must be a positive integer")
	ErrUnknownFlow         = errors.New("wfqsched: unknown flow id")
	ErrInvalidLength       = errors.New("wfqsched: packet length must be positive")
	ErrInvalidTime         = errors.New("wfqsched: timestamp must be non-negative and not earlier than current time")
	ErrSequenceExhausted   = errors.New("wfqsched: global arrival sequence counter exhausted")
	ErrIntervalBeyondInt64 = errors.New("wfqsched: scheduled transmission interval exceeds int64 range")
	ErrInconsistentState   = errors.New("wfqsched: internal scheduling inconsistency")
)

// FlowID 标识一条流。
type FlowID uint64

// Packet 描述一个到达的包。ID 由调用方指定并在 Interval 中原样回传，可为 0。
type Packet struct {
	Flow   FlowID
	ID     uint64
	Length uint64 // 包长（字节），必须为正
}

// Interval 是一个包在真实链路上的发包区间 [Start, End)，单位为字节时刻。
type Interval struct {
	Flow     FlowID
	PacketID uint64
	Start    int64
	End      int64
}

// packet 是带调度状态的内部包表示。tag 创建后不再被修改。
type packet struct {
	flow     FlowID
	id       uint64
	length   uint64
	seq      uint64 // 全局到达序号，同标签时的决胜依据
	tag      *big.Rat
	inFlight bool // 是否已被真实发包器取走（但可能尚未完成流体服务）
}

// flow 保存单流状态。queue 为尚未在真实链路上开始发送的包，按到达序排列；
// queue[0] 同时是该流在流体系统中的队首包。
type flow struct {
	weight  *big.Int
	lastTag *big.Rat
	queue   []*packet
	active  bool // 流体系统中是否有积压
}

// fluidEvent 是某个活跃流体队首包的虚拟完成事件，按标签排序。
type fluidEvent struct {
	tag  *big.Rat // 包的绝对虚拟完成标签
	flow FlowID
	pkt  *packet
	seq  uint64
}

// fluidHeap 按事件的“真实完成时刻”排序。GPS 下真实时刻由当前虚拟时间 V、
// 真实时刻 now 与活跃总权重 W 即时换算：dt = (tag - V) * W。活跃集变化会
// 改变换算速率，使绝对标签的次序偏离真实时刻次序，因此不能只按标签建堆；
// 每次推进循环用最新上下文重建堆序。
type fluidHeap struct {
	events []*fluidEvent
	vtime  *big.Rat
	now    *big.Rat
	weight *big.Int
}

func (h *fluidHeap) realTime(e *fluidEvent) *big.Rat {
	dV := new(big.Rat).Sub(e.tag, h.vtime)
	return new(big.Rat).Add(h.now,
		new(big.Rat).Mul(dV, new(big.Rat).SetInt(h.weight)))
}

func (h *fluidHeap) Len() int { return len(h.events) }

func (h *fluidHeap) Less(i, j int) bool {
	a, b := h.events[i], h.events[j]
	if c := h.realTime(a).Cmp(h.realTime(b)); c != 0 {
		return c < 0
	}
	if a.flow != b.flow {
		return a.flow < b.flow
	}
	return a.seq < b.seq
}

func (h *fluidHeap) Swap(i, j int) { h.events[i], h.events[j] = h.events[j], h.events[i] }

func (h *fluidHeap) Push(x any) { h.events = append(h.events, x.(*fluidEvent)) }

func (h *fluidHeap) Pop() any {
	n := len(h.events)
	item := h.events[n-1]
	h.events[n-1] = nil
	h.events = h.events[:n-1]
	return item
}

// waitEvent 是真实发包器的等待队列条目，按 (标签, 全局到达序号) 排序。
type waitEvent struct {
	tag *big.Rat
	seq uint64
	pkt *packet
}

type waitHeap []*waitEvent

func (h *waitHeap) Len() int { return len(*h) }

func (h *waitHeap) Less(i, j int) bool {
	if c := (*h)[i].tag.Cmp((*h)[j].tag); c != 0 {
		return c < 0
	}
	return (*h)[i].seq < (*h)[j].seq
}

func (h *waitHeap) Swap(i, j int) { (*h)[i], (*h)[j] = (*h)[j], (*h)[i] }

func (h *waitHeap) Push(x any) { *h = append(*h, x.(*waitEvent)) }

func (h *waitHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return item
}

// Scheduler 是线程不安全的单链路 WFQ 调度器，方法按模拟器时刻串行调用。
type Scheduler struct {
	flows        map[FlowID]*flow
	clock        int64    // 已结算到的整数时刻
	vtime        *big.Rat // 系统虚拟时间 V(clock)
	activeWeight *big.Int // 流体系统中活跃流的权重总和（任意精度，防止累加溢出）
	fluid        fluidHeap
	waiting      waitHeap // 等待真实发送的包
	inFlight     *packet  // 正在真实链路上发送的包，nil 表示发包器空闲
	nextFree     int64    // 下一个可开始发送的整数时刻（在途包的 End）
	seq          uint64   // 下一个全局到达序号
	intervals    []Interval

	// eventSteps 统计 advance 结算的事件数，仅用于测试验证不存在逐字节循环。
	eventSteps int
}

// New 创建空调度器，虚拟时间初始为 0。
func New() *Scheduler {
	vtime := new(big.Rat)
	return &Scheduler{
		flows:        make(map[FlowID]*flow),
		vtime:        vtime,
		activeWeight: new(big.Int),
		fluid: fluidHeap{
			vtime:  vtime,
			now:    new(big.Rat),
			weight: new(big.Int),
		},
	}
}

// AddFlow 注册一条权重为正整数的流，流号不得重复。
func (s *Scheduler) AddFlow(id FlowID, weight uint64) error {
	if weight == 0 {
		return ErrInvalidWeight
	}
	if _, exists := s.flows[id]; exists {
		return ErrDuplicateFlow
	}
	s.flows[id] = &flow{weight: new(big.Int).SetUint64(weight)}
	return nil
}

// Now 返回调度器已结算到的整数时刻。
func (s *Scheduler) Now() int64 { return s.clock }

// VirtualTime 返回当前系统虚拟时间的精确有理数值（副本，可自由修改）。
func (s *Scheduler) VirtualTime() *big.Rat {
	return new(big.Rat).Set(s.vtime)
}

// Backlogged 报告某流在流体参照系统中当前是否有积压。注意：某流的真实
// 等待队列可能已经空掉（包已被真实链路取走），但只要流体系统尚未把该包
// 服务完，该流在流体意义上仍然 backlogged，继续参与带宽分配。
func (s *Scheduler) Backlogged(id FlowID) (bool, error) {
	f, ok := s.flows[id]
	if !ok {
		return false, ErrUnknownFlow
	}
	return f.active, nil
}

// LastTag 返回某流最近一个到达包的虚拟完成标签（副本）；流尚无包时返回 nil。
func (s *Scheduler) LastTag(id FlowID) (*big.Rat, error) {
	f, ok := s.flows[id]
	if !ok {
		return nil, ErrUnknownFlow
	}
	if f.lastTag == nil {
		return nil, nil
	}
	return new(big.Rat).Set(f.lastTag), nil
}

// Waiting 返回当前等待真实发送（尚未开始）的包数。
func (s *Scheduler) Waiting() int { return s.waiting.Len() }

// eventStepCount 返回 advance 自创建以来结算的离散事件总数，供测试断言
// 时间推进的代价与包数而非字节数相关。
func (s *Scheduler) EventSteps() int { return s.eventSteps }

// Advance 把调度器（含流体系统与真实发包器）空转到整数时刻 t，期间没有到达。
func (s *Scheduler) Advance(t int64) error {
	if t < 0 || t < s.clock {
		return ErrInvalidTime
	}
	return s.advance(new(big.Rat).SetInt64(t))
}

// Arrive 在整数时刻 t 接收一整批同时到达的包。处理顺序严格为：
//  1. 先结算到 t 为止（含 t）的一切流体事件与真实发送完成；
//  2. 再按 batch 顺序纳入整批到达（同批内的次序通过全局到达序号体现）；
//  3. 最后若发包器空闲，决定此刻开始发送的包并返回它，否则返回 nil。
//
// t 不得为负、不得早于当前时刻；包必须属于已注册流且长度为正。
func (s *Scheduler) Arrive(t int64, batch []Packet) (*Packet, error) {
	if t < 0 || t < s.clock {
		return nil, ErrInvalidTime
	}
	if err := s.advance(new(big.Rat).SetInt64(t)); err != nil {
		return nil, err
	}
	if uint64(len(batch)) > math.MaxUint64-s.seq {
		return nil, ErrSequenceExhausted
	}
	for _, p := range batch {
		f, ok := s.flows[p.Flow]
		if !ok {
			return nil, ErrUnknownFlow
		}
		if p.Length == 0 {
			return nil, ErrInvalidLength
		}
		s.seq++

		// F = max(上一标签, V(t)) + 包长 / 权重，全程精确有理运算。
		base := s.vtime
		if f.lastTag != nil && f.lastTag.Cmp(base) > 0 {
			base = f.lastTag
		}
		tag := new(big.Rat).Add(base, new(big.Rat).SetFrac(
			new(big.Int).SetUint64(p.Length), f.weight))
		f.lastTag = tag

		pkt := &packet{
			flow:   p.Flow,
			id:     p.ID,
			length: p.Length,
			seq:    s.seq,
			tag:    tag,
		}
		f.queue = append(f.queue, pkt)
		heap.Push(&s.waiting, &waitEvent{tag: tag, seq: s.seq, pkt: pkt})

		// 流在流体系统中从无积压变为有积压：计入活跃总权重，
		// 该权重会在下一次推进时成为流体事件时刻换算的分母。
		if !f.active {
			f.active = true
			s.activeWeight.Add(s.activeWeight, f.weight)
		}

		// 每个包都登记一个流体完成事件；流积压期间到达的包事件会先沉在堆中，
		// 等队首包被流体服务完后才生效（completeFluid 惰性丢弃陈旧条目）。
		heap.Push(&s.fluid, &fluidEvent{tag: tag, flow: p.Flow, pkt: pkt, seq: s.seq})
	}

	var started *Packet
	if s.inFlight == nil && s.waiting.Len() > 0 {
		if err := s.startNext(new(big.Rat).SetInt64(t)); err != nil {
			return nil, err
		}
		if s.inFlight != nil {
			p := s.inFlight
			started = &Packet{Flow: p.flow, ID: p.id, Length: p.length}
		}
	}
	return started, nil
}

// Drain 先结算到时刻 t，再返回此刻仍未发完的包的完整发包区间（含正在发送
// 中的包）。区间按开始时刻排序，语义为后续不再有任何到达时的确定调度结果。
func (s *Scheduler) Drain(t int64) ([]Interval, error) {
	if t < 0 || t < s.clock {
		return nil, ErrInvalidTime
	}
	if err := s.advance(new(big.Rat).SetInt64(t)); err != nil {
		return nil, err
	}
	out := make([]Interval, 0, len(s.intervals))
	for _, iv := range s.intervals {
		if iv.End > t {
			out = append(out, iv)
		}
	}
	return out, nil
}

// advance 把流体系统与真实发包器从 clock 结算到整数目标时刻 target。
// 它只循环处理离散事件（流体完成、真实完成），循环次数与包数同阶，
// 与包长（字节数）无关，因此不存在逐字节循环。
func (s *Scheduler) advance(target *big.Rat) error {
	now := new(big.Rat).SetInt64(s.clock)

	for {
		haveFluid := s.fluid.Len() > 0
		haveReal := s.inFlight != nil
		if !haveFluid && !haveReal {
			break
		}

		// 用最新的 (V, now, W) 作为流体堆的换算上下文并重建堆序，保证堆顶
		// 始终是真实完成时刻最早的事件（活跃集变化后标签序不再可靠）。
		var fluidAt *big.Rat
		if haveFluid {
			s.fluid.vtime = s.vtime
			s.fluid.now = now
			s.fluid.weight = s.activeWeight
			heap.Init(&s.fluid)
			fluidAt = s.fluid.realTime(s.fluid.events[0])
		}
		var realAt *big.Rat
		if haveReal {
			realAt = new(big.Rat).SetInt64(s.nextFree)
		}

		// 同一时刻先结算流体完成、再结算真实完成。两套状态相互独立：流体
		// 出队只动流体积压队列，真实完成只清在途包；真实续发在循环下一轮
		// （或本次 Arrive 的最后一步）统一决策，避免同刻交叉造成状态不一致。
		fluidFirst := haveFluid && (!haveReal || fluidAt.Cmp(realAt) <= 0)
		if fluidFirst {
			if fluidAt.Cmp(target) > 0 {
				break
			}
			s.eventSteps++
			s.advanceVTo(now, fluidAt)
			if err := s.completeFluid(fluidAt); err != nil {
				return err
			}
			now = fluidAt
		} else {
			if realAt.Cmp(target) > 0 {
				break
			}
			s.eventSteps++
			s.advanceVTo(now, realAt)
			now = realAt
			s.completeReal()
			// 真实完成后发包器空闲：若有等待包立即在同一整数时刻续发，
			// 后续事件（含同刻流体事件）由循环下一轮继续结算。
			if err := s.startNext(realAt); err != nil {
				return err
			}
		}
	}

	// 推进到目标时刻：非空流体系统按 dV = dt / 总权重 前进，空闲则冻结。
	s.advanceVTo(now, target)
	// 目标时刻上若发包器空闲且仍有等待包，立即续发（Drain 的排空语义）。
	if s.inFlight == nil && s.waiting.Len() > 0 {
		if err := s.startNext(target); err != nil {
			return err
		}
	}
	if !target.IsInt() {
		return ErrInconsistentState
	}
	s.clock = target.Num().Int64()
	return nil
}

// advanceVTo 把系统虚拟时间从对应真实时刻 from 的状态推进到真实时刻 to。
// 流体系统非空时 dV = (to-from) / 活跃总权重；为空时 V 冻结。
func (s *Scheduler) advanceVTo(from, to *big.Rat) {
	dt := new(big.Rat).Sub(to, from)
	if s.fluid.Len() == 0 || dt.Sign() <= 0 {
		return
	}
	s.vtime.Add(s.vtime,
		new(big.Rat).Quo(dt, new(big.Rat).SetInt(s.activeWeight)))
}

// completeFluid 结算一个流体队首完成事件：推进虚拟时间，让对应流在流体
// 系统中出队一个包；若该流还有积压则登记下一个流体事件，否则停止参与分带宽。
func (s *Scheduler) completeFluid(at *big.Rat) error {
	for {
		ev := heap.Pop(&s.fluid).(*fluidEvent)
		f, ok := s.flows[ev.flow]
		if !ok {
			return ErrInconsistentState
		}
		// 惰性丢弃陈旧事件：流已无流体积压，或该事件不属于当前流体队首。
		// 这在“流体先于真实发送完成”等交错顺序下是正常现象。
		if !f.active || len(f.queue) == 0 || f.queue[0] != ev.pkt {
			continue
		}

		// 流体系统服务完队首包。它可能早已被真实链路取走（真实等待队列已空），
		// 也可能仍在等待真实发送——两种情况下都只从流体队列移除。
		// V 的推进由 advance 按真实经过时间统一处理，此处不修改 V。
		f.queue = f.queue[1:]
		if len(f.queue) == 0 {
			// 流体积压清零：该流不再获得流体带宽，直到下一次到达重新激活。
			f.active = false
			s.activeWeight.Sub(s.activeWeight, f.weight)
		}
		return nil
	}
}

// completeReal 结算一个真实发送完成事件：发包器变空闲。
func (s *Scheduler) completeReal() {
	s.inFlight = nil
}

// startNext 在真实发包器空闲时取等待包中标签最小者（同标签按全局序号）
// 开始不可抢占的发送，并记录整数发包区间。at 必须是整数时刻。
func (s *Scheduler) startNext(at *big.Rat) error {
	if s.inFlight != nil || s.waiting.Len() == 0 {
		return nil
	}
	if !at.IsInt() {
		return ErrInconsistentState
	}
	start := at.Num().Int64()

	ev := heap.Pop(&s.waiting).(*waitEvent)
	pkt := ev.pkt

	length := int64(pkt.length)
	if length < 0 || start > math.MaxInt64-length {
		return ErrIntervalBeyondInt64
	}
	end := start + length

	if _, ok := s.flows[pkt.flow]; !ok {
		return ErrInconsistentState
	}
	// 真实取走不等于流体服务完成：若流体尚未服务完，包仍留在流的流体队列
	// 中直到其流体事件触发 completeFluid——这正是“真实队列空掉的流仍参与
	// 流体分带宽”的根源。包也可能已先被流体服务完，两种顺序都合法。
	pkt.inFlight = true

	s.inFlight = pkt
	s.nextFree = end
	s.intervals = append(s.intervals, Interval{
		Flow:     pkt.flow,
		PacketID: pkt.id,
		Start:    start,
		End:      end,
	})
	return nil
}

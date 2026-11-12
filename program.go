package netsec

import (
	"errors"
	"fmt"
)

// MaxThreads 与 MaxEventsPerThread 给出本枚举器支持的程序规模上限。
const (
	MaxThreads         = 3
	MaxEventsPerThread = 4
	// MaxReferenceEvents 是穷举全序参考实现（全排列）支持的最大事件数。
	MaxReferenceEvents = 9
)

// Pair 是两个事件的有序对，First 与 Second 为事件在 Program.Events 中的下标。
type Pair struct {
	First  int
	Second int
}

// Program 是一个已构建完成的待枚举程序。
//
// Events 的前段是各位置的初始写（Thread == -1），其后按线程与线程内顺序
// 排列普通事件。初始写固定排在任何真实事件之前，并作为未显式给读自边的
// 读的默认来源。
type Program struct {
	// Events 包含初始写与全部线程事件。
	Events []Event
	// Initial 给出每个位置的初始写事件下标。
	Initial map[string]int
	// ThreadCount 是真实线程数。
	ThreadCount int

	// threadStart[t] 是线程 t 的首个事件在 Events 中的下标。
	threadStart []int
	// threadEnd[t] 是线程 t 最后一个事件下标加一。
	threadEnd []int
	// next[t] 是线程 t 当前已排入的事件数（构建期使用）。
	next []int
}

// ProgramBuilder 增量构建 Program，并负责填写线程内序号。
type ProgramBuilder struct {
	events      []Event
	initial     map[string]int
	threadCount int
}

// NewProgramBuilder 创建构建器，threadCount 必须在 1..MaxThreads 之间。
func NewProgramBuilder(threadCount int) (*ProgramBuilder, error) {
	if threadCount < 1 || threadCount > MaxThreads {
		return nil, fmt.Errorf("netsec: 线程数 %d 超出 1..%d", threadCount, MaxThreads)
	}
	return &ProgramBuilder{
		initial:     make(map[string]int),
		threadCount: threadCount,
	}, nil
}

// AddInitialWrite 为位置 loc 登记一个固定在最前的初始写。
// 每个位置必须且只能登记一次初始写。
func (b *ProgramBuilder) AddInitialWrite(loc string, value int) (int, error) {
	if loc == "" {
		return 0, errors.New("netsec: 初始写的位置不能为空")
	}
	if _, ok := b.initial[loc]; ok {
		return 0, fmt.Errorf("netsec: 位置 %q 已登记初始写", loc)
	}
	id := len(b.events)
	b.events = append(b.events, Event{
		Thread:  -1,
		Index:   -1,
		Loc:     loc,
		IsWrite: true,
		Value:   value,
	})
	b.initial[loc] = id
	return id, nil
}

// Add 追加一个事件到指定线程，返回事件在 Program.Events 中的下标。
// 每线程事件数不得超过 MaxEventsPerThread。
func (b *ProgramBuilder) Add(thread int, ev Event) (int, error) {
	if thread < 0 || thread >= b.threadCount {
		return 0, fmt.Errorf("netsec: 非法线程号 %d", thread)
	}
	count := 0
	for _, e := range b.events {
		if e.Thread == thread {
			count++
		}
	}
	if count >= MaxEventsPerThread {
		return 0, fmt.Errorf("netsec: 线程 %d 事件数超过 %d", thread, MaxEventsPerThread)
	}
	if !ev.IsFence && ev.Loc == "" {
		return 0, errors.New("netsec: 访问事件必须给出位置")
	}
	if ev.IsFence && ev.Loc != "" {
		return 0, errors.New("netsec: 屏障事件不能携带位置")
	}
	ev.Thread = thread
	ev.Index = count
	id := len(b.events)
	b.events = append(b.events, ev)
	return id, nil
}

// Build 完成校验并返回不可变的 Program。
func (b *ProgramBuilder) Build() (*Program, error) {
	start := make([]int, b.threadCount)
	end := make([]int, b.threadCount)
	next := make([]int, b.threadCount)
	for t := range start {
		start[t] = -1
	}
	for id, ev := range b.events {
		if ev.Thread < 0 {
			continue
		}
		if start[ev.Thread] == -1 {
			start[ev.Thread] = id
		}
		end[ev.Thread] = id + 1
		if ev.Index != next[ev.Thread] {
			return nil, fmt.Errorf("netsec: 线程 %d 的事件序号不连续", ev.Thread)
		}
		next[ev.Thread]++
		if _, ok := b.initial[ev.Loc]; !ok && !ev.IsFence {
			return nil, fmt.Errorf("netsec: 位置 %q 缺少初始写", ev.Loc)
		}
	}
	for t := 0; t < b.threadCount; t++ {
		if start[t] == -1 {
			return nil, fmt.Errorf("netsec: 线程 %d 没有任何事件", t)
		}
	}

	// 复制一份，避免调用方继续修改构建器内部切片。
	events := append([]Event(nil), b.events...)
	initial := make(map[string]int, len(b.initial))
	for k, v := range b.initial {
		initial[k] = v
	}
	return &Program{
		Events:      events,
		Initial:     initial,
		ThreadCount: b.threadCount,
		threadStart: start,
		threadEnd:   end,
		next:        next,
	}, nil
}

// ThreadEvents 返回线程 t 的事件下标区间（左闭右开）。
func (p *Program) ThreadEvents(t int) (int, int) {
	return p.threadStart[t], p.threadEnd[t]
}

// SameThread 报告两个事件是否属于同一真实线程。
func (p *Program) SameThread(a, b int) bool {
	return p.Events[a].Thread >= 0 && p.Events[a].Thread == p.Events[b].Thread
}

// MayReorder 报告同一线程内相邻事件 a（先）与 b（后）是否允许重排。
//
// 默认规则：只有两个都是弱序事件（plain 或 relaxed）且都不是屏障时才允许
// 重排；任何 release/acquire/seqcst 事件或屏障都会钉住线程内顺序。宿主可
// 以在 Query.MayReorder 中显式给出不同的允许重排关系，从而建模其它内存模型。
func (p *Program) MayReorder(a, b int) bool {
	ea, eb := p.Events[a], p.Events[b]
	if ea.Thread != eb.Thread || ea.Thread < 0 {
		return false
	}
	if eb.Index != ea.Index+1 {
		return false
	}
	weak := func(e Event) bool {
		return !e.IsFence && (e.Order == OrderPlain || e.Order == OrderRelaxed)
	}
	return weak(ea) && weak(eb)
}

// String 给出便于调试的事件表示，例如 "T1.R1(flag)"、"W2(flag)=1"。
func (p *Program) String() string {
	out := ""
	for id, e := range p.Events {
		if id > 0 {
			out += " "
		}
		switch {
		case e.Thread < 0:
			out += fmt.Sprintf("Init.W(%s)=%d", e.Loc, e.Value)
		case e.IsFence:
			out += fmt.Sprintf("T%d.fence[%d]%s", e.Thread, e.Index, e.Order)
		case e.IsWrite:
			out += fmt.Sprintf("T%d.W[%d](%s)=%d", e.Thread, e.Index, e.Loc, e.Value)
		default:
			out += fmt.Sprintf("T%d.R[%d](%s)", e.Thread, e.Index, e.Loc)
		}
	}
	return out
}

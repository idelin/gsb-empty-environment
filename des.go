package blockingflow

import "container/heap"

// 事件种类，同时刻的事件按此顺序处理：
// 停机结束 < 机器二完工 < 机器一完工 < 停机开始。
// 停机结束最先：在停机结束时刻装机的工件应看到已恢复的机器。
// 机器二完工先于机器一完工：同时刻释放的机器二可被同时刻完工的
// 机器一工件立即使用，与递推式 d1 = max(c1, d2Prev) 一致。
// 停机开始最后：恰在停机开始时刻完工的加工有效。
const (
	evDownEnd = iota
	evCompleteM2
	evCompleteM1
	evDownStart
)

type event struct {
	time    int
	kind    int
	machine int // 0 = 机器一, 1 = 机器二
	pos     int // 在加工顺序中的位置
	tok     int // 用于作废已过期的完工事件
}

type eventHeap []event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].time != h[j].time {
		return h[i].time < h[j].time
	}
	return h[i].kind < h[j].kind
}
func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x interface{}) {
	*h = append(*h, x.(event))
}
func (h *eventHeap) Pop() interface{} {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

type machineState struct {
	down       bool // 机器处于停机时段
	occupied   bool // 机器被工件占住（加工中、暂停中或阻塞中）
	processing bool // 正在加工（已排定完工事件）
	blocked    bool // 仅机器一：加工完毕等待机器二
	pos        int
	remaining  int
	startedAt  int
	tok        int
}

type simulator struct {
	jobs       []Job
	seq        []int
	down       [2][]Interval
	m          [2]machineState
	events     eventHeap
	nextLoad   int
	makespan   int
	blocks     []BlockRecord
	pauses     []PauseRecord
	blockStart []int
	pauseStart [2][]int
}

// simulateEvents 用离散事件模拟评估给定顺序（工件下标序列）。
// 停机区间 downs 必须已合并。结果与 evaluate 完全一致，用于交叉核对。
func simulateEvents(jobs []Job, seq []int, down1, down2 []Interval) Schedule {
	n := len(seq)
	s := &simulator{
		jobs:       jobs,
		seq:        seq,
		down:       [2][]Interval{down1, down2},
		blockStart: make([]int, n),
		pauseStart: [2][]int{make([]int, n), make([]int, n)},
	}
	for m := 0; m < 2; m++ {
		s.m[m].pos = -1
		for i := range s.pauseStart[m] {
			s.pauseStart[m][i] = -1
		}
		for _, d := range s.down[m] {
			if d.Start == 0 {
				s.m[m].down = true
				s.push(event{time: d.End, kind: evDownEnd, machine: m})
			} else {
				s.push(event{time: d.Start, kind: evDownStart, machine: m})
				s.push(event{time: d.End, kind: evDownEnd, machine: m})
			}
		}
	}
	s.loadNext(0)
	for len(s.events) > 0 {
		e := heap.Pop(&s.events).(event)
		switch e.kind {
		case evCompleteM1, evCompleteM2:
			s.onComplete(e)
		case evDownStart:
			s.onDownStart(e)
		case evDownEnd:
			s.onDownEnd(e)
		}
	}
	return Schedule{Seq: idsOf(jobs, seq), Makespan: s.makespan, Blocks: s.blocks, Pauses: s.pauses}
}

func (s *simulator) push(e event) {
	heap.Push(&s.events, e)
}

// startProcessing 让占据机器的工件开始（或恢复）加工；若机器停机，
// 工件占住机器等待，暂停时段自此刻起算。
func (s *simulator) startProcessing(mach, t int) {
	ms := &s.m[mach]
	if ms.down {
		if ms.remaining == 0 {
			// 零加工时间不需要可用机时，立即完工。
			ms.processing = true
			ms.tok++
			s.push(event{time: t, kind: completeKind(mach), machine: mach, pos: ms.pos, tok: ms.tok})
			return
		}
		s.pauseStart[mach][ms.pos] = t
		return
	}
	ms.processing = true
	ms.startedAt = t
	ms.tok++
	s.push(event{time: t + ms.remaining, kind: completeKind(mach), machine: mach, pos: ms.pos, tok: ms.tok})
}

func completeKind(mach int) int {
	if mach == 0 {
		return evCompleteM1
	}
	return evCompleteM2
}

// loadNext 把顺序中的下一件工件装上机器一。
func (s *simulator) loadNext(t int) {
	if s.nextLoad >= len(s.seq) {
		return
	}
	pos := s.nextLoad
	s.nextLoad++
	m1 := &s.m[0]
	m1.occupied = true
	m1.blocked = false
	m1.processing = false
	m1.pos = pos
	m1.remaining = s.jobs[s.seq[pos]].P1
	s.startProcessing(0, t)
}

// transfer 把机器一上已完工的工件移到机器二，并装入下一件工件。
func (s *simulator) transfer(t int) {
	pos := s.m[0].pos
	s.m[0].occupied = false
	s.m[0].blocked = false
	s.m[0].processing = false
	s.m[0].pos = -1
	m2 := &s.m[1]
	m2.occupied = true
	m2.blocked = false
	m2.processing = false
	m2.pos = pos
	m2.remaining = s.jobs[s.seq[pos]].P2
	s.startProcessing(1, t)
	s.loadNext(t)
}

func (s *simulator) onComplete(e event) {
	ms := &s.m[e.machine]
	if !ms.processing || ms.pos != e.pos || ms.tok != e.tok {
		return // 已过期的事件（停机作废旧完工事件）
	}
	ms.processing = false
	ms.remaining = 0
	if e.machine == 0 {
		if s.m[1].occupied {
			ms.blocked = true
			s.blockStart[e.pos] = e.time
		} else {
			s.transfer(e.time)
		}
		return
	}
	if e.time > s.makespan {
		s.makespan = e.time
	}
	ms.occupied = false
	ms.pos = -1
	if s.m[0].occupied && s.m[0].blocked {
		pos := s.m[0].pos
		s.blocks = append(s.blocks, BlockRecord{
			JobID: s.jobs[s.seq[pos]].ID,
			Start: s.blockStart[pos],
			End:   e.time,
		})
		s.transfer(e.time)
	}
}

func (s *simulator) onDownStart(e event) {
	ms := &s.m[e.machine]
	ms.down = true
	if ms.processing && ms.remaining > 0 {
		ms.remaining -= e.time - ms.startedAt
		ms.processing = false
		ms.tok++ // 作废旧完工事件
		s.pauseStart[e.machine][ms.pos] = e.time
	}
}

func (s *simulator) onDownEnd(e event) {
	ms := &s.m[e.machine]
	ms.down = false
	if ms.occupied && !ms.processing && !ms.blocked {
		if ps := s.pauseStart[e.machine][ms.pos]; ps >= 0 {
			s.pauses = append(s.pauses, PauseRecord{
				JobID:   s.jobs[s.seq[ms.pos]].ID,
				Machine: e.machine + 1,
				Start:   ps,
				End:     e.time,
			})
			s.pauseStart[e.machine][ms.pos] = -1
		}
		ms.processing = true
		ms.startedAt = e.time
		ms.tok++
		s.push(event{time: e.time + ms.remaining, kind: completeKind(e.machine), machine: e.machine, pos: ms.pos, tok: ms.tok})
	}
}

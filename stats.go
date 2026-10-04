// Package statlib 提供基于双堆（最大堆 + 最小堆）的在线统计结构，
// 支持按 ID 增删数值、查询下中位数以及两侧绝对偏差和。
//
// 删除采用懒标记（tombstone）：物理节点先打标记，到达堆顶时才弹出。
// 当物理节点总数超过 有效项数*2 + 32 时触发重建，清理所有墓碑节点。
package statlib

import (
	"container/heap"
	"errors"
	"fmt"
)

// rebuildSlack 是重建阈值中的常数余量。
const rebuildSlack = 32

// ErrDuplicateID 表示添加了一个已存在的 ID。
var ErrDuplicateID = errors.New("statlib: duplicate id")

// ErrMissingID 表示删除一个不存在的 ID。
var ErrMissingID = errors.New("statlib: missing id")

// entry 是堆中的物理节点，相同数值按 ID 区分。
type entry struct {
	val int64
	id  int
}

// valHeap 实现 heap.Interface；max=true 时为最大堆，否则为最小堆。
// 数值相同按 ID 比较，保证相同数值的节点可区分且堆序确定。
type valHeap struct {
	data []entry
	max  bool
}

func (h *valHeap) Len() int { return len(h.data) }

func (h *valHeap) Less(i, j int) bool {
	a, b := h.data[i], h.data[j]
	if a.val != b.val {
		if h.max {
			return a.val > b.val
		}
		return a.val < b.val
	}
	if h.max {
		return a.id > b.id
	}
	return a.id < b.id
}

func (h *valHeap) Swap(i, j int) { h.data[i], h.data[j] = h.data[j], h.data[i] }

func (h *valHeap) Push(x any) { h.data = append(h.data, x.(entry)) }

func (h *valHeap) Pop() any {
	old := h.data
	n := len(old)
	e := old[n-1]
	h.data = old[:n-1]
	return e
}

func (h *valHeap) top() entry { return h.data[0] }

// Stats 是双堆统计结构。low 为最大堆，保存较小的一半（含下中位数）；
// high 为最小堆，保存较大的一半。有效计数满足
// cntLow == cntHigh 或 cntLow == cntHigh+1。
type Stats struct {
	low  *valHeap // 最大堆
	high *valHeap // 最小堆

	pendLow  map[int64]int // low 侧懒删除标记：数值 -> 待删次数
	pendHigh map[int64]int // high 侧懒删除标记

	sumLow  int64 // low 侧有效元素之和
	sumHigh int64 // high 侧有效元素之和
	cntLow  int   // low 侧有效元素个数
	cntHigh int   // high 侧有效元素个数

	vals map[int]int64 // ID -> 数值
}

// New 返回一个空的统计结构。
func New() *Stats {
	return &Stats{
		low:      &valHeap{max: true},
		high:     &valHeap{max: false},
		pendLow:  make(map[int64]int),
		pendHigh: make(map[int64]int),
		vals:     make(map[int]int64),
	}
}

// Len 返回当前有效项数。
func (s *Stats) Len() int { return s.cntLow + s.cntHigh }

// physical 返回当前物理节点总数（含墓碑）。
func (s *Stats) physical() int { return s.low.Len() + s.high.Len() }

// Add 插入一个 (ID, 数值)。ID 已存在时返回 ErrDuplicateID。
func (s *Stats) Add(id int, val int64) error {
	if _, ok := s.vals[id]; ok {
		return fmt.Errorf("%w: %d", ErrDuplicateID, id)
	}
	s.vals[id] = val

	s.cleanTop(s.low, s.pendLow)
	if s.cntLow == 0 || val <= s.low.top().val {
		heap.Push(s.low, entry{val: val, id: id})
		s.sumLow += val
		s.cntLow++
	} else {
		heap.Push(s.high, entry{val: val, id: id})
		s.sumHigh += val
		s.cntHigh++
	}
	s.rebalance()
	s.maybeRebuild()
	return nil
}

// Remove 按 ID 删除数值。ID 不存在时返回 ErrMissingID。
func (s *Stats) Remove(id int) error {
	val, ok := s.vals[id]
	if !ok {
		return fmt.Errorf("%w: %d", ErrMissingID, id)
	}
	delete(s.vals, id)

	s.cleanTop(s.low, s.pendLow)
	if s.cntLow > 0 && val <= s.low.top().val {
		// 归入 low 侧。相同数值的元素可互换，按值打墓碑即可保证
		// 数值多重集正确；墓碑必然能在 low 堆内匹配到节点。
		s.pendLow[val]++
		s.sumLow -= val
		s.cntLow--
	} else {
		s.pendHigh[val]++
		s.sumHigh -= val
		s.cntHigh--
	}
	s.cleanTop(s.low, s.pendLow)
	s.cleanTop(s.high, s.pendHigh)
	s.rebalance()
	s.maybeRebuild()
	return nil
}

// Query 返回下中位数 median、low 侧绝对偏差和 leftDev（含中位数自身，偏差为 0）、
// high 侧绝对偏差和 rightDev。集合为空时 ok 为 false。
func (s *Stats) Query() (median, leftDev, rightDev int64, ok bool) {
	if s.Len() == 0 {
		return 0, 0, 0, false
	}
	s.cleanTop(s.low, s.pendLow)
	median = s.low.top().val
	leftDev = median*int64(s.cntLow) - s.sumLow
	rightDev = s.sumHigh - median*int64(s.cntHigh)
	return median, leftDev, rightDev, true
}

// cleanTop 弹出堆顶所有墓碑节点。
func (s *Stats) cleanTop(h *valHeap, pend map[int64]int) {
	for h.Len() > 0 {
		v := h.top().val
		if pend[v] == 0 {
			return
		}
		pend[v]--
		if pend[v] == 0 {
			delete(pend, v)
		}
		heap.Pop(h)
	}
}

// rebalance 恢复 cntLow == cntHigh 或 cntLow == cntHigh+1。
// 移动前先清理堆顶墓碑，因此弹出的必然是有效节点。
func (s *Stats) rebalance() {
	for s.cntLow > s.cntHigh+1 {
		s.cleanTop(s.low, s.pendLow)
		e := heap.Pop(s.low).(entry)
		heap.Push(s.high, e)
		s.sumLow -= e.val
		s.sumHigh += e.val
		s.cntLow--
		s.cntHigh++
	}
	for s.cntLow < s.cntHigh {
		s.cleanTop(s.high, s.pendHigh)
		e := heap.Pop(s.high).(entry)
		heap.Push(s.low, e)
		s.sumHigh -= e.val
		s.sumLow += e.val
		s.cntHigh--
		s.cntLow++
	}
}

// maybeRebuild 在物理节点数超过 有效项数*2+32 时重建清理。
func (s *Stats) maybeRebuild() {
	if s.physical() > 2*s.Len()+rebuildSlack {
		s.rebuild()
	}
}

// rebuild 丢弃两个堆中的全部墓碑节点并重新堆化。
func (s *Stats) rebuild() {
	s.low.data = filterLive(s.low.data, s.pendLow)
	heap.Init(s.low)
	s.high.data = filterLive(s.high.data, s.pendHigh)
	heap.Init(s.high)
}

// filterLive 返回去除墓碑后的节点切片；pend 中未能匹配的标记一并清除。
func filterLive(data []entry, pend map[int64]int) []entry {
	kept := data[:0]
	for _, e := range data {
		if pend[e.val] > 0 {
			pend[e.val]--
			if pend[e.val] == 0 {
				delete(pend, e.val)
			}
			continue
		}
		kept = append(kept, e)
	}
	return kept
}

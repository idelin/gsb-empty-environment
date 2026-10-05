package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// SegState 为房间在某一时间段内的状态。
type SegState int

const (
	StFree       SegState = iota // 空闲 (独立单间)
	StMergedIdle                 // 已合并, 空闲
	StMergePrep                  // 合并准备中
	StSplitPrep                  // 拆分恢复中
	StMeeting                    // 会议占用
)

func (s SegState) String() string {
	switch s {
	case StFree:
		return "空闲"
	case StMergedIdle:
		return "已合并"
	case StMergePrep:
		return "合并准备"
	case StSplitPrep:
		return "拆分恢复"
	case StMeeting:
		return "会议"
	}
	return "?"
}

// Segment 为单个房间的一段状态区间 [Start,End); End<0 表示开放到无穷。
// BlockLo/BlockHi 记录该时段所属合并块的范围 (单间时为自身)。
type Segment struct {
	Start, End       int
	State            SegState
	MeetingID        string
	BlockLo, BlockHi int
}

// BuildTimeline 按求解结果重放拆合与会议, 生成每个房间的状态时间线。
func BuildTimeline(cfg Config, sol Solution) [][]Segment {
	k := cfg.NumRooms
	segs := make([][]Segment, k)
	since := make([]int, k)
	curState := make([]SegState, k)
	curBlock := make([]block, k)
	for r := 0; r < k; r++ {
		curBlock[r] = block{lo: r, hi: r}
	}
	emit := func(r, end int, st SegState, id string, blo, bhi int) {
		if since[r] < end {
			segs[r] = append(segs[r], Segment{Start: since[r], End: end, State: st, MeetingID: id, BlockLo: blo, BlockHi: bhi})
		}
		since[r] = end
	}

	assigns := append([]Assignment(nil), sol.Assigns...)
	sort.Slice(assigns, func(i, j int) bool {
		if assigns[i].Meeting.Start != assigns[j].Meeting.Start {
			return assigns[i].Meeting.Start < assigns[j].Meeting.Start
		}
		return assigns[i].Meeting.ID < assigns[j].Meeting.ID
	})

	for _, a := range assigns {
		m := a.Meeting
		merging := a.Lo < a.Hi
		deadline := m.Start
		if merging {
			deadline = m.Start - cfg.MergeTime
		}
		exact := true
		for r := a.Lo; r <= a.Hi; r++ {
			if curBlock[r].lo != a.Lo || curBlock[r].hi != a.Hi {
				exact = false
				break
			}
		}
		if !exact {
			// 拆分与目标区间部分重叠的块
			for r := 0; r < k; r++ {
				b := curBlock[r]
				if r != b.lo || b.hi < a.Lo || b.lo > a.Hi {
					continue
				}
				if b.lo >= a.Lo && b.hi <= a.Hi {
					continue
				}
				splitStart := deadline - cfg.SplitTime
				for rr := b.lo; rr <= b.hi; rr++ {
					emit(rr, splitStart, StMergedIdle, "", b.lo, b.hi)
					emit(rr, deadline, StSplitPrep, "", b.lo, b.hi)
					curState[rr] = StFree
					curBlock[rr] = block{lo: rr, hi: rr}
				}
			}
			if merging {
				for r := a.Lo; r <= a.Hi; r++ {
					emit(r, deadline, curState[r], "", curBlock[r].lo, curBlock[r].hi)
					emit(r, m.Start, StMergePrep, "", a.Lo, a.Hi)
					curState[r] = StMergedIdle
					curBlock[r] = block{lo: a.Lo, hi: a.Hi}
				}
			}
		}
		for r := a.Lo; r <= a.Hi; r++ {
			emit(r, m.Start, curState[r], "", curBlock[r].lo, curBlock[r].hi)
			emit(r, m.End, StMeeting, m.ID, a.Lo, a.Hi)
			if a.Lo == a.Hi {
				curState[r] = StFree
			} else {
				curState[r] = StMergedIdle
			}
		}
	}
	for r := 0; r < k; r++ {
		segs[r] = append(segs[r], Segment{Start: since[r], End: -1, State: curState[r], BlockLo: curBlock[r].lo, BlockHi: curBlock[r].hi})
	}
	return segs
}

// RenderTimeline 将时间线渲染为文本, 房间编号从 1 起。
func RenderTimeline(tl [][]Segment) string {
	var b strings.Builder
	for r, segs := range tl {
		fmt.Fprintf(&b, "房间 %d: ", r+1)
		parts := make([]string, 0, len(segs))
		for _, s := range segs {
			label := s.State.String()
			if s.State == StMeeting {
				label = "会议" + s.MeetingID
			}
			end := "∞"
			if s.End >= 0 {
				end = strconv.Itoa(s.End)
			}
			parts = append(parts, fmt.Sprintf("[%d,%s) %s", s.Start, end, label))
		}
		b.WriteString(strings.Join(parts, " → "))
		b.WriteString("\n")
	}
	return b.String()
}

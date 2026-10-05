package main

import (
	"fmt"
	"sort"
	"strings"
)

// block 是当前房间划分中的一个连续块: 房间 [lo,hi] (含, 0 起始),
// busyUntil 之前该块被已安排的会议占用, 不可合并或拆分。
type block struct {
	lo, hi    int
	busyUntil int
}

// Assignment 表示会议被安排到房间区间 [Lo,Hi] (含, 0 起始), Ops 为此次安排
// 引发的拆合操作数。
type Assignment struct {
	Meeting Meeting
	Lo, Hi  int
	Ops     int
}

// Solution 为求解结果。
type Solution struct {
	Assigns   []Assignment
	Rejected  []Meeting
	RejWeight int // 被拒绝会议的权重之和 (主目标, 越小越好)
	Ops       int // 合并+拆分总次数 (次目标, 越小越好)
}

type costPair struct{ w, o int }

type solver struct {
	cfg   Config
	ms    []Meeting // 按开始时间排序
	memo  map[string]costPair
	bestW int
	bestO int
	best  []Assignment
}

// Solve 精确求解: 主目标最小化拒绝权重, 次目标最小化拆合次数。
func Solve(cfg Config) Solution {
	ms := make([]Meeting, len(cfg.Meetings))
	copy(ms, cfg.Meetings)
	sort.SliceStable(ms, func(i, j int) bool {
		if ms[i].Start != ms[j].Start {
			return ms[i].Start < ms[j].Start
		}
		if ms[i].End != ms[j].End {
			return ms[i].End < ms[j].End
		}
		return ms[i].ID < ms[j].ID
	})
	total := 0
	for _, m := range ms {
		total += m.Weight
	}
	s := &solver{
		cfg:   cfg,
		ms:    ms,
		memo:  make(map[string]costPair),
		bestW: total + 1,
		bestO: int(^uint(0) >> 1),
	}
	blocks := make([]block, cfg.NumRooms)
	for i := range blocks {
		blocks[i] = block{lo: i, hi: i}
	}
	s.dfs(0, blocks, 0, 0, nil)

	assigned := make(map[string]bool, len(s.best))
	accWeight := 0
	for _, a := range s.best {
		assigned[a.Meeting.ID] = true
		accWeight += a.Meeting.Weight
	}
	sol := Solution{Assigns: s.best, RejWeight: total - accWeight, Ops: s.bestO}
	for _, m := range cfg.Meetings {
		if !assigned[m.ID] {
			sol.Rejected = append(sol.Rejected, m)
		}
	}
	return sol
}

func (s *solver) dfs(idx int, blocks []block, curW, curOps int, assigns []Assignment) {
	if curW > s.bestW || (curW == s.bestW && curOps >= s.bestO) {
		return
	}
	if idx == len(s.ms) {
		s.bestW, s.bestO = curW, curOps
		s.best = append([]Assignment(nil), assigns...)
		return
	}
	key := stateKey(idx, blocks)
	if v, ok := s.memo[key]; ok && v.w <= curW && v.o <= curOps {
		return
	}
	s.memo[key] = costPair{w: curW, o: curOps}

	m := s.ms[idx]
	type cand struct{ lo, hi, ops int }
	var cands []cand
	for size := m.Rooms; size <= s.cfg.NumRooms; size++ {
		for lo := 0; lo+size <= s.cfg.NumRooms; lo++ {
			hi := lo + size - 1
			if ops, ok := planRange(blocks, lo, hi, m, s.cfg); ok {
				cands = append(cands, cand{lo, hi, ops})
			}
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].ops != cands[j].ops {
			return cands[i].ops < cands[j].ops
		}
		si, sj := cands[i].hi-cands[i].lo, cands[j].hi-cands[j].lo
		if si != sj {
			return si < sj
		}
		return cands[i].lo < cands[j].lo
	})
	for _, c := range cands {
		s.dfs(idx+1, applyRange(blocks, c.lo, c.hi, m.End), curW, curOps+c.ops,
			append(assigns, Assignment{Meeting: m, Lo: c.lo, Hi: c.hi, Ops: c.ops}))
	}
	// 拒绝该会议
	s.dfs(idx+1, blocks, curW+m.Weight, curOps, assigns)
}

// planRange 判断会议 m 能否占用连续区间 [lo,hi], 返回所需拆合操作数。
// 模型: 合并尽量晚做 (恰在会议开始前 MergeTime), 拆分按需做;
// 拆分将被切块的房间恢复为单间; 单房间区间无需合并操作。
func planRange(blocks []block, lo, hi int, m Meeting, cfg Config) (int, bool) {
	for _, b := range blocks {
		if b.lo == lo && b.hi == hi {
			if b.busyUntil <= m.Start {
				return 0, true
			}
			return 0, false
		}
	}
	deadline := m.Start // 拆分与被并入块须在该时刻前就绪
	ops := 0
	if hi > lo {
		ops = 1 // 合并操作
		deadline = m.Start - cfg.MergeTime
		if deadline < 0 {
			return 0, false
		}
	}
	for _, b := range blocks {
		if b.hi < lo || b.lo > hi {
			continue
		}
		if b.lo >= lo && b.hi <= hi {
			// 整块并入: 合并窗口期间必须空闲
			if b.busyUntil > deadline {
				return 0, false
			}
		} else {
			// 部分重叠: 须先拆分, 拆分完成后才能合并
			if b.busyUntil+cfg.SplitTime > deadline {
				return 0, false
			}
			ops++
		}
	}
	return ops, true
}

// applyRange 返回把 [lo,hi] 合并为一块 (busyUntil 更新) 后的新划分;
// 被部分切开的块, 其区间外部分恢复为单间。
func applyRange(blocks []block, lo, hi, busyUntil int) []block {
	var nb []block
	for _, b := range blocks {
		if b.hi < lo || b.lo > hi {
			nb = append(nb, b)
			continue
		}
		for r := b.lo; r < lo; r++ {
			nb = append(nb, block{lo: r, hi: r})
		}
		for r := hi + 1; r <= b.hi; r++ {
			nb = append(nb, block{lo: r, hi: r})
		}
	}
	nb = append(nb, block{lo: lo, hi: hi, busyUntil: busyUntil})
	sort.Slice(nb, func(i, j int) bool { return nb[i].lo < nb[j].lo })
	return nb
}

func stateKey(idx int, blocks []block) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d|", idx)
	for _, bl := range blocks {
		fmt.Fprintf(&b, "%d,%d,%d;", bl.lo, bl.hi, bl.busyUntil)
	}
	return b.String()
}

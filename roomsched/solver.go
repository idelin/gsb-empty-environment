package main

import (
	"math/bits"
	"sort"
)

// 模型约定（就近准备 / just-in-time）：
//   - 分区用边界位掩码表示：bit b 置 1 表示房间 b 与 b+1 处于合并状态。
//   - 拆合操作在需要它的会议开始前紧邻完成，占用相邻两房间 [start-prep, start)。
//   - 不同边界上的拆合（如 A|B 与 B|C 两道隔墙）可并行施工。
//   - 会议期间房间块固定，不可换房；合并块中的房间不可被单独分配。

type stateKey struct {
	i    int
	part uint32
	free [maxRooms]int
}

type decision struct {
	reject bool
	lo, hi int
}

type planResult struct {
	rejected int
	ops      int
	dec      decision
}

type solver struct {
	p    *Problem
	caps []int
	memo map[stateKey]planResult
}

// idleSince 表示“房间自远古起空闲”：首场会议之前的任何准备都可完成。
const idleSince = -(1 << 30)

func initialFree() (free [maxRooms]int) {
	for i := range free {
		free[i] = idleSince
	}
	return
}

// Solve 求拒绝权重最小、其次拆合次数最少的安排。
func Solve(p *Problem) *Solution {
	sp := *p
	ms := make([]Meeting, len(p.Meetings))
	copy(ms, p.Meetings)
	sort.SliceStable(ms, func(i, j int) bool {
		if ms[i].Start != ms[j].Start {
			return ms[i].Start < ms[j].Start
		}
		if ms[i].End != ms[j].End {
			return ms[i].End < ms[j].End
		}
		if ms[i].Weight != ms[j].Weight {
			return ms[i].Weight > ms[j].Weight
		}
		return ms[i].ID < ms[j].ID
	})
	sp.Meetings = ms

	s := &solver{p: &sp, caps: make([]int, len(sp.Rooms)), memo: map[stateKey]planResult{}}
	for i, r := range sp.Rooms {
		s.caps[i] = r.Capacity
	}
	s.dfs(0, 0, initialFree())
	return s.buildSolution()
}

// dfs 返回从第 i 场会议起，当前分区 part、各房间空闲时刻 free 下的
// 最优 (拒绝权重, 拆合次数)，按字典序比较。
func (s *solver) dfs(i int, part uint32, free [maxRooms]int) (int, int) {
	if i == len(s.p.Meetings) {
		return 0, 0
	}
	key := stateKey{i, part, free}
	if r, ok := s.memo[key]; ok {
		return r.rejected, r.ops
	}
	m := s.p.Meetings[i]
	R := len(s.p.Rooms)

	// 选项一：拒绝本场会议
	rw, ro := s.dfs(i+1, part, free)
	best := planResult{rejected: rw + m.Weight, ops: ro, dec: decision{reject: true}}

	// 选项二：接受，枚举容量足够的连续块（同等条件下优先小块）
	found := false
	var acc planResult
	for span := 1; span <= R; span++ {
		for lo := 0; lo+span <= R; lo++ {
			hi := lo + span - 1
			capSum := 0
			for r := lo; r <= hi; r++ {
				capSum += s.caps[r]
			}
			if capSum < m.Size {
				continue
			}
			newPart := blockPart(part, lo, hi, R)
			changed := part ^ newPart
			// 块内房间须在会议开始前空闲
			ok := true
			for r := lo; r <= hi && ok; r++ {
				if free[r] > m.Start {
					ok = false
				}
			}
			// 每个变化的边界 = 一次拆合操作，其两侧房间须在 [start-prep, start) 空闲
			for b := 0; b < R-1 && ok; b++ {
				if changed&(1<<uint(b)) == 0 {
					continue
				}
				if free[b] > m.Start-s.p.Prep || free[b+1] > m.Start-s.p.Prep {
					ok = false
				}
			}
			if !ok {
				continue
			}
			nf := free
			for r := lo; r <= hi; r++ {
				nf[r] = m.End
			}
			w, o := s.dfs(i+1, newPart, nf)
			o += bits.OnesCount32(changed)
			if !found || w < acc.rejected || (w == acc.rejected && o < acc.ops) {
				found = true
				acc = planResult{rejected: w, ops: o, dec: decision{lo: lo, hi: hi}}
			}
		}
	}
	// 拒绝权重相同、操作数也相同时，优先接受会议
	if found && (acc.rejected < best.rejected || (acc.rejected == best.rejected && acc.ops <= best.ops)) {
		best = acc
	}
	s.memo[key] = best
	return best.rejected, best.ops
}

// blockPart 返回将 [lo,hi] 变为一个块后的新分区：块内边界合并、块两端边界切开，
// 其余边界保持不变（最小改动）。
func blockPart(part uint32, lo, hi, R int) uint32 {
	newPart := part
	for b := lo; b < hi; b++ {
		newPart |= 1 << uint(b)
	}
	if lo > 0 {
		newPart &^= 1 << uint(lo-1)
	}
	if hi < R-1 {
		newPart &^= 1 << uint(hi)
	}
	return newPart
}

func (s *solver) buildSolution() *Solution {
	sol := &Solution{}
	R := len(s.p.Rooms)
	part := uint32(0)
	free := initialFree()
	for i := 0; i < len(s.p.Meetings); i++ {
		res := s.memo[stateKey{i, part, free}]
		m := s.p.Meetings[i]
		if res.dec.reject {
			sol.Rejected = append(sol.Rejected, m)
			sol.RejectedWeight += m.Weight
			continue
		}
		lo, hi := res.dec.lo, res.dec.hi
		newPart := blockPart(part, lo, hi, R)
		changed := part ^ newPart
		for b := 0; b < R-1; b++ {
			if changed&(1<<uint(b)) != 0 {
				sol.Ops = append(sol.Ops, Op{
					Boundary:  b,
					Merge:     newPart&(1<<uint(b)) != 0,
					Start:     m.Start - s.p.Prep,
					End:       m.Start,
					MeetingID: m.ID,
				})
			}
		}
		sol.Accepted = append(sol.Accepted, Assignment{Meeting: m, Lo: lo, Hi: hi})
		for r := lo; r <= hi; r++ {
			free[r] = m.End
		}
		part = newPart
		sol.TotalOps += bits.OnesCount32(changed)
	}
	return sol
}

package trainsim

import (
	"fmt"
	"sort"
)

// Replay 通过逐段占用回放核对一组列车方案：
// 每列车须满足时间规则（首尾相接、不小于最小走行时分、只在车站等待、
// 不越出时域），每个轨段上的占用经区间扫描须互斥。
func Replay(net *Network, horizon Time, plans []TrainPlan) error {
	return replay(net, horizon, plans, nil)
}

// ReplayWithDwell 同 Replay，但允许指定列车在某一非车站轨段上停留
// （故障扣车造成的占用延长）。
func ReplayWithDwell(net *Network, horizon Time, plans []TrainPlan, allow map[TrainID]SegmentID) error {
	return replay(net, horizon, plans, allow)
}

func replay(net *Network, horizon Time, plans []TrainPlan, allow map[TrainID]SegmentID) error {
	bySeg := map[SegmentID][]Reservation{}
	for _, tp := range plans {
		if len(tp.Res) != len(tp.Path) {
			return fmt.Errorf("%s: %d 条预约对应 %d 段路径", tp.Train, len(tp.Res), len(tp.Path))
		}
		for i, r := range tp.Res {
			if r.Train != tp.Train || r.Seg != tp.Path[i] {
				return fmt.Errorf("%s: 第 %d 条预约与路径不符", tp.Train, i)
			}
			seg, ok := net.Segment(r.Seg)
			if !ok {
				return fmt.Errorf("%s: 未知轨段 %s", tp.Train, r.Seg)
			}
			dur := r.Iv.Len()
			switch {
			case dur < seg.Travel:
				return fmt.Errorf("%s: 轨段 %s 走行时分 %d 小于最小值 %d",
					tp.Train, r.Seg, dur, seg.Travel)
			case dur > seg.Travel && !seg.Station && allow[tp.Train] != r.Seg:
				return fmt.Errorf("%s: 在非车站轨段 %s 上等待", tp.Train, r.Seg)
			}
			if i > 0 && tp.Res[i-1].Iv.End != r.Iv.Start {
				return fmt.Errorf("%s: 轨段 %s 与 %s 之间占用不衔接", tp.Train, tp.Path[i-1], r.Seg)
			}
			if r.Iv.Start < 0 || r.Iv.End > horizon {
				return fmt.Errorf("%s: 轨段 %s 的占用越出时域", tp.Train, r.Seg)
			}
			bySeg[r.Seg] = append(bySeg[r.Seg], r)
		}
	}
	// 逐段区间扫描：排序后扫掠，任何重叠即为冲突（含连锁冲突）。
	for seg, list := range bySeg {
		sort.Slice(list, func(i, j int) bool { return list[i].Iv.Start < list[j].Iv.Start })
		for i := 1; i < len(list); i++ {
			prev, cur := list[i-1], list[i]
			if cur.Iv.Start < prev.Iv.End {
				return fmt.Errorf("轨段 %s: %s [%d,%d) 与 %s [%d,%d) 冲突",
					seg, prev.Train, prev.Iv.Start, prev.Iv.End,
					cur.Train, cur.Iv.Start, cur.Iv.End)
			}
		}
	}
	return nil
}

// enumerateSchedules 独立地枚举一列车在 table 约束下的全部可行占用序列
// （探索所有合法进入/出发时刻组合，数量以 cap 为上限），
// 用于与贪心规划器交叉核对。
func enumerateSchedules(net *Network, table *Table, horizon Time, path []SegmentID, t0 Time, opts PlanOptions, cap int) [][]Interval {
	p := newPlanner(net, table, horizon)
	var out [][]Interval
	var recEnter func(i int, e Time, acc []Interval)
	recEnter = func(i int, e Time, acc []Interval) {
		if len(out) >= cap {
			return
		}
		if i == len(path) {
			out = append(out, append([]Interval{}, acc...))
			return
		}
		seg := net.mustSegment(path[i])
		if e+seg.Travel > horizon || !table.Free(path[i], Interval{Start: e, End: e + seg.Travel}) {
			return
		}
		minDep := e + seg.Travel
		dwell := seg.Station
		if i == 0 {
			if opts.FirstMinDep > minDep {
				minDep = opts.FirstMinDep
			}
			dwell = dwell || opts.FirstDwell
		}
		if dwell {
			end := p.gapEnd(path[i], e)
			for dep := minDep; dep <= end; dep++ {
				na := append(append([]Interval{}, acc...), Interval{Start: e, End: dep})
				recEnter(i+1, dep, na)
			}
			return
		}
		if minDep == e+seg.Travel {
			na := append(append([]Interval{}, acc...), Interval{Start: e, End: minDep})
			recEnter(i+1, minDep, na)
		}
	}
	if len(path) == 0 {
		return [][]Interval{nil}
	}
	if opts.FixedStart {
		recEnter(0, t0, nil)
		return out
	}
	seg0 := net.mustSegment(path[0])
	for e := t0; e+seg0.Travel <= horizon; e++ {
		recEnter(0, e, nil)
	}
	return out
}

// BruteForceReplan 独立重算最优目标值：枚举每列车的全部合法后缀方案，
// 再枚举所有包含延误列车的子集与方案组合，逐段回放过滤冲突，
// 返回字典序最小的 (受改列车数, 总延误)。供测试交叉核对 Replan。
func BruteForceReplan(net *Network, req ReplanRequest, capPerTrain int) (int, Time, bool) {
	n := len(req.Plans)
	if n == 0 || n > MaxReplanTrains {
		return 0, 0, false
	}
	wins, delayedIdx, err := computeWindows(net, req)
	if err != nil {
		return 0, 0, false
	}
	base := NewTable()
	for i := range wins {
		base.Add(wins[i].locked...)
	}
	cands := make([][][]Interval, n)
	for i := range wins {
		w := wins[i]
		if len(w.subPath) == 0 {
			cands[i] = [][]Interval{nil}
			continue
		}
		cands[i] = enumerateSchedules(net, base, req.Horizon, w.subPath, w.t0, w.opts, capPerTrain)
		if len(cands[i]) == 0 {
			return 0, 0, false
		}
	}

	bestChanged := n + 1
	var bestDelay Time
	found := false
	for mask := 1; mask < (1 << n); mask++ {
		if mask&(1<<delayedIdx) == 0 {
			continue
		}
		var members []int
		for i := 0; i < n; i++ {
			if mask&(1<<i) != 0 {
				members = append(members, i)
			}
		}
		combo := make([]int, len(members))
		var walk func(m int)
		walk = func(m int) {
			if m == len(members) {
				changed, delay, ok := evalCombo(req, wins, mask, members, combo, cands)
				if ok && (changed < bestChanged || (changed == bestChanged && delay < bestDelay)) {
					bestChanged, bestDelay, found = changed, delay, true
				}
				return
			}
			for ci := range cands[members[m]] {
				combo[m] = ci
				walk(m + 1)
			}
		}
		walk(0)
	}
	return bestChanged, bestDelay, found
}

// evalCombo 回放一组候选后缀组合：与未受改列车及已承诺前缀一起检查冲突，
// 并计算 (受改列车数, 总延误)。
func evalCombo(req ReplanRequest, wins []trainWindow, mask int, members, combo []int, cands [][][]Interval) (int, Time, bool) {
	table := NewTable()
	for i := range wins {
		table.Add(wins[i].locked...)
		if mask&(1<<i) == 0 {
			table.Add(wins[i].origTail...)
		}
	}
	tails := map[int][]Reservation{}
	for mi, idx := range members {
		w := wins[idx]
		ivs := cands[idx][combo[mi]]
		rs := make([]Reservation, len(w.subPath))
		for j, seg := range w.subPath {
			rs[j] = Reservation{Train: req.Plans[idx].Train, Seg: seg, Iv: ivs[j]}
		}
		if err := table.Check(rs); err != nil {
			return 0, 0, false
		}
		table.Add(rs...)
		tails[idx] = rs
	}
	changed := 0
	var delay Time
	for idx, tail := range tails {
		newRes := make([]Reservation, 0, len(req.Plans[idx].Res))
		newRes = append(newRes, wins[idx].locked...)
		newRes = append(newRes, tail...)
		if !equalReservations(newRes, req.Plans[idx].Res) {
			changed++
			if d := newRes[len(newRes)-1].Iv.End - req.Plans[idx].Arrival(); d > 0 {
				delay += d
			}
		}
	}
	return changed, delay, true
}

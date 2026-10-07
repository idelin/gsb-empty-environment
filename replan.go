package trainsim

import (
	"fmt"
	"math/bits"
)

// MaxReplanTrains 是重规划支持的列车数上限（有限时间域内枚举搜索）。
const MaxReplanTrains = 4

// TrainPlan 是一列车已确认的运行方案：每段路径一条预约，时间上首尾相接。
type TrainPlan struct {
	Train TrainID
	Path  []SegmentID
	Res   []Reservation
}

// Arrival 是列车驶离末段的时刻。
func (tp TrainPlan) Arrival() Time { return tp.Res[len(tp.Res)-1].Iv.End }

// ReplanRequest 描述一次扰动及当前已承诺的方案集合。
type ReplanRequest struct {
	Now     Time
	Horizon Time
	Plans   []TrainPlan
	Delayed TrainID
	// ReadyAt 是延误列车可继续前行的最早时刻：
	// 其当前所在轨段的占用被延长，后缀不得早于 ReadyAt 开始。
	ReadyAt Time
}

// ReplanResult 保存重优化后的方案与目标值。
type ReplanResult struct {
	Plans      []TrainPlan
	Changed    []TrainID
	TotalDelay Time
}

// trainWindow 描述一列车的可调整部分：
// locked 是已承诺前缀（已进入轨段，不可移动）；
// subPath 是待重排的子路径（可能包含边界车站段，用于延长停留）。
type trainWindow struct {
	locked   []Reservation
	subPath  []SegmentID
	t0       Time
	opts     PlanOptions
	origTail []Reservation
}

// computeWindow 把一列车的方案切成已承诺前缀与可调整后缀。
// 已进入轨段的占用绝不移动；仅允许：
//   - 延误列车：延长其当前所在轨段的占用至 ReadyAt（故障扣车）；
//   - 任意列车：若正停在车站，可延长该站停留（边界段纳入后缀）。
func computeWindow(net *Network, tp TrainPlan, now, readyAt Time, delayed bool) trainWindow {
	w := trainWindow{}
	k := len(tp.Res)
	for i, r := range tp.Res {
		if r.Iv.Start >= now {
			k = i
			break
		}
	}
	if k >= len(tp.Res) {
		w.locked = append(w.locked, tp.Res...)
		return w
	}
	if delayed && k > 0 {
		last := tp.Res[k-1]
		minDep := last.Iv.End
		if readyAt > minDep {
			minDep = readyAt
		}
		if minDep > last.Iv.End || readyAt > tp.Res[k].Iv.Start {
			w.locked = append(w.locked, tp.Res[:k-1]...)
			w.subPath = tp.Path[k-1:]
			w.t0 = last.Iv.Start
			w.opts = PlanOptions{FixedStart: true, FirstMinDep: minDep, FirstDwell: true}
			w.origTail = tp.Res[k-1:]
			return w
		}
		// 延误已被原方案吸收，按普通列车处理。
	}
	if k > 0 {
		last := tp.Res[k-1]
		seg := net.mustSegment(last.Seg)
		if seg.Station && last.Iv.End >= now {
			w.locked = append(w.locked, tp.Res[:k-1]...)
			w.subPath = tp.Path[k-1:]
			w.t0 = last.Iv.Start
			w.opts = PlanOptions{FixedStart: true, FirstMinDep: last.Iv.End}
			w.origTail = tp.Res[k-1:]
			return w
		}
	}
	w.locked = append(w.locked, tp.Res[:k]...)
	w.subPath = tp.Path[k:]
	w.t0 = tp.Res[k].Iv.Start
	if delayed && readyAt > w.t0 {
		w.t0 = readyAt
	}
	w.origTail = tp.Res[k:]
	return w
}

func computeWindows(net *Network, req ReplanRequest) ([]trainWindow, int, error) {
	delayedIdx := -1
	for i, tp := range req.Plans {
		if len(tp.Res) != len(tp.Path) {
			return nil, -1, fmt.Errorf("列车 %s 的预约数与路径段数不符", tp.Train)
		}
		if tp.Train == req.Delayed {
			delayedIdx = i
		}
	}
	if delayedIdx < 0 {
		return nil, -1, fmt.Errorf("找不到延误列车 %q", req.Delayed)
	}
	wins := make([]trainWindow, len(req.Plans))
	for i, tp := range req.Plans {
		wins[i] = computeWindow(net, tp, req.Now, req.ReadyAt, i == delayedIdx)
	}
	return wins, delayedIdx, nil
}

// Replan 在扰动后重优化各列车的可调整后缀。
// 目标按字典序：先最少受改列车数，再最小总延误（相对原方案到达时刻的正增量之和）。
// 已承诺前缀（已进入轨段的占用）保持不动；最多 MaxReplanTrains 列车。
func Replan(net *Network, req ReplanRequest) (ReplanResult, error) {
	n := len(req.Plans)
	if n == 0 {
		return ReplanResult{}, fmt.Errorf("replan: 没有列车方案")
	}
	if n > MaxReplanTrains {
		return ReplanResult{}, fmt.Errorf("replan: 最多支持 %d 列车，收到 %d", MaxReplanTrains, n)
	}
	wins, delayedIdx, err := computeWindows(net, req)
	if err != nil {
		return ReplanResult{}, err
	}

	bestChanged := n + 1
	var bestDelay Time
	var bestTails map[int][]Reservation

	// 枚举所有包含延误列车的子集，再枚举子集内的放置次序。
	for mask := 1; mask < (1 << n); mask++ {
		if mask&(1<<delayedIdx) == 0 {
			continue
		}
		if bits.OnesCount(uint(mask)) > bestChanged {
			continue
		}
		var members []int
		for i := 0; i < n; i++ {
			if mask&(1<<i) != 0 {
				members = append(members, i)
			}
		}
		for _, perm := range permutations(members) {
			tails, changed, delay, ok := tryPlacement(net, req, wins, mask, perm)
			if !ok {
				continue
			}
			if changed < bestChanged || (changed == bestChanged && delay < bestDelay) {
				bestChanged, bestDelay, bestTails = changed, delay, tails
			}
		}
	}
	if bestTails == nil {
		return ReplanResult{}, fmt.Errorf("replan: 时域 %d 内无可行方案", req.Horizon)
	}

	res := ReplanResult{TotalDelay: bestDelay}
	for i, tp := range req.Plans {
		tail, ok := bestTails[i]
		if !ok {
			res.Plans = append(res.Plans, tp)
			continue
		}
		newRes := make([]Reservation, 0, len(tp.Res))
		newRes = append(newRes, wins[i].locked...)
		newRes = append(newRes, tail...)
		if !equalReservations(newRes, tp.Res) {
			res.Changed = append(res.Changed, tp.Train)
		}
		res.Plans = append(res.Plans, TrainPlan{Train: tp.Train, Path: tp.Path, Res: newRes})
	}
	return res, nil
}

// tryPlacement 按 perm 次序为子集内列车逐个放置最早可行后缀。
func tryPlacement(net *Network, req ReplanRequest, wins []trainWindow, mask int, perm []int) (map[int][]Reservation, int, Time, bool) {
	table := NewTable()
	for i := range wins {
		table.Add(wins[i].locked...)
		if mask&(1<<i) == 0 {
			table.Add(wins[i].origTail...)
		}
	}
	tails := map[int][]Reservation{}
	for _, i := range perm {
		w := wins[i]
		if len(w.subPath) == 0 {
			tails[i] = nil
			continue
		}
		p := newPlanner(net, table, req.Horizon)
		ivs, ok := p.plan(w.subPath, w.t0, w.opts)
		if !ok {
			return nil, 0, 0, false
		}
		rs := make([]Reservation, len(w.subPath))
		for j, seg := range w.subPath {
			rs[j] = Reservation{Train: req.Plans[i].Train, Seg: seg, Iv: ivs[j]}
		}
		if err := table.Reserve(rs...); err != nil {
			return nil, 0, 0, false
		}
		tails[i] = rs
	}
	changed := 0
	var delay Time
	for i, tail := range tails {
		newRes := make([]Reservation, 0, len(req.Plans[i].Res))
		newRes = append(newRes, wins[i].locked...)
		newRes = append(newRes, tail...)
		if !equalReservations(newRes, req.Plans[i].Res) {
			changed++
			if d := newRes[len(newRes)-1].Iv.End - req.Plans[i].Arrival(); d > 0 {
				delay += d
			}
		}
	}
	return tails, changed, delay, true
}

func equalReservations(a, b []Reservation) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func permutations(items []int) [][]int {
	var out [][]int
	var rec func(prefix, rest []int)
	rec = func(prefix, rest []int) {
		if len(rest) == 0 {
			out = append(out, append([]int{}, prefix...))
			return
		}
		for i, v := range rest {
			next := append([]int{}, rest[:i]...)
			next = append(next, rest[i+1:]...)
			np := append(append([]int{}, prefix...), v)
			rec(np, next)
		}
	}
	rec(nil, items)
	return out
}

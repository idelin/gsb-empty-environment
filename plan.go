package trainsim

import "fmt"

// PlanOptions 控制子路径首段的进入方式（重规划边界段使用）。
type PlanOptions struct {
	// FixedStart 要求首段恰好于请求时刻进入（已承诺占用的延续）。
	FixedStart bool
	// FirstMinDep 抬高首段出发时刻下界（如延误列车须停留至 ReadyAt）。
	FirstMinDep Time
	// FirstDwell 允许首段即使不是车站也可延长停留（故障扣车）。
	FirstDwell bool
}

type solveKey struct {
	idx int
	t   Time
}

type solveResult struct {
	ivs []Interval
	ok  bool
}

// planner 针对预约表当前内容，为单车计算最早可行的占用区间序列。
// 不变式：相邻轨段占用首尾相接（enter_{i+1} == exit_i），
// 等待只能发生在车站轨段上（表现为延长该段占用）。
type planner struct {
	net     *Network
	table   *Table
	horizon Time
	memo    map[solveKey]solveResult
}

func newPlanner(net *Network, table *Table, horizon Time) *planner {
	return &planner{net: net, table: table, horizon: horizon, memo: map[solveKey]solveResult{}}
}

// gapEnd 返回从 from 起占用可延伸到的最晚时刻（下一个预约的开始或时域尽头）。
// 调用方须保证 [from, from+travel) 空闲。
func (p *planner) gapEnd(seg SegmentID, from Time) Time {
	end := p.horizon
	for _, r := range p.table.bySeg[seg] {
		if r.Iv.Start >= from && r.Iv.Start < end {
			end = r.Iv.Start
		}
	}
	return end
}

// solveEnter 在"第 i 段恰好于 e 时刻进入"的约束下，
// 计算 path[i:] 的最早可行占用序列。
func (p *planner) solveEnter(path []SegmentID, i int, e Time) ([]Interval, bool) {
	if i >= len(path) {
		return nil, true
	}
	key := solveKey{idx: i, t: e}
	if r, hit := p.memo[key]; hit {
		return r.ivs, r.ok
	}
	ivs, ok := p.computeEnter(path, i, e)
	p.memo[key] = solveResult{ivs: ivs, ok: ok}
	return ivs, ok
}

func (p *planner) computeEnter(path []SegmentID, i int, e Time) ([]Interval, bool) {
	seg := p.net.mustSegment(path[i])
	if e+seg.Travel > p.horizon {
		return nil, false
	}
	if !p.table.Free(path[i], Interval{Start: e, End: e + seg.Travel}) {
		return nil, false
	}
	if !seg.Station {
		dep := e + seg.Travel
		if rest, ok := p.solveEnter(path, i+1, dep); ok {
			return append([]Interval{{Start: e, End: dep}}, rest...), true
		}
		return nil, false
	}
	// 车站：可在空隙内延长停留，逐个候选出发时刻尝试。
	end := p.gapEnd(path[i], e)
	for dep := e + seg.Travel; dep <= end; dep++ {
		if rest, ok := p.solveEnter(path, i+1, dep); ok {
			return append([]Interval{{Start: e, End: dep}}, rest...), true
		}
	}
	return nil, false
}

// plan 计算整条（子）路径的占用序列。
func (p *planner) plan(path []SegmentID, t0 Time, opts PlanOptions) ([]Interval, bool) {
	if len(path) == 0 {
		return nil, true
	}
	seg := p.net.mustSegment(path[0])
	if !opts.FixedStart {
		// 首段进入时刻不早于 t0，逐个候选时刻尝试（更早进入不会更差）。
		for e := t0; e+seg.Travel <= p.horizon; e++ {
			if ivs, ok := p.solveEnter(path, 0, e); ok {
				return ivs, true
			}
		}
		return nil, false
	}
	// 首段必须恰好于 t0 进入（已承诺占用不可移动，只允许延长停留）。
	e := t0
	if e+seg.Travel > p.horizon || !p.table.Free(path[0], Interval{Start: e, End: e + seg.Travel}) {
		return nil, false
	}
	minDep := e + seg.Travel
	if opts.FirstMinDep > minDep {
		minDep = opts.FirstMinDep
	}
	dwell := seg.Station || opts.FirstDwell
	if !dwell {
		if minDep != e+seg.Travel {
			return nil, false
		}
		if rest, ok := p.solveEnter(path, 1, minDep); ok {
			return append([]Interval{{Start: e, End: minDep}}, rest...), true
		}
		return nil, false
	}
	end := p.gapEnd(path[0], e)
	for dep := minDep; dep <= end; dep++ {
		if rest, ok := p.solveEnter(path, 1, dep); ok {
			return append([]Interval{{Start: e, End: dep}}, rest...), true
		}
	}
	return nil, false
}

// Scheduler 在预约表上为列车规划新路径。
type Scheduler struct {
	Net     *Network
	Table   *Table
	Horizon Time
}

// PlanPath 为 train 找到不早于 earliest 出发、沿 path 的最早可行走行方案，
// 并原子确认全部轨段预约；失败时不留下任何部分预约。
func (s *Scheduler) PlanPath(train TrainID, path []SegmentID, earliest Time) ([]Reservation, error) {
	p := newPlanner(s.Net, s.Table, s.Horizon)
	ivs, ok := p.plan(path, earliest, PlanOptions{})
	if !ok {
		return nil, fmt.Errorf("列车 %s 在时域 %d 内无可行方案", train, s.Horizon)
	}
	rs := make([]Reservation, len(path))
	for i, seg := range path {
		rs[i] = Reservation{Train: train, Seg: seg, Iv: ivs[i]}
	}
	if err := s.Table.Reserve(rs...); err != nil {
		return nil, err
	}
	return rs, nil
}

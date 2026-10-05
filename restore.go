package main

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

// maxRegions 单个候选方案枚举的费用分段数量上限, 防止异常输入导致计算爆炸。
const maxRegions = 5_000_000

var errTooManyRegions = errors.New("补事件时间范围过大, 请用 -since/-as-of 缩小范围")

// Interval 补事件时间的可行区间(Unix 秒, 闭区间; *Open 表示该端无界)。
type Interval struct {
	From     int64
	To       int64
	FromOpen bool
	ToOpen   bool
}

// Option 一种合法补事件方案及其达到某个费用极值的时间区间。
type Option struct {
	Insert    EventType
	Pos       int // 插入位置: 已观测事件序列中第 Pos 条之前(0..len)
	Intervals []Interval
	loc       *time.Location
}

// Extremum 一个费用极值(最低或最高)及对应的补事件方案。
type Extremum struct {
	FeeCents  int64
	Unbounded bool // 费用可任意大(补事件时间无界且单价为正)
	Options   []Option
}

// Analysis 分析结果。
type Analysis struct {
	Complete bool  // 记录本身完整合法, 无需补事件
	FeeCents int64 // Complete 时的唯一费用
	Min      Extremum
	Max      Extremum
}

// UniqueBill 费用是否可唯一确定为单一账单。
func (a *Analysis) UniqueBill() bool {
	return a.Complete || (!a.Max.Unbounded && a.Min.FeeCents == a.Max.FeeCents)
}

// candidate 一个合法的补事件位置。
type candidate struct {
	pos     int
	typ     EventType
	partner time.Time // 与补事件配对的已观测事件时间
	loc     *time.Location
	base    int64 // 其余完整停车段的费用之和
	lo, hi  int64 // 补事件可行时间(Unix 秒, 闭区间)
	loOpen  bool
	hiOpen  bool
}

// Analyze 在最多补 1 个事件的条件下恢复合法停车段, 并计算费用的最低/最高可能值。
// since/asOf 为可选的补事件时间范围约束(用于限制无界情形)。
func Analyze(events []Event, rule FeeRule, since, asOf *time.Time) (*Analysis, error) {
	if err := rule.validate(); err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return &Analysis{Complete: true}, nil
	}
	if len(events)%2 == 0 {
		if !isLegal(events) {
			return nil, fmt.Errorf("事件序列不合法, 缺失事件超过 1 个, 无法修复")
		}
		return &Analysis{Complete: true, FeeCents: totalFee(events, rule)}, nil
	}
	cands := findCandidates(events, rule)
	if len(cands) == 0 {
		return nil, fmt.Errorf("无法通过补 1 个事件恢复合法停车段, 缺失事件可能超过 1 个")
	}
	var feasible []candidate
	for _, c := range cands {
		if since != nil {
			if s := since.Unix(); c.loOpen || s > c.lo {
				c.lo, c.loOpen = s, false
			}
		}
		if asOf != nil {
			if a := asOf.Unix(); c.hiOpen || a < c.hi {
				c.hi, c.hiOpen = a, false
			}
		}
		if !c.loOpen && !c.hiOpen && c.lo > c.hi {
			continue
		}
		feasible = append(feasible, c)
	}
	if len(feasible) == 0 {
		return nil, fmt.Errorf("在给定的补事件时间范围内不存在可行方案")
	}

	a := &Analysis{}
	minFee := int64(math.MaxInt64)
	maxFee := int64(math.MinInt64)
	for _, c := range feasible {
		ex, err := c.extrema(rule)
		if err != nil {
			return nil, err
		}
		if ex.minFee < minFee {
			minFee = ex.minFee
			a.Min.Options = nil
		}
		if ex.minFee == minFee {
			a.Min.Options = append(a.Min.Options, Option{c.typ, c.pos, ex.minIntervals, c.loc})
		}
		if ex.maxUnbounded {
			if !a.Max.Unbounded {
				a.Max.Options = nil
				maxFee = math.MinInt64
			}
			a.Max.Unbounded = true
			full := Interval{From: c.lo, To: c.hi, FromOpen: c.loOpen, ToOpen: c.hiOpen}
			a.Max.Options = append(a.Max.Options, Option{c.typ, c.pos, []Interval{full}, c.loc})
			continue
		}
		if a.Max.Unbounded {
			continue
		}
		if ex.maxFee > maxFee {
			maxFee = ex.maxFee
			a.Max.Options = nil
		}
		if ex.maxFee == maxFee {
			a.Max.Options = append(a.Max.Options, Option{c.typ, c.pos, ex.maxIntervals, c.loc})
		}
	}
	a.Min.FeeCents = minFee
	if !a.Max.Unbounded {
		a.Max.FeeCents = maxFee
	}
	return a, nil
}

// isLegal 判断事件序列是否为完整合法的停车记录(进出交替, 先进后出)。
func isLegal(events []Event) bool {
	if len(events)%2 != 0 {
		return false
	}
	for i, e := range events {
		if e.Type != wantType(i) {
			return false
		}
	}
	return true
}

func wantType(pos int) EventType {
	if pos%2 == 0 {
		return EventIn
	}
	return EventOut
}

func totalFee(events []Event, rule FeeRule) int64 {
	var total int64
	for i := 0; i+1 < len(events); i += 2 {
		total += rule.SegmentFee(events[i].Time, events[i+1].Time)
	}
	return total
}

// findCandidates 找出所有能通过插入 1 个事件使序列合法的位置。
// 调用前提: len(events) 为奇数。
func findCandidates(events []Event, rule FeeRule) []candidate {
	n := len(events)
	var out []candidate
	for p := 0; p <= n; p++ {
		if !matchesWithInsertion(events, p) {
			continue
		}
		c := candidate{pos: p, typ: wantType(p)}
		if c.typ == EventOut {
			// 补出场, 与前一条(进场)配对
			c.partner = events[p-1].Time
			c.lo = c.partner.Unix() + 1
			if p < n {
				c.hi = events[p].Time.Unix() - 1
			} else {
				c.hiOpen = true
			}
		} else {
			// 补进场, 与后一条(出场)配对
			c.partner = events[p].Time
			c.hi = c.partner.Unix() - 1
			if p > 0 {
				c.lo = events[p-1].Time.Unix() + 1
			} else {
				c.loOpen = true
			}
		}
		c.loc = c.partner.Location()
		c.base = baseFee(events, rule, p)
		out = append(out, c)
	}
	return out
}

// matchesWithInsertion 判断在位置 p 插入 1 个事件后序列是否进出交替。
func matchesWithInsertion(events []Event, p int) bool {
	for i, e := range events {
		f := i
		if i >= p {
			f = i + 1
		}
		if e.Type != wantType(f) {
			return false
		}
	}
	return true
}

// baseFee 计算除含补事件的段之外, 其余完整停车段的费用之和。
func baseFee(events []Event, rule FeeRule, p int) int64 {
	n := len(events)
	obs := func(f int) int { // 完整序列下标 f 对应的已观测下标
		if f < p {
			return f
		}
		return f - 1
	}
	var base int64
	for f := 0; f+1 <= n; f += 2 {
		if f == p || f+1 == p {
			continue
		}
		base += rule.SegmentFee(events[obs(f)].Time, events[obs(f+1)].Time)
	}
	return base
}

// feeAt 补事件时间为 t(Unix 秒)时的总费用。
func (c candidate) feeAt(rule FeeRule, t int64) int64 {
	tt := time.Unix(t, 0).In(c.loc)
	if c.typ == EventOut {
		return c.base + rule.SegmentFee(c.partner, tt)
	}
	return c.base + rule.SegmentFee(tt, c.partner)
}

type candidateExtrema struct {
	minFee       int64
	minIntervals []Interval
	maxFee       int64
	maxIntervals []Interval
	maxUnbounded bool
}

// extrema 计算单个候选方案下费用的最小值与最大值及对应补事件时间区间。
func (c candidate) extrema(rule FeeRule) (candidateExtrema, error) {
	var res candidateExtrema
	if c.loOpen || c.hiOpen {
		full := Interval{From: c.lo, To: c.hi, FromOpen: c.loOpen, ToOpen: c.hiOpen}
		if rule.RateCents == 0 {
			// 单价为 0, 费用恒为 base
			res.minFee, res.maxFee = c.base, c.base
			res.minIntervals = []Interval{full}
			res.maxIntervals = []Interval{full}
			return res, nil
		}
		// 费用随时长单调不减且无上界
		res.maxUnbounded = true
		res.maxIntervals = []Interval{full}
		if c.loOpen && c.hiOpen { // 防御: 实际上不会发生
			res.minFee = c.feeAt(rule, c.partner.Unix())
			res.minIntervals = []Interval{full}
			return res, nil
		}
		// 最小值出现在有界端附近; 费用关于时间单调, 最小值的达成区间
		// 宽度不超过一个自然日加一个计费单位, 用窗口枚举即可精确覆盖。
		window := int64((26*time.Hour + rule.Unit) / time.Second)
		var lo, hi int64
		if c.hiOpen {
			lo, hi = c.lo, c.lo+window
		} else {
			lo, hi = c.hi-window, c.hi
		}
		regions, err := c.regions(rule, lo, hi)
		if err != nil {
			return res, err
		}
		res.minFee = math.MaxInt64
		for _, r := range regions {
			if r.fee < res.minFee {
				res.minFee = r.fee
			}
		}
		for _, r := range regions {
			if r.fee == res.minFee {
				res.minIntervals = append(res.minIntervals, Interval{From: r.lo, To: r.hi})
			}
		}
		return res, nil
	}

	regions, err := c.regions(rule, c.lo, c.hi)
	if err != nil {
		return res, err
	}
	res.minFee = math.MaxInt64
	res.maxFee = math.MinInt64
	for _, r := range regions {
		if r.fee < res.minFee {
			res.minFee = r.fee
		}
		if r.fee > res.maxFee {
			res.maxFee = r.fee
		}
	}
	for _, r := range regions {
		if r.fee == res.minFee {
			res.minIntervals = append(res.minIntervals, Interval{From: r.lo, To: r.hi})
		}
		if r.fee == res.maxFee {
			res.maxIntervals = append(res.maxIntervals, Interval{From: r.lo, To: r.hi})
		}
	}
	return res, nil
}

// feeRegion 一段费用恒定的补事件时间区间 [lo, hi](Unix 秒, 闭区间)。
type feeRegion struct {
	lo, hi int64
	fee    int64
}

// regions 将 [lo, hi] 划分为费用恒定的分段。
// 费用只在以下时间点发生变化: 计费时长跨过计费单位整数倍的时刻、自然日边界。
func (c candidate) regions(rule FeeRule, lo, hi int64) ([]feeRegion, error) {
	freeSec := int64(rule.Free / time.Second)
	unitSec := int64(rule.Unit / time.Second)
	thresholds := map[int64]struct{}{lo - 1: {}, hi: {}}
	add := func(b int64) error {
		if b >= lo-1 && b <= hi {
			thresholds[b] = struct{}{}
			if len(thresholds) > maxRegions {
				return errTooManyRegions
			}
		}
		return nil
	}
	if c.typ == EventOut {
		e := c.partner.Unix()
		for b := e + freeSec; b <= hi; b += unitSec {
			if err := add(b); err != nil {
				return nil, err
			}
		}
	} else {
		x := c.partner.Unix()
		// 补进场时计费时长随 t 减小而增大, 阈值在 x-free-1-k*unit
		for b := x - freeSec - 1; b >= lo-1; b -= unitSec {
			if err := add(b); err != nil {
				return nil, err
			}
		}
	}
	// 自然日边界: 每个午夜 m 产生阈值 m-1(跨越午夜时自然日数变化)
	t := time.Unix(lo, 0).In(c.loc)
	y, m, d := t.Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, c.loc)
	for {
		next := midnight.AddDate(0, 0, 1)
		if next.Unix() > hi {
			break
		}
		if err := add(next.Unix() - 1); err != nil {
			return nil, err
		}
		midnight = next
	}

	sorted := make([]int64, 0, len(thresholds))
	for b := range thresholds {
		sorted = append(sorted, b)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	var regions []feeRegion
	for i := 0; i+1 < len(sorted); i++ {
		rLo := sorted[i] + 1
		if rLo < lo {
			rLo = lo
		}
		rHi := sorted[i+1]
		if rHi > hi {
			rHi = hi
		}
		if rLo > rHi {
			continue
		}
		fee := c.feeAt(rule, rLo)
		if n := len(regions); n > 0 && regions[n-1].fee == fee && regions[n-1].hi == rLo-1 {
			regions[n-1].hi = rHi
		} else {
			regions = append(regions, feeRegion{lo: rLo, hi: rHi, fee: fee})
		}
	}
	return regions, nil
}

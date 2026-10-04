package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// stateBatch 求解过程中的批次状态。
type stateBatch struct {
	id        string
	source    string
	remaining int
	expiry    int
}

type memoNode struct {
	best int
	move []int // 该状态下最优的本时段兑换份数（nil 表示不兑换）
}

// Solver 按期段做带记忆化的搜索：每个时段枚举兑换组合，
// 返还积分生成新批次在下一时段到账，因此同一时段内不存在
// "兑换 -> 返还 -> 再兑换" 的循环。
type Solver struct {
	in             Input
	offersByPeriod [][]Offer
	memo           map[string]memoNode
	states         int
	maxStates      int
	truncated      bool
}

func NewSolver(in Input) *Solver {
	s := &Solver{
		in:             in,
		offersByPeriod: make([][]Offer, in.Periods+1),
		memo:           make(map[string]memoNode),
		maxStates:      500000,
	}
	for _, o := range in.Offers {
		s.offersByPeriod[o.Period] = append(s.offersByPeriod[o.Period], o)
	}
	for t := range s.offersByPeriod {
		sort.Slice(s.offersByPeriod[t], func(i, j int) bool {
			return s.offersByPeriod[t][i].ID < s.offersByPeriod[t][j].ID
		})
	}
	return s
}

// Solve 返回最大总兑换价值。
func (s *Solver) Solve() int {
	var batches []stateBatch
	for _, b := range s.in.Batches {
		if b.Amount > 0 {
			batches = append(batches, stateBatch{b.ID, b.Source, b.Amount, b.ExpirePeriod})
		}
	}
	return s.solve(1, batches)
}

func (s *Solver) solve(t int, batches []stateBatch) int {
	if t > s.in.Periods {
		return 0
	}
	batches = normalizeBatches(batches, t)
	key := stateKey(t, batches)
	if n, ok := s.memo[key]; ok {
		return n.best
	}
	s.states++
	if s.states > s.maxStates {
		s.truncated = true
		s.memo[key] = memoNode{}
		return 0
	}
	offers := s.offersByPeriod[t]
	best := 0
	var bestMove []int
	units := make([]int, len(offers))
	var rec func(i, value int)
	rec = func(i, value int) {
		if i == len(offers) {
			ded, ok := pay(batches, offers, units)
			if !ok {
				return
			}
			next := applyDeductions(batches, ded)
			if t+1 <= s.in.Periods {
				next = append(next, rebateBatches(t, offers, units)...)
			}
			if total := value + s.solve(t+1, next); total > best {
				best = total
				bestMove = append([]int(nil), units...)
			}
			return
		}
		maxU := offers[i].Inventory
		if b := eligiblePoints(batches, offers[i]) / offers[i].Cost; b < maxU {
			maxU = b
		}
		for u := 0; u <= maxU; u++ {
			units[i] = u
			rec(i+1, value+u*offers[i].Value)
		}
		units[i] = 0
	}
	rec(0, 0)
	s.memo[key] = memoNode{best: best, move: bestMove}
	return best
}

// normalizeBatches 丢弃已耗尽与已到期的批次，并按 id 排序使状态可比较。
func normalizeBatches(batches []stateBatch, t int) []stateBatch {
	out := make([]stateBatch, 0, len(batches))
	for _, b := range batches {
		if b.remaining > 0 && b.expiry >= t {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

func stateKey(t int, batches []stateBatch) string {
	var sb strings.Builder
	sb.WriteString(strconv.Itoa(t))
	for _, b := range batches {
		sb.WriteString("|")
		sb.WriteString(b.id)
		sb.WriteString(":")
		sb.WriteString(strconv.Itoa(b.remaining))
	}
	return sb.String()
}

func eligiblePoints(batches []stateBatch, o Offer) int {
	sum := 0
	for _, b := range batches {
		if sourceAllowed(o, b.source) {
			sum += b.remaining
		}
	}
	return sum
}

func applyDeductions(batches []stateBatch, ded [][]int) []stateBatch {
	used := make([]int, len(batches))
	for j := range ded {
		for i, a := range ded[j] {
			used[i] += a
		}
	}
	out := make([]stateBatch, 0, len(batches))
	for i, b := range batches {
		b.remaining -= used[i]
		out = append(out, b)
	}
	return out
}

// rebateBatches 生成下一时段到账的返还积分批次。
func rebateBatches(t int, offers []Offer, units []int) []stateBatch {
	var out []stateBatch
	for j, o := range offers {
		if units[j] > 0 && o.Rebate > 0 {
			out = append(out, stateBatch{
				id:        fmt.Sprintf("rebate#%d:%s", t+1, o.ID),
				source:    o.RebateSource,
				remaining: units[j] * o.Rebate,
				expiry:    o.RebateExpire,
			})
		}
	}
	return out
}

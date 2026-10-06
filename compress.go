package main

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

type block struct {
	lo, hi *big.Rat
	kind   string
}

// Segment 一段被压缩的空闲区间。
type Segment struct {
	Start, End   *big.Rat
	MStart, MEnd *big.Rat
	Reason       string
}

// Skipped 未被压缩的区间及理由。
type Skipped struct {
	Start, End *big.Rat
	Reason     string
}

// Plan 压缩规划：共享映射 + 压缩段 + 跳过段。
type Plan struct {
	Map      *PiecewiseMap
	Segments []Segment
	Skipped  []Skipped
}

// subtract 从 (a,b) 中扣除受保护区间（须按 lo 排序），返回剩余区间片。
func subtract(a, b *big.Rat, blocks []block) [][2]*big.Rat {
	var pieces [][2]*big.Rat
	cur := new(big.Rat).Set(a)
	for _, bl := range blocks {
		if bl.hi.Cmp(cur) <= 0 {
			continue
		}
		if bl.lo.Cmp(b) >= 0 {
			break
		}
		if bl.lo.Cmp(cur) > 0 {
			pieces = append(pieces, [2]*big.Rat{new(big.Rat).Set(cur), new(big.Rat).Set(bl.lo)})
		}
		if bl.hi.Cmp(cur) > 0 {
			cur = new(big.Rat).Set(bl.hi)
		}
	}
	if cur.Cmp(b) < 0 {
		pieces = append(pieces, [2]*big.Rat{cur, new(big.Rat).Set(b)})
	}
	return pieces
}

func kindsIn(a, b *big.Rat, blocks []block) string {
	seen := map[string]bool{}
	var ks []string
	for _, bl := range blocks {
		if bl.lo.Cmp(b) < 0 && bl.hi.Cmp(a) > 0 && !seen[bl.kind] {
			seen[bl.kind] = true
			ks = append(ks, bl.kind)
		}
	}
	return strings.Join(ks, ", ")
}

// planCompression 找出可压缩空闲段并构造共享的单调分段线性映射。
// 空闲段 = 相邻事件时刻之间、扣除全部受保护区间后的剩余部分。
func planCompression(spec *Spec) (*Plan, error) {
	cfg := &spec.Config
	zero := ratFromInt(0)
	one := ratFromInt(1)
	if cfg.Ratio.R == nil || cfg.Ratio.R.Cmp(zero) <= 0 || cfg.Ratio.R.Cmp(one) > 0 {
		return nil, fmt.Errorf("ratio 必须在 (0,1] 内，得到 %s", cfg.Ratio)
	}
	if cfg.MinKeep.R == nil || cfg.MinKeep.R.Cmp(zero) <= 0 {
		return nil, fmt.Errorf("min_keep 必须 > 0（保证斜率为正、映射可逆）")
	}
	if cfg.MinIdle.R == nil || cfg.MinIdle.R.Cmp(zero) < 0 {
		return nil, fmt.Errorf("min_idle 不能为负")
	}

	plan := &Plan{Map: &PiecewiseMap{}}
	if len(spec.Events) == 0 {
		return plan, nil
	}

	// 时间轴范围：首末事件时刻之间。
	lo := spec.Events[0].T.R
	hi := spec.Events[0].T.R
	seen := map[string]bool{}
	var times []*big.Rat
	for _, e := range spec.Events {
		if e.T.R.Cmp(lo) < 0 {
			lo = e.T.R
		}
		if e.T.R.Cmp(hi) > 0 {
			hi = e.T.R
		}
		key := e.T.R.RatString()
		if !seen[key] {
			seen[key] = true
			times = append(times, e.T.R)
		}
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Cmp(times[j]) < 0 })

	// 受保护区间：裁剪到 [lo,hi] 并按起点排序。
	var blocks []block
	for _, p := range spec.Protected {
		if p.Start.R == nil || p.End.R == nil {
			continue
		}
		s, e := p.Start.R, p.End.R
		if s.Cmp(e) >= 0 {
			continue
		}
		s = maxRat(s, lo)
		e = minRat(e, hi)
		if s.Cmp(e) >= 0 {
			continue
		}
		kind := p.Kind
		if p.Label != "" {
			kind = kind + "(" + p.Label + ")"
		}
		blocks = append(blocks, block{lo: s, hi: e, kind: kind})
	}
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].lo.Cmp(blocks[j].lo) < 0 })

	// 逐空闲段扣除受保护区间，评估可压缩性。
	type cand struct {
		a, b, target *big.Rat
		avoided      string
	}
	var cands []cand
	for i := 0; i+1 < len(times); i++ {
		a, b := times[i], times[i+1]
		pieces := subtract(a, b, blocks)
		if len(pieces) == 0 {
			plan.Skipped = append(plan.Skipped, Skipped{
				Start: a, End: b,
				Reason: fmt.Sprintf("整段被受保护区间（%s）覆盖，不可压缩", kindsIn(a, b, blocks)),
			})
			continue
		}
		avoided := kindsIn(a, b, blocks)
		for _, pc := range pieces {
			length := rsub(pc[1], pc[0])
			if length.Cmp(cfg.MinIdle.R) <= 0 {
				plan.Skipped = append(plan.Skipped, Skipped{
					Start: pc[0], End: pc[1],
					Reason: fmt.Sprintf("空闲时长 %s 未超过阈值 %s，保持原速", length, cfg.MinIdle),
				})
				continue
			}
			target := rmul(length, cfg.Ratio.R)
			if target.Cmp(cfg.MinKeep.R) < 0 {
				target = new(big.Rat).Set(cfg.MinKeep.R)
			}
			if target.Cmp(length) >= 0 {
				plan.Skipped = append(plan.Skipped, Skipped{
					Start: pc[0], End: pc[1],
					Reason: fmt.Sprintf("按比率 %s 压缩无收益（目标 %s >= 原长 %s），保持原速", cfg.Ratio, target, length),
				})
				continue
			}
			cands = append(cands, cand{a: pc[0], b: pc[1], target: target, avoided: avoided})
		}
	}

	// 构造共享分段线性映射：压缩段斜率 = target/length ∈ (0,1)，其余斜率 1。
	knots := []Knot{{T: new(big.Rat).Set(lo), M: new(big.Rat).Set(lo)}}
	addKnot := func(t, m *big.Rat) {
		last := knots[len(knots)-1]
		if last.T.Cmp(t) == 0 {
			return // 相邻压缩段共享端点，映射值必相同（节省量连续）
		}
		knots = append(knots, Knot{T: t, M: m})
	}
	saved := ratFromInt(0)
	for _, c := range cands {
		length := rsub(c.b, c.a)
		ma := rsub(c.a, saved)
		mb := radd(ma, c.target)
		addKnot(new(big.Rat).Set(c.a), ma)
		addKnot(new(big.Rat).Set(c.b), mb)
		saved = radd(saved, rsub(length, c.target))
		reason := fmt.Sprintf("空闲 %s 超过阈值 %s，斜率 %s，%s -> %s，节省 %s",
			length, cfg.MinIdle, rquo(c.target, length), length, c.target, rsub(length, c.target))
		if c.avoided != "" {
			reason += "；已避开受保护段（" + c.avoided + "）"
		}
		plan.Segments = append(plan.Segments, Segment{
			Start: c.a, End: c.b, MStart: ma, MEnd: mb, Reason: reason,
		})
	}
	addKnot(new(big.Rat).Set(hi), rsub(hi, saved))
	plan.Map = &PiecewiseMap{Knots: knots}
	return plan, nil
}

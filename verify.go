package main

import (
	"fmt"
	"sort"
)

func (r *Resolver) universe() []string {
	set := map[string]bool{}
	var queue []string
	add := func(name string) {
		if !set[name] {
			set[name] = true
			queue = append(queue, name)
		}
	}
	for _, rt := range r.roots {
		add(rt.name)
	}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, src := range []string{SourcePrivate, SourcePublic} {
			for _, pv := range r.idx[src][p] {
				for _, d := range pv.deps {
					add(d.name)
				}
			}
		}
	}
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (r *Resolver) validAssignment(as map[string]choice) bool {
	for _, rt := range r.roots {
		c, ok := as[rt.name]
		if !ok || !rt.rng.Allows(c.ver) {
			return false
		}
	}
	for pkg, c := range as {
		for _, d := range r.depsOf(pkg, c) {
			dc, ok := as[d.name]
			if !ok || !d.rng.Allows(dc.ver) {
				return false
			}
		}
	}
	return true
}

func (r *Resolver) EnumerateValid(limit int) ([]map[string]choice, error) {
	universe := r.universe()
	opts := make([][]choice, len(universe))
	total := 1
	for i, p := range universe {
		opts[i] = append(opts[i], choice{})
		opts[i] = append(opts[i], r.candidates(p)...)
		total *= len(opts[i])
		if total > limit {
			return nil, fmt.Errorf("组合空间超过上限 %d，拒绝穷举（图太大）", limit)
		}
	}
	var valids []map[string]choice
	cur := make([]choice, len(universe))
	var rec func(i int)
	rec = func(i int) {
		if i == len(universe) {
			as := map[string]choice{}
			for j, c := range cur {
				if c.source != "" {
					as[universe[j]] = c
				}
			}
			if r.validAssignment(as) {
				valids = append(valids, as)
			}
			return
		}
		for _, o := range opts[i] {
			cur[i] = o
			rec(i + 1)
		}
	}
	rec(0)
	return valids, nil
}

func better(universe []string, a, b map[string]choice) bool {
	for _, p := range universe {
		ca, oka := a[p]
		cb, okb := b[p]
		if !oka && !okb {
			continue
		}
		if oka != okb {
			return !oka
		}
		if cmp := CompareVersion(ca.ver, cb.ver); cmp != 0 {
			return cmp > 0
		}
		if ca.source != cb.source {
			return sourceRank(ca.source) < sourceRank(cb.source)
		}
	}
	return false
}

func bestAssignment(universe []string, valids []map[string]choice) map[string]choice {
	best := valids[0]
	for _, v := range valids[1:] {
		if better(universe, v, best) {
			best = v
		}
	}
	return best
}

func choiceEq(a, b choice) bool {
	return a.source == b.source && CompareVersion(a.ver, b.ver) == 0
}

func equalAssign(a, b map[string]choice) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok || !choiceEq(va, vb) {
			return false
		}
	}
	return true
}

func (r *Resolver) prefixChoices(pkg string) map[string]choice {
	out := map[string]choice{}
	for _, p := range r.order {
		if p == pkg {
			break
		}
		out[p] = r.assigned[p]
	}
	return out
}

func Verify(in *Input) error {
	r, err := NewResolver(in)
	if err != nil {
		return err
	}
	ok := r.Resolve()
	valids, err := r.EnumerateValid(2_000_000)
	if err != nil {
		return err
	}
	if !ok {
		if len(valids) > 0 {
			return fmt.Errorf("解析器判定无解，但穷举找到 %d 个有效装配", len(valids))
		}
		return nil
	}
	if len(valids) == 0 {
		return fmt.Errorf("解析器给出了解，但穷举未找到任何有效装配")
	}
	universe := r.universe()
	best := bestAssignment(universe, valids)
	if !equalAssign(best, r.assigned) {
		return fmt.Errorf("解析结果与穷举最优不一致: 解析=%v 穷举=%v", r.assigned, best)
	}
	for pkg, rejs := range r.Rejections {
		prefix := r.prefixChoices(pkg)
		for _, rej := range rejs {
			want := choice{ver: rej.Ver, source: rej.Source}
			for _, v := range valids {
				match := true
				for p, c := range prefix {
					vc, ok := v[p]
					if !ok || !choiceEq(vc, c) {
						match = false
						break
					}
				}
				if !match {
					continue
				}
				if vc, ok := v[pkg]; ok && choiceEq(vc, want) {
					return fmt.Errorf("回退记录有误: 包 %s 的候选 %s 被标记为不可行，但穷举找到了含它的有效装配", pkg, want)
				}
			}
		}
	}
	return nil
}

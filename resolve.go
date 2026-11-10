package main

import (
	"fmt"
	"sort"
	"strings"
)

type parsedDep struct {
	name string
	rng  Range
}

type parsedVersion struct {
	ver  Version
	deps []parsedDep
}

type constraint struct {
	rng  Range
	from string
}

type choice struct {
	ver    Version
	source string
}

func (c choice) String() string { return fmt.Sprintf("%s[%s]", c.ver, c.source) }

type rootReq struct {
	name string
	rng  Range
}

type Rejection struct {
	Ver    Version
	Source string
	Chain  []string
	depth  int
}

type Resolver struct {
	in          *Input
	isPrivate   map[string]bool
	idx         map[string]map[string][]parsedVersion
	roots       []rootReq
	constraints map[string][]constraint
	assigned    map[string]choice
	order       []string
	Rejections  map[string][]Rejection
}

func sourceRank(s string) int {
	switch s {
	case SourcePrivate:
		return 0
	case SourcePublic:
		return 1
	}
	return 2
}

func NewResolver(in *Input) (*Resolver, error) {
	r := &Resolver{
		in:          in,
		isPrivate:   map[string]bool{},
		idx:         map[string]map[string][]parsedVersion{},
		constraints: map[string][]constraint{},
		assigned:    map[string]choice{},
		Rejections:  map[string][]Rejection{},
	}
	for _, p := range in.PrivatePackages {
		r.isPrivate[p] = true
	}
	for src, s := range in.Sources {
		if r.idx[src] == nil {
			r.idx[src] = map[string][]parsedVersion{}
		}
		for pkg, vers := range s.Packages {
			for vs, vi := range vers {
				v, err := ParseVersion(vs)
				if err != nil {
					return nil, fmt.Errorf("源 %s 包 %s: %w", src, pkg, err)
				}
				pv := parsedVersion{ver: v}
				for dn, dr := range vi.Deps {
					rng, err := ParseRange(dr)
					if err != nil {
						return nil, fmt.Errorf("源 %s 包 %s@%s 依赖 %s: %w", src, pkg, vs, dn, err)
					}
					pv.deps = append(pv.deps, parsedDep{name: dn, rng: rng})
				}
				sort.Slice(pv.deps, func(i, j int) bool { return pv.deps[i].name < pv.deps[j].name })
				r.idx[src][pkg] = append(r.idx[src][pkg], pv)
			}
			sort.Slice(r.idx[src][pkg], func(i, j int) bool {
				return CompareVersion(r.idx[src][pkg][i].ver, r.idx[src][pkg][j].ver) > 0
			})
		}
	}
	for name, rs := range in.Roots {
		rng, err := ParseRange(rs)
		if err != nil {
			return nil, fmt.Errorf("root 约束 %s: %w", name, err)
		}
		r.roots = append(r.roots, rootReq{name: name, rng: rng})
	}
	sort.Slice(r.roots, func(i, j int) bool { return r.roots[i].name < r.roots[j].name })
	return r, nil
}

func (r *Resolver) candidates(pkg string) []choice {
	var out []choice
	for _, src := range []string{SourcePrivate, SourcePublic} {
		for _, pv := range r.idx[src][pkg] {
			out = append(out, choice{ver: pv.ver, source: src})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if c := CompareVersion(out[i].ver, out[j].ver); c != 0 {
			return c > 0
		}
		return sourceRank(out[i].source) < sourceRank(out[j].source)
	})
	return out
}

func (r *Resolver) depsOf(pkg string, c choice) []parsedDep {
	for _, pv := range r.idx[c.source][pkg] {
		if CompareVersion(pv.ver, c.ver) == 0 {
			return pv.deps
		}
	}
	return nil
}

func (r *Resolver) maxVersion(pkg, src string) (Version, bool) {
	vs := r.idx[src][pkg]
	if len(vs) == 0 {
		return Version{}, false
	}
	return vs[0].ver, true
}

func constraintList(cons []constraint) string {
	parts := make([]string, 0, len(cons))
	for _, c := range cons {
		parts = append(parts, fmt.Sprintf("%s (来自 %s)", c.rng, c.from))
	}
	return strings.Join(parts, "; ")
}

func (r *Resolver) addRejection(pkg string, c choice, chain []string) {
	r.Rejections[pkg] = append(r.Rejections[pkg], Rejection{Ver: c.ver, Source: c.source, Chain: chain, depth: len(r.order)})
}

func (r *Resolver) rollbackRejections(depth int) {
	for pkg, rejs := range r.Rejections {
		cut := len(rejs)
		for cut > 0 && rejs[cut-1].depth > depth {
			cut--
		}
		if cut == 0 {
			delete(r.Rejections, pkg)
		} else {
			r.Rejections[pkg] = rejs[:cut]
		}
	}
}

func (r *Resolver) nextPkg() string {
	best := ""
	for pkg, cons := range r.constraints {
		if len(cons) == 0 {
			continue
		}
		if _, ok := r.assigned[pkg]; ok {
			continue
		}
		if best == "" || pkg < best {
			best = pkg
		}
	}
	return best
}

func (r *Resolver) depsConflict(pkg string, c choice) []string {
	for _, d := range r.depsOf(pkg, c) {
		if d.name == pkg {
			if !d.rng.Allows(c.ver) {
				return []string{fmt.Sprintf("%s 要求自身 %s，与候选版本 %s 矛盾", pkg, d.rng, c.ver)}
			}
			continue
		}
		if a, ok := r.assigned[d.name]; ok && !d.rng.Allows(a.ver) {
			return []string{
				fmt.Sprintf("%s 要求 %s %s", c, d.name, d.rng),
				fmt.Sprintf("%s 已定为 %s（既有约束: %s）", d.name, choice{ver: a.ver, source: a.source}, constraintList(r.constraints[d.name])),
			}
		}
	}
	return nil
}

type mark struct {
	pkg string
	n   int
}

func (r *Resolver) pushDeps(pkg string, c choice) []mark {
	var marks []mark
	from := fmt.Sprintf("%s@%s", pkg, c)
	for _, d := range r.depsOf(pkg, c) {
		if d.name == pkg {
			continue
		}
		marks = append(marks, mark{pkg: d.name, n: len(r.constraints[d.name])})
		r.constraints[d.name] = append(r.constraints[d.name], constraint{rng: d.rng, from: from})
	}
	return marks
}

func (r *Resolver) popDeps(marks []mark) {
	for _, m := range marks {
		r.constraints[m.pkg] = r.constraints[m.pkg][:m.n]
	}
}

func indent(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = "  " + l
	}
	return out
}

func (r *Resolver) Resolve() bool {
	for _, rt := range r.roots {
		r.constraints[rt.name] = append(r.constraints[rt.name], constraint{rng: rt.rng, from: "root"})
	}
	ok, _ := r.solve()
	return ok
}

func (r *Resolver) solve() (bool, []string) {
	pkg := r.nextPkg()
	if pkg == "" {
		return true, nil
	}
	cons := r.constraints[pkg]
	var viable []choice
	for _, c := range r.candidates(pkg) {
		ok := true
		for _, con := range cons {
			if !con.rng.Allows(c.ver) {
				ok = false
				break
			}
		}
		if !ok {
			r.addRejection(pkg, c, []string{
				fmt.Sprintf("版本 %s 不满足已累积约束: %s", c.ver, constraintList(cons)),
			})
			continue
		}
		viable = append(viable, c)
	}
	if len(viable) == 0 {
		return false, []string{fmt.Sprintf("%s: 没有任何版本满足约束 [%s]", pkg, constraintList(cons))}
	}
	type failedCand struct {
		c     choice
		chain []string
	}
	var failed []failedCand
	for _, c := range viable {
		if conflict := r.depsConflict(pkg, c); conflict != nil {
			r.addRejection(pkg, c, conflict)
			failed = append(failed, failedCand{c: c, chain: conflict})
			continue
		}
		r.assigned[pkg] = c
		r.order = append(r.order, pkg)
		marks := r.pushDeps(pkg, c)
		ok, chain := r.solve()
		if ok {
			return true, nil
		}
		r.popDeps(marks)
		delete(r.assigned, pkg)
		r.order = r.order[:len(r.order)-1]
		r.rollbackRejections(len(r.order))
		full := append([]string{fmt.Sprintf("选择 %s 后依赖闭包无法完成:", c)}, indent(chain)...)
		r.addRejection(pkg, c, full)
		failed = append(failed, failedCand{c: c, chain: full})
	}
	lines := []string{fmt.Sprintf("%s: 所有候选均无法完成整图（约束: %s）", pkg, constraintList(cons))}
	for _, f := range failed {
		lines = append(lines, fmt.Sprintf("  候选 %s: %s", f.c, f.chain[0]))
	}
	return false, lines
}

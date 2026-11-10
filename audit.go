package fwaudit

import (
	"bytes"
	"slices"
	"sort"
)

// Finding reports one reachable out-of-bounds read.
type Finding struct {
	Instr int     // instruction index of the vulnerable load
	Array int     // array that is indexed out of bounds
	Index uint8   // out-of-bounds index value produced by Input
	Input []uint8 // lexicographically smallest input replaying the fault
}

// Report is the outcome of Audit.
type Report struct {
	Findings  []Finding // one per vulnerable load, ordered by instruction
	Paths     int       // execution paths explored
	Truncated bool      // true if the path cap was hit (results partial)
}

// Audit explores every feasible execution path of p and returns the
// reachable out-of-bounds reads. For each vulnerable load it computes the
// lexicographically smallest input that drives execution to that load with
// an out-of-bounds index; such an input replays under Run.
func Audit(p *Program) (*Report, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	a := &auditor{
		p:        p,
		maxPaths: 1 << 16,
		best:     make(map[int][]uint8),
	}
	regs := make([]*expr, p.NRegs)
	for i := range regs {
		regs[i] = &expr{kind: eConst}
	}
	a.dfs(p.Entry, regs, nil, 0)

	rep := &Report{Paths: a.paths, Truncated: a.paths >= a.maxPaths}
	sites := make([]int, 0, len(a.best))
	for pc := range a.best {
		sites = append(sites, pc)
	}
	sort.Ints(sites)
	for _, pc := range sites {
		in := a.best[pc]
		f := Finding{Instr: pc, Array: p.Instrs[pc].Arr, Input: in}
		if oob, err := Run(p, in); err == nil && oob != nil {
			f.Index = oob.Index
		}
		rep.Findings = append(rep.Findings, f)
	}
	return rep, nil
}

// --- symbolic expressions over input bytes ---

type exprKind int

const (
	eConst exprKind = iota // constant val
	eInput                 // input byte at pos
	eAnd                   // sub & val
	eAdd                   // sub + val (mod 256)
	eLoad                  // Arrays[arr][sub] (in-bounds by path constraint)
)

type expr struct {
	kind exprKind
	val  uint8
	pos  int
	arr  int
	sub  *expr
}

// bitset is a set of byte values.
type bitset [4]uint64

func fullSet() bitset { return bitset{^uint64(0), ^uint64(0), ^uint64(0), ^uint64(0)} }
func singleton(v uint8) bitset {
	var s bitset
	s[v/64] |= 1 << (v % 64)
	return s
}
func (s bitset) has(v uint8) bool { return s[v/64]&(1<<(v%64)) != 0 }
func (s *bitset) add(v uint8)     { s[v/64] |= 1 << (v % 64) }

func (s bitset) anyNonzero() bool {
	t := s
	t[0] &^= 1
	return t != bitset{}
}

func (s bitset) hasGE(lo uint8) bool {
	for v := 255; v >= int(lo); v-- {
		if s.has(uint8(v)) {
			return true
		}
	}
	return false
}

func (s bitset) hasLT(bound uint8) bool {
	for v := 0; v < int(bound); v++ {
		if s.has(uint8(v)) {
			return true
		}
	}
	return false
}

func mapSet(s bitset, f func(uint8) uint8) bitset {
	var out bitset
	for v := 0; v < 256; v++ {
		if s.has(uint8(v)) {
			out.add(f(uint8(v)))
		}
	}
	return out
}

// --- path conditions ---

type condKind int

const (
	cZero    condKind = iota // expr == 0
	cNonzero                 // expr != 0
	cLT                      // expr < bound
)

type cond struct {
	kind  condKind
	e     *expr
	bound uint8
}

// --- solver ---

// solveCtx holds a partial assignment of input positions to byte values
// (-1 = unassigned).
type solveCtx struct {
	arrays [][]uint8
	slots  []int // input positions, ascending
	posIdx map[int]int
	assign []int
}

// evalSet over-approximates the possible values of e under the partial
// assignment: every concretely reachable value is in the returned set.
func (c *solveCtx) evalSet(e *expr) bitset {
	switch e.kind {
	case eConst:
		return singleton(e.val)
	case eInput:
		if i, ok := c.posIdx[e.pos]; ok && c.assign[i] >= 0 {
			return singleton(uint8(c.assign[i]))
		}
		return fullSet()
	case eAnd:
		return mapSet(c.evalSet(e.sub), func(v uint8) uint8 { return v & e.val })
	case eAdd:
		return mapSet(c.evalSet(e.sub), func(v uint8) uint8 { return v + e.val })
	case eLoad:
		idx := c.evalSet(e.sub)
		var out bitset
		arr := c.arrays[e.arr]
		for v := 0; v < len(arr); v++ {
			if idx.has(uint8(v)) {
				out.add(arr[v])
			}
		}
		return out
	}
	return bitset{}
}

// eval computes e concretely; ok is false if an embedded load faults.
func (c *solveCtx) eval(e *expr) (v uint8, ok bool) {
	switch e.kind {
	case eConst:
		return e.val, true
	case eInput:
		return uint8(c.assign[c.posIdx[e.pos]]), true
	case eAnd:
		if v, ok = c.eval(e.sub); ok {
			v &= e.val
		}
		return v, ok
	case eAdd:
		if v, ok = c.eval(e.sub); ok {
			v += e.val
		}
		return v, ok
	case eLoad:
		idx, ok := c.eval(e.sub)
		if !ok {
			return 0, false
		}
		arr := c.arrays[e.arr]
		if int(idx) >= len(arr) {
			return 0, false
		}
		return arr[idx], true
	}
	return 0, false
}

// feasible reports whether the partial assignment could still extend to a
// solution. It is sound: false means no completion satisfies conds/target.
func (c *solveCtx) feasible(conds []cond, target *expr, lo uint8) bool {
	for _, cd := range conds {
		s := c.evalSet(cd.e)
		switch cd.kind {
		case cZero:
			if !s.has(0) {
				return false
			}
		case cNonzero:
			if !s.anyNonzero() {
				return false
			}
		case cLT:
			if !s.hasLT(cd.bound) {
				return false
			}
		}
	}
	return c.evalSet(target).hasGE(lo)
}

// check verifies a complete assignment exactly.
func (c *solveCtx) check(conds []cond, target *expr, lo uint8) bool {
	for _, cd := range conds {
		v, ok := c.eval(cd.e)
		if !ok {
			return false
		}
		switch cd.kind {
		case cZero:
			if v != 0 {
				return false
			}
		case cNonzero:
			if v == 0 {
				return false
			}
		case cLT:
			if v >= cd.bound {
				return false
			}
		}
	}
	v, ok := c.eval(target)
	return ok && v >= lo
}

// solveInput finds the lexicographically smallest input satisfying conds
// with target >= lo. Input positions are assigned in ascending order with
// ascending byte values, so the first solution found is the minimum;
// positions left unconstrained stay 0. The result is at least minLen
// bytes so it covers every position the execution reads. budget bounds
// the search.
func solveInput(arrays [][]uint8, conds []cond, target *expr, lo uint8, minLen int, budget *int) ([]uint8, bool) {
	posSet := make(map[int]bool)
	var collect func(e *expr)
	collect = func(e *expr) {
		if e == nil {
			return
		}
		if e.kind == eInput {
			posSet[e.pos] = true
		}
		collect(e.sub)
	}
	for _, cd := range conds {
		collect(cd.e)
	}
	collect(target)

	c := &solveCtx{arrays: arrays, posIdx: make(map[int]int)}
	for pos := range posSet {
		c.slots = append(c.slots, pos)
	}
	sort.Ints(c.slots)
	c.assign = make([]int, len(c.slots))
	for i, pos := range c.slots {
		c.posIdx[pos] = i
		c.assign[i] = -1
	}

	var rec func(i int) bool
	rec = func(i int) bool {
		if *budget <= 0 {
			return false
		}
		*budget--
		if i == len(c.slots) {
			return c.check(conds, target, lo)
		}
		for v := 0; v < 256; v++ {
			c.assign[i] = v
			if c.feasible(conds, target, lo) && rec(i+1) {
				return true
			}
		}
		c.assign[i] = -1
		return false
	}
	if !rec(0) {
		return nil, false
	}
	n := minLen
	for _, pos := range c.slots {
		if pos+1 > n {
			n = pos + 1
		}
	}
	in := make([]uint8, n)
	for i, pos := range c.slots {
		in[pos] = uint8(c.assign[i])
	}
	return in, true
}

// --- path exploration ---

type auditor struct {
	p        *Program
	maxPaths int
	paths    int
	best     map[int][]uint8 // load instr -> smallest replay input
}

func (a *auditor) dfs(pc int, regs []*expr, conds []cond, maxRead int) {
	if pc == Halt || a.paths >= a.maxPaths {
		return
	}
	a.paths++
	ins := a.p.Instrs[pc]
	switch ins.Op {
	case OpInput:
		r2 := slices.Clone(regs)
		r2[ins.Dst] = &expr{kind: eInput, pos: ins.Pos}
		a.dfs(ins.Next[0], r2, conds, max(maxRead, ins.Pos+1))
	case OpAnd:
		r2 := slices.Clone(regs)
		r2[ins.Dst] = &expr{kind: eAnd, val: ins.Imm, sub: regs[ins.Dst]}
		a.dfs(ins.Next[0], r2, conds, maxRead)
	case OpAdd:
		r2 := slices.Clone(regs)
		r2[ins.Dst] = &expr{kind: eAdd, val: ins.Imm, sub: regs[ins.Dst]}
		a.dfs(ins.Next[0], r2, conds, maxRead)
	case OpJnz:
		e := regs[ins.Src]
		a.dfs(ins.Next[0], regs, append(slices.Clone(conds), cond{kind: cZero, e: e}), maxRead)
		a.dfs(ins.Next[1], regs, append(slices.Clone(conds), cond{kind: cNonzero, e: e}), maxRead)
	case OpLoad:
		arrLen := uint8(len(a.p.Arrays[ins.Arr]))
		idx := regs[ins.Src]
		budget := 1 << 18
		if in, ok := solveInput(a.p.Arrays, conds, idx, arrLen, maxRead, &budget); ok {
			if cur, seen := a.best[pc]; !seen || bytes.Compare(in, cur) < 0 {
				a.best[pc] = in
			}
		}
		// Continue past the load: the firmware faults on OOB, so any
		// later read is only reachable when this index is in bounds.
		r2 := slices.Clone(regs)
		r2[ins.Dst] = &expr{kind: eLoad, arr: ins.Arr, sub: idx}
		a.dfs(ins.Next[0], r2, append(slices.Clone(conds), cond{kind: cLT, e: idx, bound: arrLen}), maxRead)
	}
}

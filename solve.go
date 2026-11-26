package policycheck

import (
	"math"
)

// WitnessSolver 可为一个完整原子真值最小项寻找具体输入见证。
type WitnessSolver interface {
	SolveWitness(atoms []*Pred, truth map[string]bool) (*Input, bool)
}

// WitnessSolverFunc 允许把函数适配为 WitnessSolver。
type WitnessSolverFunc func(atoms []*Pred, truth map[string]bool) (*Input, bool)

// SolveWitness 实现 WitnessSolver。
func (f WitnessSolverFunc) SolveWitness(atoms []*Pred, truth map[string]bool) (*Input, bool) {
	return f(atoms, truth)
}

// DefaultWitnessSolver 使用边界值和约束回溯寻找见证。
//
// 它不是无限位向量 SMT 求解器，但覆盖掩码、无符号区间、跨最高位
// 关系和双架构号码校验常用的 0/边界/最高位邻域。
type DefaultWitnessSolver struct{ MaxSearchNodes int }

func solveWitness(opts *Options, atoms []*Pred, truth map[string]bool) (*Input, bool) {
	if opts != nil && opts.Solver != nil {
		if in, ok := opts.Solver.SolveWitness(atoms, truth); ok && in != nil {
			return in, true
		}
	}
	solver := DefaultWitnessSolver{}
	return solver.SolveWitness(atoms, truth)
}

// SolveWitness 寻找满足全部原子真值的具体输入。
func (s DefaultWitnessSolver) SolveWitness(atoms []*Pred, truth map[string]bool) (*Input, bool) {
	maxNodes := s.MaxSearchNodes
	if maxNodes <= 0 {
		maxNodes = 200000
	}

	named := make(map[string]bool)
	archCandidates := []Arch{ArchX86_64, ArchIA32}
	nrCandidates := map[uint64]struct{}{0: {}, 1: {}, 2: {}, 3: {}, 11: {}, 59: {}, 257: {}}
	argCandidates := make([]map[uint64]struct{}, 6)
	involved := [6]bool{}
	for i := range argCandidates {
		argCandidates[i] = map[uint64]struct{}{0: {}}
	}

	addArg := func(i int, values ...uint64) {
		if i < 0 || i >= 6 {
			return
		}
		involved[i] = true
		for _, v := range values {
			argCandidates[i][v] = struct{}{}
		}
	}

	for _, atom := range atoms {
		want := truth[atom.AtomKey()]
		switch atom.Kind {
		case KindNamed:
			named[atom.Name] = want
		case KindNr:
			for _, r := range atom.NrRanges {
				nrCandidates[r[0]] = struct{}{}
				nrCandidates[r[1]] = struct{}{}
				if r[0] > 0 {
					nrCandidates[r[0]-1] = struct{}{}
				}
				nrCandidates[r[1]+1] = struct{}{}
			}
		case KindArgMask:
			addArg(atom.Index, 0, 1, atom.Value, atom.Value+1, atom.Mask, atom.Mask+1,
				math.MaxUint64, 1<<63, (1<<63)-1)
			if low := lowestSetBit(atom.Mask); low != 0 {
				addArg(atom.Index, atom.Value^low, atom.Value^low+1)
			}
		case KindArgRange:
			addArg(atom.Index, 0, 1, atom.Lo, atom.Hi, atom.Lo-1, atom.Hi+1,
				math.MaxUint64, 1<<63, (1<<63)-1)
		case KindArgsEqual:
			addArg(atom.I, 0, 1, math.MaxUint64, 1<<63, (1<<63)-1)
			addArg(atom.J, 0, 1, math.MaxUint64, 1<<63, (1<<63)-1)
		case KindMaskLEArg:
			maskedMax := atom.Mask
			addArg(atom.I, 0, 1, atom.Mask, maskedMax, maskedMax-1, maskedMax+1,
				math.MaxUint64, 1<<63, (1<<63)-1)
			addArg(atom.J, 0, 1, atom.Mask, maskedMax, maskedMax-1, maskedMax+1,
				math.MaxUint64, 1<<63, (1<<63)-1)
		}
	}

	// Small values are especially useful for width-reduced exhaustive models.
	for i := 0; i < 6; i++ {
		if involved[i] {
			for v := uint64(0); v < 32; v++ {
				argCandidates[i][v] = struct{}{}
			}
		}
	}

	candidateLists := make([][]uint64, 6)
	for i := 0; i < 6; i++ {
		if !involved[i] {
			candidateLists[i] = []uint64{0}
			continue
		}
		candidateLists[i] = sortedUintSet(argCandidates[i])
	}
	nrList := sortedUintSet(nrCandidates)

	nodes := 0
	for _, arch := range archCandidates {
		for _, nr := range nrList {
			in := Input{Arch: arch, Nr: nr, Named: named}
			var assigned [6]bool
			var dfs func(pos int) (*Input, bool)
			dfs = func(pos int) (*Input, bool) {
				if nodes++; nodes > maxNodes {
					return nil, false
				}
				if pos == 6 {
					if assignmentHolds(atoms, truth, in) {
						cp := in
						cp.Args = in.Args
						cp.Named = cloneBoolMap(named)
						return &cp, true
					}
					return nil, false
				}
				for _, v := range candidateLists[pos] {
					in.Args[pos] = v
					assigned[pos] = true
					if !readyConstraintsHold(atoms, truth, in, assigned) {
						continue
					}
					if out, ok := dfs(pos + 1); ok {
						return out, true
					}
				}
				assigned[pos] = false
				return nil, false
			}
			if out, ok := dfs(0); ok {
				return out, true
			}
		}
	}
	return nil, false
}

func assignmentHolds(atoms []*Pred, truth map[string]bool, in Input) bool {
	for _, atom := range atoms {
		want, ok := truth[atom.AtomKey()]
		if !ok {
			return false
		}
		got, err := atom.Eval(in, nil)
		if err != nil || got != want {
			return false
		}
	}
	return true
}

func readyConstraintsHold(atoms []*Pred, truth map[string]bool, in Input, assigned [6]bool) bool {
	for _, atom := range atoms {
		indices := atomArgIndices(atom)
		ready := true
		for _, i := range indices {
			if !assigned[i] {
				ready = false
				break
			}
		}
		if !ready {
			continue
		}
		want := truth[atom.AtomKey()]
		got, err := atom.Eval(in, nil)
		if err != nil || got != want {
			return false
		}
	}
	return true
}

func atomArgIndices(p *Pred) []int {
	switch p.Kind {
	case KindArgMask, KindArgRange:
		return []int{p.Index}
	case KindArgsEqual, KindMaskLEArg:
		if p.I == p.J {
			return []int{p.I}
		}
		return []int{p.I, p.J}
	default:
		return nil
	}
}

func lowestSetBit(mask uint64) uint64 { return mask & -mask }

func sortedUintSet(set map[uint64]struct{}) []uint64 {
	out := make([]uint64, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	// Insertion/sort avoided import cycle? sort already used elsewhere; use sort.
	sortUint64s(out)
	return out
}

func cloneBoolMap(in map[string]bool) map[string]bool {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

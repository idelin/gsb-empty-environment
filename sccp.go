package sccp

// Lattice of abstract values, ordered Undef > Const(v) > Over.
// Analysis only ever moves values DOWN this order, which is what makes
// loop information converge monotonically (finite height => termination).
type LatKind int

const (
	LatUndef LatKind = iota // no reachable definition seen yet (top)
	LatConst                // exactly one constant value on all reachable paths
	LatOver                 // possibly different values (bottom)
)

// Lat is a lattice value; Val is meaningful only when Kind == LatConst.
type Lat struct {
	Kind LatKind
	Val  uint8
}

func (l Lat) String() string {
	switch l.Kind {
	case LatUndef:
		return "undef"
	case LatConst:
		return itoa(int(l.Val))
	default:
		return "over"
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	p := len(buf)
	for i > 0 {
		p--
		buf[p] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[p:])
}

// joinLat is the lattice meet (merge) used at control-flow joins.
func joinLat(a, b Lat) Lat {
	if a.Kind == LatUndef {
		return b
	}
	if b.Kind == LatUndef {
		return a
	}
	if a.Kind == LatOver || b.Kind == LatOver {
		return Lat{Kind: LatOver}
	}
	if a.Val == b.Val {
		return a
	}
	return Lat{Kind: LatOver}
}

// evalLat evaluates one instruction abstractly.
// trap=true means the instruction provably always traps (divisor is the
// constant 0); a merely *possible* trap (divisor Over) is not reported,
// so feasible edges are never cut on a guess.
func evalLat(op Op, x, y Lat) (Lat, bool) {
	if x.Kind == LatUndef || y.Kind == LatUndef {
		return Lat{Kind: LatUndef}, false
	}
	if x.Kind == LatConst && y.Kind == LatConst {
		v, trap := evalConcrete(op, x.Val, y.Val)
		if trap {
			return Lat{Kind: LatUndef}, true
		}
		return Lat{Kind: LatConst, Val: v}, false
	}
	if (op == OpDiv || op == OpMod) && y.Kind == LatConst && y.Val == 0 {
		return Lat{Kind: LatUndef}, true
	}
	return Lat{Kind: LatOver}, false
}

// Analysis holds the fixed-point result of sparse conditional constant
// propagation over a Func.
type Analysis struct {
	F *Func

	// Reachable[b] is true iff some feasible path from the entry reaches b.
	Reachable []bool
	// InstVal[b][i] is the final lattice value of the destination of
	// instruction i in block b (valid only for reachable blocks).
	InstVal [][]Lat
	// OpVal[b][i] is the pair of lattice operand values at instruction i
	// of block b, used to explain constant folds.
	OpVal [][][2]Lat
	// TrapAt[b] is the index of the instruction where block b provably
	// always traps, or -1.
	TrapAt []int
	// OutEnv[b] is the abstract environment at the end of block b.
	OutEnv []map[string]Lat
	// Feasible[b] is the set of successor edges proven feasible.
	Feasible []map[int]bool

	inState []map[string]Lat
}

// Analyze runs the propagation to its fixed point.
//
// Key properties:
//   - a block's entry state is the join of its REACHABLE predecessors'
//     exit states only (edges are added to Feasible lazily, as they are
//     proven feasible);
//   - a branch whose condition is still Undef contributes NO out-edges
//     yet, so an edge that has not been visited is simply unknown, never
//     assumed dead;
//   - every merge moves lattice values strictly downward, so loops
//     converge monotonically and the worklist always terminates.
func Analyze(f *Func) *Analysis {
	n := len(f.Blocks)
	a := &Analysis{
		F:         f,
		Reachable: make([]bool, n),
		InstVal:   make([][]Lat, n),
		OpVal:     make([][][2]Lat, n),
		TrapAt:    make([]int, n),
		OutEnv:    make([]map[string]Lat, n),
		Feasible:  make([]map[int]bool, n),
		inState:   make([]map[string]Lat, n),
	}
	for i := range a.TrapAt {
		a.TrapAt[i] = -1
		a.Feasible[i] = map[int]bool{}
	}
	a.Reachable[f.Entry] = true
	a.inState[f.Entry] = map[string]Lat{}

	worklist := []int{f.Entry}
	for len(worklist) > 0 {
		b := worklist[0]
		worklist = worklist[1:]
		outEnv, trapAt := a.execBlock(b)
		if trapAt >= 0 {
			// The block provably traps: none of its out-edges is feasible.
			continue
		}
		t := f.Blocks[b].Term
		switch t.Kind {
		case TermJump:
			a.flowEdge(b, t.Next, outEnv, &worklist)
		case TermBr:
			c := outEnv[t.Cond]
			switch c.Kind {
			case LatConst:
				if c.Val != 0 {
					a.flowEdge(b, t.ThenBlk, outEnv, &worklist)
				} else {
					a.flowEdge(b, t.ElseBlk, outEnv, &worklist)
				}
			case LatOver:
				a.flowEdge(b, t.ThenBlk, outEnv, &worklist)
				a.flowEdge(b, t.ElseBlk, outEnv, &worklist)
			case LatUndef:
				// Condition not determined yet: record nothing. If the
				// block's state later changes, it is reprocessed and the
				// edges are reconsidered.
			}
		case TermRet, TermTrap:
		}
	}
	return a
}

// flowEdge records edge from->to as feasible and merges env into the
// entry state of `to`. Only feasible predecessors ever contribute to a
// block's entry state.
func (a *Analysis) flowEdge(from, to int, env map[string]Lat, worklist *[]int) {
	a.Feasible[from][to] = true
	changed := !a.Reachable[to]
	a.Reachable[to] = true
	if a.inState[to] == nil {
		a.inState[to] = map[string]Lat{}
	}
	st := a.inState[to]
	for name, v := range env {
		old := st[name] // zero value is LatUndef
		j := joinLat(old, v)
		if j != old {
			st[name] = j
			changed = true
		}
	}
	if changed {
		*worklist = append(*worklist, to)
	}
}

// execBlock symbolically executes block b from its current entry state
// and records per-instruction lattice values. It returns the exit
// environment and the index of a provably trapping instruction (or -1).
func (a *Analysis) execBlock(b int) (map[string]Lat, int) {
	env := map[string]Lat{}
	for k, v := range a.inState[b] {
		env[k] = v
	}
	blk := a.F.Blocks[b]
	vals := make([]Lat, len(blk.Insts))
	ovals := make([][2]Lat, len(blk.Insts))
	trapAt := -1
	for i, in := range blk.Insts {
		var v Lat
		var trap bool
		ovals[i] = [2]Lat{env[in.A], env[in.B]}
		switch in.Op {
		case OpInput:
			v = Lat{Kind: LatOver}
		case OpConst:
			v = Lat{Kind: LatConst, Val: in.Imm}
		default:
			v, trap = evalLat(in.Op, env[in.A], env[in.B])
		}
		vals[i] = v
		env[in.Dst] = v
		if trap {
			trapAt = i
			break
		}
	}
	a.InstVal[b] = vals
	a.OpVal[b] = ovals
	a.TrapAt[b] = trapAt
	a.OutEnv[b] = env
	return env, trapAt
}

package netsec

import "math/big"

// TraceStep records one probabilistic choice and the branch taken. Occurrence
// disambiguates repeated visits to the same node inside loops.
type TraceStep struct {
	NodeID     string
	Branch     int
	Occurrence int
}

// Path is the ordered list of choices producing one concrete execution.
type Path []TraceStep

// Source pairs one originating path with its exact probability mass.
type Source struct {
	Path Path
	Mass *big.Rat
}

// loopFrame is one active bounded loop. remaining counts iterations still
// allowed for Repeat, or further body entries allowed for While. limit keeps
// the declared bound for diagnostics.
type loopFrame struct {
	remaining int
	limit     int
}

// node is a merged execution configuration. Equal configurations share one
// node: their masses are summed while every originating path is retained.
type node struct {
	state   state
	pc      int
	frames  []loopFrame
	outcome Outcome
	occ     map[string]int

	mass    *big.Rat
	sources []Source
}

type configKey string

type engine struct {
	prog  *program
	nodes map[configKey]*node
	order []configKey
}

func makeConfigKey(pc int, s state, frames []loopFrame, occ map[string]int, o Outcome) configKey {
	buf := make([]byte, 0, 40)
	putInt := func(n int) {
		buf = append(buf, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
	putInt(pc)
	buf = append(buf, stateKey(s)...)
	putInt(len(frames))
	for _, f := range frames {
		putInt(f.remaining)
	}
	names := make([]string, 0, len(occ))
	for name := range occ {
		names = append(names, name)
	}
	sortStrings(names)
	putInt(len(names))
	for _, name := range names {
		putInt(len(name))
		buf = append(buf, name...)
		putInt(occ[name])
	}
	buf = append(buf, outcomeKey(o)...)
	return configKey(buf)
}

// run executes the flattened program, merging equal configurations. It
// returns the terminal nodes (Halt or Fail), each with summed mass and the
// full list of retained sources.
func (e *engine) run() ([]*node, error) {
	root := &node{
		state:   state{},
		pc:      0,
		occ:     map[string]int{},
		mass:    big.NewRat(1, 1),
		sources: []Source{{Path: Path{}, Mass: big.NewRat(1, 1)}},
	}
	key := makeConfigKey(0, root.state, nil, root.occ, root.outcome)
	e.nodes = map[configKey]*node{key: root}
	e.order = []configKey{key}

	var terminals []*node

	for head := 0; head < len(e.order); head++ {
		cur := e.nodes[e.order[head]]
		in := e.prog.code[cur.pc]
		switch in.op {
		case opHalt:
			terminals = append(terminals, cur)
		case opFail:
			cur.outcome.Exception = in.name
			terminals = append(terminals, cur)
		case opAssign:
			v, err := evalExpr(in.expr, cur.state)
			if err != nil {
				return nil, err
			}
			next := cur.state.clone()
			next[in.name] = v
			if err := e.goto_(cur, cur.pc+1, cur.frames, next, cur.outcome, nil, big.NewRat(1, 1), cur.occ); err != nil {
				return nil, err
			}
		case opObserve:
			v, err := evalExpr(in.expr, cur.state)
			if err != nil {
				return nil, err
			}
			o := cloneOutcome(cur.outcome)
			o.Observations = append(o.Observations, Observation{Label: in.name, Value: v})
			if err := e.goto_(cur, cur.pc+1, cur.frames, cur.state, o, nil, big.NewRat(1, 1), cur.occ); err != nil {
				return nil, err
			}
		case opJump:
			if err := e.goto_(cur, in.targets[0], cur.frames, cur.state, cur.outcome, nil, big.NewRat(1, 1), cur.occ); err != nil {
				return nil, err
			}
		case opFlip:
			for i, w := range in.weights {
				if w.Sign() == 0 {
					continue
				}
				occ := cloneOcc(cur.occ)
				occ[in.nodeID]++
				step := TraceStep{NodeID: in.nodeID, Branch: i, Occurrence: occ[in.nodeID]}
				if err := e.goto_(cur, in.targets[i], cur.frames, cur.state, cur.outcome, &step, w, occ); err != nil {
					return nil, err
				}
			}
		case opRepeatEnter:
			// Zero iterations jumps straight out; otherwise push a frame with
			// the remaining iteration count and enter the body.
			if in.limit == 0 {
				if err := e.goto_(cur, in.targets[1], cur.frames, cur.state, cur.outcome, nil, big.NewRat(1, 1), cur.occ); err != nil {
					return nil, err
				}
			} else {
				frames := pushFrame(cur.frames, in.limit, in.limit)
				if err := e.goto_(cur, in.targets[0], frames, cur.state, cur.outcome, nil, big.NewRat(1, 1), cur.occ); err != nil {
					return nil, err
				}
			}
		case opRepeatBack:
			if err := e.repeatBack(cur, in.targets[0], in.targets[1]); err != nil {
				return nil, err
			}
		case opWhileEnter:
			cond, err := evalExpr(in.expr, cur.state)
			if err != nil {
				return nil, err
			}
			if !cond {
				if err := e.goto_(cur, in.targets[1], cur.frames, cur.state, cur.outcome, nil, big.NewRat(1, 1), cur.occ); err != nil {
					return nil, err
				}
				continue
			}
			if in.limit == 0 {
				return nil, &LimitError{Limit: in.limit}
			}
			// Frame holds the number of further body entries still allowed.
			frames := pushFrame(cur.frames, in.limit-1, in.limit)
			if err := e.goto_(cur, in.targets[0], frames, cur.state, cur.outcome, nil, big.NewRat(1, 1), cur.occ); err != nil {
				return nil, err
			}
		case opWhileBack:
			cond, err := evalExpr(in.expr, cur.state)
			if err != nil {
				return nil, err
			}
			if !cond {
				if err := e.goto_(cur, in.targets[1], cur.frames, cur.state, cur.outcome, nil, big.NewRat(1, 1), cur.occ); err != nil {
					return nil, err
				}
				continue
			}
			if err := e.whileBack(cur, in.targets[0]); err != nil {
				return nil, err
			}
		case opRepeatExit, opWhileExit:
			frames := popFrame(cur.frames)
			if err := e.goto_(cur, cur.pc+1, frames, cur.state, cur.outcome, nil, big.NewRat(1, 1), cur.occ); err != nil {
				return nil, err
			}
		default:
			return nil, &EvalError{Reason: "unknown opcode"}
		}
	}

	return terminals, nil
}

// repeatBack decrements the active frame and re-enters the body while
// iterations remain; an exhausted frame jumps to the exit instruction.
func (e *engine) repeatBack(cur *node, bodyPC, exitPC int) error {
	if len(cur.frames) == 0 {
		return &EvalError{Reason: "loop back-edge without active frame"}
	}
	frames := cloneFrames(cur.frames)
	frames[len(frames)-1].remaining--
	if frames[len(frames)-1].remaining == 0 {
		return e.goto_(cur, exitPC, frames, cur.state, cur.outcome, nil, big.NewRat(1, 1), cur.occ)
	}
	return e.goto_(cur, bodyPC, frames, cur.state, cur.outcome, nil, big.NewRat(1, 1), cur.occ)
}

// whileBack consumes one allowed re-entry. A zero budget with a still-true
// condition means the bounded loop would run forever and is rejected.
func (e *engine) whileBack(cur *node, bodyPC int) error {
	if len(cur.frames) == 0 {
		return &EvalError{Reason: "loop back-edge without active frame"}
	}
	top := cur.frames[len(cur.frames)-1]
	if top.remaining == 0 {
		return &LimitError{Limit: top.limit}
	}
	frames := cloneFrames(cur.frames)
	frames[len(frames)-1].remaining--
	return e.goto_(cur, bodyPC, frames, cur.state, cur.outcome, nil, big.NewRat(1, 1), cur.occ)
}

// goto_ routes one transition into the configuration map. factor is the
// probability multiplier of this edge: one for deterministic edges and a
// branch weight for flips. Equal configurations merge: node mass is summed
// while each retained source keeps the exact probability of its own path.
func (e *engine) goto_(cur *node, pc int, frames []loopFrame, s state, o Outcome, step *TraceStep, factor *big.Rat, occ map[string]int) error {
	key := makeConfigKey(pc, s, frames, occ, o)
	existing, ok := e.nodes[key]
	if !ok {
		existing = &node{
			state:   s,
			pc:      pc,
			frames:  frames,
			outcome: o,
			occ:     occ,
			mass:    new(big.Rat),
		}
		e.nodes[key] = existing
		e.order = append(e.order, key)
	}
	edgeMass := new(big.Rat).Mul(cur.mass, factor)
	existing.mass.Add(existing.mass, edgeMass)

	for _, src := range cur.sources {
		next := Source{Mass: new(big.Rat).Mul(src.Mass, factor)}
		if step != nil {
			p := make(Path, 0, len(src.Path)+1)
			p = append(p, src.Path...)
			p = append(p, *step)
			next.Path = p
		} else {
			next.Path = append(Path(nil), src.Path...)
		}
		existing.sources = append(existing.sources, next)
	}
	return nil
}

func pushFrame(frames []loopFrame, remaining, limit int) []loopFrame {
	out := make([]loopFrame, 0, len(frames)+1)
	out = append(out, frames...)
	return append(out, loopFrame{remaining: remaining, limit: limit})
}

func popFrame(frames []loopFrame) []loopFrame {
	if len(frames) == 0 {
		return nil
	}
	out := make([]loopFrame, 0, len(frames)-1)
	out = append(out, frames[:len(frames)-1]...)
	return out
}

func cloneFrames(f []loopFrame) []loopFrame {
	cp := make([]loopFrame, len(f))
	copy(cp, f)
	return cp
}

func cloneOcc(o map[string]int) map[string]int {
	cp := make(map[string]int, len(o))
	for k, v := range o {
		cp[k] = v
	}
	return cp
}

func cloneOutcome(o Outcome) Outcome {
	cp := make([]Observation, len(o.Observations))
	copy(cp, o.Observations)
	return Outcome{Observations: cp, Exception: o.Exception}
}

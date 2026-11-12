package taskpkg

// Witness is the shortest concrete assignment that reaches a forward node
// while a required mark is absent.
type Witness struct {
	NodeID     int
	Forward    string
	Assignment []bool
	Marks      []Mark
	Missing    Mark
	Steps      int // ops executed along the shortest path
	Reachable  bool
}

type bfsState struct {
	pc       int
	values   uint16 // fixed input values (only meaningful where fixed is set)
	fixed    uint16 // mask of inputs already constrained by the path
	markBits uint32
}

type bfsNode struct {
	state bfsState
	prev  int // parent index in the discovered slice, -1 for root
	input int // input fixed by the edge into this state, -1 otherwise
	depth int // leaf occurrences along the path
}

// ShortestMissingWitness returns the shortest assignment that makes forward
// node forwardID deny because wanted is absent. BFS over
// (pc, fixed inputs, mark set) yields the fewest executed ops; ties are
// broken by deterministic value order (false before true) and pc order.
func (p *Program) ShortestMissingWitness(forwardID int, wanted Mark) (*Witness, error) {
	fwdOp, err := p.findForward(forwardID)
	if err != nil {
		return nil, err
	}
	if !containsMark(fwdOp.Fwd.Required, wanted) {
		return nil, errf("mark %s/%s trusted=%v is not required by forward %d",
			wanted.Field, wanted.Name, wanted.Trusted, forwardID)
	}

	markIndex, inverse := p.buildMarkIndex()
	if p.rule.Inputs > 16 || len(markIndex) > 32 {
		return nil, errf("internal: witness space too large (inputs=%d, marks=%d)",
			p.rule.Inputs, len(markIndex))
	}

	root := bfsState{pc: p.entry}
	nodes := []bfsNode{{state: root, prev: -1, input: -1}}
	discovered := map[bfsState]int{root: 0}

	var hitIndex = -1
	for head := 0; head < len(nodes) && hitIndex < 0; head++ {
		cur := nodes[head]
		op := &p.ops[cur.state.pc]

		if op.Kind == opForward && op.NodeID == forwardID {
			marks := decodeMarks(cur.state.markBits, inverse)
			if !HasMark(marks, wanted) {
				hitIndex = head
				break
			}
		}

		for _, nxt := range p.successors(op, cur.state, markIndex) {
			if _, seen := discovered[nxt.state]; seen {
				continue
			}
			discovered[nxt.state] = len(nodes)
			nodes = append(nodes, bfsNode{
				state: nxt.state, prev: head, input: nxt.input,
				depth: cur.depth + leafDepth(*op),
			})
		}
	}

	if hitIndex < 0 {
		return &Witness{
			NodeID: forwardID, Forward: fwdOp.Fwd.Name,
			Missing: wanted, Reachable: false,
		}, nil
	}

	assignment := make([]bool, p.rule.Inputs)
	idx := hitIndex
	for idx > 0 {
		node := nodes[idx]
		if node.input >= 0 {
			assignment[node.input] = node.state.values&(1<<uint(node.input)) != 0
		}
		idx = node.prev
	}
	hit := nodes[hitIndex]
	return &Witness{
		NodeID:     forwardID,
		Forward:    fwdOp.Fwd.Name,
		Assignment: assignment,
		Marks:      decodeMarks(hit.state.markBits, inverse),
		Missing:    wanted,
		Steps:      hit.depth,
		Reachable:  true,
	}, nil
}
func leafDepth(op Op) int {
	if op.Kind == opLeaf {
		return 1
	}
	return 0
}

type successor struct {
	state bfsState
	input int
}

func (p *Program) successors(op *Op, st bfsState, markIndex map[Mark]int) []successor {
	switch op.Kind {
	case opEnd, opReject:
		return nil

	case opLeaf:
		return p.leafSuccessors(op, st, markIndex)

	case opNot:
		return []successor{{state: st.withPC(op.A)}}

	case opAnd, opOr:
		next := op.B
		if !op.Short {
			next = op.A
		}
		return []successor{{state: st.withPC(next)}}

	case opSeq:
		return []successor{{state: st.withPC(op.A)}}

	case opBranch:
		return []successor{{state: st.withPC(op.T)}}

	case opTry:
		return []successor{{state: st.withPC(op.A)}}

	case opForward:
		// A deny is terminal; allow continues to T.
		satisfied := true
		for _, req := range op.Fwd.Required {
			bit, ok := markIndex[req]
			if !ok || st.markBits&(1<<uint(bit)) == 0 {
				satisfied = false
				break
			}
		}
		if !satisfied {
			return nil
		}
		return []successor{{state: st.withPC(op.T)}}
	}
	return nil
}

func (p *Program) leafSuccessors(op *Op, st bfsState, markIndex map[Mark]int) []successor {
	chk := op.Check
	bit := uint16(1 << uint(chk.ID))

	tryValue := func(val bool) successor {
		next := bfsState{pc: op.False, values: st.values, fixed: st.fixed | bit, markBits: st.markBits}
		if val {
			next.values |= bit
			next.pc = op.True
		}
		if chk.RaiseOn != nil && val == *chk.RaiseOn {
			if op.Raise == 0 {
				return successor{} // unhandled: dead end for witnesses
			}
			next.pc = op.Raise
			return successor{state: next, input: chk.ID}
		}
		if m, ok := chk.Produces(); ok && emitFires(chk.Emit, val) {
			next.markBits |= 1 << uint(markIndex[m])
		}
		return successor{state: next, input: chk.ID}
	}

	if st.fixed&bit != 0 {
		val := st.values&bit != 0
		s := tryValue(val)
		if s.state.pc == 0 && op.Raise == 0 && chk.RaiseOn != nil && val == *chk.RaiseOn {
			return nil
		}
		return []successor{s}
	}

	out := make([]successor, 0, 2)
	for _, val := range []bool{false, true} {
		s := tryValue(val)
		if chk.RaiseOn != nil && val == *chk.RaiseOn && op.Raise == 0 {
			continue
		}
		out = append(out, s)
	}
	return out
}

func (st bfsState) withPC(pc int) bfsState {
	st.pc = pc
	return st
}

func (p *Program) findForward(id int) (*Op, error) {
	for i := range p.ops {
		op := &p.ops[i]
		if op.Kind == opForward && op.NodeID == id {
			return op, nil
		}
	}
	return nil, errf("forward node %d not found", id)
}

func (p *Program) buildMarkIndex() (map[Mark]int, map[int]Mark) {
	idx := map[Mark]int{}
	inv := map[int]Mark{}
	add := func(m Mark) {
		if _, ok := idx[m]; ok {
			return
		}
		i := len(idx)
		idx[m] = i
		inv[i] = m
	}
	for i := range p.ops {
		op := &p.ops[i]
		if op.Check != nil {
			if m, ok := op.Check.Produces(); ok {
				add(m)
			}
		}
		if op.Kind == opForward {
			for _, m := range op.Fwd.Required {
				add(m)
			}
		}
	}
	return idx, inv
}

func decodeMarks(bits uint32, inv map[int]Mark) []Mark {
	out := []Mark{}
	for i := 0; i < 32; i++ {
		if bits&(1<<uint(i)) != 0 {
			if m, ok := inv[i]; ok {
				out = append(out, m)
			}
		}
	}
	return out
}

func containsMark(marks []Mark, m Mark) bool {
	for _, x := range marks {
		if x == m {
			return true
		}
	}
	return false
}

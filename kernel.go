package taskpkg

// mode selects whether pure leaf occurrences may be served from a memo table.
type mode int

const (
	modeInterpret mode = iota // reference semantics: every occurrence executes
	modeCompiled              // share pure checks; never share effectful ones
)

type frame struct {
	tryNodeID int // AST id of the owning StmtTry (for catch/normal edges)
	normal    int // continuation when the body completes normally
	handler   int // handler label (0 = none)
}

type kernel struct {
	prog   *Program
	inputs []bool
	mode   mode

	pc     int
	marks  []Mark
	trace  Trace
	memo   map[int]bool // check id -> cached value (pure checks only)
	frames []frame
}

func newKernel(prog *Program, inputs []bool, m mode) *kernel {
	return &kernel{
		prog: prog, inputs: inputs, mode: m,
		pc:   prog.entry,
		memo: map[int]bool{},
	}
}

// run drives the CFG until a terminal op or the end op (label 0).
func (k *kernel) run() {
	k.runWithHook(nil)
}

// runWithHook invokes hook with each op before it executes.
func (k *kernel) runWithHook(hook func(*Op)) {
	for k.pc != 0 {
		depthBefore := len(k.frames)
		op := &k.prog.ops[k.pc]
		if hook != nil {
			hook(op)
		}
		switch op.Kind {
		case opLeaf:
			k.runLeaf(op)
		case opNot:
			k.edge(EdgeNot, op.NodeID, k.targetNode(op.A))
			k.pc = op.A
		case opAnd:
			if op.Short {
				k.edge(EdgeAndFalse, op.NodeID, k.targetNode(op.B))
				k.pc = op.B
			} else {
				k.edge(EdgeAndTrue, op.NodeID, k.targetNode(op.A))
				k.pc = op.A
			}
		case opOr:
			if op.Short {
				k.edge(EdgeOrTrue, op.NodeID, k.targetNode(op.B))
				k.pc = op.B
			} else {
				k.edge(EdgeOrFalse, op.NodeID, k.targetNode(op.A))
				k.pc = op.A
			}
		case opSeq:
			k.edge(EdgeSeq, op.NodeID, k.targetNode(op.A))
			k.pc = op.A
		case opBranch:
			// Anchor op only: expression leaf chains jump straight to
			// then/else continuations, so this op is never executed.
			k.pc = op.T
		case opTry:
			k.edge(EdgeTryEnter, op.NodeID, k.targetNode(op.A))
			k.frames = append(k.frames, frame{
				tryNodeID: k.tryASTID(op.NodeID), normal: op.T, handler: op.C,
			})
			k.pc = op.A
		case opReject:
			k.edge(EdgeReject, op.NodeID, 0)
			k.trace.Decision = DecisionRejected
			k.trace.RejectReason = op.Reason
			return
		case opForward:
			k.runForward(op)
			if k.trace.Decision == DecisionDenied {
				return
			}
		}
		k.emitTryNormalEdges(depthBefore)
	}
	if k.trace.Decision == 0 {
		k.trace.Decision = DecisionPassed
	}
}

// emitTryNormalEdges closes try frames whose body finished normally. A frame
// closes when control transfers to its recorded normal continuation (or to
// the end op) without going through its handler.
func (k *kernel) emitTryNormalEdges(depthBefore int) {
	for len(k.frames) > 0 {
		top := k.frames[len(k.frames)-1]
		if len(k.frames) > depthBefore {
			return // newly entered frame still owns the pc
		}
		if k.pc == top.handler && top.handler != 0 {
			return // control entered the handler via the catch path
		}
		if k.pc != top.normal && !(k.pc == 0 && top.normal == 0) {
			return // still executing inside the try body
		}
		k.edge(EdgeTryNormal, top.tryNodeID, k.targetNode(top.normal))
		k.frames = k.frames[:len(k.frames)-1]
	}
}

// tryASTID recovers the StmtTry AST id from its synthetic op node id.
func (k *kernel) tryASTID(synthetic int) int { return -(synthetic + 10_000) }

func (k *kernel) edge(kind EdgeKind, from, to int) {
	k.trace.Edges = append(k.trace.Edges, EdgeEvent{Kind: kind, From: from, To: to})
}

func (k *kernel) targetNode(label int) int {
	if label == 0 {
		return 0
	}
	return k.prog.ops[label].NodeID
}

func (k *kernel) runLeaf(op *Op) {
	chk := op.Check
	raw := k.inputs[chk.ID]

	// Exception path: no boolean result and no mark.
	if chk.RaiseOn != nil && raw == *chk.RaiseOn {
		k.trace.Leaves = append(k.trace.Leaves, LeafEvent{
			NodeID: op.NodeID, Check: chk.ID, Raised: true,
		})
		handler := op.Raise
		k.edge(EdgeLeafRaise, op.NodeID, k.handlerNode(handler))
		if handler == 0 {
			k.frames = nil
			k.edge(EdgeRaiseEscape, op.NodeID, 0)
			k.trace.Decision = DecisionUnhandled
			k.pc = 0
			return
		}
		tryID := k.popToHandler(handler)
		k.edge(EdgeTryCatch, tryID, k.targetNode(handler))
		k.pc = handler
		return
	}

	// Pure-check sharing. Memoized reads have no side effects and never touch
	// the mark set; effectful occurrences always execute.
	cached := false
	if k.mode == modeCompiled && chk.IsPure() {
		if v, ok := k.memo[chk.ID]; ok {
			cached = true
			raw = v
		} else {
			k.memo[chk.ID] = raw
		}
	}

	ev := LeafEvent{NodeID: op.NodeID, Check: chk.ID, Value: raw, Cached: cached}
	if !cached {
		if m, ok := chk.Produces(); ok && emitFires(chk.Emit, raw) {
			ev.Marks = append(ev.Marks, m)
			k.marks = append(k.marks, m)
			k.trace.Marks = append(k.trace.Marks, m)
		}
	}
	k.trace.Leaves = append(k.trace.Leaves, ev)

	if raw {
		k.edge(EdgeLeafTrue, op.NodeID, k.targetNode(op.True))
		k.pc = op.True
	} else {
		k.edge(EdgeLeafFalse, op.NodeID, k.targetNode(op.False))
		k.pc = op.False
	}
}

func (k *kernel) runForward(op *Op) {
	if missing, bad := MissingRequired(k.marks, op.Fwd.Required); bad {
		k.edge(EdgeForwardDeny, op.NodeID, 0)
		k.trace.Decision = DecisionDenied
		k.trace.ForwardName = op.Fwd.Name
		k.trace.DenyMissing = append(k.trace.DenyMissing, missing)
		k.pc = 0
		return
	}
	k.edge(EdgeForwardAllow, op.NodeID, k.targetNode(op.T))
	k.trace.Decision = DecisionForwarded
	k.trace.ForwardName = op.Fwd.Name
	k.pc = op.T
}

func (k *kernel) handlerNode(label int) int {
	if label == 0 {
		return 0
	}
	return k.prog.ops[label].NodeID
}

// popToHandler unwinds frames until the frame owning handlerLabel, emits the
// normal-exit edges for any tries the exception passes through is handled by
// the caller; returns the owning try's AST id.
func (k *kernel) popToHandler(handlerLabel int) int {
	for i := len(k.frames) - 1; i >= 0; i-- {
		if k.frames[i].handler == handlerLabel {
			tryID := k.frames[i].tryNodeID
			k.frames = k.frames[:i]
			return tryID
		}
	}
	return 0
}

func emitFires(m Emit, val bool) bool {
	switch m {
	case EmitAlways:
		return true
	case EmitOnTrue:
		return val
	case EmitOnFalse:
		return !val
	default:
		return false
	}
}

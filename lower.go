package taskpkg

// opKind enumerates compiled control-flow operations.
type opKind int

const (
	opEnd opKind = iota
	opLeaf
	opNot
	opAnd
	opOr
	opSeq
	opBranch
	opTry
	opReject
	opForward
)

// Op is one lowered operation with explicit successors. Label 0 is the shared
// end operation; -1 marks a successor that is backpatched later.
type Op struct {
	Kind   opKind
	NodeID int // source AST node id; negative for synthetic scaffolding
	Check  *Check
	Reason string
	Fwd    *Forward

	// opLeaf: True/False/Raise.
	True, False, Raise int
	// opNot: A = child. opAnd/opOr: A = right subtree, B = short-circuit cont.
	A, B int
	// opSeq: A=first B=second. opBranch: T=then C=else.
	// opTry: A=body T=normal-cont C=handler.
	T, C int
	// Short marks an And/Or anchor reached by the short-circuit edge.
	Short bool
}

// Program is a compiled rule: an explicit-edge CFG plus the AST map used by
// the reference interpreter. Both executors share the same runtime kernel.
type Program struct {
	rule   *Rule
	ops    []Op
	entry  int
	labels map[int]int // AST node id -> op label
}

// Compile validates r and lowers it to an explicit-edge Program.
func Compile(r *Rule) (*Program, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	l := &lowerer{
		labels:  map[int]int{},
		anchors: map[anchorRef]int{},
	}
	l.ops = append(l.ops, Op{Kind: opEnd}) // label 0
	entry := 0
	if r.Body != nil {
		entry, _ = l.stmt(r.Body, 0, 0)
	}
	l.resolveRefs()
	return &Program{rule: r, ops: l.ops, entry: entry, labels: l.labels}, nil
}

// anchorRef names a not-yet-emitted op: a binary's join or short anchor.
type anchorRef struct {
	node       int
	short      bool
	concrete   int
	isConcrete bool
}

// leafRef records a leaf successor that must point at an anchor.
type leafRef struct {
	opLabel int
	field   fieldKind
	ref     anchorRef
}

type fieldKind int

const (
	fpTrue fieldKind = iota
	fpFalse
	fpB
)

type lowerer struct {
	ops     []Op
	labels  map[int]int
	anchors map[anchorRef]int
	refs    []leafRef
}

func (l *lowerer) emit(op Op) int {
	label := len(l.ops)
	l.ops = append(l.ops, op)
	return label
}

func (l *lowerer) resolveRefs() {
	for _, r := range l.refs {
		var target int
		if r.ref.isConcrete {
			target = r.ref.concrete
		} else {
			target = l.anchors[anchorRef{node: r.ref.node, short: r.ref.short}]
		}
		if r.field == fpTrue {
			l.ops[r.opLabel].True = target
		} else if r.field == fpFalse {
			l.ops[r.opLabel].False = target
		} else {
			l.ops[r.opLabel].B = target
		}
	}
}

func seqNodeID(id int) int { return -id }
func tryNodeID(id int) int { return -10_000 - id }

// stmt lowers s. next is the continuation label; handler is the enclosing
// try-handler label (0 = none).
// stmt lowers s and returns (entryLabel, anchorLabel). entryLabel is the
// first op that actually executes; anchorLabel represents the statement.
func (l *lowerer) stmt(s *Stmt, next, handler int) (entry, anchor int) {
	switch s.Kind {
	case StmtCheck:
		label := l.emit(Op{
			Kind: opLeaf, NodeID: s.ID, Check: s.Check,
			True: next, False: next, Raise: raiseTarget(s.Check, handler),
		})
		l.labels[s.ID] = label
		return label, label

	case StmtReject:
		label := l.emit(Op{Kind: opReject, NodeID: s.ID, Reason: s.Reject})
		l.labels[s.ID] = label
		return label, label

	case StmtForward:
		label := l.emit(Op{Kind: opForward, NodeID: s.ID, Fwd: s.Forward, T: next})
		l.labels[s.ID] = label
		return label, label

	case StmtSeq:
		secondE, _ := l.stmt(s.Second, next, handler)
		firstE, _ := l.stmt(s.First, secondE, handler)
		label := l.emit(Op{Kind: opSeq, NodeID: seqNodeID(s.ID), A: firstE, B: secondE})
		l.labels[s.ID] = label
		return firstE, label

	case StmtBranch:
		thenE, _ := l.stmt(s.Then, next, handler)
		elseL := next
		if s.Else != nil {
			elseL, _ = l.stmt(s.Else, next, handler)
		}
		branchL := l.emit(Op{Kind: opBranch, NodeID: s.ID, T: thenE, C: elseL})
		l.labels[s.ID] = branchL
		condEntry := l.exprEntry(s.Cond, thenE, elseL, handler)
		return condEntry, branchL

	case StmtTry:
		var handlerL int
		if s.Handler != nil {
			handlerL, _ = l.stmt(s.Handler, next, handler)
		}
		tryL := l.emit(Op{Kind: opTry, NodeID: tryNodeID(s.ID), T: next, C: handlerL})
		l.labels[s.ID] = tryL
		bodyE, _ := l.stmt(s.Then, next, handlerL)
		l.ops[tryL].A = bodyE
		return tryL, tryL
	}
	return 0, 0
}

// exprEntry lowers e and returns the label of its first executed op.
func (l *lowerer) exprEntry(e *Expr, whenTrue, whenFalse, handler int) int {
	l.expr(e, whenTrue, whenFalse, handler)
	return l.firstExprOp(e)
}

func (l *lowerer) firstExprOp(e *Expr) int {
	switch e.Kind {
	case ExprCheck:
		return l.labels[e.ID]
	case ExprNot:
		return l.labels[e.ID]
	case ExprAnd, ExprOr:
		return l.firstExprOp(e.Left)
	}
	return 0
}

func raiseTarget(chk *Check, handler int) int {
	if chk != nil && chk.RaiseOn != nil {
		return handler
	}
	return -1
}

// expr lowers e with result continuations whenTrue/whenFalse.
func (l *lowerer) expr(e *Expr, whenTrue, whenFalse, handler int) {
	switch e.Kind {
	case ExprCheck:
		label := l.emit(Op{
			Kind: opLeaf, NodeID: e.ID, Check: e.Check,
			True: whenTrue, False: whenFalse, Raise: raiseTarget(e.Check, handler),
		})
		l.labels[e.ID] = label

	case ExprNot:
		label := l.emit(Op{Kind: opNot, NodeID: e.ID})
		l.labels[e.ID] = label
		l.expr(e.Child, whenFalse, whenTrue, handler)
		l.ops[label].A = l.labels[e.Child.ID]

	case ExprAnd:
		l.binaryRefs(e, contRef(whenTrue), contRef(whenFalse), handler, true)

	case ExprOr:
		l.binaryRefs(e, contRef(whenTrue), contRef(whenFalse), handler, false)
	}
}

// contRef wraps a concrete label as a resolved anchor.
func contRef(label int) anchorRef {
	return anchorRef{concrete: label, isConcrete: true, node: -1}
}

// binary emits ops in concrete execution order:
//
//	operand leaves/subtrees first, then the right-subtree join, then the
//	short-circuit anchor. Leaf successors that reach an anchor use symbolic
//	anchor references resolved after every op is emitted.
func (l *lowerer) binaryRefs(e *Expr, whenTrue, whenFalse anchorRef, handler int, isAnd bool) {
	kind := opAnd
	if !isAnd {
		kind = opOr
	}
	joinRef := anchorRef{node: e.ID, short: false}
	shortRef := anchorRef{node: e.ID, short: true}

	// 1. left subtree: its true/false edges land on the join/short anchors.
	l.exprWithAnchors(e.Left, joinRef, shortRef, isAnd, handler)

	// 2. right subtree (its join is emitted after it).
	l.exprWithAnchors(e.Right, whenTrue, whenFalse, isAnd, handler)

	join := l.emit(Op{Kind: kind, NodeID: e.ID, A: l.labels[e.Right.ID], B: -1})
	short := l.emit(Op{Kind: kind, NodeID: e.ID, B: -1, Short: true})
	l.labels[e.ID] = short
	l.anchors[joinRef] = join
	l.anchors[shortRef] = short

	// The anchors' own continuations are concrete labels or outer anchors.
	l.refs = append(l.refs,
		leafRef{opLabel: join, field: fpB, ref: chooseRef(isAnd, whenFalse, whenTrue)},
		leafRef{opLabel: short, field: fpB, ref: chooseRef(isAnd, whenFalse, whenTrue)},
	)
}

func chooseRef(isAnd bool, a, b anchorRef) anchorRef {
	if isAnd {
		return a
	}
	return b
}

// exprWithAnchors lowers x whose boolean result must jump to onTrue/onFalse
// anchors. Not flips the anchors; nested binaries resolve internally.
func (l *lowerer) exprWithAnchors(x *Expr, onTrue, onFalse anchorRef, isAnd bool, handler int) {
	switch x.Kind {
	case ExprCheck:
		label := l.emit(Op{
			Kind: opLeaf, NodeID: x.ID, Check: x.Check,
			True: -1, False: -1, Raise: raiseTarget(x.Check, handler),
		})
		l.labels[x.ID] = label
		l.refs = append(l.refs,
			leafRef{opLabel: label, field: fpTrue, ref: onTrue},
			leafRef{opLabel: label, field: fpFalse, ref: onFalse},
		)
	case ExprNot:
		label := l.emit(Op{Kind: opNot, NodeID: x.ID})
		l.labels[x.ID] = label
		l.exprWithAnchors(x.Child, onFalse, onTrue, !isAnd, handler)
		l.ops[label].A = l.labels[x.Child.ID]
	default:
		l.binaryRefs(x, onTrue, onFalse, handler, x.Kind == ExprAnd)
	}
}

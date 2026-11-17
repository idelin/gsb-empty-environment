package netsec

import "fmt"

type symKind uint8

const (
	symConst symKind = iota + 1
	symInput
	symAdd
	symSub
	symMul
	symTrunc
	symExtend
	symCmp
	symOverflow
)

type symExpr struct {
	kind   symKind
	width  uint8
	value  uint64
	input  int
	a      *symExpr
	b      *symExpr
	signed bool
	pred   CmpPred
	op     Op
	check  OverflowCheck
}

type exprKey struct {
	kind   symKind
	width  uint8
	value  uint64
	input  int
	a      *symExpr
	b      *symExpr
	signed bool
	pred   CmpPred
	op     Op
	check  OverflowCheck
}

type exprInterner struct {
	exprs map[exprKey]*symExpr
}

func newExprInterner() *exprInterner {
	return &exprInterner{exprs: make(map[exprKey]*symExpr)}
}

func (in *exprInterner) intern(e *symExpr) *symExpr {
	if in == nil {
		return e
	}
	key := exprKey{
		kind: e.kind, width: e.width, value: e.value, input: e.input,
		a: e.a, b: e.b, signed: e.signed, pred: e.pred,
		op: e.op, check: e.check,
	}
	if existing, ok := in.exprs[key]; ok {
		return existing
	}
	in.exprs[key] = e
	return e
}

type symbolicGraph struct {
	byID   map[NodeID]Node
	memo   map[NodeID]*symExpr
	intern *exprInterner
}

func newSymbolicGraph(g *Graph, interner *exprInterner) *symbolicGraph {
	byID := make(map[NodeID]Node, len(g.Nodes))
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	return &symbolicGraph{
		byID:   byID,
		memo:   make(map[NodeID]*symExpr, len(g.Nodes)),
		intern: interner,
	}
}

func (s *symbolicGraph) value(id NodeID) (*symExpr, error) {
	if e, ok := s.memo[id]; ok {
		return e, nil
	}
	n, ok := s.byID[id]
	if !ok {
		return nil, fmt.Errorf("netsec: missing symbolic node %d", id)
	}

	var e *symExpr
	switch n.Op {
	case OpInput:
		e = &symExpr{kind: symInput, width: 8, input: n.InputIndex}
	case OpConst:
		e = &symExpr{kind: symConst, width: n.Width, value: n.Value & maskBits(n.Width)}
	case OpAdd, OpSub, OpMul:
		a, err := s.value(n.X)
		if err != nil {
			return nil, err
		}
		b, err := s.value(n.Y)
		if err != nil {
			return nil, err
		}
		kind := symAdd
		if n.Op == OpSub {
			kind = symSub
		} else if n.Op == OpMul {
			kind = symMul
		}
		e = &symExpr{kind: kind, width: n.Width, a: a, b: b}
	case OpTrunc:
		a, err := s.value(n.X)
		if err != nil {
			return nil, err
		}
		e = &symExpr{kind: symTrunc, width: n.Width, a: a}
	case OpExtend:
		a, err := s.value(n.X)
		if err != nil {
			return nil, err
		}
		e = &symExpr{kind: symExtend, width: n.Width, a: a, signed: n.Signed}
	case OpCmp:
		a, err := s.value(n.X)
		if err != nil {
			return nil, err
		}
		b, err := s.value(n.Y)
		if err != nil {
			return nil, err
		}
		e = &symExpr{kind: symCmp, width: 1, a: a, b: b, pred: n.Pred, signed: n.Signed}
	case OpOverflow:
		arith, ok := s.byID[n.X]
		if !ok || !arith.Op.IsArithmetic() || arith.Check == nil {
			return nil, fmt.Errorf("netsec: overflow node %d has invalid operand", n.ID)
		}
		a, err := s.value(arith.X)
		if err != nil {
			return nil, err
		}
		b, err := s.value(arith.Y)
		if err != nil {
			return nil, err
		}
		e = &symExpr{
			kind: symOverflow, width: 1, a: a, b: b,
			op: arith.Op, check: *arith.Check,
		}
	default:
		return nil, fmt.Errorf("netsec: node %d is not a symbolic value", id)
	}
	e = s.intern.intern(e)
	s.memo[id] = e
	return e, nil
}

type pathCondition struct {
	expr   *symExpr
	expect bool
}

type symbolicOutcome struct {
	kind   OutcomeKind
	value  *symExpr
	source NodeID
}

type symbolicPath struct {
	conditions []pathCondition
	outcome    symbolicOutcome
}

func enumerateSymbolicPaths(g *Graph, maxPaths int, interner *exprInterner) ([]symbolicPath, bool, error) {
	sg := newSymbolicGraph(g, interner)
	byID := sg.byID
	var paths []symbolicPath
	exhausted := false

	var walk func(NodeID, []pathCondition) error
	walk = func(id NodeID, conds []pathCondition) error {
		if exhausted {
			return nil
		}
		if maxPaths > 0 && len(paths) >= maxPaths {
			exhausted = true
			return nil
		}
		n, ok := byID[id]
		if !ok {
			return fmt.Errorf("netsec: missing control node %d", id)
		}
		switch n.Op {
		case OpReturn:
			v, err := sg.value(n.X)
			if err != nil {
				return err
			}
			paths = append(paths, symbolicPath{
				conditions: append([]pathCondition(nil), conds...),
				outcome:    symbolicOutcome{kind: OutcomeReturn, value: v},
			})
		case OpError:
			paths = append(paths, symbolicPath{
				conditions: append([]pathCondition(nil), conds...),
				outcome:    symbolicOutcome{kind: OutcomeError, source: n.Source},
			})
		case OpBranch:
			cond, err := sg.value(n.X)
			if err != nil {
				return err
			}
			thenConds := append(append([]pathCondition(nil), conds...), pathCondition{expr: cond, expect: true})
			if err := walk(n.Then, thenConds); err != nil {
				return err
			}
			if exhausted {
				return nil
			}
			elseConds := append(append([]pathCondition(nil), conds...), pathCondition{expr: cond, expect: false})
			if err := walk(n.Else, elseConds); err != nil {
				return err
			}
		default:
			return fmt.Errorf("netsec: non-control node %d in control path", id)
		}
		return nil
	}
	if err := walk(g.Entry, nil); err != nil {
		return nil, exhausted, err
	}
	return paths, exhausted, nil
}

type compileContext struct {
	cnf    *cnfBuilder
	inputs map[int][]Lit
	memo   map[*symExpr][]Lit
}

func newCompileContext(cnf *cnfBuilder) *compileContext {
	return &compileContext{
		cnf:    cnf,
		inputs: make(map[int][]Lit),
		memo:   make(map[*symExpr][]Lit),
	}
}

func (c *compileContext) inputBits(index int) []Lit {
	if bits, ok := c.inputs[index]; ok {
		return bits
	}
	bits := make([]Lit, 8)
	for i := range bits {
		bits[i] = c.cnf.newVar()
	}
	c.inputs[index] = bits
	return bits
}

func (c *compileContext) bits(e *symExpr) ([]Lit, error) {
	if bits, ok := c.memo[e]; ok {
		return bits, nil
	}
	var out []Lit
	switch e.kind {
	case symConst:
		out = make([]Lit, e.width)
		for i := range out {
			out[i] = boolToLit(e.value&(uint64(1)<<uint(i)) != 0)
		}
	case symInput:
		out = append([]Lit(nil), c.inputBits(e.input)...)
	case symAdd, symSub, symMul:
		a, err := c.bits(e.a)
		if err != nil {
			return nil, err
		}
		b, err := c.bits(e.b)
		if err != nil {
			return nil, err
		}
		switch e.kind {
		case symAdd:
			out = c.bvAdd(a, b)
		case symSub:
			out = c.bvSub(a, b)
		case symMul:
			out = c.bvMul(a, b)
		}
	case symTrunc:
		a, err := c.bits(e.a)
		if err != nil {
			return nil, err
		}
		if int(e.width) > len(a) {
			return nil, fmt.Errorf("netsec: truncation widens in symbolic form")
		}
		out = append([]Lit(nil), a[:e.width]...)
	case symExtend:
		a, err := c.bits(e.a)
		if err != nil {
			return nil, err
		}
		out = c.bvExtend(a, int(e.width), e.signed)
	case symCmp:
		a, err := c.bits(e.a)
		if err != nil {
			return nil, err
		}
		b, err := c.bits(e.b)
		if err != nil {
			return nil, err
		}
		out = []Lit{c.bvCompare(a, b, e.pred, e.signed)}
	case symOverflow:
		a, err := c.bits(e.a)
		if err != nil {
			return nil, err
		}
		b, err := c.bits(e.b)
		if err != nil {
			return nil, err
		}
		out = []Lit{c.bvOverflow(a, b, e.op, e.check)}
	default:
		return nil, fmt.Errorf("netsec: unknown symbolic expression kind %d", e.kind)
	}
	c.memo[e] = out
	return out, nil
}

func (c *compileContext) bvAdd(a, b []Lit) []Lit {
	out := make([]Lit, len(a))
	carry := litFalse
	for i := range a {
		axb := c.cnf.xor(a[i], b[i])
		out[i] = c.cnf.xor(axb, carry)
		carry = c.cnf.or(c.cnf.and(a[i], b[i]), c.cnf.and(carry, axb))
	}
	return out
}

func (c *compileContext) bvSub(a, b []Lit) []Lit {
	notB := make([]Lit, len(b))
	for i := range b {
		notB[i] = neg(b[i])
	}
	one := make([]Lit, len(a))
	one[0] = litTrue
	sum := c.bvAdd(a, notB)
	return c.bvAdd(sum, one)
}

func (c *compileContext) bvMul(a, b []Lit) []Lit {
	n := len(a)
	acc := make([]Lit, n)
	for i := range acc {
		acc[i] = litFalse
	}
	for i := 0; i < n; i++ {
		partial := make([]Lit, n)
		for j := 0; j < n; j++ {
			partial[j] = litFalse
		}
		for j := 0; i+j < n; j++ {
			partial[i+j] = c.cnf.and(a[j], b[i])
		}
		acc = c.bvAdd(acc, partial)
	}
	return acc
}

func (c *compileContext) bvExtend(a []Lit, width int, signed bool) []Lit {
	out := make([]Lit, width)
	copy(out, a)
	fill := litFalse
	if signed && len(a) > 0 {
		fill = a[len(a)-1]
	}
	for i := len(a); i < width; i++ {
		out[i] = fill
	}
	return out
}

func (c *compileContext) bvCompare(a, b []Lit, pred CmpPred, signed bool) Lit {
	eq := c.cnf.andAll(func() []Lit {
		out := make([]Lit, len(a))
		for i := range a {
			out[i] = c.cnf.eq(a[i], b[i])
		}
		return out
	}())
	lt := c.bvUnsignedLt(a, b, signed)
	switch pred {
	case CmpEq:
		return eq
	case CmpNe:
		return neg(eq)
	case CmpLt:
		return lt
	case CmpLe:
		return c.cnf.or(lt, eq)
	case CmpGt:
		return c.cnf.and(neg(lt), neg(eq))
	case CmpGe:
		return neg(lt)
	default:
		return litFalse
	}
}

func (c *compileContext) bvUnsignedLt(a, b []Lit, signed bool) Lit {
	if len(a) == 0 {
		return litFalse
	}
	aa := append([]Lit(nil), a...)
	bb := append([]Lit(nil), b...)
	if signed {
		aa[len(aa)-1] = neg(aa[len(aa)-1])
		bb[len(bb)-1] = neg(bb[len(bb)-1])
	}
	lt := litFalse
	for i := 0; i < len(aa); i++ {
		less := c.cnf.and(neg(aa[i]), bb[i])
		same := c.cnf.eq(aa[i], bb[i])
		lt = c.cnf.or(less, c.cnf.and(same, lt))
	}
	return lt
}

func (c *compileContext) bvOverflow(a, b []Lit, op Op, check OverflowCheck) Lit {
	const wide = 64
	aa := c.bvExtend(a, wide, check.Signed)
	bb := c.bvExtend(b, wide, check.Signed)
	var result []Lit
	switch op {
	case OpAdd:
		result = c.bvAdd(aa, bb)
	case OpSub:
		result = c.bvSub(aa, bb)
	case OpMul:
		result = c.bvMul(aa, bb)
	default:
		return litFalse
	}
	cw := int(check.Width)
	if cw >= wide {
		return litFalse
	}
	if cw <= 0 {
		cw = 1
	}
	if check.Signed {
		sign := result[cw-1]
		bad := make([]Lit, 0, wide-cw)
		for i := cw; i < wide; i++ {
			bad = append(bad, c.cnf.xor(result[i], sign))
		}
		return c.cnf.orAll(bad)
	}
	return c.cnf.orAll(result[cw:])
}

func nonZero(c *compileContext, bits []Lit) Lit {
	return c.cnf.orAll(bits)
}

func signedEqual(c *compileContext, a, b []Lit) Lit {
	width := len(a)
	if len(b) > width {
		width = len(b)
	}
	aa := c.bvExtend(a, width+1, true)
	bb := c.bvExtend(b, width+1, true)
	eqs := make([]Lit, len(aa))
	for i := range aa {
		eqs[i] = c.cnf.eq(aa[i], bb[i])
	}
	return c.cnf.andAll(eqs)
}

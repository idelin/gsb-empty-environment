package netsec

// bvec is a bit-vector value in the symbolic domain: bits[0] is LSB.
type bvec struct {
	width int
	bits  []*BNode
}

// engine bit-blasts one graph into boolean expressions using a shared
// variable factory so two graphs can be fed into a single SAT query.
type engine struct {
	g            *Graph
	factory      *BFactory
	prefix       string
	cache        map[int]bvec
	mulWideCache map[int][]*BNode
}

func newEngine(g *Graph, f *BFactory, prefix string) *engine {
	return &engine{
		g:            g,
		factory:      f,
		prefix:       prefix,
		cache:        map[int]bvec{},
		mulWideCache: map[int][]*BNode{},
	}
}

func (e *engine) inputVar(index, bit int) *BNode {
	// Input variables are shared across the old and new graph so a SAT model
	// assigns the same concrete input bytes to both.
	return e.factory.Var("in" + itoa(index) + ":" + itoa(bit))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [24]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func constVec(f *BFactory, width int, imm uint64) bvec {
	bits := make([]*BNode, width)
	for i := 0; i < width; i++ {
		bits[i] = f.Const(imm&(uint64(1)<<i) != 0)
	}
	return bvec{width: width, bits: bits}
}

func (e *engine) blast(id int) bvec {
	if v, ok := e.cache[id]; ok {
		return v
	}
	v := e.g.Values[id]
	f := e.factory
	var out bvec
	switch v.Op {
	case OpConst:
		out = constVec(f, v.Width, v.Imm)
	case OpInput:
		bits := make([]*BNode, 8)
		for i := 0; i < 8; i++ {
			bits[i] = e.inputVar(v.Input, i)
		}
		out = bvec{width: 8, bits: bits}
	case OpAdd, OpSub, OpMul:
		a := e.blast(v.X)
		b := e.blast(v.Y)
		switch v.Op {
		case OpAdd:
			out = e.blastAdd(v.Width, a, b, false)
		case OpSub:
			out = e.blastAdd(v.Width, a, b, true)
		case OpMul:
			wide := e.mulWide(v)
			out = bvec{width: v.Width, bits: append([]*BNode(nil), wide[:v.Width]...)}
		}
	case OpOverflow:
		out = e.blastOverflow(v)
	case OpTrunc:
		a := e.blast(v.X)
		out = bvec{width: v.Width, bits: append([]*BNode(nil), a.bits[:v.Width]...)}
	case OpZExt:
		a := e.blast(v.X)
		bits := make([]*BNode, v.Width)
		copy(bits, a.bits)
		for i := a.width; i < v.Width; i++ {
			bits[i] = f.Const(false)
		}
		out = bvec{width: v.Width, bits: bits}
	case OpSExt:
		a := e.blast(v.X)
		bits := make([]*BNode, v.Width)
		copy(bits, a.bits)
		sign := a.bits[a.width-1]
		for i := a.width; i < v.Width; i++ {
			bits[i] = sign
		}
		out = bvec{width: v.Width, bits: bits}
	case OpEq, OpNe, OpSlt, OpSle:
		a := e.blast(v.X)
		b := e.blast(v.Y)
		out = bvec{width: 1, bits: []*BNode{e.blastCompare(v.Op, a, b)}}
	case OpNot:
		a := e.blast(v.X)
		out = bvec{width: 1, bits: []*BNode{f.Not(a.bits[0])}}
	case OpAnd:
		a := e.blast(v.X)
		b := e.blast(v.Y)
		out = bvec{width: 1, bits: []*BNode{f.And(a.bits[0], b.bits[0])}}
	case OpOr:
		a := e.blast(v.X)
		b := e.blast(v.Y)
		out = bvec{width: 1, bits: []*BNode{f.Or(a.bits[0], b.bits[0])}}
	}
	e.cache[id] = out
	return out
}

// blastAdd implements a ripple-carry adder/subtractor. For subtraction the
// second operand is bitwise negated with carry-in 1.
func (e *engine) blastAdd(width int, a, b bvec, subtract bool) bvec {
	f := e.factory
	bits := make([]*BNode, width)
	carry := f.Const(subtract)
	for i := 0; i < width; i++ {
		bb := b.bits[i]
		if subtract {
			bb = f.Not(bb)
		}
		sum := f.Xor(f.Xor(a.bits[i], bb), carry)
		carry = f.Or(f.And(a.bits[i], bb),
			f.And(f.Xor(a.bits[i], bb), carry))
		bits[i] = sum
	}
	return bvec{width: width, bits: bits}
}

// addWide returns the low bits of a ripple-carry addition over equal widths.
func addWide(f *BFactory, a, b []*BNode) []*BNode {
	out := make([]*BNode, len(a))
	carry := f.Const(false)
	for i := range a {
		sum := f.Xor(f.Xor(a[i], b[i]), carry)
		carry = f.Or(f.And(a[i], b[i]),
			f.And(f.Xor(a[i], b[i]), carry))
		out[i] = sum
	}
	return out
}

// blastOverflow derives the signed overflow flag of the referenced
// arithmetic value without re-blasting the operation.
func (e *engine) blastOverflow(v *Value) bvec {
	f := e.factory
	arith := e.g.Values[v.X]
	a := e.blast(arith.X)
	r := e.blast(arith.ID)
	width := arith.Width
	signA, signB := a.bits[width-1], e.blast(arith.Y).bits[width-1]
	signR := r.bits[width-1]
	var ovf *BNode
	switch arith.Op {
	case OpAdd:
		posPos := f.And(f.Not(signA), f.And(f.Not(signB), signR))
		negNeg := f.And(signA, f.And(signB, f.Not(signR)))
		ovf = f.Or(posPos, negNeg)
	case OpSub:
		// a - b overflows when signs differ and result sign equals b's sign
		// (i.e. differs from a's sign).
		diff := f.Xor(signA, signB)
		wrong := f.Xor(signA, signR)
		ovf = f.And(diff, wrong)
	case OpMul:
		// Overflow iff the high half is not exactly the sign extension of
		// the truncated result's sign bit (bit width-1).
		wide := e.mulWide(arith)
		lowSign := wide[width-1]
		bad := f.Const(false)
		for i := width; i < len(wide); i++ {
			bad = f.Or(bad, f.Xor(wide[i], lowSign))
		}
		ovf = bad
	}
	return bvec{width: 1, bits: []*BNode{ovf}}
}

// mulWide returns the double-width symbolic product for overflow checks.
func (e *engine) mulWide(arith *Value) []*BNode {
	if cached, ok := e.mulWideCache[arith.ID]; ok {
		return cached
	}
	width := arith.Width
	a := e.blast(arith.X)
	b := e.blast(arith.Y)
	f := e.factory
	doubleW := 2 * width

	// Signed product via the identity
	//   signed(a) * signed(b) = unsigned(a)*unsigned(b)
	//       - sign(a)*2^w*unsigned(b) - sign(b)*unsigned(a)*2^w.
	// All arithmetic is kept in 2w bits; subtraction uses two's complement,
	// whose low 2w bits are exact.
	acc := make([]*BNode, doubleW)
	for i := range acc {
		acc[i] = f.Const(false)
	}
	signA, signB := a.bits[width-1], b.bits[width-1]

	// Unsigned partial-product sum.
	for i := 0; i < width; i++ {
		for j := 0; j < width; j++ {
			row := make([]*BNode, doubleW)
			for k := range row {
				row[k] = f.Const(false)
			}
			row[i+j] = f.And(a.bits[i], b.bits[j])
			acc = addWide(f, acc, row)
		}
	}

	// signA * unsigned(b), shifted left by w; then subtract it.
	corrA := make([]*BNode, doubleW)
	for k := range corrA {
		corrA[k] = f.Const(false)
	}
	for j := 0; j < width; j++ {
		corrA[width+j] = f.And(signA, b.bits[j])
	}
	acc = subWide(f, acc, corrA)

	// signB * unsigned(a), shifted left by w; then subtract it.
	corrB := make([]*BNode, doubleW)
	for k := range corrB {
		corrB[k] = f.Const(false)
	}
	for i := 0; i < width; i++ {
		corrB[width+i] = f.And(signB, a.bits[i])
	}
	acc = subWide(f, acc, corrB)

	e.mulWideCache[arith.ID] = acc
	return acc
}

// subWide returns a - b over equal-width bit vectors, using two's complement
// (b negated with carry-in 1).
func subWide(f *BFactory, a, b []*BNode) []*BNode {
	nb := make([]*BNode, len(b))
	for i := range b {
		nb[i] = f.Not(b[i])
	}
	out := make([]*BNode, len(a))
	carry := f.Const(true)
	for i := 0; i < len(out); i++ {
		sum := f.Xor(f.Xor(a[i], nb[i]), carry)
		carry = f.Or(f.And(a[i], nb[i]),
			f.And(f.Xor(a[i], nb[i]), carry))
		out[i] = sum
	}
	return out
}

func (e *engine) blastCompare(op Op, a, b bvec) *BNode {
	f := e.factory
	width := a.width
	eq := f.Const(true)
	// gt/lt accumulate the most recent (highest processed) bit where the
	// operands differ, so no explicit gating on the running equality is
	// required.
	lt := f.Const(false)
	for i := 0; i < width; i++ {
		ai, bi := a.bits[i], b.bits[i]
		bOne := f.And(f.Not(ai), bi)
		diff := f.Xor(ai, bi)
		// Only overwrite the ordering when this bit actually differs.
		lt = f.Or(f.And(diff, bOne), f.And(f.Not(diff), lt))
		eq = f.And(eq, f.Not(diff))
	}
	// Signed ordering: same as unsigned after flipping both sign bits, which
	// is equivalent to flipping the unsigned result based on sign difference.
	signDiff := f.Xor(a.bits[width-1], b.bits[width-1])
	signedLt := f.Or(f.And(signDiff, a.bits[width-1]),
		f.And(f.Not(signDiff), lt))
	switch op {
	case OpEq:
		return eq
	case OpNe:
		return f.Not(eq)
	case OpSlt:
		return signedLt
	case OpSle:
		return f.Or(signedLt, eq)
	default:
		panic("netsec: bad compare op")
	}
}

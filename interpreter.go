package netsec

import "fmt"

// OutcomeKind distinguishes normal returns from reported errors.
type OutcomeKind uint8

const (
	OutcomeInvalid OutcomeKind = iota
	OutcomeReturn
	OutcomeError
)

// Outcome is the observable result of running a graph on one input.
type Outcome struct {
	Kind    OutcomeKind
	Value   uint64 // OutcomeReturn: masked returned integer
	Width   int    // OutcomeReturn: return width
	ErrNode int    // OutcomeError: source node reported by the error terminator
}

// Interpret is the independent reference evaluator. It does not share any
// symbolic machinery; arithmetic is evaluated directly on fixed-width words
// with separately derived signed-overflow flags.
func Interpret(g *Graph, inputs []byte) (Outcome, error) {
	if err := g.Validate(); err != nil {
		return Outcome{}, err
	}
	if len(inputs) != g.Inputs {
		return Outcome{}, fmt.Errorf("netsec: expected %d input bytes, got %d", g.Inputs, len(inputs))
	}

	env := newInterpEnv(g)
	cur := g.block(g.Entry)
	steps := 0
	const maxSteps = 1_000_000
	for {
		steps++
		if steps > maxSteps {
			return Outcome{}, fmt.Errorf("netsec: interpreter step limit exceeded")
		}
		for _, vid := range cur.Insts {
			v := g.Values[vid]
			env.eval(v, inputs)
		}
		switch cur.Term {
		case TermReturn:
			rv := g.Values[cur.Ret]
			return Outcome{
				Kind:  OutcomeReturn,
				Value: env.vals[cur.Ret],
				Width: rv.Width,
			}, nil
		case TermError:
			return Outcome{Kind: OutcomeError, ErrNode: cur.ErrNode}, nil
		case TermBranch:
			if env.vals[cur.Cond] == 1 {
				cur = g.block(cur.Then)
			} else {
				cur = g.block(cur.Else)
			}
		default:
			return Outcome{}, fmt.Errorf("netsec: block %d missing terminator", cur.ID)
		}
	}
}

type interpEnv struct {
	g        *Graph
	vals     map[int]uint64
	overflow map[int]bool
}

func newInterpEnv(g *Graph) *interpEnv {
	return &interpEnv{
		g:        g,
		vals:     make(map[int]uint64, len(g.Values)),
		overflow: make(map[int]bool, len(g.Values)),
	}
}

func (e *interpEnv) eval(v *Value, inputs []byte) {
	if _, done := e.vals[v.ID]; done {
		return
	}
	mask := widthMask(v.Width)
	switch v.Op {
	case OpConst:
		e.vals[v.ID] = v.Imm & mask
	case OpInput:
		e.vals[v.ID] = uint64(inputs[v.Input])
	case OpAdd, OpSub, OpMul:
		x := e.g.Values[v.X]
		y := e.g.Values[v.Y]
		e.eval(x, inputs)
		e.eval(y, inputs)
		a, b := e.vals[x.ID], e.vals[y.ID]
		var raw uint64
		var ovf bool
		switch v.Op {
		case OpAdd:
			raw, ovf = signedAdd(v.Width, a, b)
		case OpSub:
			raw, ovf = signedSub(v.Width, a, b)
		case OpMul:
			raw, ovf = signedMul(v.Width, a, b)
		}
		e.vals[v.ID] = raw & mask
		e.overflow[v.ID] = ovf
	case OpOverflow:
		x := e.g.Values[v.X]
		e.eval(x, inputs)
		if e.overflow[x.ID] {
			e.vals[v.ID] = 1
		} else {
			e.vals[v.ID] = 0
		}
	case OpTrunc:
		x := e.g.Values[v.X]
		e.eval(x, inputs)
		e.vals[v.ID] = e.vals[x.ID] & mask
	case OpZExt:
		x := e.g.Values[v.X]
		e.eval(x, inputs)
		e.vals[v.ID] = e.vals[x.ID] & mask
	case OpSExt:
		x := e.g.Values[v.X]
		e.eval(x, inputs)
		e.vals[v.ID] = signExtend(e.vals[x.ID], x.Width, v.Width)
	case OpEq, OpNe, OpSlt, OpSle:
		x := e.g.Values[v.X]
		y := e.g.Values[v.Y]
		e.eval(x, inputs)
		e.eval(y, inputs)
		a := toSigned(e.vals[x.ID], x.Width)
		b := toSigned(e.vals[y.ID], y.Width)
		var r bool
		switch v.Op {
		case OpEq:
			r = a == b
		case OpNe:
			r = a != b
		case OpSlt:
			r = a < b
		case OpSle:
			r = a <= b
		}
		if r {
			e.vals[v.ID] = 1
		} else {
			e.vals[v.ID] = 0
		}
	case OpNot:
		x := e.g.Values[v.X]
		e.eval(x, inputs)
		if e.vals[x.ID] == 0 {
			e.vals[v.ID] = 1
		} else {
			e.vals[v.ID] = 0
		}
	case OpAnd, OpOr:
		x := e.g.Values[v.X]
		y := e.g.Values[v.Y]
		e.eval(x, inputs)
		e.eval(y, inputs)
		var r uint64
		if v.Op == OpAnd {
			if e.vals[x.ID] == 1 && e.vals[y.ID] == 1 {
				r = 1
			}
		} else if e.vals[x.ID] == 1 || e.vals[y.ID] == 1 {
			r = 1
		}
		e.vals[v.ID] = r
	}
}

func toSigned(raw uint64, width int) int64 {
	mask := widthMask(width)
	raw &= mask
	signBit := uint64(1) << (width - 1)
	if raw&signBit != 0 {
		return int64(raw) - int64(uint64(1)<<width)
	}
	return int64(raw)
}

func signExtend(raw uint64, fromWidth, toWidth int) uint64 {
	signBit := uint64(1) << (fromWidth - 1)
	if raw&signBit != 0 {
		raw |= widthMask(toWidth) ^ widthMask(fromWidth)
	}
	return raw & widthMask(toWidth)
}

func signedAdd(width int, a, b uint64) (uint64, bool) {
	sum := a + b
	mask := widthMask(width)
	sa, sb := toSigned(a, width), toSigned(b, width)
	ovf := (sa > 0 && sb > 0 && toSigned(sum&mask, width) < 0) ||
		(sa < 0 && sb < 0 && toSigned(sum&mask, width) >= 0)
	return sum, ovf
}

func signedSub(width int, a, b uint64) (uint64, bool) {
	diff := a - b
	mask := widthMask(width)
	sa, sb := toSigned(a, width), toSigned(b, width)
	ovf := (sa >= 0 && sb < 0 && toSigned(diff&mask, width) < 0) ||
		(sa < 0 && sb >= 0 && toSigned(diff&mask, width) >= 0)
	return diff, ovf
}

// signedMul computes in double width and detects results that do not fit the
// signed target width, independently of the symbolic engine.
func signedMul(width int, a, b uint64) (uint64, bool) {
	sa, sb := toSigned(a, width), toSigned(b, width)
	wide := uint64(sa * sb)
	mask := widthMask(width)
	low := wide & mask
	ovf := toSigned(low, width) != sa*sb
	return low, ovf
}

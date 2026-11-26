package seccomp

import "math"

// Dom is the reduced-product abstract domain for one 32-bit value:
//
//   - a tristate bit domain: bits in km are known, bits in on are known 1
//     (on is always a subset of km);
//   - an unsigned inclusive interval [lo, hi].
//
// All transfer functions are sound over-approximations.
type Dom struct {
	km, on uint32
	lo, hi uint32
}

func topDom() Dom { return Dom{lo: 0, hi: math.MaxUint32} }

func constDom(c uint32) Dom {
	return Dom{km: math.MaxUint32, on: c, lo: c, hi: c}
}

// bitsDom builds a domain where mask bits km are known and on bits are 1.
func bitsDom(km, on uint32) Dom {
	on &= km
	return Dom{km: km, on: on, lo: on, hi: math.MaxUint32}
}

func (d Dom) bottom() bool {
	// A bit cannot be both known zero and known one; represented by on
	// subset of km so conflict is tracked during meet.  Interval emptiness
	// is encoded by lo>hi.
	return d.lo > d.hi
}

func (d Dom) isConst() (uint32, bool) {
	if d.km == math.MaxUint32 {
		return d.on, true
	}
	return 0, false
}

func (d Dom) zeroKnown() uint32 { return d.km &^ d.on }

// meetBits intersects two tristate bit domains; ok=false on contradiction.
func meetBits(a, b Dom) (Dom, bool) {
	common := a.km & b.km
	if (a.on^b.on)&common != 0 {
		return Dom{}, false
	}
	km := a.km | b.km
	on := a.on | b.on
	lo := a.lo
	if b.lo > lo {
		lo = b.lo
	}
	hi := a.hi
	if b.hi < hi {
		hi = b.hi
	}
	out := Dom{km: km, on: on, lo: lo, hi: hi}
	if lo > hi {
		// Intervals may be conservative; a contradiction here is still a
		// valid prune because intervals are sound.
		return Dom{}, false
	}
	return out, true
}

func mayEqConst(d Dom, c uint32) bool {
	if (c & d.km) != d.on {
		return false
	}
	return c >= d.lo && c <= d.hi
}

func mayEqDom(a, b Dom) bool {
	if (a.on & b.zeroKnown()) != 0 {
		return false
	}
	if (b.on & a.zeroKnown()) != 0 {
		return false
	}
	return max32(a.lo, b.lo) <= min32(a.hi, b.hi)
}

func max32(a, b uint32) uint32 {
	if a > b {
		return a
	}
	return b
}

func min32(a, b uint32) uint32 {
	if a < b {
		return a
	}
	return b
}

// EOp identifies a symbolic bit-vector operator.
type EOp uint8

const (
	EVar EOp = iota
	EConst
	EAnd
	EOr
	EXor
	EAdd
	ESub
	EMul
	EDiv
	EMod
	EShl
	EShr
	ENeg
)

// Expr is an immutable bit-vector expression DAG over 32-bit words.
type Expr struct {
	op  EOp
	vid int
	c   uint32
	l   *Expr
	r   *Expr
}

func eVar(id int) *Expr     { return &Expr{op: EVar, vid: id} }
func eConst(c uint32) *Expr { return &Expr{op: EConst, c: c} }
func eBin(op EOp, l, r *Expr) *Expr {
	return &Expr{op: op, l: l, r: r}
}
func eUnary(op EOp, l *Expr) *Expr { return &Expr{op: op, l: l} }

// exprVars collects referenced variable ids.
func exprVars(e *Expr, set map[int]struct{}) {
	if e == nil {
		return
	}
	switch e.op {
	case EVar:
		set[e.vid] = struct{}{}
	case EConst:
	default:
		exprVars(e.l, set)
		exprVars(e.r, set)
	}
}

// evalConcrete evaluates e under concrete word values.  ok=false indicates
// division/modulo by zero.
func evalConcrete(e *Expr, vals []uint32) (v uint32, ok bool) {
	switch e.op {
	case EVar:
		if e.vid < 0 || e.vid >= len(vals) {
			return 0, true
		}
		return vals[e.vid], true
	case EConst:
		return e.c, true
	case ENeg:
		a, ok := evalConcrete(e.l, vals)
		if !ok {
			return 0, false
		}
		return -a, true
	}
	a, ok := evalConcrete(e.l, vals)
	if !ok {
		return 0, false
	}
	b, ok := evalConcrete(e.r, vals)
	if !ok {
		return 0, false
	}
	switch e.op {
	case EAnd:
		return a & b, true
	case EOr:
		return a | b, true
	case EXor:
		return a ^ b, true
	case EAdd:
		return a + b, true
	case ESub:
		return a - b, true
	case EMul:
		return a * b, true
	case EDiv:
		if b == 0 {
			return 0, false
		}
		return a / b, true
	case EMod:
		if b == 0 {
			return 0, false
		}
		return a % b, true
	case EShl:
		return shiftLeft32(a, b), true
	case EShr:
		return shiftRight32(a, b), true
	}
	return 0, false
}

// evalDom evaluates the abstract domain of e under variable domains dv.
func evalDom(e *Expr, dv []Dom) Dom {
	switch e.op {
	case EVar:
		if e.vid >= 0 && e.vid < len(dv) {
			return dv[e.vid]
		}
		return topDom()
	case EConst:
		return constDom(e.c)
	case ENeg:
		d := evalDom(e.l, dv)
		if c, ok := d.isConst(); ok {
			return constDom(-c)
		}
		return topDom()
	}
	dl := evalDom(e.l, dv)
	dr := evalDom(e.r, dv)
	return binDom(e.op, dl, dr)
}

func binDom(op EOp, l, r Dom) Dom {
	if cl, ok := l.isConst(); ok {
		if cr, ok := r.isConst(); ok {
			switch op {
			case EAnd:
				return constDom(cl & cr)
			case EOr:
				return constDom(cl | cr)
			case EXor:
				return constDom(cl ^ cr)
			case EAdd:
				return constDom(cl + cr)
			case ESub:
				return constDom(cl - cr)
			case EMul:
				return constDom(cl * cr)
			case EDiv:
				if cr == 0 {
					return topDom()
				}
				return constDom(cl / cr)
			case EMod:
				if cr == 0 {
					return topDom()
				}
				return constDom(cl % cr)
			case EShl:
				return constDom(shiftLeft32(cl, cr))
			case EShr:
				return constDom(shiftRight32(cl, cr))
			}
		}
	}
	switch op {
	case EAnd:
		return andDom(l, r)
	case EOr:
		return orDom(l, r)
	case EXor:
		return xorDom(l, r)
	case EShl:
		if c, ok := r.isConst(); ok {
			return shlDom(l, c)
		}
	case EShr:
		if c, ok := r.isConst(); ok {
			return shrDom(l, c)
		}
	case EAdd:
		return addDom(l, r)
	case ESub:
		return subDom(l, r)
	}
	return topDom()
}

func andDom(a, b Dom) Dom {
	za := a.km &^ a.on
	zb := b.km &^ b.on
	km := za | zb | (a.on & b.on)
	on := a.on & b.on
	d := Dom{km: km, on: on, lo: on, hi: math.MaxUint32}
	if c, ok := b.isConst(); ok {
		d.hi = c
	} else if c, ok := a.isConst(); ok {
		d.hi = c
	}
	return d
}

func orDom(a, b Dom) Dom {
	za := a.km &^ a.on
	zb := b.km &^ b.on
	on := a.on | b.on
	km := on | (za & zb)
	return Dom{km: km, on: on, lo: on, hi: math.MaxUint32}
}

func xorDom(a, b Dom) Dom {
	km := a.km & b.km
	on := (a.on ^ b.on) & km
	return Dom{km: km, on: on, lo: on, hi: math.MaxUint32}
}

func shlDom(d Dom, n uint32) Dom {
	if n == 0 {
		return d
	}
	if n >= 32 {
		return constDom(0)
	}
	km := (d.km << n) | (uint32(1)<<n - 1)
	on := d.on << n
	lo64 := uint64(d.lo) << n
	hi64 := uint64(d.hi) << n
	if hi64 > math.MaxUint32 {
		return Dom{km: km, on: on, lo: on, hi: math.MaxUint32}
	}
	return Dom{km: km, on: on, lo: uint32(lo64), hi: uint32(hi64)}
}

func shrDom(d Dom, n uint32) Dom {
	if n == 0 {
		return d
	}
	if n >= 32 {
		return constDom(0)
	}
	km := (d.km >> n) | (math.MaxUint32 << (32 - n))
	on := d.on >> n
	return Dom{km: km, on: on, lo: d.lo >> n, hi: d.hi >> n}
}

func addDom(a, b Dom) Dom {
	lo64 := uint64(a.lo) + uint64(b.lo)
	hi64 := uint64(a.hi) + uint64(b.hi)
	if hi64 > math.MaxUint32 {
		return topDom()
	}
	return Dom{lo: uint32(lo64), hi: uint32(hi64)}
}

func subDom(a, b Dom) Dom {
	if a.lo < b.hi {
		// Possible unsigned wrap: widen.
		return topDom()
	}
	return Dom{lo: a.lo - b.hi, hi: a.hi - b.lo}
}

// PredKind enumerates branch predicates.
type PredKind uint8

const (
	PredEQ PredKind = iota
	PredNE
	PredUGT
	PredUGE
	PredULT
	PredULE
)

// Pred is a single path constraint l <kind> r.
type Pred struct {
	Kind PredKind
	L, R *Expr
}

func (p Pred) vars() map[int]struct{} {
	m := map[int]struct{}{}
	exprVars(p.L, m)
	exprVars(p.R, m)
	return m
}

// feasiblePredAbstract reports whether the predicate cannot be disproven
// under the current variable domains (sound over-approx).
func feasiblePredAbstract(p Pred, dv []Dom) bool {
	dl := evalDom(p.L, dv)
	dr := evalDom(p.R, dv)
	switch p.Kind {
	case PredEQ:
		return mayEqDom(dl, dr)
	case PredNE:
		if cl, ok := dl.isConst(); ok {
			if cr, ok := dr.isConst(); ok {
				return cl != cr
			}
		}
		return true
	case PredUGT:
		return dl.hi > dr.lo
	case PredUGE:
		return dl.hi >= dr.lo
	case PredULT:
		return dl.lo < dr.hi
	case PredULE:
		return dl.lo <= dr.hi
	}
	return true
}

// checkPredExact evaluates a predicate exactly when all referenced variables
// are constant.  The second return is false when not yet fully concrete.
func checkPredExact(p Pred, vals []uint32) (bool, bool) {
	needed := p.vars()
	for id := range needed {
		if id < 0 || id >= len(vals) {
			return false, false
		}
	}
	lv, lok := evalConcrete(p.L, vals)
	rv, rok := evalConcrete(p.R, vals)
	if !lok || !rok {
		// Division by zero in the evaluated expression invalidates this
		// assignment for a non-divzero path.
		return false, true
	}
	switch p.Kind {
	case PredEQ:
		return lv == rv, true
	case PredNE:
		return lv != rv, true
	case PredUGT:
		return lv > rv, true
	case PredUGE:
		return lv >= rv, true
	case PredULT:
		return lv < rv, true
	case PredULE:
		return lv <= rv, true
	}
	return false, true
}

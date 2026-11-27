package minijit

import (
	"math"
	"testing"
)

type builder struct {
	f *Function
}

func newBuilder(name string, nArgs, nLocals int) *builder {
	return &builder{f: NewFunction(name, nArgs, nLocals)}
}

func (b *builder) constIdx(v Value) int { return b.f.AddConst(v) }

func (b *builder) loadConst(v Value) *builder {
	b.f.Emit(OpLoadConst, 0, b.constIdx(v))
	return b
}

func (b *builder) loadLocal(slot int) *builder {
	b.f.Emit(OpLoadLocal, slot, 0)
	return b
}

func (b *builder) storeLocal(slot int) *builder {
	b.f.Emit(OpStoreLocal, slot, 0)
	return b
}

func (b *builder) add() *builder   { b.f.Emit(OpAdd, 0, 0); return b }
func (b *builder) sub() *builder   { b.f.Emit(OpSub, 0, 0); return b }
func (b *builder) less() *builder  { b.f.Emit(OpLess, 0, 0); return b }
func (b *builder) equal() *builder { b.f.Emit(OpEqual, 0, 0); return b }
func (b *builder) newObject() *builder {
	b.f.Emit(OpNewObject, 0, 0)
	return b
}

func (b *builder) getField() *builder { b.f.Emit(OpGetField, 0, 0); return b }
func (b *builder) setField() *builder { b.f.Emit(OpSetField, 0, 0); return b }
func (b *builder) transition() *builder {
	b.f.Emit(OpTransitionClass, 0, 0)
	return b
}

func (b *builder) guardClass() *builder {
	b.f.Emit(OpGuardClass, 0, 0)
	return b
}

func (b *builder) call(nArgs int) *builder {
	b.f.Emit(OpCall, nArgs, 0)
	return b
}

func (b *builder) pop() *builder {
	b.f.Emit(OpPop, 0, 0)
	return b
}

func (b *builder) ret() *builder { b.f.Emit(OpReturn, 0, 0); return b }
func (b *builder) throw() *builder {
	b.f.Emit(OpThrow, 0, 0)
	return b
}

func (b *builder) jump(target int) *builder {
	b.f.Emit(OpJump, target, 0)
	return b
}

func (b *builder) jumpIfFalse(target int) *builder {
	b.f.Emit(OpJumpIfFalse, target, 0)
	return b
}

func (b *builder) here() int { return len(b.f.Code) }

func (b *builder) patchJump(emittedAt, target int) { b.f.Code[emittedAt].A = target }

func (b *builder) handler(start, end, target int) *builder {
	b.f.AddHandler(Handler{Start: start, End: end, Target: target})
	return b
}

func (b *builder) function() *Function { return b.f }

func strConst(s string) Value { return StringValue(s) }
func intConst(i int64) Value  { return IntValue(i) }

func runInterp(t *testing.T, prog *Program, entry string, args []Value) Value {
	t.Helper()
	v, err := NewInterpreter(prog).Run(entry, args)
	if err != nil {
		t.Fatalf("interpreter %s failed: %v", entry, err)
	}
	return v
}

func runOptimized(t *testing.T, prog *Program, entry string, args []Value) (Value, *OptimizedRunner) {
	t.Helper()
	r := NewOptimizedRunner(prog)
	v, err := r.Run(entry, args)
	if err != nil {
		t.Fatalf("optimized %s failed: %v", entry, err)
	}
	return v, r
}

func maxInt64() int64 { return math.MaxInt64 }

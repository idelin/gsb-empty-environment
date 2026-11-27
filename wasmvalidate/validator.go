package wasmvalidate

import "fmt"

// frameFunc marks the implicit outermost function control frame. It is larger
// than any real opcode and can never appear in Body.Instrs.
const frameFunc Opcode = 0xFFFF

// ctrlFrame mirrors the control frame of the specification appendix:
// opcode, start_types, end_types, height and the unreachable flag.
type ctrlFrame struct {
	opcode      Opcode
	startTypes  []ValType
	endTypes    []ValType
	height      int
	unreachable bool
	inElse      bool
}

type validator struct {
	mod   *Module
	body  *Body
	stack []ValType
	ctrl  []*ctrlFrame
	index int
}

// Validate runs the single-pass structured stack typing algorithm over one
// function body. A non-nil error is always a *ValidationError carrying the
// offending instruction index and an ErrorKind classification.
func Validate(mod *Module, body *Body) (err error) {
	v := &validator{mod: mod, body: body}
	defer func() {
		if r := recover(); r != nil {
			if ve, ok := r.(*ValidationError); ok {
				err = ve
				return
			}
			panic(r)
		}
	}()
	v.run()
	return nil
}

func (v *validator) run() {
	if v.mod == nil || v.body == nil {
		v.index = -1
		v.fail(ErrInvalidValType, "nil module or body")
	}
	v.pushCtrl(frameFunc, nil, append([]ValType(nil), v.body.Type.Results...))
	for i := range v.body.Instrs {
		v.index = i
		v.exec(v.body.Instrs[i])
	}

	// Function end: the implicit frame must be the only frame left and the
	// stack must match the function result types exactly.
	if len(v.ctrl) != 1 {
		v.index = -1
		v.fail(ErrUnclosedBlock, "%d structured instruction(s) lack a matching end",
			len(v.ctrl)-1)
	}
	frame := v.ctrl[0]
	v.index = -1
	v.expectVals(frame.endTypes)
	if len(v.stack) > frame.height {
		v.fail(ErrExtraOperands, "function end leaves %d operand(s), expected %d result type(s)",
			len(v.stack), len(frame.endTypes))
	}
}

func (v *validator) fail(kind ErrorKind, format string, args ...any) {
	panic(&ValidationError{
		Kind:       kind,
		InstrIndex: v.index,
		Message:    fmt.Sprintf(format, args...),
	})
}

// ---- operand stack ---------------------------------------------------------

func (v *validator) pushVal(t ValType) {
	v.stack = append(v.stack, t)
}

// popVal implements the polymorphic pop: popping at the height of an
// unreachable frame yields the Unknown placeholder instead of underflowing.
func (v *validator) popVal() ValType {
	frame := v.topCtrl()
	if len(v.stack) == frame.height {
		if frame.unreachable {
			return Unknown
		}
		v.fail(ErrStackUnderflow, "pop below control frame height %d", frame.height)
	}
	top := v.stack[len(v.stack)-1]
	v.stack = v.stack[:len(v.stack)-1]
	return top
}

// expectVal pops one operand and unifies it with want: Unknown unifies with
// any concrete type, while two concrete types must be identical.
func (v *validator) expectVal(want ValType) {
	got := v.popVal()
	if got != Unknown && got != want {
		v.fail(ErrTypeMismatch, "expected %s operand, got %s", want, got)
	}
}

func (v *validator) expectVals(wants []ValType) {
	for i := len(wants) - 1; i >= 0; i-- {
		v.expectVal(wants[i])
	}
}

// ---- control frame stack ---------------------------------------------------

func (v *validator) pushCtrl(op Opcode, start, end []ValType) {
	v.ctrl = append(v.ctrl, &ctrlFrame{
		opcode:     op,
		startTypes: start,
		endTypes:   end,
		height:     len(v.stack),
	})
}

func (v *validator) topCtrl() *ctrlFrame {
	return v.ctrl[len(v.ctrl)-1]
}

// labelTypes returns the branch label types for a relative depth. Loop labels
// carry start_types (the branch re-enters the loop header); block/if labels
// carry end_types.
func (v *validator) labelTypes(depth int) []ValType {
	if depth < 0 || depth >= len(v.ctrl) {
		v.fail(ErrUnknownLabel, "branch depth %d exceeds control stack depth %d",
			depth, len(v.ctrl))
	}
	frame := v.ctrl[len(v.ctrl)-1-depth]
	if frame.opcode == OpLoop {
		return frame.startTypes
	}
	return frame.endTypes
}

// markUnreachable truncates the operand stack back to the frame height and
// flips the frame into the polymorphic unreachable state.
func (v *validator) markUnreachable() {
	frame := v.topCtrl()
	v.stack = v.stack[:frame.height]
	frame.unreachable = true
}

// popCtrl handles end: end_types must match the stack, no extra values may
// remain, then the frame's result types are pushed onto the outer stack.
func (v *validator) popCtrl() *ctrlFrame {
	frame := v.topCtrl()
	v.expectVals(frame.endTypes)
	if len(v.stack) != frame.height {
		v.fail(ErrExtraOperands, "stack height %d exceeds frame height %d after matching end types",
			len(v.stack), frame.height)
	}
	v.ctrl = v.ctrl[:len(v.ctrl)-1]
	for _, t := range frame.endTypes {
		v.pushVal(t)
	}
	return frame
}

func validValType(t ValType) bool {
	switch t {
	case I32, I64, F32, F64, V128, FuncRef, ExternRef:
		return true
	default:
		return false
	}
}

func typesEqual(a, b []ValType) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (v *validator) resolveBlockType(bt BlockType) (start, end []ValType) {
	switch {
	case bt.Empty:
		return nil, nil
	case bt.SingleValid:
		if !validValType(bt.Single) {
			v.fail(ErrInvalidValType, "invalid single-value block type %#x", byte(bt.Single))
		}
		return nil, []ValType{bt.Single}
	default:
		idx := int(bt.TypeIndex)
		if idx < 0 || idx >= len(v.mod.Types) {
			v.fail(ErrUnknownTypeIndex, "block type index %d out of range (module has %d type(s))",
				idx, len(v.mod.Types))
		}
		ft := v.mod.Types[idx]
		return append([]ValType(nil), ft.Params...), append([]ValType(nil), ft.Results...)
	}
}

package wasmvalidate

func (v *validator) exec(ins Instr) {
	switch ins.Op {
	case OpUnreachable:
		v.markUnreachable()
	case OpNop:

	// ---- structured control instructions ----
	case OpBlock:
		start, end := v.resolveBlockType(ins.BlockType)
		v.expectVals(start)
		v.pushCtrl(OpBlock, start, end)
	case OpLoop:
		start, end := v.resolveBlockType(ins.BlockType)
		v.expectVals(start)
		v.pushCtrl(OpLoop, start, end)
	case OpIf:
		v.expectVal(I32)
		start, end := v.resolveBlockType(ins.BlockType)
		v.expectVals(start)
		v.pushCtrl(OpIf, start, end)
	case OpElse:
		frame := v.topCtrl()
		if frame.opcode != OpIf {
			v.fail(ErrUnexpectedElse, "else is only valid inside an if frame")
		}
		if frame.inElse {
			v.fail(ErrUnexpectedElse, "duplicate else in if frame")
		}
		v.expectVals(frame.endTypes)
		if len(v.stack) != frame.height {
			v.fail(ErrExtraOperands, "if-then leaves extra operands before else")
		}
		frame.inElse = true
		frame.unreachable = false
		v.stack = v.stack[:frame.height]
		for _, t := range frame.startTypes {
			v.pushVal(t)
		}
	case OpEnd:
		if len(v.ctrl) <= 1 {
			v.fail(ErrUnexpectedEnd, "end has no matching block/loop/if")
		}
		frame := v.topCtrl()
		if frame.opcode == OpIf && !frame.inElse &&
			!typesEqual(frame.startTypes, frame.endTypes) {
			v.fail(ErrIfNoElseMultiValue,
				"if without else needs start types %v equal to end types %v",
				frame.startTypes, frame.endTypes)
		}
		v.popCtrl()

	// ---- branches ----
	case OpBr:
		if int(ins.Depth) >= len(v.ctrl) {
			v.fail(ErrUnknownLabel, "br depth %d exceeds control stack depth %d",
				ins.Depth, len(v.ctrl))
		}
		label := v.labelTypes(int(ins.Depth))
		v.expectVals(label)
		v.markUnreachable()
	case OpBrIf:
		if int(ins.Depth) >= len(v.ctrl) {
			v.fail(ErrUnknownLabel, "br_if depth %d exceeds control stack depth %d",
				ins.Depth, len(v.ctrl))
		}
		label := v.labelTypes(int(ins.Depth))
		v.expectVal(I32)
		v.expectVals(label)
		for _, t := range label {
			v.pushVal(t)
		}
	case OpBrTable:
		if len(ins.Targets) == 0 {
			v.fail(ErrEmptyBrTable, "br_table needs at least one non-default target")
		}
		all := make([]uint32, 0, len(ins.Targets)+1)
		all = append(all, ins.Targets...)
		all = append(all, ins.Depth)
		for _, depth := range all {
			if int(depth) >= len(v.ctrl) {
				v.fail(ErrUnknownLabel, "br_table target depth %d exceeds control stack depth %d",
					depth, len(v.ctrl))
			}
		}
		defaultLabel := v.labelTypes(int(ins.Depth))
		for i, depth := range ins.Targets {
			label := v.labelTypes(int(depth))
			if !typesEqual(label, defaultLabel) {
				v.fail(ErrBranchTypeMismatch,
					"br_table target %d label types %v differ from default label types %v",
					i, label, defaultLabel)
			}
		}
		v.expectVal(I32)
		v.expectVals(defaultLabel)
		v.markUnreachable()
	case OpReturn:
		v.expectVals(v.body.Type.Results)
		v.markUnreachable()

	// ---- calls ----
	case OpCall:
		if int(ins.FuncIndex) >= len(v.mod.Funcs) {
			v.fail(ErrUnknownFunc, "call function index %d out of range (module has %d function(s))",
				ins.FuncIndex, len(v.mod.Funcs))
		}
		fn := v.mod.Funcs[ins.FuncIndex]
		v.expectVals(fn.Params)
		for _, t := range fn.Results {
			v.pushVal(t)
		}
	case OpCallIndirect:
		if int(ins.TableIndex) >= len(v.mod.Tables) {
			v.fail(ErrUnknownTable, "call_indirect table index %d out of range (module has %d table(s))",
				ins.TableIndex, len(v.mod.Tables))
		}
		if int(ins.TypeIndex) >= len(v.mod.Types) {
			v.fail(ErrUnknownTypeIndex, "call_indirect type index %d out of range (module has %d type(s))",
				ins.TypeIndex, len(v.mod.Types))
		}
		fn := v.mod.Types[ins.TypeIndex]
		v.expectVal(I32)
		v.expectVals(fn.Params)
		for _, t := range fn.Results {
			v.pushVal(t)
		}

	// ---- variables ----
	case OpLocalGet:
		if int(ins.LocalIndex) >= len(v.body.Locals) {
			v.fail(ErrUnknownLocal, "local.get index %d out of range (body has %d local(s))",
				ins.LocalIndex, len(v.body.Locals))
		}
		v.pushVal(v.body.Locals[ins.LocalIndex])
	case OpLocalSet:
		if int(ins.LocalIndex) >= len(v.body.Locals) {
			v.fail(ErrUnknownLocal, "local.set index %d out of range (body has %d local(s))",
				ins.LocalIndex, len(v.body.Locals))
		}
		v.expectVal(v.body.Locals[ins.LocalIndex])
	case OpLocalTee:
		if int(ins.LocalIndex) >= len(v.body.Locals) {
			v.fail(ErrUnknownLocal, "local.tee index %d out of range (body has %d local(s))",
				ins.LocalIndex, len(v.body.Locals))
		}
		v.expectVal(v.body.Locals[ins.LocalIndex])
		v.pushVal(v.body.Locals[ins.LocalIndex])
	case OpGlobalGet:
		if int(ins.GlobalIndex) >= len(v.mod.Globals) {
			v.fail(ErrUnknownGlobal, "global.get index %d out of range (module has %d global(s))",
				ins.GlobalIndex, len(v.mod.Globals))
		}
		v.pushVal(v.mod.Globals[ins.GlobalIndex].ValType)
	case OpGlobalSet:
		if int(ins.GlobalIndex) >= len(v.mod.Globals) {
			v.fail(ErrUnknownGlobal, "global.set index %d out of range (module has %d global(s))",
				ins.GlobalIndex, len(v.mod.Globals))
		}
		global := v.mod.Globals[ins.GlobalIndex]
		if !global.Mutable {
			v.fail(ErrImmutableGlobal, "global.set targets immutable global %d", ins.GlobalIndex)
		}
		v.expectVal(global.ValType)

	// ---- parametric ----
	case OpDrop:
		v.popVal()
	case OpSelect:
		v.execSelect(false, nil)
	case OpTypedSelect:
		v.execSelect(true, ins.ValTypes)

	default:
		v.execNumeric(ins)
	}
}

// execSelect handles both the untyped (0x1B) and typed (0x1C) forms.
// Stack order, top to bottom: condition, val2, val1.
func (v *validator) execSelect(typed bool, types []ValType) {
	v.expectVal(I32)
	t2 := v.popVal()
	t1 := v.popVal()
	if typed {
		if len(types) != 1 || !validValType(types[0]) {
			v.fail(ErrSelectType, "typed select requires exactly one valid result type")
		}
		want := types[0]
		if t1 != Unknown && t1 != want {
			v.fail(ErrTypeMismatch, "typed select first operand expected %s, got %s", want, t1)
		}
		if t2 != Unknown && t2 != want {
			v.fail(ErrTypeMismatch, "typed select second operand expected %s, got %s", want, t2)
		}
		v.pushVal(want)
		return
	}

	// Untyped select: both operands must be numeric/vector types and equal;
	// two Unknown placeholders stay Unknown rather than inventing a type.
	switch {
	case t1 == Unknown && t2 == Unknown:
		v.pushVal(Unknown)
	case t1 == Unknown:
		if !t2.IsNumeric() {
			v.fail(ErrSelectType, "untyped select operand type %s is not numeric or vector", t2)
		}
		v.pushVal(t2)
	case t2 == Unknown:
		if !t1.IsNumeric() {
			v.fail(ErrSelectType, "untyped select operand type %s is not numeric or vector", t1)
		}
		v.pushVal(t1)
	default:
		if !t1.IsNumeric() || !t2.IsNumeric() {
			v.fail(ErrSelectType, "untyped select requires numeric or vector operands, got %s and %s", t1, t2)
		}
		if t1 != t2 {
			v.fail(ErrTypeMismatch, "untyped select operands must have equal types, got %s and %s", t1, t2)
		}
		v.pushVal(t1)
	}
}

package shadow

// Op identifies one decoded instruction accepted by the embedded host.
type Op int

const (
	OpMov Op = iota
	OpAnd
	OpOr
	OpXor
	OpNot
	OpAdd
	OpAdc
	OpSub
	OpSbb
	OpCmp
	OpShl
	OpShr
	OpSar
	OpLoad
	OpStore
	OpIndirectJump
)

// OperandKind identifies how an instruction operand is located.
type OperandKind int

const (
	OperandRegister OperandKind = iota
	OperandImmediate
	OperandMemory
)

// Operand is a register, immediate, or byte-addressed memory location.
type Operand struct {
	Kind          OperandKind
	Register      string
	Immediate     Value
	MemoryAddress Value
	Width         Width
}

// Instruction is a small machine-independent IR. A Go host decodes real target
// instructions into this form; the package never receives or executes native code.
type Instruction struct {
	Op    Op
	Dst   Operand
	SrcA  Operand
	SrcB  Operand
	Count Operand
}

// Execute executes one IR instruction. Indirect jumps return a non-nil target.
func (machine *Machine) Execute(instruction Instruction) (*JumpTarget, error) {
	switch instruction.Op {
	case OpMov:
		source, err := machine.readOperand(instruction.SrcA)
		if err != nil {
			return nil, err
		}
		return nil, machine.writeOperand(instruction.Dst, source)
	case OpAnd, OpOr, OpXor, OpAdd, OpAdc, OpSub, OpSbb:
		return nil, machine.executeBinary(instruction)
	case OpNot:
		source, err := machine.readOperand(instruction.SrcA)
		if err != nil {
			return nil, err
		}
		return nil, machine.writeOperand(instruction.Dst, machine.Not(source))
	case OpCmp:
		left, err := machine.readOperand(instruction.SrcA)
		if err != nil {
			return nil, err
		}
		right, err := machine.readOperand(instruction.SrcB)
		if err != nil {
			return nil, err
		}
		_, err = machine.Cmp(left, right)
		return nil, err
	case OpShl, OpShr, OpSar:
		return nil, machine.executeShift(instruction)
	case OpLoad:
		value, err := machine.readOperand(instruction.SrcA)
		if err != nil {
			return nil, err
		}
		return nil, machine.writeOperand(instruction.Dst, value)
	case OpStore:
		value, err := machine.readOperand(instruction.SrcA)
		if err != nil {
			return nil, err
		}
		return nil, machine.writeOperand(instruction.SrcB, value)
	case OpIndirectJump:
		target, err := machine.readOperand(instruction.SrcA)
		if err != nil {
			return nil, err
		}
		jump := machine.IndirectJump(target)
		return &jump, nil
	default:
		return nil, &UnknownOpError{Op: instruction.Op}
	}
}

func (machine *Machine) executeBinary(instruction Instruction) error {
	left, err := machine.readOperand(instruction.SrcA)
	if err != nil {
		return err
	}
	right, err := machine.readOperand(instruction.SrcB)
	if err != nil {
		return err
	}

	var result Value
	switch instruction.Op {
	case OpAnd:
		result, err = machine.And(left, right)
	case OpOr:
		result, err = machine.Or(left, right)
	case OpXor:
		result, err = machine.Xor(left, right)
	case OpAdd:
		result, _, err = machine.Add(left, right)
	case OpAdc:
		result, _, err = machine.Adc(left, right)
	case OpSub:
		result, _, err = machine.Sub(left, right)
	case OpSbb:
		result, _, err = machine.Sbb(left, right)
	default:
		return &UnknownOpError{Op: instruction.Op}
	}
	if err != nil {
		return err
	}
	return machine.writeOperand(instruction.Dst, result)
}

func (machine *Machine) executeShift(instruction Instruction) error {
	value, err := machine.readOperand(instruction.SrcA)
	if err != nil {
		return err
	}

	var result Value
	if instruction.Count.Kind == OperandImmediate {
		count := instruction.Count.Immediate.Uint64()
		switch instruction.Op {
		case OpShl:
			result = machine.ShiftLeftImmediate(value, count)
		case OpShr:
			result = machine.ShiftRightImmediate(value, count)
		default:
			result = machine.ShiftArithmeticRightImmediate(value, count)
		}
	} else {
		count, err := machine.readOperand(instruction.Count)
		if err != nil {
			return err
		}
		switch instruction.Op {
		case OpShl:
			result = machine.ShiftLeft(value, count)
		case OpShr:
			result = machine.ShiftRight(value, count)
		default:
			result = machine.ShiftArithmeticRight(value, count)
		}
	}
	return machine.writeOperand(instruction.Dst, result)
}

func (machine *Machine) readOperand(operand Operand) (Value, error) {
	switch operand.Kind {
	case OperandRegister:
		return machine.ReadRegister(operand.Register, operand.Width)
	case OperandImmediate:
		if operand.Immediate.width != operand.Width {
			return Value{}, &WidthMismatchError{Left: operand.Immediate.width, Right: operand.Width}
		}
		return operand.Immediate, nil
	case OperandMemory:
		return machine.ReadMemoryAt(operand.MemoryAddress, operand.Width)
	default:
		return Value{}, &UnknownOperandError{Kind: operand.Kind}
	}
}

func (machine *Machine) writeOperand(operand Operand, value Value) error {
	switch operand.Kind {
	case OperandRegister:
		if value.width != operand.Width {
			return &WidthMismatchError{Left: value.width, Right: operand.Width}
		}
		return machine.WriteRegister(operand.Register, value)
	case OperandMemory:
		if value.width != operand.Width {
			return &WidthMismatchError{Left: value.width, Right: operand.Width}
		}
		return machine.WriteMemoryAt(operand.MemoryAddress, value)
	default:
		return &UnknownOperandError{Kind: operand.Kind}
	}
}

// UnknownOpError indicates an unsupported IR operation.
type UnknownOpError struct {
	Op Op
}

func (err *UnknownOpError) Error() string {
	return "shadow: unknown operation " + itoa(int(err.Op))
}

// UnknownOperandError indicates an unsupported operand kind.
type UnknownOperandError struct {
	Kind OperandKind
}

func (err *UnknownOperandError) Error() string {
	return "shadow: unknown operand kind " + itoa(int(err.Kind))
}

package linker

import (
	"encoding/binary"
	"fmt"
)

const (
	minInt32 = -1 << 31
	maxInt32 = 1<<31 - 1
)

// Opcode is the instruction set of the built-in section VM.  It is deliberately
// tiny: sections can be used as ordinary opaque linker input, while tests and
// tools that want semantic checking can encode deterministic functions with
// this instruction set.
type Opcode uint8

const (
	OpNop Opcode = iota
	OpRet
	OpPush
	OpDup
	OpDrop
	OpAdd
	OpSub
	OpMul
	OpEq
	OpLt
	OpJmp
	OpJz
	OpJnz
	OpCall
	OpAddr
	OpLoad8
	OpStore8
)

// Instruction is an assembler input.  Imm is used by Push and by JUMP-family
// instructions without a Jump label.  Label defines a label at this
// instruction's address.  Jump names a label for Jmp/Jz/Jnz.
type Instruction struct {
	Op     Opcode
	Imm    int64
	Args   uint8
	Symbol string
	Target SectionID
	Addend int64
	Label  string
	Jump   string
}

// Code is assembled section bytes plus their relocation records.
type Code struct {
	Data        []byte
	Relocations []Relocation
}

// Assemble encodes a stack-machine function.  CALL and ADDR operands are
// 8-byte relocation holes; a CALL instruction is one opcode byte, one argument
// count byte, and the relocation hole.
func Assemble(program []Instruction) (Code, error) {
	labels := make(map[string]int)
	off := 0
	for i, ins := range program {
		n, err := instructionSize(ins)
		if err != nil {
			return Code{}, fmt.Errorf("instruction %d: %w", i, err)
		}
		if ins.Label != "" {
			if _, exists := labels[ins.Label]; exists {
				return Code{}, fmt.Errorf("duplicate label %q", ins.Label)
			}
			labels[ins.Label] = off
		}
		off += n
	}

	data := make([]byte, 0, off)
	var relocs []Relocation
	for i, ins := range program {
		pc := len(data)
		switch ins.Op {
		case OpNop:
			data = append(data, byte(OpNop))
		case OpRet:
			data = append(data, byte(OpRet))
		case OpPush:
			data = append(data, byte(OpPush))
			data = appendI64(data, ins.Imm)
		case OpDup:
			data = append(data, byte(OpDup))
		case OpDrop:
			data = append(data, byte(OpDrop))
		case OpAdd:
			data = append(data, byte(OpAdd))
		case OpSub:
			data = append(data, byte(OpSub))
		case OpMul:
			data = append(data, byte(OpMul))
		case OpEq:
			data = append(data, byte(OpEq))
		case OpLt:
			data = append(data, byte(OpLt))
		case OpJmp, OpJz, OpJnz:
			var rel int64
			if ins.Jump != "" {
				target, ok := labels[ins.Jump]
				if !ok {
					return Code{}, fmt.Errorf("instruction %d: undefined label %q", i, ins.Jump)
				}
				rel = int64(target - (pc + 5))
			} else {
				rel = ins.Imm
			}
			if rel < minInt32 || rel > maxInt32 {
				return Code{}, fmt.Errorf("instruction %d: jump offset out of range", i)
			}
			data = append(data, byte(ins.Op))
			var buf [4]byte
			binary.BigEndian.PutUint32(buf[:], uint32(int32(rel)))
			data = append(data, buf[:]...)
		case OpCall:
			data = append(data, byte(OpCall), ins.Args)
			relocOffset := uint64(len(data))
			data = append(data, make([]byte, 8)...)
			relocs = append(relocs, Relocation{
				From:   NoSection,
				Offset: relocOffset,
				Type:   RelocCall,
				Symbol: ins.Symbol,
				Addend: ins.Addend,
				Target: ins.Target,
				Size:   8,
			})
		case OpAddr:
			data = append(data, byte(OpAddr))
			relocOffset := uint64(len(data))
			data = append(data, make([]byte, 8)...)
			relocs = append(relocs, Relocation{
				From:   NoSection,
				Offset: relocOffset,
				Type:   RelocAddrTaken,
				Symbol: ins.Symbol,
				Addend: ins.Addend,
				Target: ins.Target,
				Size:   8,
			})
		case OpLoad8:
			data = append(data, byte(OpLoad8))
		case OpStore8:
			data = append(data, byte(OpStore8))
		default:
			return Code{}, fmt.Errorf("instruction %d: unknown opcode %d", i, ins.Op)
		}
	}
	return Code{Data: data, Relocations: relocs}, nil
}

func instructionSize(ins Instruction) (int, error) {
	switch ins.Op {
	case OpPush:
		return 9, nil
	case OpJmp, OpJz, OpJnz:
		return 5, nil
	case OpCall:
		return 10, nil
	case OpAddr:
		return 9, nil
	case OpNop, OpRet, OpDup, OpDrop, OpAdd, OpSub, OpMul, OpEq, OpLt, OpLoad8, OpStore8:
		return 1, nil
	default:
		return 0, fmt.Errorf("unknown opcode %d", ins.Op)
	}
}

// ValueKind distinguishes integer and address values.
type ValueKind uint8

const (
	ValueInt ValueKind = iota
	ValuePtr
)

// Address is an abstract pointer: a section plus a byte offset.
type Address struct {
	Section SectionID
	Offset  uint64
}

// Value is a VM value.
type Value struct {
	Kind ValueKind
	Int  int64
	Ptr  Address
}

// Int constructs an integer value.
func Int(v int64) Value { return Value{Kind: ValueInt, Int: v} }

// Ptr constructs an address value.
func Ptr(section SectionID, offset uint64) Value {
	return Value{Kind: ValuePtr, Ptr: Address{Section: section, Offset: offset}}
}

// Equal reports VM-level equality.  Addresses compare by section and offset,
// which is exactly the comparison a C/C++ function-pointer equality check
// performs after relocation.
func (v Value) Equal(w Value) bool {
	if v.Kind != w.Kind {
		return false
	}
	if v.Kind == ValueInt {
		return v.Int == w.Int
	}
	return v.Ptr == w.Ptr
}

func (v Value) String() string {
	if v.Kind == ValueInt {
		return fmt.Sprintf("%d", v.Int)
	}
	return fmt.Sprintf("&[%d]+%d", v.Ptr.Section, v.Ptr.Offset)
}

// VM executes code sections.  A section is a function whose entry is an offset
// inside that section.  Values are passed on the operand stack.
type VM struct {
	img      *Image
	symbols  map[string]Symbol
	relocs   map[SectionID]map[uint64]Relocation
	memory   map[SectionID][]byte
	steps    int
	maxSteps int
	depth    int
}

// Execute runs one section as a zero-based function.
func Execute(img *Image, section SectionID, args []int64, maxSteps int) (Value, error) {
	return executeAt(img, section, 0, args, maxSteps)
}

// ExecuteSymbol runs a named function from its symbol value.
func ExecuteSymbol(img *Image, symbol string, args []int64, maxSteps int) (Value, error) {
	if img == nil {
		return Value{}, fmt.Errorf("linker: nil image")
	}
	sym, ok := img.LookupSymbol(symbol)
	if !ok {
		return Value{}, fmt.Errorf("linker: unknown symbol %q", symbol)
	}
	if sym.Section == NoSection {
		return Value{}, fmt.Errorf("linker: symbol %q is undefined", symbol)
	}
	return executeAt(img, sym.Section, int(sym.Value), args, maxSteps)
}

func executeAt(img *Image, section SectionID, start int, args []int64, maxSteps int) (Value, error) {
	if img == nil {
		return Value{}, fmt.Errorf("linker: nil image")
	}
	if int(section) < 0 || int(section) >= len(img.Sections) {
		return Value{}, fmt.Errorf("linker: invalid section %d", section)
	}
	if maxSteps <= 0 {
		maxSteps = 100000
	}
	vm := &VM{
		img:      img,
		symbols:  img.SymbolMap(),
		relocs:   make(map[SectionID]map[uint64]Relocation),
		memory:   make(map[SectionID][]byte),
		maxSteps: maxSteps,
	}
	for _, rel := range img.Relocations {
		m := vm.relocs[rel.From]
		if m == nil {
			m = make(map[uint64]Relocation)
			vm.relocs[rel.From] = m
		}
		m[rel.Offset] = rel
	}
	stack := make([]Value, 0, len(args))
	for _, a := range args {
		stack = append(stack, Int(a))
	}
	return vm.run(section, start, stack)
}

func (vm *VM) run(section SectionID, start int, stack []Value) (Value, error) {
	if vm.depth >= 512 {
		return Value{}, fmt.Errorf("linker: VM call depth exceeded")
	}
	vm.depth++
	defer func() { vm.depth-- }()

	data := vm.dataFor(section)
	pc := start
	for {
		if vm.steps >= vm.maxSteps {
			return Value{}, fmt.Errorf("linker: VM step limit exceeded")
		}
		vm.steps++
		if pc < 0 || pc >= len(data) {
			return Value{}, fmt.Errorf("linker: program counter %d outside section %d", pc, section)
		}
		op := Opcode(data[pc])
		switch op {
		case OpNop:
			pc++
		case OpRet:
			if len(stack) == 0 {
				return Int(0), nil
			}
			v := stack[len(stack)-1]
			return v, nil
		case OpPush:
			if pc+9 > len(data) {
				return Value{}, fmt.Errorf("linker: truncated PUSH")
			}
			imm := int64(binary.BigEndian.Uint64(data[pc+1 : pc+9]))
			stack = append(stack, Int(imm))
			pc += 9
		case OpDup:
			if len(stack) == 0 {
				return Value{}, fmt.Errorf("linker: stack underflow at DUP")
			}
			stack = append(stack, stack[len(stack)-1])
			pc++
		case OpDrop:
			if len(stack) == 0 {
				return Value{}, fmt.Errorf("linker: stack underflow at DROP")
			}
			stack = stack[:len(stack)-1]
			pc++
		case OpAdd, OpSub, OpMul:
			if len(stack) < 2 {
				return Value{}, fmt.Errorf("linker: stack underflow at arithmetic")
			}
			b := stack[len(stack)-1]
			a := stack[len(stack)-2]
			stack = stack[:len(stack)-2]
			if a.Kind != ValueInt || b.Kind != ValueInt {
				return Value{}, fmt.Errorf("linker: arithmetic on non-integer")
			}
			switch op {
			case OpAdd:
				stack = append(stack, Int(a.Int+b.Int))
			case OpSub:
				stack = append(stack, Int(a.Int-b.Int))
			case OpMul:
				stack = append(stack, Int(a.Int*b.Int))
			}
			pc++
		case OpEq:
			if len(stack) < 2 {
				return Value{}, fmt.Errorf("linker: stack underflow at EQ")
			}
			b := stack[len(stack)-1]
			a := stack[len(stack)-2]
			stack = stack[:len(stack)-2]
			if a.Equal(b) {
				stack = append(stack, Int(1))
			} else {
				stack = append(stack, Int(0))
			}
			pc++
		case OpLt:
			if len(stack) < 2 {
				return Value{}, fmt.Errorf("linker: stack underflow at LT")
			}
			b := stack[len(stack)-1]
			a := stack[len(stack)-2]
			stack = stack[:len(stack)-2]
			if a.Kind != ValueInt || b.Kind != ValueInt {
				return Value{}, fmt.Errorf("linker: LT on non-integer")
			}
			if a.Int < b.Int {
				stack = append(stack, Int(1))
			} else {
				stack = append(stack, Int(0))
			}
			pc++
		case OpJmp:
			if pc+5 > len(data) {
				return Value{}, fmt.Errorf("linker: truncated JMP")
			}
			rel := int64(int32(binary.BigEndian.Uint32(data[pc+1 : pc+5])))
			pc += 5 + int(rel)
		case OpJz, OpJnz:
			if pc+5 > len(data) {
				return Value{}, fmt.Errorf("linker: truncated conditional jump")
			}
			if len(stack) == 0 {
				return Value{}, fmt.Errorf("linker: stack underflow at jump")
			}
			cond := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if cond.Kind != ValueInt {
				return Value{}, fmt.Errorf("linker: condition is not an integer")
			}
			rel := int64(int32(binary.BigEndian.Uint32(data[pc+1 : pc+5])))
			take := cond.Int == 0
			if op == OpJnz {
				take = !take
			}
			if take {
				pc += 5 + int(rel)
			} else {
				pc += 5
			}
		case OpCall:
			if pc+10 > len(data) {
				return Value{}, fmt.Errorf("linker: truncated CALL")
			}
			argc := int(data[pc+1])
			rel, ok := vm.relocs[section][uint64(pc+2)]
			if !ok {
				return Value{}, fmt.Errorf("linker: no CALL relocation at section %d offset %d", section, pc+2)
			}
			if len(stack) < argc {
				return Value{}, fmt.Errorf("linker: stack underflow at CALL")
			}
			args := make([]Value, argc)
			copy(args, stack[len(stack)-argc:])
			stack = stack[:len(stack)-argc]
			tgt, err := vm.img.resolveRelocation(rel, vm.symbols)
			if err != nil {
				return Value{}, err
			}
			if tgt.IsExternal || tgt.Section == NoSection {
				return Value{}, fmt.Errorf("linker: call to external symbol")
			}
			if kindOf(vm.img.Sections[tgt.Section]) != KindCode {
				return Value{}, fmt.Errorf("linker: call target is not code")
			}
			if tgt.Offset < 0 {
				return Value{}, fmt.Errorf("linker: negative call target")
			}
			ret, err := vm.run(tgt.Section, int(tgt.Offset), args)
			if err != nil {
				return Value{}, err
			}
			stack = append(stack, ret)
			pc += 10
		case OpAddr:
			if pc+9 > len(data) {
				return Value{}, fmt.Errorf("linker: truncated ADDR")
			}
			rel, ok := vm.relocs[section][uint64(pc+1)]
			if !ok {
				return Value{}, fmt.Errorf("linker: no ADDR_TAKEN relocation at section %d offset %d", section, pc+1)
			}
			tgt, err := vm.img.resolveRelocation(rel, vm.symbols)
			if err != nil {
				return Value{}, err
			}
			if tgt.IsExternal || tgt.Section == NoSection {
				return Value{}, fmt.Errorf("linker: address of external symbol")
			}
			if tgt.Offset < 0 {
				return Value{}, fmt.Errorf("linker: negative address offset")
			}
			stack = append(stack, Ptr(tgt.Section, uint64(tgt.Offset)))
			pc += 9
		case OpLoad8:
			if len(stack) == 0 {
				return Value{}, fmt.Errorf("linker: stack underflow at LOAD8")
			}
			addr := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if addr.Kind != ValuePtr {
				return Value{}, fmt.Errorf("linker: LOAD8 on non-pointer")
			}
			mem, err := vm.read(addr.Ptr, 8)
			if err != nil {
				return Value{}, err
			}
			stack = append(stack, Int(int64(binary.BigEndian.Uint64(mem))))
			pc++
		case OpStore8:
			if len(stack) < 2 {
				return Value{}, fmt.Errorf("linker: stack underflow at STORE8")
			}
			val := stack[len(stack)-1]
			addr := stack[len(stack)-2]
			stack = stack[:len(stack)-2]
			if addr.Kind != ValuePtr || val.Kind != ValueInt {
				return Value{}, fmt.Errorf("linker: STORE8 operand types")
			}
			if err := vm.write(addr.Ptr, val.Int); err != nil {
				return Value{}, err
			}
			pc++
		default:
			return Value{}, fmt.Errorf("linker: unknown opcode %d at section %d offset %d", op, section, pc)
		}
	}
}

func (vm *VM) dataFor(section SectionID) []byte {
	if d, ok := vm.memory[section]; ok {
		return d
	}
	d := append([]byte(nil), vm.img.Sections[section].Data...)
	vm.memory[section] = d
	return d
}

func (vm *VM) read(addr Address, n uint64) ([]byte, error) {
	if int(addr.Section) < 0 || int(addr.Section) >= len(vm.img.Sections) {
		return nil, fmt.Errorf("linker: invalid memory section %d", addr.Section)
	}
	data := vm.dataFor(addr.Section)
	if addr.Offset > uint64(len(data)) || n > uint64(len(data))-addr.Offset {
		return nil, fmt.Errorf("linker: out-of-bounds read at section %d offset %d", addr.Section, addr.Offset)
	}
	return data[addr.Offset : addr.Offset+n], nil
}

func (vm *VM) write(addr Address, value int64) error {
	if int(addr.Section) < 0 || int(addr.Section) >= len(vm.img.Sections) {
		return fmt.Errorf("linker: invalid memory section %d", addr.Section)
	}
	sec := vm.img.Sections[addr.Section]
	if sec.Flags&SHFWritable == 0 && kindOf(sec) != KindData {
		return fmt.Errorf("linker: write to non-writable section %d", addr.Section)
	}
	data := vm.dataFor(addr.Section)
	if addr.Offset > uint64(len(data)) || 8 > uint64(len(data))-addr.Offset {
		return fmt.Errorf("linker: out-of-bounds write at section %d offset %d", addr.Section, addr.Offset)
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(value))
	copy(data[addr.Offset:addr.Offset+8], buf[:])
	return nil
}

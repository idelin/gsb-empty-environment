package shadow

import "fmt"

// Machine is an embedded shadow execution host. It contains no native code.
type Machine struct {
	registers map[string]Value
	memory    map[uint64]Value
	carry     Value
	nextID    uint64
}

// NewMachine creates a machine with named 32-bit registers.
func NewMachine(registerNames ...string) *Machine {
	machine := &Machine{
		registers: make(map[string]Value, len(registerNames)),
		memory:    make(map[uint64]Value),
		nextID:    1,
	}
	for _, name := range registerNames {
		machine.registers[name] = machine.Const(Width32, 0)
	}
	machine.carry = machine.Const(Width1, 0)
	return machine
}

// Const creates a constant value without input sources.
func (machine *Machine) Const(width Width, value uint64) Value {
	if err := checkWidth(width); err != nil {
		panic(err)
	}
	bits := make([]bitTag, width)
	for index := range bits {
		bits[index] = machine.tag(uint8(value&1), nil)
		value >>= 1
	}
	return Value{width: width, bits: bits}
}

// InputValue creates a value whose bits are labeled with an input byte's bits.
func (machine *Machine) InputValue(byteIndex int, width Width, value uint64) (Value, error) {
	if err := checkWidth(width); err != nil {
		return Value{}, err
	}
	bits := make([]bitTag, width)
	for index := range bits {
		source := Source{Byte: byteIndex, Bit: uint8(index)}
		bits[index] = machine.tag(uint8(value&1), Sources(source))
		value >>= 1
	}
	return Value{width: width, bits: bits}, nil
}

// ReadRegister reads the low width bits of a named register.
func (machine *Machine) ReadRegister(name string, width Width) (Value, error) {
	if err := checkWidth(width); err != nil {
		return Value{}, err
	}
	register, ok := machine.registers[name]
	if !ok {
		return Value{}, &UnknownRegisterError{Name: name}
	}
	return Value{width: width, bits: register.bits[:width]}, nil
}

// WriteRegister performs an 8-, 16-, or 32-bit register write. Narrow
// writes preserve the unaffected high bits of the 32-bit register.
func (machine *Machine) WriteRegister(name string, value Value) error {
	if err := value.validate(); err != nil {
		return err
	}
	if value.width == Width1 {
		return &WidthError{Width: value.width}
	}
	if _, ok := machine.registers[name]; !ok {
		return &UnknownRegisterError{Name: name}
	}
	if value.width == Width32 {
		machine.registers[name] = value
		return nil
	}
	combined := make([]bitTag, Width32)
	copy(combined, machine.registers[name].bits)
	copy(combined[:value.width], value.bits)
	machine.registers[name] = Value{width: Width32, bits: combined}
	return nil
}

// ReadMemory reads width bytes in little-endian byte order. Uninitialized
// bytes read as unsourced zero.
func (machine *Machine) ReadMemory(address uint64, width Width) (Value, error) {
	if err := checkWidth(width); err != nil {
		return Value{}, err
	}
	if width == Width1 {
		return Value{}, &WidthError{Width: width}
	}
	bits := make([]bitTag, 0, width)
	for offset := Width(0); offset < width; offset += 8 {
		stored, ok := machine.memory[address+uint64(offset/8)]
		if !ok {
			stored = machine.Const(Width8, 0)
		}
		bits = append(bits, stored.bits...)
	}
	return Value{width: width, bits: bits}, nil
}

// ReadMemoryAt reads at a shadow-derived address. The address value itself is
// added to every result bit's sources because changing it can select another
// byte cell.
func (machine *Machine) ReadMemoryAt(address Value, width Width) (Value, error) {
	if err := address.validate(); err != nil {
		return Value{}, err
	}
	value, err := machine.ReadMemory(address.Uint64(), width)
	if err != nil {
		return Value{}, err
	}
	addressSources := address.bitSourcesAll()
	for index := range value.bits {
		value.bits[index] = bitTag{
			value:    value.bits[index].value,
			id:       value.bits[index].id,
			constant: value.bits[index].constant && len(addressSources) == 0,
			sources:  unionSources(value.bits[index].sources, addressSources),
		}
	}
	return value, nil
}

// WriteMemory writes a value as one or more byte-granular memory cells.
func (machine *Machine) WriteMemory(address uint64, value Value) error {
	if err := value.validate(); err != nil {
		return err
	}
	if value.width == Width1 {
		return &WidthError{Width: value.width}
	}
	for offset := Width(0); offset < value.width; offset += 8 {
		bits := append([]bitTag(nil), value.bits[offset:offset+8]...)
		machine.memory[address+uint64(offset/8)] = Value{width: Width8, bits: bits}
	}
	return nil
}

// WriteMemoryAt writes at a shadow-derived address. The address value is added
// to every stored bit's sources because it can change which byte cell is
// observed by a later fixed-address read.
func (machine *Machine) WriteMemoryAt(address, value Value) error {
	if err := address.validate(); err != nil {
		return err
	}
	if err := value.validate(); err != nil {
		return err
	}
	addressSources := address.bitSourcesAll()
	stored := Value{width: value.width, bits: make([]bitTag, len(value.bits))}
	for index, bit := range value.bits {
		stored.bits[index] = machine.tag(bit.value, unionSources(bit.sources, addressSources))
	}
	return machine.WriteMemory(address.Uint64(), stored)
}

// Carry returns the internal carry/borrow bit.
func (machine *Machine) Carry() Value {
	return machine.carry
}

// SetCarry sets the internal carry/borrow bit.
func (machine *Machine) SetCarry(value Value) error {
	if err := value.validate(); err != nil {
		return err
	}
	if value.width != Width1 {
		return &WidthError{Width: value.width}
	}
	machine.carry = value
	return nil
}

// JumpTarget is an indirect jump address and its per-bit sources.
type JumpTarget struct {
	address uint64
	width   Width
	sources []SourceSet
}

// IndirectJump resolves a shadow target without executing native machine code.
func (machine *Machine) IndirectJump(target Value) JumpTarget {
	if err := target.validate(); err != nil {
		panic(err)
	}
	sources := make([]SourceSet, target.width)
	for index, bit := range target.bits {
		sources[index] = cloneSources(bit.sources)
	}
	return JumpTarget{
		address: target.Uint64(),
		width:   target.width,
		sources: sources,
	}
}

// Address returns the concrete resolved target address.
func (target JumpTarget) Address() uint64 {
	return target.address
}

// Width returns the target width.
func (target JumpTarget) Width() Width {
	return target.width
}

// Sources returns the source set for one target-address bit.
func (target JumpTarget) Sources(bitIndex int) (SourceSet, error) {
	if bitIndex < 0 || bitIndex >= len(target.sources) {
		return nil, &BitIndexError{Index: bitIndex, Width: target.width}
	}
	return cloneSources(target.sources[bitIndex]), nil
}

// BitSources returns all target-address bits with their source sets.
func (target JumpTarget) BitSources() []BitSource {
	result := make([]BitSource, target.width)
	for index, sources := range target.sources {
		bit := uint8((target.address >> index) & 1)
		result[index] = BitSource{
			Bit:     bit,
			Sources: cloneSources(sources),
		}
	}
	return result
}

// UnknownRegisterError indicates that a register name was not declared.
type UnknownRegisterError struct {
	Name string
}

func (err *UnknownRegisterError) Error() string {
	return fmt.Sprintf("shadow: unknown register %q", err.Name)
}

func (machine *Machine) tag(value uint8, sources SourceSet) bitTag {
	machine.nextID++
	return bitTag{
		value:    value,
		id:       machine.nextID,
		constant: len(sources) == 0,
		sources:  cloneSources(sources),
	}
}

func (machine *Machine) tagged(value uint8, tags ...bitTag) bitTag {
	sets := make([]SourceSet, 0, len(tags))
	for _, tagged := range tags {
		sets = append(sets, tagged.sources)
	}
	return machine.tag(value, unionSources(sets...))
}

package minijit

// Input describes how to obtain one value during frame reconstruction or
// virtual-object materialization.
type Input struct {
	kind uint8
	slot int
	virt int
	lit  Value
}

const (
	inputSlot    = iota // a physical machine slot (register or stack slot)
	inputVirtual        // a reference to a virtual (not yet allocated) object
	inputLiteral        // a constant literal
)

// SlotInput references a physical machine slot by index.
func SlotInput(slot int) Input { return Input{kind: inputSlot, slot: slot} }

// VirtualInput references virtual object id.
func VirtualInput(id int) Input { return Input{kind: inputVirtual, virt: id} }

// LiteralInput embeds a constant value.
func LiteralInput(v Value) Input { return Input{kind: inputLiteral, lit: v} }

// FieldInput is one scalar-replaced field of a virtual object. The value is
// itself an Input so fields may point at other virtual objects, including
// cycles.
type FieldInput struct {
	Name  string
	Value Input
}

// VirtualObject is a delayed-allocation object: it has no heap identity until
// the materializer runs. Several frame slots may reference the same id.
type VirtualObject struct {
	ID     int
	Class  string
	Fields []FieldInput
}

// FrameState is one layer of an inlined call tree. Inner points at the frame
// of the most recently inlined callee; nil marks the innermost frame.
type FrameState struct {
	Function *Function
	PC       int
	Locals   []Input
	Stack    []Input
	Inner    *FrameState
}

// Depth reports the number of inlined layers in the frame tree.
func (fs *FrameState) Depth() int {
	n := 0
	for cur := fs; cur != nil; cur = cur.Inner {
		n++
	}
	return n
}

// MachineFrame is the single physical machine frame snapshot. Slots stand in
// for both machine registers and compiler stack slots; Virtuals is the
// scalar-replacement table; Root is the outermost FrameState whose Inner
// chain describes every inlined activation.
type MachineFrame struct {
	Slots    []Value
	Virtuals []VirtualObject
	Root     *FrameState
}

// DeoptMetadata bundles the physical snapshot needed to leave optimized code.
type DeoptMetadata struct {
	Machine *MachineFrame
	Reason  string
}

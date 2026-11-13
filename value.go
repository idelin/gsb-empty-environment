package netsec

import "fmt"

// ObjectID is a host-heap object identifier.  It is opaque to the
// reconstruction algorithm.
type ObjectID uint64

// VirtualID identifies an object in the optimized frame description.  A
// virtual identifier is not a heap object identifier.
type VirtualID uint32

// Convenient identifier aliases used by some test hosts.
type (
	HeapID          = ObjectID
	ObjID           = ObjectID
	ObjectId        = ObjectID
	VirtualObjectID = VirtualID
	VirtualId       = VirtualID
)

// ValueKind describes the representation of a Value.
type ValueKind uint8

const (
	// KindInt is a machine integer.
	KindInt ValueKind = iota
	// KindHeap is an existing heap object identifier while parsing input, and
	// an actual object identifier in reconstructed output.
	KindHeap
	// KindVirtual is a reference to a virtual object definition.  It is only
	// valid in input; reconstructed output never contains this kind.
	KindVirtual
)

// Short aliases for callers that prefer them.
const (
	IntKind      = KindInt
	HeapKind     = KindHeap
	VirtualKind  = KindVirtual
	KindInteger  = KindInt
	KindObject   = KindHeap
	KindExisting = KindHeap
	KindRef      = KindHeap
	ValueInt     = KindInt
	ValueHeap    = KindHeap
	ValueVirtual = KindVirtual
)

// Value is a field or frame-slot value.  Only the member selected by Kind is
// meaningful.  The zero Value is the integer zero.
type Value struct {
	Kind    ValueKind
	Int     int64
	Heap    ObjectID
	Virtual VirtualID
}

// IntValue constructs an integer value.
func IntValue(n int64) Value { return Value{Kind: KindInt, Int: n} }

// Int is an alias for IntValue.
func Int(n int64) Value { return IntValue(n) }

// NewInt is an alias for IntValue.
func NewInt(n int64) Value { return IntValue(n) }

// HeapRef constructs a reference to an existing heap object.
func HeapRef(id ObjectID) Value { return Value{Kind: KindHeap, Heap: id} }

// Existing is an alias for HeapRef.
func Existing(id ObjectID) Value { return HeapRef(id) }

// HeapValue is an alias for HeapRef.
func HeapValue(id ObjectID) Value { return HeapRef(id) }

// ObjectValue is an alias for HeapRef.
func ObjectValue(id ObjectID) Value { return HeapRef(id) }

// NewHeapRef is an alias for HeapRef.
func NewHeapRef(id ObjectID) Value { return HeapRef(id) }

// VirtualRef constructs a reference to a virtual object definition.
func VirtualRef(id VirtualID) Value { return Value{Kind: KindVirtual, Virtual: id} }

// VirtualValue is an alias for VirtualRef.
func VirtualValue(id VirtualID) Value { return VirtualRef(id) }

// VirtualObjectRef is an alias for VirtualRef.
func VirtualObjectRef(id VirtualID) Value { return VirtualRef(id) }

// NewVirtualRef is an alias for VirtualRef.
func NewVirtualRef(id VirtualID) Value { return VirtualRef(id) }

// IsInt reports whether v is an integer.
func (v Value) IsInt() bool { return v.Kind == KindInt }

// IsHeap reports whether v is a concrete heap object reference.
func (v Value) IsHeap() bool { return v.Kind == KindHeap }

// IsVirtual reports whether v is a virtual object reference.
func (v Value) IsVirtual() bool { return v.Kind == KindVirtual }

// AsInt returns the integer payload and whether v is an integer.
func (v Value) AsInt() (int64, bool) {
	return v.Int, v.Kind == KindInt
}

// AsObject returns the concrete object identifier and whether v is one.
func (v Value) AsObject() (ObjectID, bool) {
	return v.Heap, v.Kind == KindHeap
}

// AsVirtual returns the virtual object identifier and whether v is one.
func (v Value) AsVirtual() (VirtualID, bool) {
	return v.Virtual, v.Kind == KindVirtual
}

func (v Value) String() string {
	switch v.Kind {
	case KindInt:
		return fmt.Sprintf("int(%d)", v.Int)
	case KindHeap:
		return fmt.Sprintf("object(%d)", v.Heap)
	case KindVirtual:
		return fmt.Sprintf("virtual(%d)", v.Virtual)
	default:
		return fmt.Sprintf("value(kind=%d)", v.Kind)
	}
}

// Field is an ordered name/value pair.  Reconstruction uses the map form in
// VirtualObject, but Field is convenient for table-driven tests.
type Field struct {
	Name  string
	Value Value
}

// Convenient value and definition aliases.
type (
	Ref                     = Value
	SlotValue               = Value
	FieldValue              = Value
	ObjectDef               = VirtualObject
	VirtualDef              = VirtualObject
	VirtualObjectDefinition = VirtualObject
)

// VirtualObject is one object definition in an optimized frame.
//
// Fields may contain integer values, references to objects already present in
// the host heap, or references to other virtual objects.  Sharing and cycles
// are allowed.  Closed is materialized as the object's closed state; a field
// named "closed" with an integer payload is also honored.
type VirtualObject struct {
	ID     VirtualID
	Fields map[string]Value
	Closed bool
}

// Reconstruction is the metadata needed to rebuild an interpreter frame.
//
// Registers and Stack are the canonical interpreter-frame roots; Regs,
// StackSlots, and Slots are accepted aliases.  Objects contains the virtual
// object definitions; Virtuals, VirtualObjects, and Definitions are accepted
// aliases.  Callers should normally use exactly one field from each group.
type Reconstruction struct {
	Registers  []Value
	Regs       []Value
	Stack      []Value
	StackSlots []Value
	Slots      []Value

	Objects        []VirtualObject
	Virtuals       []VirtualObject
	VirtualObjects []VirtualObject
	Definitions    []VirtualObject

	// Existing and ExistingIDs let a host declare heap identifiers that must
	// remain untouched even when they are not referenced by a root or field.
	Existing    []ObjectID
	ExistingIDs []ObjectID
}

// Convenient aliases for the reconstruction description.
type (
	Layout           = Reconstruction
	Input            = Reconstruction
	FrameInput       = Reconstruction
	FrameDescription = Reconstruction
)

// Frame is a successfully reconstructed interpreter frame.  Objects contains
// only objects allocated by this reconstruction; existing host objects are
// never copied into it.
type Frame struct {
	Registers  []Value
	Regs       []Value
	Stack      []Value
	StackSlots []Value
	Slots      []Value

	Objects    map[ObjectID]*Object
	NewObjects []*Object
}

// Object returns a newly materialized object by identifier.
func (f *Frame) Object(id ObjectID) (*Object, bool) {
	if f == nil || f.Objects == nil {
		return nil, false
	}
	o, ok := f.Objects[id]
	return o, ok
}

// Register returns a reconstructed register value.
func (f *Frame) Register(i int) (Value, bool) {
	if f == nil || i < 0 || i >= len(f.Registers) {
		return Value{}, false
	}
	return f.Registers[i], true
}

// Slot returns a reconstructed stack slot.
func (f *Frame) Slot(i int) (Value, bool) {
	if f == nil || i < 0 || i >= len(f.Stack) {
		return Value{}, false
	}
	return f.Stack[i], true
}

// Proof maps every reachable virtual identifier to the single concrete object
// identifier allocated for it.
type Proof map[VirtualID]ObjectID

// MaterializationProof is an alias for Proof.
type MaterializationProof = Proof

// Object returns the concrete identifier associated with v.
func (p Proof) Object(v VirtualID) (ObjectID, bool) {
	id, ok := p[v]
	return id, ok
}

// Verify checks that the proof is one-to-one.  A proof with two virtual
// identifiers mapped to one object would prove that the reconstruction lost an
// object identity, so it is rejected.
func (p Proof) Verify() error {
	seen := make(map[ObjectID]VirtualID, len(p))
	for v, id := range p {
		if prior, ok := seen[id]; ok && prior != v {
			return fmt.Errorf("%w: virtual %d and %d both map to object %d", ErrDuplicateObjectID, prior, v, id)
		}
		seen[id] = v
	}
	return nil
}

// Inverse returns the object-to-virtual direction of the proof.
func (p Proof) Inverse() map[ObjectID]VirtualID {
	out := make(map[ObjectID]VirtualID, len(p))
	for v, id := range p {
		out[id] = v
	}
	return out
}

// AllObjects returns the definitions from the three accepted definition
// fields, preserving their relative order.
func (r Reconstruction) AllObjects() []VirtualObject {
	n := len(r.Objects) + len(r.Virtuals) + len(r.VirtualObjects) + len(r.Definitions)
	if n == 0 {
		return nil
	}
	out := make([]VirtualObject, 0, n)
	out = append(out, r.Objects...)
	out = append(out, r.Virtuals...)
	out = append(out, r.VirtualObjects...)
	out = append(out, r.Definitions...)
	return out
}

// AllRegisters returns register roots from the accepted register fields.
func (r Reconstruction) AllRegisters() []Value {
	n := len(r.Registers) + len(r.Regs)
	if n == 0 {
		return nil
	}
	out := make([]Value, 0, n)
	out = append(out, r.Registers...)
	out = append(out, r.Regs...)
	return out
}

// AllStack returns stack roots from the accepted stack fields.
func (r Reconstruction) AllStack() []Value {
	n := len(r.Stack) + len(r.StackSlots) + len(r.Slots)
	if n == 0 {
		return nil
	}
	out := make([]Value, 0, n)
	out = append(out, r.Stack...)
	out = append(out, r.StackSlots...)
	out = append(out, r.Slots...)
	return out
}

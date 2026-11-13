package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// ValueID identifies an SSA instruction and, when the instruction produces a
// value, the value itself. IDs are unique within a Function. NoValue is the
// absent-value sentinel.
type ValueID uint64

// NoValue denotes the absence of an SSA value.
const NoValue ValueID = 0

// BlockID identifies a basic block.
type BlockID string

// ObjectID identifies an allocation site. Every execution of an allocation
// site creates a fresh concrete generation of that site.
type ObjectID string

// ValueKind is the coarse SSA type used by the verifier. The verifier
// deliberately has no "maybe pointer" kind: a Phi with a pointer on any
// incoming edge is a Ptr, and the concrete interpreter checks whether the
// selected arm is actually a pointer.
type ValueKind uint8

const (
	KindNone ValueKind = iota
	KindPtr
	KindNonPtr
)

func (k ValueKind) String() string {
	switch k {
	case KindNone:
		return "none"
	case KindPtr:
		return "ptr"
	case KindNonPtr:
		return "nonptr"
	default:
		return fmt.Sprintf("kind(%d)", k)
	}
}

// OffsetRange is a half-open range [Start, End) of possible pointer offsets
// from an object's base. A root pointer to an object has offset [0, 1), while
// a pointer known to point exactly at field offset 8 has offset [8, 9).
type OffsetRange struct {
	Start int
	End   int
}

// Valid reports whether the range is non-empty and well formed.
func (r OffsetRange) Valid() bool {
	return r.Start >= 0 && r.End > r.Start
}

// Within reports whether every offset in r is inside an object of size bytes.
func (r OffsetRange) Within(size int) bool {
	return r.Valid() && r.Start >= 0 && r.End <= size
}

// Shift adds delta to both ends of r.
func (r OffsetRange) Shift(delta int) OffsetRange {
	return OffsetRange{Start: r.Start + delta, End: r.End + delta}
}

// Union returns the smallest range containing both r and q.
func (r OffsetRange) Union(q OffsetRange) OffsetRange {
	if r.Start == 0 && r.End == 0 {
		return q
	}
	if q.Start == 0 && q.End == 0 {
		return r
	}
	if q.Start < r.Start {
		r.Start = q.Start
	}
	if q.End > r.End {
		r.End = q.End
	}
	return r
}

func (r OffsetRange) String() string {
	return fmt.Sprintf("[%d,%d)", r.Start, r.End)
}

// ObjectSet is a set of abstract allocation sites. It is kept sorted and
// duplicate-free by Normalize.
type ObjectSet []ObjectID

// NewObjectSet builds a normalized object set.
func NewObjectSet(ids ...ObjectID) ObjectSet {
	return ObjectSet(ids).Normalize()
}

// Normalize returns a sorted, duplicate-free copy of s.
func (s ObjectSet) Normalize() ObjectSet {
	if len(s) == 0 {
		return nil
	}
	out := append(ObjectSet(nil), s...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	n := 1
	for i := 1; i < len(out); i++ {
		if out[i] != out[n-1] {
			out[n] = out[i]
			n++
		}
	}
	return out[:n]
}

// Union merges two object sets.
func (s ObjectSet) Union(t ObjectSet) ObjectSet {
	return append(append(ObjectSet(nil), s...), t...).Normalize()
}

// Contains reports whether id is in s.
func (s ObjectSet) Contains(id ObjectID) bool {
	for _, candidate := range s {
		if candidate == id {
			return true
		}
	}
	return false
}

// Equal compares normalized object sets.
func (s ObjectSet) Equal(t ObjectSet) bool {
	a, b := s.Normalize(), t.Normalize()
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

func (s ObjectSet) String() string {
	parts := make([]string, len(s))
	for i, id := range s {
		parts[i] = string(id)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// ValueType is either NonPtr or a Ptr to one of Objects at an offset in
// Offset. Base records the immediate SSA value from which an interior pointer
// was derived; it is NoValue for an allocation result, a Phi, or a plain copy
// whose source no longer carries a useful base relation.
type ValueType struct {
	Kind    ValueKind
	Objects ObjectSet
	Offset  OffsetRange
	Base    ValueID
}

// Type is a short alias for ValueType.
type Type = ValueType

// PtrType constructs a pointer type. For a root pointer use offset [0,1).
func PtrType(objects ObjectSet, offset OffsetRange) ValueType {
	return ValueType{Kind: KindPtr, Objects: objects.Normalize(), Offset: offset}
}

// Ptr is a short alias for PtrType.
func Ptr(objects ObjectSet, offset OffsetRange) ValueType {
	return PtrType(objects, offset)
}

// NonPtrType constructs a non-pointer type.
func NonPtrType() ValueType {
	return ValueType{Kind: KindNonPtr}
}

// NonPtr is a short alias for NonPtrType.
func NonPtr() ValueType {
	return NonPtrType()
}

// IsPtr reports whether t is a static pointer type.
func (t ValueType) IsPtr() bool { return t.Kind == KindPtr }

// IsNone reports whether the instruction does not produce an SSA value.
func (t ValueType) IsNone() bool { return t.Kind == KindNone }

func (t ValueType) String() string {
	if t.Kind == KindPtr {
		return fmt.Sprintf("Ptr(%s,%s)", t.Objects, t.Offset)
	}
	if t.Kind == KindNonPtr {
		return "NonPtr"
	}
	return "None"
}

// Op is an SSA instruction opcode.
type Op uint8

const (
	OpAlloc Op = iota
	OpCopy
	OpField
	OpInt
	OpPhi
	OpCall
	OpSafepoint
	OpDereference
	OpReturn
	OpJump
	OpCondJump
)

func (op Op) String() string {
	switch op {
	case OpAlloc:
		return "Alloc"
	case OpCopy:
		return "Copy"
	case OpField:
		return "Field"
	case OpInt:
		return "Int"
	case OpPhi:
		return "Phi"
	case OpCall:
		return "Call"
	case OpSafepoint:
		return "Safepoint"
	case OpDereference:
		return "Dereference"
	case OpReturn:
		return "Return"
	case OpJump:
		return "Jump"
	case OpCondJump:
		return "CondJump"
	default:
		return fmt.Sprintf("Op(%d)", op)
	}
}

// PhiIn is one incoming value on the edge from Pred.
type PhiIn struct {
	Pred  BlockID
	Value ValueID
}

// ReturnKind describes where a call's return value comes from.
type ReturnKind uint8

const (
	// RetNone means the call has no return value.
	RetNone ReturnKind = iota
	// RetNonPtr returns an integer or another non-pointer value.
	RetNonPtr
	// RetArg aliases the argument at Arg, optionally shifted by Offset bytes.
	RetArg
	// RetFresh allocates a fresh generation of Object with the given Size.
	RetFresh
)

func (k ReturnKind) String() string {
	switch k {
	case RetNone:
		return "none"
	case RetNonPtr:
		return "nonptr"
	case RetArg:
		return "arg"
	case RetFresh:
		return "fresh"
	default:
		return fmt.Sprintf("return(%d)", k)
	}
}

// ReturnSpec is the return clause of a call summary.
type ReturnSpec struct {
	Kind   ReturnKind
	Arg    int
	Object ObjectID
	Size   int
	Offset int
}

// CallSummary declares the observable effects needed by the verifier.
// LiveArgs indexes Args and names arguments that the callee keeps live for the
// duration of the call. MayThrow adds an exceptional CFG edge; Exception is
// the handler block. Return describes the provenance of a returned pointer.
type CallSummary struct {
	Name      string
	LiveArgs  []int
	MayThrow  bool
	Exception BlockID
	Return    ReturnSpec
}

// Instr is one SSA instruction. ID identifies both the instruction and, for
// value-producing operations, the defined value.
type Instr struct {
	ID ValueID
	Op Op

	// Type is the result type. KindNone means no result.
	Type ValueType

	Args []ValueID

	Object ObjectID
	Size   int
	Offset int
	Int    int64

	Phi []PhiIn

	Callee  string
	Summary CallSummary

	Target  BlockID
	True    BlockID
	False   BlockID
	Cond    ValueID
	Results []ValueID

	Source string
}

// Def returns the value defined by the instruction, or NoValue.
func (in *Instr) Def() ValueID {
	if in == nil || in.Type.Kind == KindNone || in.Op == OpSafepoint ||
		in.Op == OpReturn || in.Op == OpJump || in.Op == OpCondJump {
		return NoValue
	}
	return in.ID
}

// Uses returns the ordinary SSA operands. Phi inputs are edge-specific and
// are intentionally not included here.
func (in *Instr) Uses() []ValueID {
	switch in.Op {
	case OpCopy, OpField, OpDereference:
		if len(in.Args) > 0 {
			return []ValueID{in.Args[0]}
		}
	case OpInt, OpAlloc, OpSafepoint:
		return nil
	case OpPhi:
		return nil
	case OpCall:
		return append([]ValueID(nil), in.Args...)
	case OpReturn:
		if len(in.Results) > 0 {
			return append([]ValueID(nil), in.Results...)
		}
		return append([]ValueID(nil), in.Args...)
	case OpCondJump:
		if in.Cond != NoValue {
			return []ValueID{in.Cond}
		}
		return append([]ValueID(nil), in.Args...)
	}
	return append([]ValueID(nil), in.Args...)
}

// Block is a basic block. Phi instructions, if present, must be first.
type Block struct {
	ID     BlockID
	Name   string
	Instrs []*Instr

	fn *Function
}

// Function is a finite SSA function.
type Function struct {
	Name   string
	Entry  BlockID
	Blocks []*Block

	// ObjectSizes optionally records allocation-site layouts. Alloc
	// instructions and RetFresh summaries are checked against it.
	ObjectSizes map[ObjectID]int

	nextID ValueID
}

// NewFunction creates an empty function. The first block added becomes Entry.
func NewFunction(name string) *Function {
	return &Function{Name: name, ObjectSizes: make(map[ObjectID]int), nextID: 1}
}

// NewBlock appends a block and associates it with f.
func (f *Function) NewBlock(name string) *Block {
	if f.ObjectSizes == nil {
		f.ObjectSizes = make(map[ObjectID]int)
	}
	id := BlockID(name)
	if id == "" {
		id = BlockID(fmt.Sprintf("b%d", len(f.Blocks)))
	}
	b := &Block{ID: id, Name: name, fn: f}
	f.Blocks = append(f.Blocks, b)
	if f.Entry == "" {
		f.Entry = id
	}
	return b
}

// Block returns the block with id, or nil.
func (f *Function) Block(id BlockID) *Block {
	for _, b := range f.Blocks {
		if b.ID == id {
			return b
		}
	}
	return nil
}

func (f *Function) nextValue() ValueID {
	id := f.nextID
	f.nextID++
	return id
}

func (b *Block) appendInstr(in *Instr) *Instr {
	if b.fn == nil {
		panic("netsec: block is not associated with a Function")
	}
	if in.ID == NoValue {
		in.ID = b.fn.nextValue()
	} else if in.ID >= b.fn.nextID {
		b.fn.nextID = in.ID + 1
	}
	b.Instrs = append(b.Instrs, in)
	return in
}

// Alloc appends an allocation of a fresh generation of object.
func (b *Block) Alloc(object ObjectID, size int, source string) ValueID {
	f := b.fn
	if old, ok := f.ObjectSizes[object]; ok && old != size {
		panic(fmt.Sprintf("netsec: allocation site %q has sizes %d and %d", object, old, size))
	}
	f.ObjectSizes[object] = size
	in := b.appendInstr(&Instr{
		Op:     OpAlloc,
		Object: object,
		Size:   size,
		Type:   PtrType(NewObjectSet(object), OffsetRange{Start: 0, End: 1}),
		Source: source,
	})
	return in.ID
}

// Copy appends a pointer copy.
func (b *Block) Copy(src ValueID, source string) ValueID {
	typ := b.fn.typeOf(src)
	if typ.Kind == KindNone {
		panic(fmt.Sprintf("netsec: copy of undefined value %d", src))
	}
	in := b.appendInstr(&Instr{Op: OpCopy, Args: []ValueID{src}, Type: typ, Source: source})
	return in.ID
}

// Field appends an interior-pointer computation.
func (b *Block) Field(base ValueID, offset int, source string) ValueID {
	baseType := b.fn.typeOf(base)
	if !baseType.IsPtr() {
		panic(fmt.Sprintf("netsec: field base %d is not a pointer", base))
	}
	typ := ValueType{
		Kind:    KindPtr,
		Objects: append(ObjectSet(nil), baseType.Objects...),
		Offset:  baseType.Offset.Shift(offset),
		Base:    base,
	}
	in := b.appendInstr(&Instr{
		Op: OpField, Args: []ValueID{base}, Offset: offset, Type: typ, Source: source,
	})
	return in.ID
}

// Int appends a concrete non-pointer integer.
func (b *Block) Int(value int64, source string) ValueID {
	in := b.appendInstr(&Instr{Op: OpInt, Int: value, Type: NonPtrType(), Source: source})
	return in.ID
}

// Phi appends a Phi. Its type is the conservative join of the incoming arms:
// any pointer arm makes the result Ptr. A non-pointer arm is still a legal
// incoming value; the concrete interpreter diagnoses a non-pointer selected on
// a path whose stack map treats the Phi as a pointer.
func (b *Block) Phi(edges ...PhiIn) ValueID {
	typ := b.fn.joinPhiTypes(edges)
	return b.PhiTyped(typ, edges...)
}

// PhiTyped appends a Phi with an explicitly declared type. It is useful for
// loop-carried Phis whose backedge definition appears later in the function.
func (b *Block) PhiTyped(typ ValueType, edges ...PhiIn) ValueID {
	if typ.Kind != KindPtr && typ.Kind != KindNonPtr {
		panic("netsec: PhiTyped requires Ptr or NonPtr")
	}
	in := b.appendInstr(&Instr{Op: OpPhi, Phi: edges, Type: typ})
	return in.ID
}

// Call appends a call. A summary with RetNone produces no SSA value.
func (b *Block) Call(callee string, summary CallSummary, args ...ValueID) ValueID {
	f := b.fn
	if summary.Name == "" {
		summary.Name = callee
	}
	in := &Instr{
		Op: OpCall, Args: append([]ValueID(nil), args...), Callee: callee, Summary: summary,
	}
	typ, err := f.callReturnType(in)
	if err != nil {
		panic(err)
	}
	in.Type = typ
	b.appendInstr(in)
	if typ.Kind == KindNone {
		return NoValue
	}
	return in.ID
}

// Safepoint appends a safepoint. Its ID is the key used by StackMap.At.
func (b *Block) Safepoint(source string) ValueID {
	in := b.appendInstr(&Instr{Op: OpSafepoint, Type: ValueType{Kind: KindNone}, Source: source})
	return in.ID
}

// Dereference appends a read through ptr. The read result is NonPtr.
func (b *Block) Dereference(ptr ValueID, source string) ValueID {
	in := b.appendInstr(&Instr{
		Op: OpDereference, Args: []ValueID{ptr}, Type: NonPtrType(), Source: source,
	})
	return in.ID
}

// Return appends a return. value may be NoValue.
func (b *Block) Return(value ValueID, source string) {
	in := &Instr{Op: OpReturn, Type: ValueType{Kind: KindNone}, Source: source}
	if value != NoValue {
		in.Results = []ValueID{value}
		in.Args = []ValueID{value}
	}
	b.appendInstr(in)
}

// Jump appends an unconditional branch.
func (b *Block) Jump(target BlockID, source string) {
	b.appendInstr(&Instr{Op: OpJump, Target: target, Type: ValueType{Kind: KindNone}, Source: source})
}

// CondJump appends a conditional branch. An integer condition of zero takes
// the false edge; any non-zero value takes the true edge.
func (b *Block) CondJump(cond ValueID, trueTarget, falseTarget BlockID, source string) {
	b.appendInstr(&Instr{
		Op: OpCondJump, Cond: cond, True: trueTarget, False: falseTarget,
		Type: ValueType{Kind: KindNone}, Source: source,
	})
}

func (f *Function) typeOf(id ValueID) ValueType {
	if id == NoValue {
		return ValueType{}
	}
	for _, b := range f.Blocks {
		for _, in := range b.Instrs {
			if in.ID == id {
				return in.Type
			}
		}
	}
	return ValueType{}
}

func (f *Function) joinPhiTypes(edges []PhiIn) ValueType {
	var (
		objects ObjectSet
		offset  OffsetRange
		sawPtr  bool
	)
	for _, edge := range edges {
		t := f.typeOf(edge.Value)
		if !t.IsPtr() {
			continue
		}
		sawPtr = true
		objects = objects.Union(t.Objects)
		offset = offset.Union(t.Offset)
	}
	if !sawPtr {
		return NonPtrType()
	}
	return ValueType{Kind: KindPtr, Objects: objects, Offset: offset}
}

func (f *Function) callReturnType(in *Instr) (ValueType, error) {
	switch in.Summary.Return.Kind {
	case RetNone:
		return ValueType{Kind: KindNone}, nil
	case RetNonPtr:
		return NonPtrType(), nil
	case RetArg:
		idx := in.Summary.Return.Arg
		if idx < 0 || idx >= len(in.Args) {
			return ValueType{}, fmt.Errorf("netsec: call %q return argument %d out of range", in.Callee, idx)
		}
		t := f.typeOf(in.Args[idx])
		if !t.IsPtr() {
			return ValueType{}, fmt.Errorf("netsec: call %q return argument %d is not a pointer", in.Callee, idx)
		}
		t.Offset = t.Offset.Shift(in.Summary.Return.Offset)
		t.Base = in.Args[idx]
		t.Objects = append(ObjectSet(nil), t.Objects...)
		return t, nil
	case RetFresh:
		if in.Summary.Return.Object == "" || in.Summary.Return.Size <= 0 {
			return ValueType{}, fmt.Errorf("netsec: call %q fresh return needs an object site and positive size", in.Callee)
		}
		if old, ok := f.ObjectSizes[in.Summary.Return.Object]; ok && old != in.Summary.Return.Size {
			return ValueType{}, fmt.Errorf("netsec: object site %q has inconsistent sizes", in.Summary.Return.Object)
		}
		f.ObjectSizes[in.Summary.Return.Object] = in.Summary.Return.Size
		return PtrType(NewObjectSet(in.Summary.Return.Object), OffsetRange{Start: 0, End: 1}), nil
	default:
		return ValueType{}, fmt.Errorf("netsec: call %q has unknown return kind %d", in.Callee, in.Summary.Return.Kind)
	}
}

// ValueSet is a set of SSA values.
type ValueSet map[ValueID]struct{}

// Add inserts id.
func (s ValueSet) Add(id ValueID) { s[id] = struct{}{} }

// Contains reports membership.
func (s ValueSet) Contains(id ValueID) bool {
	_, ok := s[id]
	return ok
}

// Clone copies the set.
func (s ValueSet) Clone() ValueSet {
	out := make(ValueSet, len(s))
	for id := range s {
		out[id] = struct{}{}
	}
	return out
}

// Sorted returns IDs in ascending order.
func (s ValueSet) Sorted() []ValueID {
	out := make([]ValueID, 0, len(s))
	for id := range s {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (s ValueSet) String() string {
	ids := s.Sorted()
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprintf("%d", id)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

package typeset

// Fact is one pointer-flow statement supplied by the analysis host.
// Facts are processed in slice order inside a fixpoint loop, so the same
// facts always produce the same result regardless of SSA ordering.
type Fact interface {
	factKind() string
}

// AllocFact creates an object of Class at AllocPoint and stores it in Dst.
type AllocFact struct {
	AllocPoint string
	Class      string
	Dst        string
}

// CopyFact copies one local pointer value to another.
type CopyFact struct {
	Src string
	Dst string
}

// CastFact is an upcast or downcast along one inheritance path. Path
// records the concrete subobject traversal (including which edges are
// virtual), which disambiguates repeated non-virtual bases.
type CastFact struct {
	Src  string
	Dst  string
	Path []PathStep
}

// FieldStoreFact writes Src into field FieldName of the object currently
// referenced by Base. Every concrete (object, subobject) in Base gets its
// own field cell, so objects and repeated-base occurrences never alias.
type FieldStoreFact struct {
	Base      string
	FieldName string
	Src       string
}

// FieldLoadFact reads field FieldName from the object referenced by Base
// into Dst.
type FieldLoadFact struct {
	Base      string
	FieldName string
	Dst       string
}

// CallFact is a virtual call of Method on receiver Recv at CallSite.
type CallFact struct {
	CallSite string
	Recv     string
	Method   string
}

func (AllocFact) factKind() string      { return "alloc" }
func (CopyFact) factKind() string       { return "copy" }
func (CastFact) factKind() string       { return "cast" }
func (FieldStoreFact) factKind() string { return "field_store" }
func (FieldLoadFact) factKind() string  { return "field_load" }
func (CallFact) factKind() string       { return "call" }

// PointerTarget is one element of an abstract pointer set: an object
// allocation plus the subobject the pointer currently refers to.
type PointerTarget struct {
	allocPoint  string
	mostDerived string
	key         subKey
}

// AllocPoint returns the allocation site that created the object.
func (p PointerTarget) AllocPoint() string { return p.allocPoint }

// MostDerived returns the most-derived class of the object.
func (p PointerTarget) MostDerived() string { return p.mostDerived }

// Origin returns the allocation provenance and subobject path. Prefer the
// origins on CallResult.Targets for allowlist reporting: those retain the
// concrete cast edges supplied by the host, while this decoded path is only
// a representative reconstructed from the canonical subobject key.
func (p PointerTarget) Origin() Origin {
	return Origin{
		AllocPoint:  p.allocPoint,
		MostDerived: p.mostDerived,
		Path:        decodeKey(p.key),
	}
}

// Cell identifies one field cell: an object subobject plus a field name.
type Cell struct {
	target    PointerTarget
	fieldName string
}

// FieldName returns the field this cell represents.
func (c Cell) FieldName() string { return c.fieldName }

// Target returns the object subobject that owns the cell.
func (c Cell) Target() PointerTarget { return c.target }

// CallResult is the resolution of one virtual call.
type CallResult struct {
	// CallSite mirrors CallFact.CallSite.
	CallSite string

	// Method mirrors CallFact.Method.
	Method string

	// Targets are the allowed call targets, one per (origin, final
	// overrider). Empty for an empty receiver set.
	Targets []ResolvedTarget

	// Ambiguities reports shared virtual-base slots without a unique final
	// overrider, per object origin.
	Ambiguities []Ambiguity

	// EmptyRecv reports that no object ever reaches the receiver. Such a
	// call is unreachable under the supplied facts.
	EmptyRecv bool

	// NoSlot reports origins whose receiver subobject has no virtual slot
	// for Method. The call is ill-formed on those origins.
	NoSlot []Origin
}

// ResolvedTarget is one concrete allowlist entry for a virtual call.
type ResolvedTarget struct {
	// Decl is the method that the runtime would dispatch to.
	Decl MethodDecl

	// subKey is the canonical receiver subobject inside the object.
	subKey subKey

	// Origin records the allocation point and subobject path that justify
	// this target.
	Origin Origin
}

// Ambiguity records a virtual slot with multiple mutually incomparable
// final overriders.
type Ambiguity struct {
	// Origin identifies the object and shared virtual subobject.
	Origin Origin

	// Contenders are the incomparable final overriders.
	Contenders []MethodDecl
}

// Solution is the result of Analyze.
type Solution struct {
	// Vars maps every local variable to its abstract pointer set.
	Vars map[string][]PointerTarget

	// Cells maps every field cell to its abstract pointer set.
	Cells map[Cell][]PointerTarget

	// Calls holds one entry per virtual call, in input order.
	Calls []CallResult
}

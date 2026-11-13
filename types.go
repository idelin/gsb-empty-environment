package netsec

import (
	"fmt"
	"strings"
)

// ObjectID and ValueID are symbolic names.  Keeping them as strings makes the
// input graph easy to serialize from a compiler verification host.
type (
	ObjectID   string
	ValueID    string
	FunctionID string
)

// Purpose describes why a local object exists.  Control metadata is accepted
// as input only so that callers can describe an externally-created stack
// layout; this package never assigns an address to it.
type Purpose string

const (
	PurposeLocal           Purpose = "local"
	PurposeSliceBacking    Purpose = "slice-backing"
	PurposeVariableLength  Purpose = "variable-length"
	PurposeReturnAddress   Purpose = "return-address"
	PurposeShadowStack     Purpose = "shadow-stack"
	PurposeControlMetadata Purpose = "control-metadata"
)

const (
	PurposeNormal   = PurposeLocal
	PurposeVariable = PurposeVariableLength
	PurposeControl  = PurposeControlMetadata
)

// StackClass is the result of the safety classification.
type StackClass uint8

const (
	// ClassUnsafe is the zero value so an uninitialized decision is fail-safe.
	ClassUnsafe StackClass = iota
	ClassSafe
	// ClassUnmanaged denotes control metadata.  The package neither places nor
	// classifies such an object.
	ClassUnmanaged
)

const (
	UnsafeStack = ClassUnsafe
	SafeStack   = ClassSafe
	Unmanaged   = ClassUnmanaged
	StackUnsafe = ClassUnsafe
	StackSafe   = ClassSafe
)

func (c StackClass) String() string {
	switch c {
	case ClassSafe:
		return "safe"
	case ClassUnsafe:
		return "unsafe"
	case ClassUnmanaged:
		return "unmanaged"
	default:
		return fmt.Sprintf("StackClass(%d)", uint8(c))
	}
}

func (c StackClass) IsSafe() bool { return c == ClassSafe }

// Object is a local object supplied by the compiler host.  Size and Align are
// byte counts.  A non-zero/zero alignment is normalized to one; sizes must be
// positive for a managed object.
type Object struct {
	ID    ObjectID
	Name  string
	Size  uint64
	Align uint64
	// SizeBytes and Alignment are accepted aliases for hosts that use longer
	// field names.
	SizeBytes uint64
	Alignment uint64
	Purpose   Purpose
	Variadic  bool
	// VariableLength is accepted as a synonym of Variadic.
	VariableLength bool
	// LayoutGroup names a layout shared by several objects.  Any member of a
	// group containing a variable-length object is ineligible for the safe
	// stack.
	LayoutGroup string
	// Group, LayoutID and SharedLayout are accepted aliases of LayoutGroup.
	Group        string
	LayoutID     string
	SharedLayout string
	Order        int
}

func (o Object) normalizedID() ObjectID {
	if o.ID != "" {
		return o.ID
	}
	return ObjectID(o.Name)
}

func (o Object) normalizedAlign() uint64 {
	if o.Align != 0 {
		return o.Align
	}
	if o.Alignment != 0 {
		return o.Alignment
	}
	return 1
}

func (o Object) sizeBytes() uint64 {
	if o.Size != 0 {
		return o.Size
	}
	return o.SizeBytes
}

func (o Object) layoutGroup() string {
	for _, group := range []string{o.LayoutGroup, o.Group, o.LayoutID, o.SharedLayout} {
		if group != "" {
			return group
		}
	}
	return ""
}

func (o Object) isVariableLength() bool {
	p := normalizePurpose(o.Purpose)
	return o.Variadic || o.VariableLength || p == "variablelength" || p == "variadic"
}

func (o Object) isControl() bool {
	switch normalizePurpose(o.Purpose) {
	case "returnaddress", "retaddr", "shadowstack", "controlmetadata", "control":
		return true
	default:
		return false
	}
}

func normalizePurpose(p Purpose) string {
	s := strings.TrimSpace(string(p))
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, " ", "")
	return strings.ToLower(s)
}

// OpKind is the SSA operation kind.  The constants deliberately use simple
// spellings; normalizeOp also accepts common aliases and the String spelling.
type OpKind string

const (
	OpAddrOf    OpKind = "AddrOf"
	OpGEP       OpKind = "GEP"
	OpPhi       OpKind = "Phi"
	OpSelect    OpKind = "Select"
	OpStorePtr  OpKind = "StorePtr"
	OpLoadPtr   OpKind = "LoadPtr"
	OpPassCall  OpKind = "PassCall"
	OpPtrToInt  OpKind = "PtrToInt"
	OpIntToPtr  OpKind = "IntToPtr"
	OpReturnPtr OpKind = "ReturnPtr"
	OpParam     OpKind = "Param"
)

// Short aliases for hosts that use unprefixed operation names.
const (
	AddrOf    = OpAddrOf
	GEP       = OpGEP
	Phi       = OpPhi
	Select    = OpSelect
	StorePtr  = OpStorePtr
	LoadPtr   = OpLoadPtr
	PassCall  = OpPassCall
	PtrToInt  = OpPtrToInt
	IntToPtr  = OpIntToPtr
	ReturnPtr = OpReturnPtr
)

func normalizeOp(op OpKind) OpKind {
	s := strings.TrimSpace(string(op))
	s = strings.ReplaceAll(s, "_", "")
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ToLower(s)
	switch s {
	case "addr", "addressof", "addrof", "allocaaddr":
		return OpAddrOf
	case "gep", "getelementptr", "field":
		return OpGEP
	case "phi":
		return OpPhi
	case "select":
		return OpSelect
	case "store", "storeptr", "storepointer":
		return OpStorePtr
	case "load", "loadptr", "loadpointer":
		return OpLoadPtr
	case "passcall", "call", "callarg":
		return OpPassCall
	case "ptrtoint", "ptr2int":
		return OpPtrToInt
	case "inttoptr", "int2ptr":
		return OpIntToPtr
	case "returnptr", "returnpointer", "retptr":
		return OpReturnPtr
	case "param", "argument", "arg":
		return OpParam
	default:
		return OpKind(s)
	}
}

// IndexRange describes one GEP component.  Min and Max are element indices
// (inclusive).  Unknown makes the resulting address a whole-object alias and
// makes an access through it unprovable.  ElemSize defaults to one byte.
type IndexRange struct {
	Min      int64
	Max      int64
	ElemSize uint64
	Stride   int64
	Unknown  bool
}

func (r IndexRange) elemSize() uint64 {
	if r.ElemSize == 0 {
		return 1
	}
	return r.ElemSize
}

func (r IndexRange) stride() int64 {
	if r.Stride == 0 {
		return int64(r.elemSize())
	}
	return r.Stride
}

// Value is one node of the finite SSA graph.  Several aliases are accepted for
// the fields that are most likely to be named differently by a host (for
// example Pointer/Base and Then/TrueValue).  Only fields relevant to Kind are
// interpreted.
type Value struct {
	ID   ValueID
	Name string
	Kind OpKind
	// Op is an accepted alias of Kind.
	Op OpKind

	Function FunctionID

	Object ObjectID

	// Base and Pointer are accepted pointer-input aliases for GEP/LoadPtr and
	// the unary pointer operations.
	Base    ValueID
	Pointer ValueID
	Ptr     ValueID

	// Offset is a constant byte displacement for GEP.
	Offset      int64
	Field       int64
	FieldOffset int64
	// Index and Indices describe variable GEP components.  A missing or zero
	// bound is treated as the constant element zero, unless Unknown is set.
	Index   *IndexRange
	Indices []IndexRange

	Inputs []ValueID

	Cond       ValueID
	Condition  ValueID
	Then       ValueID
	Else       ValueID
	True       ValueID
	False      ValueID
	TrueValue  ValueID
	FalseValue ValueID
	IfTrue     ValueID
	IfFalse    ValueID

	// Dest is the pointer written through by StorePtr.  Src is the pointer
	// value stored.  For unary pointer operations, Src is accepted when neither
	// Ptr nor Pointer nor Base is present.
	Dest        ValueID
	Destination ValueID
	Src         ValueID
	Source      ValueID
	// Value is an accepted StorePtr source alias.
	Value ValueID

	// Callee, Args and ParamIndices describe PassCall.  Args are in call
	// argument order unless ParamIndices maps them explicitly.
	Callee       FunctionID
	Args         []ValueID
	ParamIndices []int
	Summary      *CallSummary
	CallSummary  *CallSummary

	// Param is the parameter index of OpParam.
	Param int

	// Size/AccessSize are the number of bytes read or written by a memory
	// operation.  Zero means the graph pointer size.
	Size       uint64
	AccessSize uint64
}

func (v Value) normalizedID() ValueID {
	if v.ID != "" {
		return v.ID
	}
	return ValueID(v.Name)
}

func (v Value) kind() OpKind {
	if v.Kind != "" {
		return normalizeOp(v.Kind)
	}
	return normalizeOp(v.Op)
}

func (v Value) pointerInput() ValueID {
	if v.Ptr != "" {
		return v.Ptr
	}
	if v.Pointer != "" {
		return v.Pointer
	}
	if v.Base != "" {
		return v.Base
	}
	if v.Src != "" {
		return v.Src
	}
	if v.Source != "" {
		return v.Source
	}
	if len(v.Inputs) != 0 {
		return v.Inputs[0]
	}
	if len(v.Args) != 0 {
		return v.Args[0]
	}
	return ""
}

func (v Value) offset() int64 {
	if v.Offset != 0 {
		return v.Offset
	}
	if v.Field != 0 {
		return v.Field
	}
	return v.FieldOffset
}

func (v Value) accessSize(pointerSize uint64) uint64 {
	if v.AccessSize != 0 {
		return v.AccessSize
	}
	if v.Size != 0 {
		return v.Size
	}
	if pointerSize == 0 {
		return 8
	}
	return pointerSize
}

func (v Value) storeDest() ValueID {
	if v.Dest != "" {
		return v.Dest
	}
	if v.Destination != "" {
		return v.Destination
	}
	if v.Base != "" {
		return v.Base
	}
	if v.Ptr != "" {
		return v.Ptr
	}
	if v.Pointer != "" {
		return v.Pointer
	}
	if len(v.Inputs) != 0 {
		return v.Inputs[0]
	}
	if len(v.Args) != 0 {
		return v.Args[0]
	}
	return ""
}

func (v Value) storeSrc() ValueID {
	if v.Src != "" {
		return v.Src
	}
	if v.Source != "" {
		return v.Source
	}
	if v.Value != "" {
		return v.Value
	}
	if len(v.Inputs) > 1 {
		return v.Inputs[1]
	}
	if len(v.Args) > 1 {
		return v.Args[1]
	}
	return ""
}

func (v Value) thenValue() ValueID {
	if v.Then != "" {
		return v.Then
	}
	if v.True != "" {
		return v.True
	}
	if v.IfTrue != "" {
		return v.IfTrue
	}
	if v.TrueValue != "" {
		return v.TrueValue
	}
	if len(v.Inputs) > 1 {
		return v.Inputs[1]
	}
	if len(v.Args) > 1 {
		return v.Args[1]
	}
	return ""
}

func (v Value) elseValue() ValueID {
	if v.Else != "" {
		return v.Else
	}
	if v.False != "" {
		return v.False
	}
	if v.IfFalse != "" {
		return v.IfFalse
	}
	if v.FalseValue != "" {
		return v.FalseValue
	}
	if len(v.Inputs) > 2 {
		return v.Inputs[2]
	}
	if len(v.Args) > 2 {
		return v.Args[2]
	}
	return ""
}

// ParamSummary is a per-argument call effect.  MaxOffset is an inclusive byte
// offset relative to the argument pointer.  A negative MaxOffset means the
// bound is unknown (equivalent to an arbitrary access).
type ParamSummary struct {
	Index      int
	Param      int
	Argument   int
	ParamIndex int
	NoCapture  bool
	ReadOnly   bool
	MaxOffset  int64
	// MaxAccessOffset is an accepted alias of MaxOffset.
	MaxAccessOffset int64
}

func (p ParamSummary) maxOffset() int64 {
	if p.MaxOffset != 0 {
		return p.MaxOffset
	}
	return p.MaxAccessOffset
}

func (p ParamSummary) parameterIndex(position int) int {
	if p.Param > 0 {
		return p.Param
	}
	if p.Argument > 0 {
		return p.Argument
	}
	if p.ParamIndex > 0 {
		return p.ParamIndex
	}
	if p.Index != 0 {
		return p.Index
	}
	return position
}

// CallSummary describes the effect of a call on each argument.  An absent
// entry, Unknown, or an unbounded MaxOffset is treated as capture plus arbitrary
// access for that argument.
type CallSummary struct {
	Function   FunctionID
	Params     []ParamSummary
	Args       []ParamSummary
	Parameters []ParamSummary
	Unknown    bool
}

func (s CallSummary) params() []ParamSummary {
	if len(s.Params) != 0 {
		return s.Params
	}
	if len(s.Args) != 0 {
		return s.Args
	}
	return s.Parameters
}

func (s CallSummary) lookup(param int) (ParamSummary, bool) {
	ps := s.params()
	for i, p := range ps {
		if p.parameterIndex(i) == param {
			return p, true
		}
	}
	return ParamSummary{}, false
}

// Function describes a function body for recursive summary inference.  If no
// body is supplied, a missing call summary remains unknown and conservative.
type Function struct {
	ID          FunctionID
	Params      []ValueID
	ParamValues []ValueID
	Values      []ValueID
	Body        []ValueID
	Returns     []ValueID
}

func (f Function) paramValues() []ValueID {
	if len(f.Params) != 0 {
		return f.Params
	}
	return f.ParamValues
}

func (f Function) bodyValues() []ValueID {
	if len(f.Values) != 0 {
		return f.Values
	}
	return f.Body
}

// Graph is a finite SSA graph.  It may contain one function or several
// functions; Functions is optional unless summary inference is desired.
type Graph struct {
	Objects   []Object
	Values    []Value
	Functions []Function

	// Summaries and CallSummaries are accepted aliases.  Explicit summaries
	// override summaries inferred from a Function body.
	Summaries     map[FunctionID]CallSummary
	CallSummaries map[FunctionID]CallSummary

	PointerSize uint64
	Layout      *Layout
}

func (g Graph) summaryMap() map[FunctionID]CallSummary {
	out := make(map[FunctionID]CallSummary, len(g.Summaries)+len(g.CallSummaries))
	for id, s := range g.CallSummaries {
		out[id] = s
	}
	for id, s := range g.Summaries {
		out[id] = s
	}
	return out
}

// ValuesOf returns the IDs in the graph in input order.
func (g Graph) ValuesOf() []ValueID {
	out := make([]ValueID, 0, len(g.Values))
	for _, v := range g.Values {
		out = append(out, v.normalizedID())
	}
	return out
}

// ReasonCode is a stable, machine-readable explanation for an unsafe decision.
type ReasonCode string

const (
	ReasonEscape            ReasonCode = "escape"
	ReasonOutOfBounds       ReasonCode = "out-of-bounds"
	ReasonPointerInteger    ReasonCode = "pointer-integer"
	ReasonVariableLayout    ReasonCode = "variable-layout"
	ReasonInvalidObject     ReasonCode = "invalid-object"
	ReasonUnknownPointer    ReasonCode = "unknown-pointer"
	ReasonBudgetExhausted   ReasonCode = "budget-exhausted"
	ReasonUnmanagedMetadata ReasonCode = "unmanaged-control-metadata"
)

// AccessKind describes the operation that reached an object.
type AccessKind string

const (
	AccessLoad  AccessKind = "load"
	AccessStore AccessKind = "store"
	AccessCall  AccessKind = "call"
)

// Access is a concrete pointer access discovered by the analysis.
type Access struct {
	Object    ObjectID
	Value     ValueID
	Source    ValueID
	Kind      AccessKind
	OffsetMin int64
	OffsetMax int64
	Width     uint64
	InBounds  bool
}

// Reason identifies an unsafe witness.  Value is the operation/SSA value at
// which the witness occurred; Source is the pointer value that led to it.
type Reason struct {
	Code   ReasonCode
	Object ObjectID
	Value  ValueID
	Source ValueID
	Detail string
}

func (r Reason) String() string {
	if r.Detail == "" {
		return fmt.Sprintf("%s(object=%s value=%s)", r.Code, r.Object, r.Value)
	}
	return fmt.Sprintf("%s(object=%s value=%s): %s", r.Code, r.Object, r.Value, r.Detail)
}

// Decision is the classification of one object.
type Decision struct {
	Object   ObjectID
	Class    StackClass
	Reasons  []Reason
	Accesses []Access
}

func (d Decision) Safe() bool { return d.Class == ClassSafe }

// Report is the complete classification result.
type Report struct {
	Objects         map[ObjectID]Decision
	Decisions       map[ObjectID]Decision
	Classes         map[ObjectID]StackClass
	Safe            []ObjectID
	Unsafe          []ObjectID
	Unmanaged       []ObjectID
	Writes          []UnsafeWrite
	Summaries       map[FunctionID]CallSummary
	Iterations      int
	BudgetExhausted bool
	Warnings        []string
}

// ClassOf returns the class of id.  Unknown IDs are conservatively unsafe.
func (r *Report) ClassOf(id ObjectID) StackClass {
	if r == nil {
		return ClassUnsafe
	}
	if d, ok := r.Objects[id]; ok {
		return d.Class
	}
	return ClassUnsafe
}

// DecisionOf returns the decision of id.
func (r *Report) DecisionOf(id ObjectID) (Decision, bool) {
	if r == nil {
		return Decision{}, false
	}
	d, ok := r.Objects[id]
	return d, ok
}

// SafeObjects returns the safe object IDs in input order.
func (r *Report) SafeObjects() []ObjectID {
	if r == nil {
		return nil
	}
	return append([]ObjectID(nil), r.Safe...)
}

// SlotKind distinguishes local objects from externally-addressed control
// metadata.  A control slot is input to the simulator, not assigned by this
// package.
type SlotKind string

const (
	SlotLocal    SlotKind = "local"
	SlotSafe     SlotKind = "safe"
	SlotUnsafe   SlotKind = "unsafe"
	SlotControl  SlotKind = "control"
	SlotPadding  SlotKind = "padding"
	SlotExternal SlotKind = "external"
)

// LayoutSlot is one explicitly located byte interval.  Offset and Start are
// aliases.  For control slots, ID (or Object) names the metadata.
type LayoutSlot struct {
	Object  ObjectID
	Obj     ObjectID
	ID      string
	Offset  uint64
	Start   uint64
	Address uint64
	Size    uint64
	Length  uint64
	Align   uint64
	Kind    SlotKind
	Control bool
}

func (s LayoutSlot) start() uint64 {
	if s.Offset != 0 {
		return s.Offset
	}
	if s.Start != 0 {
		return s.Start
	}
	return s.Address
}

func (s LayoutSlot) id() string {
	if s.ID != "" {
		return s.ID
	}
	if s.Object != "" {
		return string(s.Object)
	}
	return string(s.Obj)
}

func (s LayoutSlot) objectID() ObjectID {
	if s.Object != "" {
		return s.Object
	}
	return s.Obj
}

func (s LayoutSlot) size() uint64 {
	if s.Size != 0 {
		return s.Size
	}
	return s.Length
}

func (s LayoutSlot) isControl() bool {
	return s.Control || s.Kind == SlotControl
}

// Layout is an externally supplied address map.  The package never invents
// offsets for control slots; callers must provide them.
type Layout struct {
	Slots []LayoutSlot
}

// UnsafeWrite is a byte range written through a pointer.  Offset is relative to
// the source object's slot.  An interval can start before the object (negative
// offset) and can extend past its end.
type UnsafeWrite struct {
	Object    ObjectID
	Offset    int64
	OffsetMin int64
	OffsetMax int64
	Size      uint64
	Source    ValueID
	// Address selects an absolute write start instead of a source-object
	// relative offset.
	Address  uint64
	Absolute bool
}

func (w UnsafeWrite) normalizedOffset() int64 {
	if w.Offset != 0 {
		return w.Offset
	}
	if w.OffsetMin != 0 {
		return w.OffsetMin
	}
	return w.OffsetMax
}

// LayoutImpact is emitted only when a simulated write reaches control metadata
// or another safe object.
type LayoutImpact struct {
	Source      ObjectID
	Write       UnsafeWrite
	Control     bool
	SafeObjects []ObjectID
	Touched     []string
	Bytes       uint64
}

func (i LayoutImpact) Serious() bool {
	return i.Control || len(i.SafeObjects) != 0
}

// ProvenanceNode describes one node on a shortest witness path.
type ProvenanceNode string

const (
	NodeObject    ProvenanceNode = "object"
	NodeValue     ProvenanceNode = "value"
	NodeOperation ProvenanceNode = "operation"
	NodeMemory    ProvenanceNode = "memory"
	NodeLayout    ProvenanceNode = "layout"
)

// ProvenanceStep is one edge/node in a provenance path.  Operation and Value
// are populated according to Kind.
type ProvenanceStep struct {
	Kind      ProvenanceNode
	ID        string
	Object    ObjectID
	Value     ValueID
	Operation OpKind
	Detail    string
}

// Mismatch compares a pending (callback) classification with the reference
// model.  Dangerous is true only for class-safe in the callback and class-unsafe
// in the reference model.
type Mismatch struct {
	Object    ObjectID
	Pending   StackClass
	Reference StackClass
	Reasons   []Reason
	Path      []ProvenanceStep
	// SourcePath is Path in sink-to-object order.
	SourcePath []ProvenanceStep
	Distance   int
	Dangerous  bool
}

// AuditReport contains the reference report plus false-safe findings and their
// shortest provenance paths.  Only layout impacts that reach control metadata
// or another safe object are included.
type AuditReport struct {
	Reference       *Report
	Mismatches      []Mismatch
	FalseSafe       []Mismatch
	FalseUnsafe     []Mismatch
	Impacts         []LayoutImpact
	BudgetExhausted bool
	Warnings        []string
}

// PendingClassifier is the convenient concrete callback type.  Audit also
// accepts equivalent functions with one or two arguments and bool/string
// return values.
type PendingClassifier func(ObjectID, Object) StackClass

// Options controls budgets and optional layout handling.  The zero value is
// conservative and uses a bounded fixed-point budget.
type Options struct {
	PointerSize    uint64
	PointerSizeSet bool
	MaxIterations  int
	MaxSteps       int
	MaxPath        int
	InferSummaries bool
	Layout         *Layout
}

// Option mutates Options.
type Option func(*Options)

func WithPointerSize(size uint64) Option {
	return func(o *Options) {
		o.PointerSize = size
		o.PointerSizeSet = true
	}
}

func WithBudget(iterations, steps int) Option {
	return func(o *Options) {
		o.MaxIterations = iterations
		o.MaxSteps = steps
	}
}

func WithPathBudget(maxPath int) Option {
	return func(o *Options) { o.MaxPath = maxPath }
}

func WithInferredSummaries(enabled bool) Option {
	return func(o *Options) { o.InferSummaries = enabled }
}

func WithLayout(l Layout) Option {
	return func(o *Options) { o.Layout = &l }
}

func normalizeOptions(opts []Option) Options {
	o := Options{PointerSize: 8, MaxIterations: 256, MaxSteps: 200000, MaxPath: 128, InferSummaries: true}
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	if o.PointerSize == 0 {
		o.PointerSize = 8
	}
	if o.MaxIterations <= 0 {
		o.MaxIterations = 1
	}
	if o.MaxSteps <= 0 {
		o.MaxSteps = 1
	}
	if o.MaxPath <= 0 {
		o.MaxPath = 1
	}
	return o
}

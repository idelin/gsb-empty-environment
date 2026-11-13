package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// FrameID identifies a source-level activation record. When the same frame is
// reached through different call sites, CallSite is used together with FrameID
// to keep the two instances distinct.
type FrameID string

// Slot is a frame-relative stack slot.
type Slot int

// OpID identifies one node in the finite SSA control-flow graph.
type OpID string

// ValueID identifies one SSA value.
type ValueID string

// CellID identifies one heap cell.
type CellID string

// ClosureID identifies a MakeClosure result.
type ClosureID string

// ValueKind is the small, closed value domain used by the analysis.
type ValueKind uint8

const (
	ScalarKind ValueKind = iota + 1
	FrameRefKind
	HeapCellKind
	ClosureKind
)

func (k ValueKind) String() string {
	switch k {
	case ScalarKind:
		return "scalar"
	case FrameRefKind:
		return "FrameRef"
	case HeapCellKind:
		return "HeapCell"
	case ClosureKind:
		return "Closure"
	default:
		return fmt.Sprintf("ValueKind(%d)", uint8(k))
	}
}

// Value is one of Scalar, FrameRef, HeapCell, or Closure.
type Value interface {
	Kind() ValueKind
	String() string
}

// Scalar is copied by CaptureByValue.
type Scalar struct {
	Tenant string
	Data   string
	Bytes  []byte
}

func (Scalar) Kind() ValueKind { return ScalarKind }
func (s Scalar) String() string {
	if s.Tenant == "" {
		return fmt.Sprintf("scalar(%q)", s.Data)
	}
	return fmt.Sprintf("scalar(%s:%q)", s.Tenant, s.Data)
}

// FrameRef points at a stack slot. Mutable is an access property, not part of
// the identity of the location.
type FrameRef struct {
	Frame    FrameID
	Slot     Slot
	Mutable  bool
	CallSite string
}

func (FrameRef) Kind() ValueKind { return FrameRefKind }
func (r FrameRef) String() string {
	return fmt.Sprintf("FrameRef(%s[%s].%d mutable=%t)", r.Frame, r.CallSite, r.Slot, r.Mutable)
}

// Location is the identity of a stack slot. CallSite separates recursive
// instances that share a FrameID.
type Location struct {
	Frame    FrameID
	CallSite string
	Slot     Slot
}

func (l Location) String() string {
	if l.CallSite == "" {
		return fmt.Sprintf("%s.%d", l.Frame, l.Slot)
	}
	return fmt.Sprintf("%s@%s.%d", l.Frame, l.CallSite, l.Slot)
}

func (r FrameRef) Location() Location {
	return Location{Frame: r.Frame, CallSite: r.CallSite, Slot: r.Slot}
}

// HeapCell is the unique promoted home for one frame slot.
type HeapCell struct {
	ID CellID
}

func (HeapCell) Kind() ValueKind { return HeapCellKind }
func (c HeapCell) String() string {
	return "HeapCell(" + string(c.ID) + ")"
}

// CaptureKind distinguishes the two capture operations.
type CaptureKind uint8

const (
	CaptureByRef CaptureKind = iota + 1
	CaptureByValue
)

func (k CaptureKind) String() string {
	switch k {
	case CaptureByRef:
		return "by-ref"
	case CaptureByValue:
		return "by-value"
	default:
		return fmt.Sprintf("CaptureKind(%d)", uint8(k))
	}
}

// Capture describes one closure environment entry. Source is the SSA value
// captured by MakeClosure. Frame/Slot are populated for direct slot captures
// and for convenience in reports.
type Capture struct {
	Kind     CaptureKind
	Source   ValueID
	Frame    FrameID
	Slot     Slot
	Mutable  bool
	CallSite string
	Label    string
}

// Closure is the MakeClosure value.
type Closure struct {
	ID       ClosureID
	Captures []Capture
}

func (Closure) Kind() ValueKind { return ClosureKind }
func (c Closure) String() string {
	return "Closure(" + string(c.ID) + ")"
}

// OpKind is the operation alphabet from the analysis specification, plus Phi
// (an SSA merge point), StoreHeap (a write into an existing HeapCell), and
// Branch (a control-flow join/split without a value).
type OpKind string

const (
	OpFrameAlloc     OpKind = "FrameAlloc"
	OpCaptureByRef   OpKind = "CaptureByRef"
	OpCaptureByValue OpKind = "CaptureByValue"
	OpMakeClosure    OpKind = "MakeClosure"
	OpStoreGlobal    OpKind = "StoreGlobal"
	OpPassAsync      OpKind = "PassAsync"
	OpCallNow        OpKind = "CallNow"
	OpReturn         OpKind = "Return"
	OpHeapPromote    OpKind = "HeapPromote"
	OpStoreHeap      OpKind = "StoreHeap"
	OpScalar         OpKind = "Scalar"
	OpPhi            OpKind = "Phi"
	OpBranch         OpKind = "Branch"
)

// Op is a node in the finite SSA graph. A node has zero or one result value.
// Value carries the concrete result for value-producing operations; ResultKind
// is a fallback for Phi and hand-written programs.
type Op struct {
	ID         OpID
	Kind       OpKind
	Frame      FrameID
	CallSite   string
	Slots      int
	Slot       Slot
	Mutable    bool
	Inputs     []ValueID
	Captures   []Capture
	Global     string
	Cell       CellID
	ClosureID  ClosureID
	Result     ValueID
	Value      Value
	ResultKind ValueKind
	Succs      []OpID
	Label      string
}

func (o *Op) ResultType() ValueKind {
	if o == nil {
		return 0
	}
	if o.Value != nil {
		return o.Value.Kind()
	}
	return o.ResultKind
}

// FrameSlot describes the initial tenant-owned bytes in a stack slot. It is
// used by the companion interpreter, not by the escape lattice.
type FrameSlot struct {
	Tenant string
	Data   string
	Bytes  []byte
}

// Frame describes a stack frame template.
type Frame struct {
	ID       FrameID
	CallSite string
	Tenant   string
	Slots    []FrameSlot
}

// Program is a finite SSA control-flow graph.
type Program struct {
	Entry  OpID
	Frames []*Frame
	Ops    []*Op

	opIndex      map[OpID]*Op
	frameIndex   map[FrameID]*Frame
	valueIndex   map[ValueID]*Op
	closureIndex map[ClosureID]*Op
	autoValue    int
	autoOp       int
	dirty        bool
}

// NewProgram returns an empty program.
func NewProgram() *Program {
	return &Program{
		opIndex:      make(map[OpID]*Op),
		frameIndex:   make(map[FrameID]*Frame),
		valueIndex:   make(map[ValueID]*Op),
		closureIndex: make(map[ClosureID]*Op),
	}
}

// AddFrame appends a frame template. If a frame with the same ID exists it is
// replaced.
func (p *Program) AddFrame(f *Frame) *Frame {
	if f == nil {
		return nil
	}
	for i, old := range p.Frames {
		if old.ID == f.ID && old.CallSite == f.CallSite {
			p.Frames[i] = f
			p.dirty = true
			return f
		}
	}
	p.Frames = append(p.Frames, f)
	p.dirty = true
	return f
}

// AddOp appends an op and fills in a few convenient defaults.
func (p *Program) AddOp(o *Op) *Op {
	if o == nil {
		return nil
	}
	if o.ID == "" {
		p.autoOp++
		o.ID = OpID(fmt.Sprintf("op%d", p.autoOp))
	}
	if o.Result == "" && o.ResultType() != 0 {
		p.autoValue++
		o.Result = ValueID(fmt.Sprintf("v%d", p.autoValue))
	}
	if o.Kind == OpMakeClosure && o.ClosureID == "" && o.Result != "" {
		o.ClosureID = ClosureID(o.Result)
	}
	if o.Kind == OpMakeClosure && o.Value == nil {
		o.Value = Closure{ID: o.ClosureID, Captures: append([]Capture(nil), o.Captures...)}
	}
	if o.Value == nil {
		switch o.ResultKind {
		case FrameRefKind:
			o.Value = FrameRef{Frame: o.Frame, Slot: o.Slot, Mutable: o.Mutable, CallSite: o.CallSite}
		case HeapCellKind:
			cell := o.Cell
			if cell == "" {
				cell = CellID(o.ID)
				o.Cell = cell
			}
			o.Value = HeapCell{ID: cell}
		}
	}
	p.Ops = append(p.Ops, o)
	p.dirty = true
	return o
}

// Link adds a control-flow edge from -> to.
func (p *Program) Link(from, to OpID) {
	f := p.Op(from)
	if f == nil {
		return
	}
	for _, s := range f.Succs {
		if s == to {
			return
		}
	}
	f.Succs = append(f.Succs, to)
	p.dirty = true
}

// Op returns the op with id, or nil.
func (p *Program) Op(id OpID) *Op {
	p.rebuild()
	return p.opIndex[id]
}

// Frame returns the frame template with id, or nil.
func (p *Program) Frame(id FrameID) *Frame {
	p.rebuild()
	return p.frameIndex[id]
}

// ValueOp returns the operation defining id, or nil.
func (p *Program) ValueOp(id ValueID) *Op {
	p.rebuild()
	return p.valueIndex[id]
}

// Value returns the concrete definition of a value when the defining op has a
// concrete Value. Phi results may not have one.
func (p *Program) Value(id ValueID) Value {
	o := p.ValueOp(id)
	if o == nil {
		return nil
	}
	if o.Value != nil {
		return o.Value
	}
	switch o.ResultKind {
	case FrameRefKind:
		return FrameRef{Frame: o.Frame, Slot: o.Slot, Mutable: o.Mutable, CallSite: o.CallSite}
	case HeapCellKind:
		return HeapCell{ID: o.Cell}
	}
	return nil
}

// ClosureOp returns the MakeClosure operation for id.
func (p *Program) ClosureOp(id ClosureID) *Op {
	p.rebuild()
	return p.closureIndex[id]
}

func (p *Program) rebuild() {
	if !p.dirty && p.opIndex != nil {
		return
	}
	p.opIndex = make(map[OpID]*Op, len(p.Ops))
	p.frameIndex = make(map[FrameID]*Frame, len(p.Frames))
	p.valueIndex = make(map[ValueID]*Op, len(p.Ops))
	p.closureIndex = make(map[ClosureID]*Op)
	for _, f := range p.Frames {
		if f == nil {
			continue
		}
		p.frameIndex[f.ID] = f
	}
	for _, o := range p.Ops {
		if o == nil {
			continue
		}
		p.opIndex[o.ID] = o
		if o.Result != "" {
			p.valueIndex[o.Result] = o
		}
		if o.Kind == OpMakeClosure {
			id := o.ClosureID
			if id == "" && o.Result != "" {
				id = ClosureID(o.Result)
			}
			if id != "" {
				p.closureIndex[id] = o
			}
		}
	}
	p.dirty = false
}

// Clone returns a deep-enough copy for callers that build variants of a
// program. Concrete Scalar byte slices are copied.
func (p *Program) Clone() *Program {
	out := NewProgram()
	out.Entry = p.Entry
	for _, f := range p.Frames {
		if f == nil {
			continue
		}
		nf := *f
		nf.Slots = append([]FrameSlot(nil), f.Slots...)
		for i := range nf.Slots {
			nf.Slots[i].Bytes = append([]byte(nil), nf.Slots[i].Bytes...)
		}
		out.Frames = append(out.Frames, &nf)
	}
	for _, o := range p.Ops {
		if o == nil {
			continue
		}
		no := *o
		no.Inputs = append([]ValueID(nil), o.Inputs...)
		no.Captures = append([]Capture(nil), o.Captures...)
		no.Succs = append([]OpID(nil), o.Succs...)
		switch v := o.Value.(type) {
		case Scalar:
			v.Bytes = append([]byte(nil), v.Bytes...)
			no.Value = v
		case *Scalar:
			cp := *v
			cp.Bytes = append([]byte(nil), v.Bytes...)
			no.Value = &cp
		case Closure:
			v.Captures = append([]Capture(nil), v.Captures...)
			no.Value = v
		case *Closure:
			cp := *v
			cp.Captures = append([]Capture(nil), v.Captures...)
			no.Value = &cp
		}
		out.Ops = append(out.Ops, &no)
	}
	out.dirty = true
	return out
}

func (p *Program) sortedOps() []*Op {
	out := append([]*Op(nil), p.Ops...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func captureLocation(c Capture, values map[ValueID]valueState) Location {
	if c.Source != "" {
		if v, ok := values[c.Source]; ok {
			if loc, ok := v.oneLocation(); ok {
				return loc
			}
		}
	}
	return Location{Frame: c.Frame, CallSite: c.CallSite, Slot: c.Slot}
}

func (p *Program) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "entry=%s frames=%d ops=%d\n", p.Entry, len(p.Frames), len(p.Ops))
	for _, o := range p.sortedOps() {
		fmt.Fprintf(&b, "  %-12s %-16s", o.ID, o.Kind)
		if o.Frame != "" {
			fmt.Fprintf(&b, " frame=%s", o.Frame)
		}
		if o.CallSite != "" {
			fmt.Fprintf(&b, " callsite=%s", o.CallSite)
		}
		if len(o.Inputs) > 0 {
			fmt.Fprintf(&b, " in=%v", o.Inputs)
		}
		if o.Result != "" {
			fmt.Fprintf(&b, " -> %s:%s", o.Result, o.ResultType())
		}
		if len(o.Succs) > 0 {
			fmt.Fprintf(&b, " succ=%v", o.Succs)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

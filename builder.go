package netsec

import "fmt"

// Builder is a small fluent constructor for Programs. Each method appends an
// op and, unless the previous op already has successors, connects it to the
// new op.
type Builder struct {
	*Program
	last OpID
}

func NewBuilder() *Builder {
	return &Builder{Program: NewProgram()}
}

func NewBuilderFor(p *Program) *Builder {
	if p == nil {
		p = NewProgram()
	}
	return &Builder{Program: p}
}

type OpOption func(*Op)

func WithID(id OpID) OpOption           { return func(o *Op) { o.ID = id } }
func WithCallSite(site string) OpOption { return func(o *Op) { o.CallSite = site } }
func WithLabel(label string) OpOption   { return func(o *Op) { o.Label = label } }
func WithCell(cell CellID) OpOption     { return func(o *Op) { o.Cell = cell } }
func WithSuccs(succs ...OpID) OpOption {
	return func(o *Op) { o.Succs = append([]OpID(nil), succs...) }
}
func WithInputs(inputs ...ValueID) OpOption {
	return func(o *Op) { o.Inputs = append([]ValueID(nil), inputs...) }
}
func WithCaptures(caps ...Capture) OpOption {
	return func(o *Op) { o.Captures = append([]Capture(nil), caps...) }
}
func WithSlots(n int) OpOption            { return func(o *Op) { o.Slots = n } }
func WithMutable(mutable bool) OpOption   { return func(o *Op) { o.Mutable = mutable } }
func WithGlobal(name string) OpOption     { return func(o *Op) { o.Global = name } }
func WithFrameID(id FrameID) OpOption     { return func(o *Op) { o.Frame = id } }
func WithSlot(slot Slot) OpOption         { return func(o *Op) { o.Slot = slot } }
func WithResult(id ValueID) OpOption      { return func(o *Op) { o.Result = id } }
func WithResultValue(v Value) OpOption    { return func(o *Op) { o.Value = v } }
func WithResultKind(k ValueKind) OpOption { return func(o *Op) { o.ResultKind = k } }
func WithClosureID(id ClosureID) OpOption { return func(o *Op) { o.ClosureID = id } }
func WithEntry(id OpID) OpOption          { return func(o *Op) { o.ID = id } }
func WithFrameSlot(id FrameID, slot Slot) OpOption {
	return func(o *Op) {
		o.Frame = id
		o.Slot = slot
	}
}

func (b *Builder) add(o *Op, opts ...OpOption) *Op {
	for _, opt := range opts {
		opt(o)
	}
	b.Program.AddOp(o)
	if b.last != "" {
		b.Program.Link(b.last, o.ID)
	}
	b.last = o.ID
	if b.Entry == "" {
		b.Entry = o.ID
	}
	return o
}

func (b *Builder) Frame(f *Frame) *Frame { return b.Program.AddFrame(f) }

func (b *Builder) FrameTemplate(id FrameID, tenant string, slots ...FrameSlot) *Frame {
	return b.Program.AddFrame(&Frame{ID: id, Tenant: tenant, Slots: append([]FrameSlot(nil), slots...)})
}

func (b *Builder) FrameAlloc(frame FrameID, slots int, opts ...OpOption) *Op {
	return b.add(&Op{Kind: OpFrameAlloc, Frame: frame, Slots: slots}, opts...)
}

func (b *Builder) CaptureByRef(source ValueID, mutable bool, opts ...OpOption) *Op {
	o := &Op{
		Kind:       OpCaptureByRef,
		Inputs:     []ValueID{source},
		Mutable:    mutable,
		ResultKind: FrameRefKind,
	}
	o.Value = FrameRef{Mutable: mutable}
	return b.add(o, opts...)
}

func (b *Builder) CaptureByRefSlot(frame FrameID, slot Slot, mutable bool, opts ...OpOption) *Op {
	return b.add(&Op{
		Kind:       OpCaptureByRef,
		Frame:      frame,
		Slot:       slot,
		Mutable:    mutable,
		ResultKind: FrameRefKind,
		Value:      FrameRef{Frame: frame, Slot: slot, Mutable: mutable},
	}, opts...)
}

func (b *Builder) CaptureByValue(source ValueID, opts ...OpOption) *Op {
	return b.add(&Op{
		Kind:       OpCaptureByValue,
		Inputs:     []ValueID{source},
		ResultKind: ScalarKind,
	}, opts...)
}

// CaptureByValueSlot copies the scalar currently held in a slot.
func (b *Builder) CaptureByValueSlot(frame FrameID, slot Slot, opts ...OpOption) *Op {
	return b.add(&Op{
		Kind:       OpCaptureByValue,
		Frame:      frame,
		Slot:       slot,
		ResultKind: ScalarKind,
	}, opts...)
}

func (b *Builder) MakeClosure(caps ...Capture) *Op {
	return b.MakeClosureWith(caps)
}

// MakeClosureWith is MakeClosure with operation options such as WithID.
func (b *Builder) MakeClosureWith(caps []Capture, opts ...OpOption) *Op {
	o := &Op{Kind: OpMakeClosure, Captures: append([]Capture(nil), caps...), ResultKind: ClosureKind}
	for _, c := range caps {
		if c.Source != "" {
			o.Inputs = append(o.Inputs, c.Source)
		}
	}
	return b.add(o, opts...)
}

func (b *Builder) HeapPromote(source ValueID, opts ...OpOption) *Op {
	o := &Op{
		Kind:       OpHeapPromote,
		Inputs:     []ValueID{source},
		ResultKind: HeapCellKind,
	}
	return b.add(o, opts...)
}

func (b *Builder) StoreGlobal(name string, source ValueID, opts ...OpOption) *Op {
	return b.add(&Op{Kind: OpStoreGlobal, Global: name, Inputs: []ValueID{source}}, opts...)
}

func (b *Builder) PassAsync(source ValueID, opts ...OpOption) *Op {
	return b.add(&Op{Kind: OpPassAsync, Inputs: []ValueID{source}}, opts...)
}

func (b *Builder) CallNow(source ValueID, opts ...OpOption) *Op {
	return b.add(&Op{Kind: OpCallNow, Inputs: []ValueID{source}}, opts...)
}

func (b *Builder) Return(source ValueID, frame FrameID, opts ...OpOption) *Op {
	o := &Op{Kind: OpReturn, Frame: frame}
	if source != "" {
		o.Inputs = []ValueID{source}
	}
	return b.add(o, opts...)
}

func (b *Builder) StoreHeap(cell, value ValueID, opts ...OpOption) *Op {
	return b.add(&Op{Kind: OpStoreHeap, Inputs: []ValueID{cell, value}}, opts...)
}

// Scalar creates a scalar constant. It is the source used by
// CaptureByValue.
func (b *Builder) Scalar(tenant, data string, opts ...OpOption) *Op {
	return b.add(&Op{
		Kind:       OpScalar,
		ResultKind: ScalarKind,
		Value:      Scalar{Tenant: tenant, Data: data},
	}, opts...)
}

func (b *Builder) Phi(inputs ...ValueID) *Op {
	kind := ValueKind(0)
	for _, id := range inputs {
		if v := b.Value(id); v != nil {
			kind = v.Kind()
			break
		}
	}
	return b.add(&Op{
		Kind:       OpPhi,
		Inputs:     append([]ValueID(nil), inputs...),
		ResultKind: kind,
	})
}

func (b *Builder) Branch(succs ...OpID) *Op {
	return b.add(&Op{Kind: OpBranch, Succs: append([]OpID(nil), succs...)})
}

// Jump starts a new linear segment at an already existing op.
func (b *Builder) Jump(id OpID) {
	b.last = id
	if b.Entry == "" {
		b.Entry = id
	}
}

// Op appends a hand-built operation and applies options. The normal linear
// auto-link from the previous op still applies; call Break first when starting
// a second basic-block arm.
func (b *Builder) Op(o *Op, opts ...OpOption) *Op { return b.add(o, opts...) }

// Break prevents the next appended op from being auto-linked to the previous
// one.
func (b *Builder) Break() { b.last = "" }

// Last returns the most recently appended/selected op.
func (b *Builder) Last() OpID { return b.last }

func (b *Builder) chain(ids ...OpID) {
	for i := 0; i+1 < len(ids); i++ {
		b.Program.Link(ids[i], ids[i+1])
	}
}

func autoResult(prefix string, n int) ValueID {
	return ValueID(fmt.Sprintf("%s%d", prefix, n))
}

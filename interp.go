package netsec

import (
	"fmt"
	"sort"
)

// EventKind describes one observable interpreter action.
type EventKind string

const (
	EventAlloc    EventKind = "alloc"
	EventCapture  EventKind = "capture"
	EventPromote  EventKind = "promote"
	EventEscape   EventKind = "escape"
	EventReturn   EventKind = "return"
	EventRelease  EventKind = "release"
	EventRead     EventKind = "read"
	EventConflict EventKind = "conflict"
)

// Event is a compact execution trace entry.
type Event struct {
	Kind     EventKind `json:"kind"`
	Op       OpID      `json:"op,omitempty"`
	Frame    FrameID   `json:"frame,omitempty"`
	CallSite string    `json:"callsite,omitempty"`
	Slot     Slot      `json:"slot,omitempty"`
	Cell     CellID    `json:"cell,omitempty"`
	Closure  ClosureID `json:"closure,omitempty"`
	Tenant   string    `json:"tenant,omitempty"`
	Data     string    `json:"data,omitempty"`
	Note     string    `json:"note,omitempty"`
}

// CaptureRead is one observation of a by-reference closure environment entry.
// AfterRelease distinguishes the post-frame-exit tenant check from a
// synchronous CallNow read.
type CaptureRead struct {
	Closure      ClosureID `json:"closure"`
	Frame        FrameID   `json:"frame,omitempty"`
	CallSite     string    `json:"callsite,omitempty"`
	Slot         Slot      `json:"slot,omitempty"`
	Cell         CellID    `json:"cell,omitempty"`
	Tenant       string    `json:"tenant,omitempty"`
	Data         string    `json:"data,omitempty"`
	AfterRelease bool      `json:"after_release"`
	Leaked       bool      `json:"leaked"`
}

// Execution is the result of replaying one SSA path.
type Execution struct {
	Path           []OpID        `json:"path"`
	Events         []Event       `json:"events,omitempty"`
	Reads          []CaptureRead `json:"reads,omitempty"`
	Leaked         bool          `json:"leaked"`
	AttackerTenant string        `json:"attacker_tenant,omitempty"`
}

// ExecuteOptions controls the tenant-reuse simulation.
type ExecuteOptions struct {
	// AttackerTenant names the tenant that reuses freed stack slots.
	AttackerTenant string
	// AttackerData is the byte payload written into every freed slot.
	AttackerData string
}

func (o ExecuteOptions) normalized() ExecuteOptions {
	if o.AttackerTenant == "" {
		o.AttackerTenant = "tenant-B"
	}
	if o.AttackerData == "" {
		o.AttackerData = "OTHER-TENANT-READS-MY-SECRET"
	}
	return o
}

type runtimeValue struct {
	kind    ValueKind
	scalar  Scalar
	ref     FrameRef
	hasRef  bool
	cell    CellID
	closure *runtimeClosure
}

func (v runtimeValue) String() string {
	switch v.kind {
	case ScalarKind:
		return v.scalar.String()
	case FrameRefKind:
		return v.ref.String()
	case HeapCellKind:
		return HeapCell{ID: v.cell}.String()
	case ClosureKind:
		if v.closure != nil {
			return Closure{ID: v.closure.id}.String()
		}
		return "Closure(?)"
	default:
		return "zero"
	}
}

type runtimeCapture struct {
	kind     CaptureKind
	scalar   Scalar
	ref      FrameRef
	hasRef   bool
	cell     CellID
	nested   *runtimeClosure
	original string
}

type runtimeClosure struct {
	id       ClosureID
	captures []runtimeCapture
}

type runtimeFrame struct {
	inst     frameInstance
	tenant   string
	slots    []Scalar
	promoted map[Slot]CellID
	returned bool
	released bool
}

type interpreter struct {
	p          *Program
	opts       ExecuteOptions
	frames     map[frameInstance]*runtimeFrame
	heap       map[CellID]runtimeValue
	heapOwner  map[CellID]Location
	values     map[ValueID]runtimeValue
	globals    map[string]runtimeValue
	async      []runtimeValue
	returned   []runtimeValue
	heapEsc    []runtimeValue
	events     []Event
	reads      []CaptureRead
	invoked    map[string]bool
	conflicted bool
}

func newInterpreter(p *Program, opts ExecuteOptions) *interpreter {
	return &interpreter{
		p:         p,
		opts:      opts.normalized(),
		frames:    make(map[frameInstance]*runtimeFrame),
		heap:      make(map[CellID]runtimeValue),
		heapOwner: make(map[CellID]Location),
		values:    make(map[ValueID]runtimeValue),
		globals:   make(map[string]runtimeValue),
		invoked:   make(map[string]bool),
	}
}

// ExecutePath replays path in order, releases every remaining frame by filling
// its slots with attacker bytes, then invokes asynchronous, global, returned,
// and heap-stored closures. It is the executable counterpart of an analysis
// evidence path.
func ExecutePath(p *Program, path []OpID, opts ...ExecuteOptions) (*Execution, error) {
	if p == nil {
		return nil, fmt.Errorf("netsec: nil program")
	}
	if len(path) == 0 {
		return nil, fmt.Errorf("netsec: empty execution path")
	}
	opt := ExecuteOptions{}
	if len(opts) > 0 {
		opt = opts[len(opts)-1]
	}
	ip := newInterpreter(p, opt)
	for _, id := range path {
		op := p.Op(id)
		if op == nil {
			return nil, fmt.Errorf("netsec: path references unknown op %q", id)
		}
		if err := ip.execute(op); err != nil {
			return nil, err
		}
	}
	ip.releaseAll()
	ip.invokeEscaped()
	ex := &Execution{
		Path:           append([]OpID(nil), path...),
		Events:         append([]Event(nil), ip.events...),
		Reads:          append([]CaptureRead(nil), ip.reads...),
		AttackerTenant: ip.opts.AttackerTenant,
	}
	for _, r := range ex.Reads {
		if r.Leaked {
			ex.Leaked = true
			break
		}
	}
	return ex, nil
}

// Execute follows the first successor at each branch. Use ExecutePath for
// evidence paths and branch-specific verification.
func Execute(p *Program, opts ...ExecuteOptions) (*Execution, error) {
	if p == nil {
		return nil, fmt.Errorf("netsec: nil program")
	}
	path := make([]OpID, 0, len(p.Ops))
	seen := make(map[OpID]bool)
	for id := p.Entry; id != ""; {
		if seen[id] {
			break
		}
		seen[id] = true
		path = append(path, id)
		op := p.Op(id)
		if op == nil || len(op.Succs) == 0 {
			break
		}
		id = op.Succs[0]
	}
	return ExecutePath(p, path, opts...)
}

// LeakCheck is the result of checking one analysis finding in the interpreter.
type LeakCheck struct {
	Leaked         bool          `json:"leaked"`
	Path           []OpID        `json:"path"`
	ExpectedTenant string        `json:"expected_tenant,omitempty"`
	ObservedTenant string        `json:"observed_tenant,omitempty"`
	ObservedData   string        `json:"observed_data,omitempty"`
	Read           CaptureRead   `json:"read,omitempty"`
	Reads          []CaptureRead `json:"reads,omitempty"`
}

// VerifyFinding replays the finding's shortest unpromoted path, exits the
// frame, fills its slots with another tenant's bytes, and confirms that the
// reported capture observes the other tenant's value.
func VerifyFinding(p *Program, f Finding, opts ...ExecuteOptions) (*LeakCheck, error) {
	path := f.LeakPath
	if len(path) == 0 {
		path = f.AllocPath
	}
	if len(path) == 0 && f.Capture.CaptureAt != "" {
		path = []OpID{f.Capture.CaptureAt, f.EscapeAt}
	}
	ex, err := ExecutePath(p, path, opts...)
	if err != nil {
		return nil, err
	}
	lc := &LeakCheck{Path: append([]OpID(nil), ex.Path...), Reads: ex.Reads}
	if fr := p.Frame(f.Capture.Frame); fr != nil {
		if f.Capture.Slot >= 0 && int(f.Capture.Slot) < len(fr.Slots) {
			lc.ExpectedTenant = fr.Slots[f.Capture.Slot].Tenant
		}
		if lc.ExpectedTenant == "" {
			lc.ExpectedTenant = fr.Tenant
		}
	}
	for _, r := range ex.Reads {
		match := r.Leaked
		if f.Capture.Frame != "" {
			match = match && r.Frame == f.Capture.Frame && r.Slot == f.Capture.Slot
			if f.Capture.CallSite != "" && r.CallSite != "" {
				match = match && r.CallSite == f.Capture.CallSite
			}
		}
		if match {
			lc.Leaked = true
			lc.Read = r
			lc.ObservedTenant = r.Tenant
			lc.ObservedData = r.Data
			return lc, nil
		}
	}
	for _, r := range ex.Reads {
		if r.Leaked {
			lc.Leaked = true
			lc.Read = r
			lc.ObservedTenant = r.Tenant
			lc.ObservedData = r.Data
			break
		}
	}
	return lc, nil
}

func (i *interpreter) execute(op *Op) error {
	switch op.Kind {
	case OpFrameAlloc:
		i.allocFrame(op)
	case OpCaptureByRef:
		i.captureByRef(op)
	case OpCaptureByValue:
		i.captureByValue(op)
	case OpMakeClosure:
		i.makeClosure(op)
	case OpHeapPromote:
		i.promote(op)
	case OpStoreGlobal:
		i.storeGlobal(op)
	case OpPassAsync:
		i.passAsync(op)
	case OpCallNow:
		i.callNow(op)
	case OpReturn:
		i.doReturn(op)
	case OpStoreHeap:
		i.storeHeap(op)
	case OpScalar:
		s, _ := op.Value.(Scalar)
		if s.Tenant == "" && s.Data == "" {
			s = Scalar{Tenant: "literal", Data: string(op.ID)}
		}
		i.values[op.Result] = runtimeValue{kind: ScalarKind, scalar: cloneScalar(s)}
	case OpPhi:
		for _, id := range op.Inputs {
			if v, ok := i.values[id]; ok {
				i.values[op.Result] = v
				break
			}
		}
	case OpBranch:
		return nil
	default:
		return fmt.Errorf("netsec: interpreter does not support op %s (%s)", op.ID, op.Kind)
	}
	return nil
}

func (i *interpreter) allocFrame(op *Op) {
	inst := frameInstance{Frame: op.Frame, CallSite: i.instanceCallSite(op)}
	tmpl := i.p.Frame(op.Frame)
	n := op.Slots
	if tmpl != nil && n < len(tmpl.Slots) {
		n = len(tmpl.Slots)
	}
	tenant := string(op.Frame)
	if tmpl != nil && tmpl.Tenant != "" {
		tenant = tmpl.Tenant
	}
	slots := make([]Scalar, n)
	for slot := 0; slot < n; slot++ {
		var s Scalar
		if tmpl != nil && slot < len(tmpl.Slots) {
			fs := tmpl.Slots[slot]
			s = Scalar{Tenant: fs.Tenant, Data: fs.Data, Bytes: append([]byte(nil), fs.Bytes...)}
			if s.Tenant == "" {
				s.Tenant = tenant
			}
			if s.Data == "" && len(s.Bytes) > 0 {
				s.Data = string(s.Bytes)
			}
		} else {
			s = Scalar{Tenant: tenant, Data: fmt.Sprintf("tenant:%s:slot:%d", tenant, slot)}
		}
		if s.Data == "" && len(s.Bytes) == 0 {
			s.Data = fmt.Sprintf("tenant:%s:slot:%d", s.Tenant, slot)
		}
		slots[slot] = cloneScalar(s)
	}
	i.frames[inst] = &runtimeFrame{
		inst:     inst,
		tenant:   tenant,
		slots:    slots,
		promoted: make(map[Slot]CellID),
	}
	i.events = append(i.events, Event{Kind: EventAlloc, Op: op.ID, Frame: op.Frame, CallSite: inst.CallSite, Note: fmt.Sprintf("%d slots", n)})
}

func (i *interpreter) captureByRef(op *Op) {
	var ref FrameRef
	var cell CellID
	if len(op.Inputs) > 0 {
		if v, ok := i.values[op.Inputs[0]]; ok {
			switch v.kind {
			case HeapCellKind:
				cell = v.cell
			case FrameRefKind:
				ref = v.ref
			}
		}
	}
	if ref.Frame == "" && cell == "" && op.Frame != "" {
		ref = FrameRef{Frame: op.Frame, Slot: op.Slot, Mutable: op.Mutable, CallSite: i.instanceCallSite(op)}
	}
	if cell == "" {
		cell = i.resolvePromotion(ref)
	}
	if cell != "" {
		i.values[op.Result] = runtimeValue{kind: HeapCellKind, cell: cell}
	} else {
		ref.Mutable = ref.Mutable || op.Mutable
		i.values[op.Result] = runtimeValue{kind: FrameRefKind, ref: ref, hasRef: true}
	}
	i.events = append(i.events, Event{Kind: EventCapture, Op: op.ID, Frame: ref.Frame, CallSite: ref.CallSite, Slot: ref.Slot, Cell: cell})
}

func (i *interpreter) captureByValue(op *Op) {
	var s Scalar
	if len(op.Inputs) > 0 {
		if v, ok := i.values[op.Inputs[0]]; ok {
			switch v.kind {
			case ScalarKind:
				s = cloneScalar(v.scalar)
			case FrameRefKind:
				rv := i.readCapture(runtimeCapture{kind: CaptureByRef, ref: v.ref, hasRef: true})
				s = cloneScalar(rv.scalar)
			case HeapCellKind:
				if hv, ok := i.heap[v.cell]; ok {
					s = cloneScalar(hv.scalar)
				}
			}
		}
	} else if op.Frame != "" {
		rv := i.readCapture(runtimeCapture{
			kind:   CaptureByRef,
			ref:    FrameRef{Frame: op.Frame, Slot: op.Slot, CallSite: i.instanceCallSite(op)},
			hasRef: true,
		})
		s = cloneScalar(rv.scalar)
	}
	i.values[op.Result] = runtimeValue{kind: ScalarKind, scalar: s}
	i.events = append(i.events, Event{Kind: EventCapture, Op: op.ID, Tenant: s.Tenant, Data: s.Data, Note: "by-value copy"})
}

func (i *interpreter) makeClosure(op *Op) {
	caps := op.Captures
	if len(caps) == 0 {
		if c, ok := op.Value.(Closure); ok {
			caps = c.Captures
		} else if c, ok := op.Value.(*Closure); ok {
			caps = c.Captures
		}
	}
	id := op.ClosureID
	if id == "" {
		id = ClosureID(op.Result)
	}
	c := &runtimeClosure{id: id}
	for _, cap := range caps {
		switch cap.Kind {
		case CaptureByValue:
			rc := runtimeCapture{kind: CaptureByValue}
			if cap.Source != "" {
				if v, ok := i.values[cap.Source]; ok && v.kind == ScalarKind {
					rc.scalar = cloneScalar(v.scalar)
					rc.original = v.scalar.Tenant
				}
			}
			c.captures = append(c.captures, rc)
		default:
			rc := runtimeCapture{kind: CaptureByRef}
			if cap.Source != "" {
				if v, ok := i.values[cap.Source]; ok {
					switch v.kind {
					case HeapCellKind:
						rc.cell = v.cell
					case FrameRefKind:
						rc.ref = v.ref
						rc.hasRef = true
						rc.original = i.originalTenant(v.ref)
					case ClosureKind:
						rc.nested = v.closure
					}
				}
			}
			if !rc.hasRef && rc.cell == "" && rc.nested == nil && cap.Frame != "" {
				rc.ref = FrameRef{Frame: cap.Frame, Slot: cap.Slot, Mutable: cap.Mutable, CallSite: cap.CallSite}
				if rc.ref.CallSite == "" {
					rc.ref.CallSite = i.instanceCallSite(op)
				}
				rc.hasRef = true
				rc.original = i.originalTenant(rc.ref)
			}
			if rc.hasRef {
				if cell := i.resolvePromotion(rc.ref); cell != "" {
					rc.cell = cell
					rc.hasRef = false
				}
			}
			c.captures = append(c.captures, rc)
		}
	}
	i.values[op.Result] = runtimeValue{kind: ClosureKind, closure: c}
}

func (i *interpreter) promote(op *Op) {
	v := runtimeValue{}
	if len(op.Inputs) > 0 {
		v = i.values[op.Inputs[0]]
	}
	if v.kind == HeapCellKind {
		i.values[op.Result] = v
		return
	}
	if v.kind != FrameRefKind {
		i.values[op.Result] = runtimeValue{kind: HeapCellKind, cell: op.Cell}
		return
	}
	ref := v.ref
	cell := op.Cell
	if cell == "" {
		cell = CellID("cell:" + string(op.ID))
	}
	loc := ref.Location()
	if owner, ok := i.heapOwner[cell]; ok && owner != loc {
		i.conflicted = true
		i.events = append(i.events, Event{Kind: EventConflict, Op: op.ID, Cell: cell, Note: "HeapCell already owned by " + owner.String()})
	}
	i.heapOwner[cell] = loc
	value := runtimeValue{kind: ScalarKind, scalar: Scalar{}}
	if f := i.findFrame(ref); f != nil {
		slot := int(ref.Slot)
		if slot >= 0 && slot < len(f.slots) {
			value = runtimeValue{kind: ScalarKind, scalar: cloneScalar(f.slots[slot])}
		}
		f.promoted[ref.Slot] = cell
	}
	i.heap[cell] = value
	// Rewrite already-materialized SSA aliases.
	for id, av := range i.values {
		if av.kind == FrameRefKind && av.ref.Location() == loc {
			i.values[id] = runtimeValue{kind: HeapCellKind, cell: cell}
		}
	}
	i.values[op.Result] = runtimeValue{kind: HeapCellKind, cell: cell}
	i.events = append(i.events, Event{Kind: EventPromote, Op: op.ID, Frame: loc.Frame, CallSite: loc.CallSite, Slot: loc.Slot, Cell: cell})
}

func (i *interpreter) storeGlobal(op *Op) {
	if len(op.Inputs) == 0 {
		return
	}
	v := i.values[op.Inputs[0]]
	i.globals[op.Global] = v
	if v.kind == ClosureKind {
		i.events = append(i.events, Event{Kind: EventEscape, Op: op.ID, Closure: closureID(v), Note: "StoreGlobal " + op.Global})
	}
}

func (i *interpreter) passAsync(op *Op) {
	if len(op.Inputs) == 0 {
		return
	}
	v := i.values[op.Inputs[0]]
	i.async = append(i.async, v)
	if v.kind == ClosureKind {
		i.events = append(i.events, Event{Kind: EventEscape, Op: op.ID, Closure: closureID(v), Note: "PassAsync"})
	}
}

func (i *interpreter) callNow(op *Op) {
	if len(op.Inputs) == 0 {
		return
	}
	v := i.values[op.Inputs[0]]
	if v.kind == ClosureKind {
		i.invokeClosure(v, "CallNow", false)
	}
}

func (i *interpreter) doReturn(op *Op) {
	if len(op.Inputs) > 0 {
		if v := i.values[op.Inputs[0]]; v.kind == ClosureKind {
			i.returned = append(i.returned, v)
			i.events = append(i.events, Event{Kind: EventEscape, Op: op.ID, Closure: closureID(v), Note: "Return"})
		}
	}
	inst := frameInstance{Frame: op.Frame, CallSite: i.instanceCallSite(op)}
	if f := i.frames[inst]; f != nil {
		i.releaseFrame(f)
	} else if op.Frame == "" {
		for _, f := range i.frames {
			i.releaseFrame(f)
		}
	}
}

func (i *interpreter) storeHeap(op *Op) {
	if len(op.Inputs) < 2 {
		return
	}
	cellVal := i.values[op.Inputs[0]]
	val := i.values[op.Inputs[1]]
	cell := cellVal.cell
	if cell == "" {
		cell = CellID(op.Cell)
	}
	if cell == "" {
		return
	}
	i.heap[cell] = val
	if val.kind == ClosureKind {
		i.heapEsc = append(i.heapEsc, val)
		i.events = append(i.events, Event{Kind: EventEscape, Op: op.ID, Cell: cell, Closure: closureID(val), Note: "StoreHeap"})
	}
}

func (i *interpreter) releaseAll() {
	insts := make([]frameInstance, 0, len(i.frames))
	for inst := range i.frames {
		insts = append(insts, inst)
	}
	sort.Slice(insts, func(a, b int) bool { return insts[a].String() < insts[b].String() })
	for _, inst := range insts {
		i.releaseFrame(i.frames[inst])
	}
}

func (i *interpreter) releaseFrame(f *runtimeFrame) {
	if f == nil || f.released {
		return
	}
	for slot := range f.slots {
		f.slots[slot] = Scalar{
			Tenant: i.opts.AttackerTenant,
			Data:   i.opts.AttackerData,
			Bytes:  []byte(i.opts.AttackerData),
		}
	}
	f.returned = true
	f.released = true
	i.events = append(i.events, Event{
		Kind:     EventRelease,
		Frame:    f.inst.Frame,
		CallSite: f.inst.CallSite,
		Tenant:   i.opts.AttackerTenant,
		Data:     i.opts.AttackerData,
		Note:     "freed slots filled with another tenant's bytes",
	})
}

func (i *interpreter) invokeEscaped() {
	for _, v := range i.async {
		i.invokeClosure(v, "async", true)
	}
	names := make([]string, 0, len(i.globals))
	for name := range i.globals {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		i.invokeClosure(i.globals[name], "global:"+name, true)
	}
	for _, v := range i.returned {
		i.invokeClosure(v, "returned", true)
	}
	for _, v := range i.heapEsc {
		i.invokeClosure(v, "heap", true)
	}
	for _, v := range i.heap {
		if v.kind == ClosureKind {
			i.invokeClosure(v, "heap-scan", true)
		}
	}
}

func (i *interpreter) invokeClosure(v runtimeValue, phase string, afterRelease bool) {
	if v.kind != ClosureKind || v.closure == nil {
		return
	}
	key := fmt.Sprintf("%s|%p", phase, v.closure)
	if i.invoked[key] {
		return
	}
	i.invoked[key] = true
	for _, cap := range v.closure.captures {
		if cap.kind == CaptureByValue {
			continue
		}
		if cap.nested != nil {
			i.invokeClosure(runtimeValue{kind: ClosureKind, closure: cap.nested}, phase, afterRelease)
			continue
		}
		rv := i.readCapture(cap)
		read := CaptureRead{
			Closure:      v.closure.id,
			Tenant:       rv.scalar.Tenant,
			Data:         rv.scalar.Data,
			AfterRelease: afterRelease,
		}
		if cap.hasRef {
			read.Frame = cap.ref.Frame
			read.CallSite = cap.ref.CallSite
			read.Slot = cap.ref.Slot
		}
		if cap.cell != "" {
			read.Cell = cap.cell
		}
		if rv.kind == HeapCellKind {
			read.Cell = rv.cell
		}
		if afterRelease && rv.kind == ScalarKind && rv.scalar.Tenant == i.opts.AttackerTenant {
			read.Leaked = true
		}
		i.reads = append(i.reads, read)
		i.events = append(i.events, Event{
			Kind:     EventRead,
			Closure:  v.closure.id,
			Frame:    read.Frame,
			CallSite: read.CallSite,
			Slot:     read.Slot,
			Cell:     read.Cell,
			Tenant:   read.Tenant,
			Data:     read.Data,
			Note:     phase,
		})
		if rv.kind == ClosureKind {
			i.invokeClosure(rv, phase, afterRelease)
		}
	}
}

func (i *interpreter) readCapture(c runtimeCapture) runtimeValue {
	if c.cell != "" {
		if v, ok := i.heap[c.cell]; ok {
			return v
		}
		return runtimeValue{kind: HeapCellKind, cell: c.cell}
	}
	if !c.hasRef {
		return runtimeValue{kind: ScalarKind, scalar: cloneScalar(c.scalar)}
	}
	f := i.findFrame(c.ref)
	if f == nil {
		return runtimeValue{kind: FrameRefKind, ref: c.ref, hasRef: true}
	}
	if cell := f.promoted[c.ref.Slot]; cell != "" {
		if v, ok := i.heap[cell]; ok {
			return v
		}
		return runtimeValue{kind: HeapCellKind, cell: cell}
	}
	slot := int(c.ref.Slot)
	if slot < 0 || slot >= len(f.slots) {
		return runtimeValue{kind: FrameRefKind, ref: c.ref, hasRef: true}
	}
	return runtimeValue{kind: ScalarKind, scalar: cloneScalar(f.slots[slot])}
}

func (i *interpreter) resolvePromotion(ref FrameRef) CellID {
	f := i.findFrame(ref)
	if f == nil {
		return ""
	}
	return f.promoted[ref.Slot]
}

func (i *interpreter) findFrame(ref FrameRef) *runtimeFrame {
	if ref.Frame == "" {
		return nil
	}
	if ref.CallSite != "" {
		return i.frames[frameInstance{Frame: ref.Frame, CallSite: ref.CallSite}]
	}
	var only *runtimeFrame
	for inst, f := range i.frames {
		if inst.Frame != ref.Frame {
			continue
		}
		if only != nil {
			return only
		}
		only = f
	}
	return only
}

func (i *interpreter) originalTenant(ref FrameRef) string {
	f := i.findFrame(ref)
	if f == nil {
		return ""
	}
	if int(ref.Slot) >= 0 && int(ref.Slot) < len(f.slots) {
		return f.slots[ref.Slot].Tenant
	}
	return f.tenant
}

func (i *interpreter) instanceCallSite(op *Op) string {
	if op.CallSite != "" {
		return op.CallSite
	}
	if f := i.p.Frame(op.Frame); f != nil {
		return f.CallSite
	}
	return ""
}

func closureID(v runtimeValue) ClosureID {
	if v.closure == nil {
		return ""
	}
	return v.closure.id
}

func cloneScalar(s Scalar) Scalar {
	s.Bytes = append([]byte(nil), s.Bytes...)
	return s
}

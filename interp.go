package netsec

import (
	"fmt"
	"sort"
	"strings"
)

const (
	// DefaultHeapBase is the address of the first concrete object.
	DefaultHeapBase int64 = 0x10000
	// DefaultHeapStride separates consecutive object addresses.
	DefaultHeapStride int64 = 0x1000
)

// RunConfig bounds concrete exploration. Non-positive values select the
// corresponding default.
type RunConfig struct {
	MaxSteps       int
	MaxStates      int
	MaxObjects     int
	MaxAllocations int
	HeapBase       int64
	HeapStride     int64
}

// DefaultRunConfig returns conservative bounds suitable for unit tests.
func DefaultRunConfig() RunConfig {
	return RunConfig{
		MaxSteps:       2000,
		MaxStates:      20000,
		MaxObjects:     512,
		MaxAllocations: 512,
		HeapBase:       DefaultHeapBase,
		HeapStride:     DefaultHeapStride,
	}
}

func (c RunConfig) normalized() RunConfig {
	def := DefaultRunConfig()
	if c.MaxSteps <= 0 {
		c.MaxSteps = def.MaxSteps
	}
	if c.MaxStates <= 0 {
		c.MaxStates = def.MaxStates
	}
	if c.MaxObjects <= 0 {
		c.MaxObjects = def.MaxObjects
	}
	if c.MaxAllocations <= 0 {
		c.MaxAllocations = def.MaxAllocations
	}
	if c.HeapBase == 0 {
		c.HeapBase = def.HeapBase
	}
	if c.HeapStride <= 0 {
		c.HeapStride = def.HeapStride
	}
	return c
}

// ExecStatus is the overall result of bounded concrete execution.
type ExecStatus uint8

const (
	ExecSafe ExecStatus = iota
	ExecUnsafe
	ExecRejected
)

func (s ExecStatus) String() string {
	switch s {
	case ExecSafe:
		return "safe"
	case ExecUnsafe:
		return "unsafe"
	case ExecRejected:
		return "rejected"
	default:
		return fmt.Sprintf("status(%d)", s)
	}
}

// ViolationKind classifies concrete pointer-safety failures.
type ViolationKind uint8

const (
	UseAfterFree ViolationKind = iota + 1
	MislabeledInteger
	WrongTypeDereference
	OutOfBoundsDereference
	InvalidRuntimeValue
)

func (k ViolationKind) String() string {
	switch k {
	case UseAfterFree:
		return "use-after-free"
	case MislabeledInteger:
		return "mislabeled-integer"
	case WrongTypeDereference:
		return "wrong-type-dereference"
	case OutOfBoundsDereference:
		return "out-of-bounds-dereference"
	case InvalidRuntimeValue:
		return "invalid-runtime-value"
	default:
		return fmt.Sprintf("violation(%d)", k)
	}
}

// Event is one executed instruction on a concrete path.
type Event struct {
	Block  BlockID
	Instr  ValueID
	Op     Op
	Source string
	Detail string
}

func (e Event) String() string {
	label := fmt.Sprintf("%s/%d:%s", e.Block, e.Instr, e.Op)
	if e.Source != "" {
		label += "(" + e.Source + ")"
	}
	if e.Detail != "" {
		label += " [" + e.Detail + "]"
	}
	return label
}

// Violation is a concrete failure with the shortest path found for its kind.
type Violation struct {
	Kind       ViolationKind
	Message    string
	Path       []Event
	Steps      int
	Safepoint  ValueID
	Location   Location
	Value      ValueID
	Object     ObjectID
	Generation int
	Address    int64
}

func (v Violation) PathString() string {
	parts := make([]string, len(v.Path))
	for i, event := range v.Path {
		parts[i] = event.String()
	}
	return strings.Join(parts, " -> ")
}

// ExecutionReport is the bounded interpreter result.
type ExecutionReport struct {
	Status       ExecStatus
	MapReport    *StackMapReport
	Violations   []Violation
	UseAfterFree *Violation
	Mislabeled   *Violation

	BudgetExhausted bool
	StatesExplored  int
	Reason          string
}

// Execute validates the maps, then concretely explores all feasible paths
// within cfg. A Safepoint reclaims every generation not marked by the
// candidate map. A budget cutoff never yields ExecSafe: the map is rejected
// because safety was not established.
func Execute(fn *Function, maps []StackMap, cfg RunConfig) *ExecutionReport {
	cfg = cfg.normalized()
	report := &ExecutionReport{}
	mapReport := CheckStackMaps(fn, maps)
	report.MapReport = mapReport
	if mapReport.Analysis == nil {
		report.Status = ExecRejected
		report.Reason = "invalid SSA function"
		return report
	}
	prog := make(map[BlockID][]*Instr, len(fn.Blocks))
	for _, b := range fn.Blocks {
		prog[b.ID] = b.Instrs
	}
	mapsBySP := selectRuntimeMaps(maps, mapReport.Analysis)

	start := &rtState{
		pc:          programCounter{block: fn.Entry},
		values:      make(map[ValueID]runtimeValue),
		heap:        make(map[int64]heapObject),
		reclaimedAt: make(map[int64]ValueID),
		siteGen:     make(map[ObjectID]int),
		nextObj:     1,
		nextAddress: cfg.HeapBase,
		stride:      cfg.HeapStride,
		maxAllocs:   cfg.MaxAllocations,
		maxObjs:     cfg.MaxObjects,
	}
	queue := []*rtState{start}
	seen := make(map[string]bool)
	seen[start.signature()] = true
	head := 0

	for head < len(queue) {
		if report.StatesExplored >= cfg.MaxStates {
			report.BudgetExhausted = true
			break
		}
		st := queue[head]
		head++
		report.StatesExplored++
		instrs, ok := prog[st.pc.block]
		if !ok || st.pc.index < 0 || st.pc.index >= len(instrs) {
			report.BudgetExhausted = true
			continue
		}
		in := instrs[st.pc.index]

		// Clone first so branch successors never alias. This also makes the
		// event list immutable for the parent path.
		next := st.clone()
		next.path = append(next.path, Event{
			Block: next.pc.block, Instr: in.ID, Op: in.Op, Source: in.Source,
		})

		switch in.Op {
		case OpAlloc:
			ptr, ok := next.allocate(in.Object, in.Size)
			if !ok {
				report.BudgetExhausted = true
				continue
			}
			next.values[in.ID] = ptr
			enqueue(&queue, seen, next.step(), cfg, report)

		case OpCopy:
			value, ok := next.values[in.Args[0]]
			if !ok {
				report.addViolation(InvalidRuntimeValue, next, in, NoValue, Location{}, NoValue,
					"copy operand %d has no runtime value", in.Args[0])
				continue
			}
			next.values[in.ID] = value
			enqueue(&queue, seen, next.step(), cfg, report)

		case OpField:
			base, ok := next.values[in.Args[0]]
			if !ok {
				report.addViolation(InvalidRuntimeValue, next, in, NoValue, Location{}, NoValue,
					"field base %d has no runtime value", in.Args[0])
				continue
			}
			if base.kind != valuePtr {
				report.addViolation(WrongTypeDereference, next, in, NoValue, Location{}, in.Args[0],
					"field base %d is an integer, not a pointer", in.Args[0])
				continue
			}
			base.offset += in.Offset
			next.values[in.ID] = base
			enqueue(&queue, seen, next.step(), cfg, report)

		case OpInt:
			next.values[in.ID] = runtimeValue{kind: valueInt, integer: in.Int}
			enqueue(&queue, seen, next.step(), cfg, report)

		case OpPhi:
			selected, ok := phiValue(in, next.pc.pred)
			if !ok {
				report.addViolation(InvalidRuntimeValue, next, in, NoValue, Location{}, NoValue,
					"no Phi arm for predecessor %q", next.pc.pred)
				continue
			}
			value, ok := next.values[selected]
			if !ok {
				report.addViolation(InvalidRuntimeValue, next, in, NoValue, Location{}, NoValue,
					"Phi arm %d has no runtime value", selected)
				continue
			}
			next.values[in.ID] = value
			enqueue(&queue, seen, next.step(), cfg, report)

		case OpCall:
			handler := callExceptionTarget(in)
			if handler != "" {
				exc := next.clone()
				exc.path = append([]Event(nil), next.path...)
				exc.pc = programCounter{block: handler, pred: next.pc.block}
				if in.Def() != NoValue {
					delete(exc.values, in.Def())
				}
				enqueue(&queue, seen, exc, cfg, report)
			}
			normal := next
			if err := normal.applyCallReturn(in); err != nil {
				report.Reason = err.Error()
				report.BudgetExhausted = true
				continue
			}
			enqueue(&queue, seen, normal.step(), cfg, report)

		case OpSafepoint:
			entries := mapsBySP[in.ID]
			next.gc(entries, report, in)
			enqueue(&queue, seen, next.step(), cfg, report)

		case OpDereference:
			ptr, ok := next.values[in.Args[0]]
			if !ok {
				report.addViolation(InvalidRuntimeValue, next, in, NoValue, Location{}, in.Args[0],
					"dereference operand %d has no runtime value", in.Args[0])
				continue
			}
			if ptr.kind != valuePtr {
				report.addViolation(WrongTypeDereference, next, in, NoValue, Location{}, in.Args[0],
					"dereference operand %d is an integer", in.Args[0])
				continue
			}
			obj, alive := next.heap[ptr.ptr]
			if !alive || !obj.Alive {
				safepoint := next.reclaimedAt[ptr.ptr]
				if safepoint == NoValue {
					safepoint = next.lastSafepoint
				}
				report.addViolation(UseAfterFree, next, in, safepoint, Location{}, in.Args[0],
					"dereference of %s generation %d at address 0x%x after it was reclaimed",
					obj.Site, obj.Generation, obj.Base)
				continue
			}
			if ptr.offset < 0 || ptr.offset >= obj.Size {
				report.addViolation(OutOfBoundsDereference, next, in, NoValue, Location{}, in.Args[0],
					"interior offset %d is outside %s generation %d of size %d",
					ptr.offset, obj.Site, obj.Generation, obj.Size)
				continue
			}
			next.values[in.ID] = runtimeValue{kind: valueInt, integer: obj.Base + int64(ptr.offset)}
			enqueue(&queue, seen, next.step(), cfg, report)

		case OpReturn:
			// A return is a successful terminal path.

		case OpJump:
			next.pc = programCounter{block: in.Target, pred: next.pc.block}
			enqueue(&queue, seen, next, cfg, report)

		case OpCondJump:
			condID := in.Cond
			if condID == NoValue && len(in.Args) > 0 {
				condID = in.Args[0]
			}
			cond, ok := next.values[condID]
			if !ok || cond.kind != valueInt {
				report.addViolation(InvalidRuntimeValue, next, in, NoValue, Location{}, NoValue,
					"branch condition %d is not an integer", condID)
				continue
			}
			target := in.False
			if cond.integer != 0 {
				target = in.True
			}
			next.pc = programCounter{block: target, pred: next.pc.block}
			enqueue(&queue, seen, next, cfg, report)
		}
	}

	report.finish()
	if report.Status != ExecUnsafe {
		if !report.MapReport.Valid {
			report.Status = ExecRejected
			if report.Reason == "" {
				report.Reason = "candidate stack map was rejected"
			}
		} else if report.BudgetExhausted {
			report.Status = ExecRejected
			if report.Reason == "" {
				report.Reason = "exploration budget exhausted; refusing to certify the map"
			}
		} else {
			report.Status = ExecSafe
		}
	}
	return report
}

// Run is an alias for Execute.
func Run(fn *Function, maps []StackMap, cfg RunConfig) *ExecutionReport {
	return Execute(fn, maps, cfg)
}

type valueKind uint8

const (
	valuePtr valueKind = iota + 1
	valueInt
)

type runtimeValue struct {
	kind    valueKind
	ptr     int64
	offset  int
	integer int64
}

type heapObject struct {
	Ref        int64
	Site       ObjectID
	Generation int
	Size       int
	Base       int64
	Alive      bool
}

type programCounter struct {
	block BlockID
	index int
	pred  BlockID
}

type rtState struct {
	pc          programCounter
	values      map[ValueID]runtimeValue
	heap        map[int64]heapObject
	reclaimedAt map[int64]ValueID
	siteGen     map[ObjectID]int
	nextObj     int64
	nextAddress int64

	allocations   int
	stride        int64
	maxAllocs     int
	maxObjs       int
	path          []Event
	lastSafepoint ValueID
}

func (s *rtState) clone() *rtState {
	out := &rtState{
		pc:            s.pc,
		values:        make(map[ValueID]runtimeValue, len(s.values)),
		heap:          make(map[int64]heapObject, len(s.heap)),
		reclaimedAt:   make(map[int64]ValueID, len(s.reclaimedAt)),
		siteGen:       make(map[ObjectID]int, len(s.siteGen)),
		nextObj:       s.nextObj,
		nextAddress:   s.nextAddress,
		allocations:   s.allocations,
		stride:        s.stride,
		maxAllocs:     s.maxAllocs,
		maxObjs:       s.maxObjs,
		path:          append([]Event(nil), s.path...),
		lastSafepoint: s.lastSafepoint,
	}
	for id, value := range s.values {
		out.values[id] = value
	}
	for ref, obj := range s.heap {
		out.heap[ref] = obj
	}
	for ref, sp := range s.reclaimedAt {
		out.reclaimedAt[ref] = sp
	}
	for site, gen := range s.siteGen {
		out.siteGen[site] = gen
	}
	return out
}

func (s *rtState) signature() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s:%d:%s;", s.pc.block, s.pc.index, s.pc.pred)
	ids := make([]int, 0, len(s.values))
	for id := range s.values {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)
	for _, raw := range ids {
		v := s.values[ValueID(raw)]
		fmt.Fprintf(&b, "v%d=%d,%d,%d,%d;", raw, v.kind, v.ptr, v.offset, v.integer)
	}
	refs := make([]int64, 0, len(s.heap))
	for ref := range s.heap {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i] < refs[j] })
	for _, ref := range refs {
		o := s.heap[ref]
		fmt.Fprintf(&b, "o%d=%s,%d,%d,%d,%t;", o.Ref, o.Site, o.Generation, o.Size, o.Base, o.Alive)
		if sp := s.reclaimedAt[ref]; sp != NoValue {
			fmt.Fprintf(&b, "r%d=%d;", ref, sp)
		}
	}
	fmt.Fprintf(&b, "n%d;x%d;a%d;s%d", s.nextObj, s.nextAddress, s.allocations, s.lastSafepoint)
	return b.String()
}

func (s *rtState) step() *rtState {
	s.pc.index++
	return s
}

func (s *rtState) allocate(site ObjectID, size int) (runtimeValue, bool) {
	if s.allocations >= s.maxAllocs || len(s.heap) >= s.maxObjs {
		return runtimeValue{}, false
	}
	ref := s.nextObj
	s.nextObj++
	s.allocations++
	s.siteGen[site]++
	obj := heapObject{
		Ref: ref, Site: site, Generation: s.siteGen[site], Size: size,
		Base: s.nextAddress, Alive: true,
	}
	step := s.stride
	if step < int64(size) {
		step = int64(size)
	}
	step = (step + 7) &^ 7
	s.nextAddress += step
	s.heap[ref] = obj
	return runtimeValue{kind: valuePtr, ptr: ref}, true
}

func (s *rtState) objectAlive(ref int64) bool {
	obj, ok := s.heap[ref]
	return ok && obj.Alive
}

func (s *rtState) applyCallReturn(in *Instr) error {
	switch in.Summary.Return.Kind {
	case RetNone:
		return nil
	case RetNonPtr:
		s.values[in.ID] = runtimeValue{kind: valueInt}
		return nil
	case RetArg:
		idx := in.Summary.Return.Arg
		if idx < 0 || idx >= len(in.Args) {
			return fmt.Errorf("netsec: call %d return argument %d out of range", in.ID, idx)
		}
		v, ok := s.values[in.Args[idx]]
		if !ok {
			return fmt.Errorf("netsec: call %d return argument %d has no runtime value", in.ID, idx)
		}
		if v.kind == valuePtr {
			v.offset += in.Summary.Return.Offset
		}
		s.values[in.ID] = v
		return nil
	case RetFresh:
		ptr, ok := s.allocate(in.Summary.Return.Object, in.Summary.Return.Size)
		if !ok {
			return fmt.Errorf("netsec: allocation budget exhausted in call %d", in.ID)
		}
		s.values[in.ID] = ptr
		return nil
	default:
		return fmt.Errorf("netsec: call %d has invalid return kind", in.ID)
	}
}

func (s *rtState) gc(entries []StackMapEntry, report *ExecutionReport, in *Instr) {
	s.lastSafepoint = in.ID
	marked := make(map[int64]bool)
	seenLoc := make(map[Location]bool)
	for _, entry := range entries {
		if entry.Location.Kind != LocationRegister && entry.Location.Kind != LocationSlot {
			continue
		}
		if seenLoc[entry.Location] {
			// Duplicate locations are rejected by CheckStackMaps. Runtime
			// uses the first occurrence deterministically.
			continue
		}
		seenLoc[entry.Location] = true
		value, ok := s.values[entry.Value]
		if !ok {
			continue
		}
		switch value.kind {
		case valuePtr:
			if s.objectAlive(value.ptr) {
				marked[value.ptr] = true
			}
		case valueInt:
			if obj, ok := s.objectAtAddress(value.integer); ok {
				marked[obj.Ref] = true
				before := len(report.Violations)
				report.addViolation(
					MislabeledInteger, s, in, in.ID, entry.Location, entry.Value,
					"integer %d at %s is interpreted as a pointer to %s generation %d at 0x%x",
					value.integer, entry.Location, obj.Site, obj.Generation, obj.Base,
				)
				if len(report.Violations) > before {
					v := &report.Violations[before]
					v.Object = obj.Site
					v.Generation = obj.Generation
					v.Address = value.integer
				}
			}
		}
		if entry.DerivedBase != NoValue {
			if base, ok := s.values[entry.DerivedBase]; ok && base.kind == valuePtr && s.objectAlive(base.ptr) {
				marked[base.ptr] = true
			}
		}
	}
	reclaimed := 0
	for ref, obj := range s.heap {
		if !obj.Alive || marked[ref] {
			continue
		}
		obj.Alive = false
		s.heap[ref] = obj
		s.reclaimedAt[ref] = in.ID
		reclaimed++
	}
	last := &s.path[len(s.path)-1]
	if last.Instr == in.ID {
		last.Detail = fmt.Sprintf("gc reclaimed %d generation(s)", reclaimed)
	}
}

func (s *rtState) objectAtAddress(address int64) (heapObject, bool) {
	var best heapObject
	found := false
	for _, obj := range s.heap {
		if !obj.Alive {
			continue
		}
		if address >= obj.Base && address < obj.Base+int64(obj.Size) {
			if !found || obj.Ref < best.Ref {
				best = obj
				found = true
			}
		}
	}
	return best, found
}

func phiValue(in *Instr, pred BlockID) (ValueID, bool) {
	for _, edge := range in.Phi {
		if edge.Pred == pred {
			return edge.Value, true
		}
	}
	return NoValue, false
}

func enqueue(queue *[]*rtState, seen map[string]bool, st *rtState, cfg RunConfig, report *ExecutionReport) {
	if len(st.path) > cfg.MaxSteps {
		report.BudgetExhausted = true
		return
	}
	sig := st.signature()
	if seen[sig] {
		return
	}
	seen[sig] = true
	*queue = append(*queue, st)
}

func selectRuntimeMaps(maps []StackMap, analysis *Analysis) map[ValueID][]StackMapEntry {
	validIDs := make(map[ValueID]bool, len(analysis.Safepoints))
	for _, sp := range analysis.Safepoints {
		validIDs[sp.At] = true
	}
	out := make(map[ValueID][]StackMapEntry)
	seen := make(map[ValueID]bool)
	for _, m := range maps {
		if !validIDs[m.At] || seen[m.At] {
			continue
		}
		seen[m.At] = true
		out[m.At] = append([]StackMapEntry(nil), m.Entries...)
	}
	return out
}

func (r *ExecutionReport) addViolation(kind ViolationKind, st *rtState, in *Instr, safepoint ValueID, loc Location, value ValueID, format string, args ...any) {
	path := append([]Event(nil), st.path...)
	if len(path) == 0 || path[len(path)-1].Instr != in.ID {
		path = append(path, Event{Block: st.pc.block, Instr: in.ID, Op: in.Op, Source: in.Source})
	}
	v := Violation{
		Kind: kind, Message: fmt.Sprintf(format, args...), Path: path, Steps: len(path),
		Safepoint: safepoint, Location: loc, Value: value,
	}
	// Fill object metadata for pointer faults when possible.
	if in != nil && in.Op == OpDereference && len(in.Args) > 0 {
		if ptr, ok := st.values[in.Args[0]]; ok && ptr.kind == valuePtr {
			if obj, ok := st.heap[ptr.ptr]; ok {
				v.Object, v.Generation, v.Address = obj.Site, obj.Generation, obj.Base+int64(ptr.offset)
			}
		}
	}
	r.Violations = append(r.Violations, v)
}

func (r *ExecutionReport) finish() {
	sort.SliceStable(r.Violations, func(i, j int) bool {
		if r.Violations[i].Steps != r.Violations[j].Steps {
			return r.Violations[i].Steps < r.Violations[j].Steps
		}
		return r.Violations[i].Kind < r.Violations[j].Kind
	})
	for i := range r.Violations {
		v := &r.Violations[i]
		switch v.Kind {
		case UseAfterFree:
			if r.UseAfterFree == nil || shorter(v, r.UseAfterFree) {
				r.UseAfterFree = v
			}
		case MislabeledInteger:
			if r.Mislabeled == nil || shorter(v, r.Mislabeled) {
				r.Mislabeled = v
			}
		}
	}
	if len(r.Violations) > 0 {
		r.Status = ExecUnsafe
	}
}

func shorter(a, b *Violation) bool {
	if a.Steps != b.Steps {
		return a.Steps < b.Steps
	}
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	return a.Value < b.Value
}

package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// Status is the verdict for a whole program.
type Status uint8

const (
	StatusSafe Status = iota + 1
	StatusVulnerable
	StatusUnknown
)

func (s Status) String() string {
	switch s {
	case StatusSafe:
		return "safe"
	case StatusVulnerable:
		return "vulnerable"
	case StatusUnknown:
		return "unknown"
	default:
		return "invalid"
	}
}

// EscapeKind names the sink through which a closure escaped.
type EscapeKind string

const (
	EscapeGlobal EscapeKind = "StoreGlobal"
	EscapeAsync  EscapeKind = "PassAsync"
	EscapeReturn EscapeKind = "Return"
	EscapeHeap   EscapeKind = "StoreHeap"
)

// IssueReason describes why a capture is unsafe at an escape sink.
type IssueReason string

const (
	MissingPromotion IssueReason = "missing-promotion"
	ReturnedFrame    IssueReason = "capture-after-return"
)

// CaptureIssue is one unsafe by-reference capture.
type CaptureIssue struct {
	Closure   ClosureID
	Frame     FrameID
	CallSite  string
	Slot      Slot
	Mutable   bool
	CaptureAt OpID
	Reason    IssueReason
	Origins   []OpID
	Cell      CellID
}

// Finding is one escape path. AllocPath is the shortest FrameAlloc -> escape
// evidence path. LeakPath is a shortest path that also avoids promotion of the
// unsafe capture. MissingPromotionPaths holds one or more capture -> escape
// suffixes with no dominating promotion.
type Finding struct {
	EscapeAt              OpID
	Sink                  EscapeKind
	Closure               ClosureID
	Capture               CaptureIssue
	AllocPath             []OpID
	LeakPath              []OpID
	MissingPromotionPaths [][]OpID
}

// Unknown records an incompleteness reason. An empty Budget or a malformed
// value flow is never reported as safe.
type Unknown struct {
	At     OpID
	Reason string
}

// Stats reports analysis effort.
type Stats struct {
	States      int
	Transitions int
	Budget      int
	Exhausted   bool
}

// Options controls analysis limits.
type Options struct {
	// Budget is the maximum number of op-state transitions. Zero uses the
	// default. A negative value means unlimited.
	Budget int
	// PathBudget is the maximum number of states explored by one evidence
	// search. Zero uses the default.
	PathBudget int
	// MaxMissingPaths caps the number of missing-promotion paths retained per
	// finding.
	MaxMissingPaths int
}

// Report is the result of Analyze.
type Report struct {
	Status   Status
	Findings []Finding
	Unknowns []Unknown
	Errors   []string
	Stats    Stats
}

func (r *Report) addError(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	for _, old := range r.Errors {
		if old == msg {
			return
		}
	}
	r.Errors = append(r.Errors, msg)
}

func (r *Report) addUnknown(at OpID, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	for _, old := range r.Unknowns {
		if old.At == at && old.Reason == msg {
			return
		}
	}
	r.Unknowns = append(r.Unknowns, Unknown{At: at, Reason: msg})
}

// Analyze runs a finite, path-sensitive abstract interpretation over the SSA
// graph. Merges union value origins and promotion alternatives; a state is
// never silently widened to "safe".
func Analyze(p *Program, opts ...Options) (*Report, error) {
	if p == nil {
		return nil, fmt.Errorf("netsec: nil program")
	}
	opt := Options{}
	if len(opts) > 0 {
		opt = opts[len(opts)-1]
	}
	budget := opt.Budget
	if budget == 0 {
		budget = 200000
	}
	pathBudget := opt.PathBudget
	if pathBudget == 0 {
		pathBudget = 100000
	}
	maxMissing := opt.MaxMissingPaths
	if maxMissing == 0 {
		maxMissing = 4
	}
	if maxMissing < 1 {
		maxMissing = 1
	}

	report := &Report{Status: StatusSafe, Stats: Stats{Budget: budget}}
	p.rebuild()
	a := newAnalyzer(p, report, pathBudget, maxMissing)
	a.validate()
	if len(report.Errors) > 0 {
		report.Status = StatusUnknown
		return report, nil
	}
	a.run(budget)
	a.finish()

	if report.Stats.Exhausted {
		report.Status = StatusUnknown
	} else if len(report.Findings) > 0 {
		report.Status = StatusVulnerable
	} else if len(report.Unknowns) > 0 {
		report.Status = StatusUnknown
	} else {
		report.Status = StatusSafe
	}
	sortReport(report)
	return report, nil
}

type promotionState struct {
	cells      map[CellID]bool
	unpromoted bool
}

type valueState struct {
	kinds        map[ValueKind]bool
	locations    map[Location]bool
	cells        map[CellID]bool
	closures     map[ClosureID]bool
	scalars      map[string]Scalar
	origins      map[OpID]bool
	deadCaptures map[int]bool
	afterReturn  bool
}

func newValueState() valueState {
	return valueState{
		kinds:        make(map[ValueKind]bool),
		locations:    make(map[Location]bool),
		cells:        make(map[CellID]bool),
		closures:     make(map[ClosureID]bool),
		scalars:      make(map[string]Scalar),
		origins:      make(map[OpID]bool),
		deadCaptures: make(map[int]bool),
	}
}

func (v valueState) clone() valueState {
	out := newValueState()
	for k := range v.kinds {
		out.kinds[k] = true
	}
	for x := range v.locations {
		out.locations[x] = true
	}
	for x := range v.cells {
		out.cells[x] = true
	}
	for x := range v.closures {
		out.closures[x] = true
	}
	for x, s := range v.scalars {
		out.scalars[x] = s
	}
	for x := range v.origins {
		out.origins[x] = true
	}
	for i := range v.deadCaptures {
		out.deadCaptures[i] = true
	}
	out.afterReturn = v.afterReturn
	return out
}

func (v *valueState) addKind(k ValueKind) {
	if v.kinds == nil {
		*v = newValueState()
	}
	v.kinds[k] = true
}

func (v *valueState) addOrigin(id OpID) {
	if id != "" {
		v.origins[id] = true
	}
}

func (v valueState) oneLocation() (Location, bool) {
	if len(v.locations) != 1 {
		return Location{}, false
	}
	for l := range v.locations {
		return l, true
	}
	return Location{}, false
}

func (v valueState) locationsSorted() []Location {
	out := make([]Location, 0, len(v.locations))
	for l := range v.locations {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func mergeValue(a, b valueState) valueState {
	out := a.clone()
	for k := range b.kinds {
		out.kinds[k] = true
	}
	for l := range b.locations {
		out.locations[l] = true
	}
	for c := range b.cells {
		out.cells[c] = true
	}
	for c := range b.closures {
		out.closures[c] = true
	}
	for k, s := range b.scalars {
		out.scalars[k] = s
	}
	for o := range b.origins {
		out.origins[o] = true
	}
	for i := range b.deadCaptures {
		out.deadCaptures[i] = true
	}
	out.afterReturn = out.afterReturn || b.afterReturn
	return out
}

type frameInstance struct {
	Frame    FrameID
	CallSite string
}

func (f frameInstance) String() string {
	if f.CallSite == "" {
		return string(f.Frame)
	}
	return string(f.Frame) + "@" + f.CallSite
}

type absState struct {
	values   map[ValueID]valueState
	promoted map[Location]promotionState
	returned map[frameInstance]bool
	active   map[FrameID]map[string]bool
	heap     map[CellID]valueState
}

func newState() *absState {
	return &absState{
		values:   make(map[ValueID]valueState),
		promoted: make(map[Location]promotionState),
		returned: make(map[frameInstance]bool),
		active:   make(map[FrameID]map[string]bool),
		heap:     make(map[CellID]valueState),
	}
}

func (s *absState) clone() *absState {
	out := newState()
	for id, v := range s.values {
		out.values[id] = v.clone()
	}
	for loc, p := range s.promoted {
		np := promotionState{cells: make(map[CellID]bool), unpromoted: p.unpromoted}
		for c := range p.cells {
			np.cells[c] = true
		}
		out.promoted[loc] = np
	}
	for f := range s.returned {
		out.returned[f] = true
	}
	for frame, sites := range s.active {
		out.active[frame] = make(map[string]bool, len(sites))
		for site := range sites {
			out.active[frame][site] = true
		}
	}
	for c, v := range s.heap {
		out.heap[c] = v.clone()
	}
	return out
}

func mergeState(a, b *absState) *absState {
	if a == nil {
		return b.clone()
	}
	if b == nil {
		return a.clone()
	}
	out := a.clone()
	for id, bv := range b.values {
		if av, ok := out.values[id]; ok {
			out.values[id] = mergeValue(av, bv)
		} else {
			out.values[id] = bv.clone()
		}
	}
	locSet := make(map[Location]bool)
	for loc := range a.promoted {
		locSet[loc] = true
	}
	for loc := range b.promoted {
		locSet[loc] = true
	}
	for loc := range locSet {
		ap, oka := a.promoted[loc]
		bp, okb := b.promoted[loc]
		np := promotionState{cells: make(map[CellID]bool)}
		if oka {
			for c := range ap.cells {
				np.cells[c] = true
			}
			np.unpromoted = np.unpromoted || ap.unpromoted
		}
		if okb {
			for c := range bp.cells {
				np.cells[c] = true
			}
			np.unpromoted = np.unpromoted || bp.unpromoted
		}
		// An entry present on only one predecessor means the other path
		// reaches the merge unpromoted.
		if !oka || !okb {
			np.unpromoted = true
		}
		out.promoted[loc] = np
	}
	for f := range b.returned {
		out.returned[f] = true
	}
	for frame, sites := range b.active {
		if out.active[frame] == nil {
			out.active[frame] = make(map[string]bool)
		}
		for site := range sites {
			out.active[frame][site] = true
		}
	}
	for c, bv := range b.heap {
		if av, ok := out.heap[c]; ok {
			out.heap[c] = mergeValue(av, bv)
		} else {
			out.heap[c] = bv.clone()
		}
	}
	return out
}

func (s *absState) promotion(loc Location) promotionState {
	if p, ok := s.promoted[loc]; ok {
		return p
	}
	return promotionState{cells: make(map[CellID]bool), unpromoted: true}
}

func (s *absState) addActive(frame FrameID, callsite string) {
	if frame == "" {
		return
	}
	if s.active[frame] == nil {
		s.active[frame] = make(map[string]bool)
	}
	s.active[frame][callsite] = true
}

func (s *absState) removeActive(frame FrameID, callsite string) {
	sites := s.active[frame]
	if sites == nil {
		return
	}
	delete(sites, callsite)
	if len(sites) == 0 {
		delete(s.active, frame)
	}
}

func (s *absState) uniqueActiveCallsite(frame FrameID) (string, bool) {
	sites := s.active[frame]
	if len(sites) != 1 {
		return "", false
	}
	for site := range sites {
		return site, true
	}
	return "", false
}

func (s *absState) markPromoted(loc Location, cell CellID) {
	p := promotionState{cells: map[CellID]bool{cell: true}}
	s.promoted[loc] = p
}

func (s *absState) rewriteLocation(loc Location, cell CellID) {
	for id, v := range s.values {
		if !v.locations[loc] {
			continue
		}
		delete(v.locations, loc)
		if len(v.locations) == 0 {
			delete(v.kinds, FrameRefKind)
		}
		v.addKind(HeapCellKind)
		if v.cells == nil {
			v.cells = make(map[CellID]bool)
		}
		v.cells[cell] = true
		s.values[id] = v
	}
}

func (s *absState) rewriteCellAliases(old map[CellID]bool, cell CellID) {
	if len(old) == 0 {
		return
	}
	for id, v := range s.values {
		changed := false
		for oc := range old {
			if v.cells[oc] {
				delete(v.cells, oc)
				changed = true
			}
		}
		if changed {
			v.addKind(HeapCellKind)
			v.cells[cell] = true
			s.values[id] = v
		}
	}
}

func (s *absState) poisonCellAliases(cell CellID) {
	for id, v := range s.values {
		if v.cells[cell] {
			v.afterReturn = true
			s.values[id] = v
		}
	}
}

func (s *absState) key() string {
	var b strings.Builder
	ids := make([]string, 0, len(s.values))
	for id := range s.values {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	for _, id := range ids {
		v := s.values[ValueID(id)]
		b.WriteString("v:" + id + "=")
		writeBoolSet(&b, "k", v.kinds)
		locs := make([]string, 0, len(v.locations))
		for l := range v.locations {
			locs = append(locs, l.String())
		}
		sort.Strings(locs)
		b.WriteString("|l=" + strings.Join(locs, ","))
		cs := make([]string, 0, len(v.cells))
		for c := range v.cells {
			cs = append(cs, string(c))
		}
		sort.Strings(cs)
		b.WriteString("|c=" + strings.Join(cs, ","))
		cls := make([]string, 0, len(v.closures))
		for c := range v.closures {
			cls = append(cls, string(c))
		}
		sort.Strings(cls)
		b.WriteString("|cl=" + strings.Join(cls, ","))
		or := make([]string, 0, len(v.origins))
		for o := range v.origins {
			or = append(or, string(o))
		}
		sort.Strings(or)
		b.WriteString("|o=" + strings.Join(or, ","))
		d := make([]int, 0, len(v.deadCaptures))
		for i := range v.deadCaptures {
			d = append(d, i)
		}
		sort.Ints(d)
		b.WriteString("|dc=")
		for i, n := range d {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, "%d", n)
		}
		if v.afterReturn {
			b.WriteString("|dead")
		}
		b.WriteByte('\n')
	}
	locs := make([]string, 0, len(s.promoted))
	for l := range s.promoted {
		locs = append(locs, l.String())
	}
	sort.Strings(locs)
	for _, ls := range locs {
		var loc Location
		for l := range s.promoted {
			if l.String() == ls {
				loc = l
				break
			}
		}
		p := s.promoted[loc]
		cs := make([]string, 0, len(p.cells))
		for c := range p.cells {
			cs = append(cs, string(c))
		}
		sort.Strings(cs)
		fmt.Fprintf(&b, "p:%s=%s unp=%t\n", ls, strings.Join(cs, ","), p.unpromoted)
	}
	fs := make([]string, 0, len(s.returned))
	for f := range s.returned {
		fs = append(fs, f.String())
	}
	sort.Strings(fs)
	b.WriteString("r=" + strings.Join(fs, ","))
	frames := make([]string, 0, len(s.active))
	for frame := range s.active {
		frames = append(frames, string(frame))
	}
	sort.Strings(frames)
	b.WriteString("|a=")
	for i, frame := range frames {
		if i > 0 {
			b.WriteByte(';')
		}
		sites := make([]string, 0, len(s.active[FrameID(frame)]))
		for site := range s.active[FrameID(frame)] {
			sites = append(sites, site)
		}
		sort.Strings(sites)
		b.WriteString(frame + ":" + strings.Join(sites, ","))
	}
	return b.String()
}

func writeBoolSet[T ~uint8](b *strings.Builder, prefix string, set map[T]bool) {
	vals := make([]int, 0, len(set))
	for v := range set {
		vals = append(vals, int(v))
	}
	sort.Ints(vals)
	b.WriteString("|" + prefix + "=")
	for i, v := range vals {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(b, "%d", v)
	}
}

type analyzer struct {
	p             *Program
	report        *Report
	in            map[OpID]*absState
	promotedAt    map[OpID]map[Location]bool
	pathBudget    int
	maxMissing    int
	succs         map[OpID][]OpID
	allocOps      map[FrameID][]*Op
	opPromotions  map[OpID][]Location
	stateCount    int
	transitionCnt int
}

func newAnalyzer(p *Program, report *Report, pathBudget, maxMissing int) *analyzer {
	a := &analyzer{
		p:            p,
		report:       report,
		in:           make(map[OpID]*absState),
		promotedAt:   make(map[OpID]map[Location]bool),
		pathBudget:   pathBudget,
		maxMissing:   maxMissing,
		succs:        make(map[OpID][]OpID),
		allocOps:     make(map[FrameID][]*Op),
		opPromotions: make(map[OpID][]Location),
	}
	for _, o := range p.Ops {
		if o == nil {
			continue
		}
		a.succs[o.ID] = append([]OpID(nil), o.Succs...)
		if o.Kind == OpFrameAlloc {
			a.allocOps[o.Frame] = append(a.allocOps[o.Frame], o)
		}
	}
	return a
}

func (a *analyzer) validate() {
	p := a.p
	if p.Entry == "" && len(p.Ops) > 0 {
		p.Entry = p.Ops[0].ID
	}
	seen := make(map[OpID]bool)
	values := make(map[ValueID]bool)
	closures := make(map[ClosureID]bool)
	for _, o := range p.Ops {
		if o == nil {
			a.report.addError("nil op")
			continue
		}
		if o.ID == "" {
			a.report.addError("op with empty ID")
			continue
		}
		if seen[o.ID] {
			a.report.addError("duplicate op ID %q", o.ID)
		}
		seen[o.ID] = true
		if o.Result != "" {
			if values[o.Result] {
				a.report.addError("duplicate value ID %q", o.Result)
			}
			values[o.Result] = true
		}
		if o.Kind == OpMakeClosure {
			id := o.ClosureID
			if id == "" {
				id = ClosureID(o.Result)
			}
			if id == "" {
				a.report.addError("MakeClosure %q has no closure ID", o.ID)
			} else if closures[id] {
				a.report.addError("duplicate closure ID %q", id)
			}
			closures[id] = true
		}
		for _, s := range o.Succs {
			if _, ok := p.opIndex[s]; !ok {
				a.report.addError("%s: successor %q does not exist", o.ID, s)
			}
		}
	}
	if p.Entry == "" {
		a.report.addError("program has no entry")
	} else if !seen[p.Entry] {
		a.report.addError("entry %q does not exist", p.Entry)
	}
	for _, o := range p.Ops {
		if o == nil || o.Kind != OpHeapPromote || o.Cell == "" {
			continue
		}
		loc, ok := a.staticRefLocation(o.Inputs)
		if !ok {
			continue
		}
		for _, other := range p.Ops {
			if other == nil || other.Kind != OpHeapPromote || other == o || other.Cell != o.Cell {
				continue
			}
			oloc, ok := a.staticRefLocation(other.Inputs)
			if ok && oloc != loc {
				a.report.addError("HeapCell %q is shared by distinct FrameRefs %s and %s", o.Cell, loc, oloc)
			}
		}
	}
}

func (a *analyzer) staticRefLocation(inputs []ValueID) (Location, bool) {
	if len(inputs) == 0 {
		return Location{}, false
	}
	seen := make(map[ValueID]bool)
	var walk func(ValueID) (Location, bool)
	walk = func(id ValueID) (Location, bool) {
		if seen[id] {
			return Location{}, false
		}
		seen[id] = true
		o := a.p.ValueOp(id)
		if o == nil {
			return Location{}, false
		}
		if o.Frame != "" {
			return Location{Frame: o.Frame, CallSite: o.CallSite, Slot: o.Slot}, true
		}
		switch o.Kind {
		case OpCaptureByRef, OpCaptureByValue, OpHeapPromote, OpPhi:
			if len(o.Inputs) == 0 {
				return Location{}, false
			}
			if loc, ok := walk(o.Inputs[0]); ok {
				return loc, true
			}
		}
		if v, ok := o.Value.(FrameRef); ok {
			return v.Location(), true
		}
		return Location{}, false
	}
	return walk(inputs[0])
}

func (a *analyzer) run(budget int) {
	if a.report.Status == StatusUnknown {
		return
	}
	queue := []OpID{a.p.Entry}
	a.in[a.p.Entry] = newState()
	a.stateCount = 1
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		st := a.in[id]
		if st == nil {
			continue
		}
		op := a.p.Op(id)
		if op == nil {
			continue
		}
		if budget >= 0 && a.transitionCnt >= budget {
			a.report.Stats.Exhausted = true
			a.report.addUnknown(id, "analysis budget of %d transitions exhausted", budget)
			break
		}
		a.transitionCnt++
		out := a.transfer(op, st)
		if out == nil {
			continue
		}
		for _, succID := range op.Succs {
			old := a.in[succID]
			merged := mergeState(old, out)
			if old != nil && old.key() == merged.key() {
				continue
			}
			a.in[succID] = merged
			a.stateCount++
			if budget >= 0 && a.transitionCnt >= budget {
				a.report.Stats.Exhausted = true
				a.report.addUnknown(succID, "analysis budget of %d transitions exhausted", budget)
				break
			}
			queue = append(queue, succID)
		}
		if a.report.Stats.Exhausted {
			break
		}
	}
	a.report.Stats.States = a.stateCount
	a.report.Stats.Transitions = a.transitionCnt
}

func (a *analyzer) transfer(op *Op, in *absState) *absState {
	out := in.clone()
	switch op.Kind {
	case OpFrameAlloc:
		inst := frameInstance{Frame: op.Frame, CallSite: a.instanceCallSite(op)}
		delete(out.returned, inst)
		out.addActive(op.Frame, inst.CallSite)
		for slot := 0; slot < op.Slots; slot++ {
			delete(out.promoted, Location{Frame: op.Frame, CallSite: inst.CallSite, Slot: Slot(slot)})
		}
		return out
	case OpCaptureByRef:
		return a.captureByRef(op, out)
	case OpCaptureByValue:
		return a.captureByValue(op, out)
	case OpMakeClosure:
		return a.makeClosure(op, out)
	case OpHeapPromote:
		return a.promote(op, out)
	case OpPhi:
		return a.phi(op, out)
	case OpScalar:
		res := newValueState()
		res.addKind(ScalarKind)
		res.addOrigin(op.ID)
		s, _ := op.Value.(Scalar)
		res.scalars[scalarKey(s)] = s
		if op.Result != "" {
			out.values[op.Result] = res
		}
		return out
	case OpStoreGlobal, OpPassAsync, OpCallNow, OpReturn, OpStoreHeap:
		if op.Kind == OpReturn && op.Frame != "" {
			inst := frameInstance{Frame: op.Frame, CallSite: a.resolveCallSite(out, op.Frame, op.CallSite)}
			out.returned[inst] = true
			out.removeActive(op.Frame, inst.CallSite)
		}
		if op.Kind == OpStoreHeap {
			if len(op.Inputs) >= 2 {
				if cell, ok := a.heapCellOf(out, op.Inputs[0]); ok {
					if v, ok := out.values[op.Inputs[1]]; ok {
						out.heap[cell] = mergeValue(out.heap[cell], v)
					}
				}
			}
		}
		return out
	case OpBranch:
		return out
	default:
		a.report.addUnknown(op.ID, "unsupported op kind %q", op.Kind)
		return out
	}
}

func (a *analyzer) instanceCallSite(op *Op) string {
	if op.CallSite != "" {
		return op.CallSite
	}
	if f := a.p.Frame(op.Frame); f != nil {
		return f.CallSite
	}
	return ""
}

func (a *analyzer) captureByRef(op *Op, out *absState) *absState {
	var locs []Location
	if len(op.Inputs) > 0 {
		if v, ok := out.values[op.Inputs[0]]; ok {
			locs = append(locs, v.locationsSorted()...)
		}
	}
	if len(locs) == 0 && op.Frame != "" {
		locs = append(locs, Location{Frame: op.Frame, CallSite: a.instanceCallSite(op), Slot: op.Slot})
	}
	if len(locs) == 0 {
		a.report.addUnknown(op.ID, "CaptureByRef has no resolvable frame slot")
		return out
	}
	res := newValueState()
	res.addOrigin(op.ID)
	if op.Label != "" {
		res.origins[OpID(op.Label)] = true
	}
	for _, loc := range locs {
		p := out.promotion(loc)
		if !p.unpromoted && len(p.cells) > 0 {
			res.addKind(HeapCellKind)
			for c := range p.cells {
				res.cells[c] = true
			}
			continue
		}
		res.addKind(FrameRefKind)
		res.locations[loc] = true
		if out.returned[frameInstance{Frame: loc.Frame, CallSite: loc.CallSite}] {
			res.afterReturn = true
		}
	}
	if op.Result != "" {
		out.values[op.Result] = res
	}
	return out
}

func (a *analyzer) captureByValue(op *Op, out *absState) *absState {
	res := newValueState()
	res.addOrigin(op.ID)
	if len(op.Inputs) == 0 {
		if op.Frame == "" {
			a.report.addUnknown(op.ID, "CaptureByValue has no input")
			return out
		}
		res.addKind(ScalarKind)
		if op.Result != "" {
			out.values[op.Result] = res
		}
		return out
	}
	src, ok := out.values[op.Inputs[0]]
	if !ok {
		a.report.addUnknown(op.ID, "CaptureByValue input %q is not available", op.Inputs[0])
		return out
	}
	// A scalar source is copied directly. A FrameRef/HeapCell source means
	// "load the scalar value held there and copy it"; both are safe because the
	// resulting closure owns a value copy.
	if src.kinds[ClosureKind] {
		a.report.addUnknown(op.ID, "CaptureByValue input %q is a closure, want scalar or slot", op.Inputs[0])
		if op.Result != "" {
			out.values[op.Result] = res
		}
		return out
	}
	res.addKind(ScalarKind)
	for k, s := range src.scalars {
		res.scalars[k] = s
	}
	if op.Result != "" {
		out.values[op.Result] = res
	}
	return out
}

func (a *analyzer) makeClosure(op *Op, out *absState) *absState {
	caps := op.Captures
	if len(caps) == 0 {
		if c, ok := op.Value.(Closure); ok {
			caps = c.Captures
		} else if c, ok := op.Value.(*Closure); ok {
			caps = c.Captures
		}
	}
	res := newValueState()
	res.addKind(ClosureKind)
	id := op.ClosureID
	if id == "" {
		id = ClosureID(op.Result)
	}
	if id == "" {
		a.report.addUnknown(op.ID, "MakeClosure has no closure ID")
		return out
	}
	res.closures[id] = true
	res.addOrigin(op.ID)
	for capIndex, c := range caps {
		if c.Kind == CaptureByValue {
			src, ok := out.values[c.Source]
			if !ok {
				if c.Source != "" {
					a.report.addUnknown(op.ID, "capture source %q is not available", c.Source)
				}
				continue
			}
			if !src.kinds[ScalarKind] {
				a.report.addUnknown(op.ID, "by-value capture %q is %s, want scalar", c.Source, kindsString(src.kinds))
			}
			continue
		}
		// By-reference capture.
		var locs []Location
		hasFrameRef := false
		captureDead := false
		if c.Source != "" {
			src, ok := out.values[c.Source]
			if !ok {
				a.report.addUnknown(op.ID, "capture source %q is not available", c.Source)
			} else {
				locs = append(locs, src.locationsSorted()...)
				hasFrameRef = src.kinds[FrameRefKind]
				if src.afterReturn && (src.kinds[FrameRefKind] || src.kinds[HeapCellKind]) {
					res.afterReturn = true
					captureDead = true
				}
				if src.kinds[ClosureKind] {
					// A nested closure is inspected recursively when the outer
					// closure reaches an escape sink.
					continue
				}
			}
		}
		if len(locs) == 0 && c.Frame != "" {
			locs = append(locs, Location{Frame: c.Frame, CallSite: c.CallSite, Slot: c.Slot})
			hasFrameRef = true
		}
		for _, loc := range locs {
			if hasFrameRef && out.returned[frameInstance{Frame: loc.Frame, CallSite: loc.CallSite}] {
				res.afterReturn = true
				captureDead = true
			}
		}
		if captureDead {
			res.deadCaptures[capIndex] = true
		}
	}
	if op.Result != "" {
		out.values[op.Result] = res
	}
	return out
}

func (a *analyzer) promote(op *Op, out *absState) *absState {
	if len(op.Inputs) == 0 {
		a.report.addUnknown(op.ID, "HeapPromote has no input")
		return out
	}
	src, ok := out.values[op.Inputs[0]]
	if !ok {
		a.report.addUnknown(op.ID, "HeapPromote input %q is not available", op.Inputs[0])
		return out
	}
	res := newValueState()
	res.addKind(HeapCellKind)
	res.addOrigin(op.ID)
	locs := src.locationsSorted()
	if len(locs) == 0 {
		for c := range src.cells {
			res.cells[c] = true
		}
		if op.Result != "" {
			out.values[op.Result] = res
		}
		return out
	}
	for i, loc := range locs {
		cell := op.Cell
		if cell == "" {
			cell = CellID("cell:" + string(op.ID))
		}
		if len(locs) > 1 {
			cell = CellID(fmt.Sprintf("%s:%d", cell, i))
		}
		res.cells[cell] = true
		late := out.returned[frameInstance{Frame: loc.Frame, CallSite: loc.CallSite}]
		oldCells := out.promotion(loc).cells
		out.markPromoted(loc, cell)
		out.rewriteLocation(loc, cell)
		out.rewriteCellAliases(oldCells, cell)
		if late {
			res.afterReturn = true
			out.poisonCellAliases(cell)
		}
		if a.promotedAt[op.ID] == nil {
			a.promotedAt[op.ID] = make(map[Location]bool)
		}
		a.promotedAt[op.ID][loc] = true
		found := false
		for _, known := range a.opPromotions[op.ID] {
			if known == loc {
				found = true
				break
			}
		}
		if !found {
			a.opPromotions[op.ID] = append(a.opPromotions[op.ID], loc)
		}
	}
	if op.Result != "" {
		out.values[op.Result] = res
	}
	return out
}

func (a *analyzer) phi(op *Op, out *absState) *absState {
	res := newValueState()
	for _, id := range op.Inputs {
		v, ok := out.values[id]
		if !ok {
			a.report.addUnknown(op.ID, "Phi input %q is not available", id)
			continue
		}
		res = mergeValue(res, v)
	}
	res.addOrigin(op.ID)
	if op.Result != "" {
		out.values[op.Result] = res
	}
	return out
}

func (a *analyzer) heapCellOf(s *absState, id ValueID) (CellID, bool) {
	v, ok := s.values[id]
	if !ok || len(v.cells) != 1 {
		return "", false
	}
	for c := range v.cells {
		return c, true
	}
	return "", false
}

func (a *analyzer) finish() {
	seen := make(map[string]bool)
	for _, op := range a.p.sortedOps() {
		if op == nil {
			continue
		}
		st := a.in[op.ID]
		if st == nil {
			continue
		}
		var sink EscapeKind
		var closureValues []ValueID
		switch op.Kind {
		case OpStoreGlobal:
			sink = EscapeGlobal
			if len(op.Inputs) > 0 {
				closureValues = append(closureValues, op.Inputs[0])
			}
		case OpPassAsync:
			sink = EscapeAsync
			if len(op.Inputs) > 0 {
				closureValues = append(closureValues, op.Inputs[0])
			}
		case OpReturn:
			sink = EscapeReturn
			if len(op.Inputs) > 0 {
				closureValues = append(closureValues, op.Inputs[0])
			}
		case OpStoreHeap:
			sink = EscapeHeap
			if len(op.Inputs) > 1 {
				closureValues = append(closureValues, op.Inputs[1])
			}
		default:
			continue
		}
		for _, vid := range closureValues {
			v, ok := st.values[vid]
			if !ok || !v.kinds[ClosureKind] {
				continue
			}
			ids := make([]string, 0, len(v.closures))
			for id := range v.closures {
				ids = append(ids, string(id))
			}
			sort.Strings(ids)
			for _, sid := range ids {
				cid := ClosureID(sid)
				for _, issue := range a.inspectClosure(cid, st, make(map[ClosureID]bool), v.deadCaptures) {
					key := fmt.Sprintf("%s|%s|%s|%s|%s|%s", op.ID, sink, cid, issue.Capture.CaptureAt, issue.Capture.Frame, issue.Capture.CallSite)
					key += fmt.Sprintf("|%d|%s", issue.Capture.Slot, issue.Capture.Reason)
					if seen[key] {
						continue
					}
					seen[key] = true
					f := Finding{
						EscapeAt: op.ID,
						Sink:     sink,
						Closure:  cid,
						Capture:  issue.Capture,
					}
					f.AllocPath = a.allocEvidencePath(issue.Capture, op.ID)
					if issue.Capture.Reason == ReturnedFrame {
						f.LeakPath = append([]OpID(nil), f.AllocPath...)
					} else {
						f.LeakPath = a.unsafeLeakPath(issue.Capture, op.ID)
					}
					f.MissingPromotionPaths = a.missingPromotionPaths(issue.Capture, op.ID)
					a.report.Findings = append(a.report.Findings, f)
				}
			}
		}
	}
}

type closureIssue struct {
	Capture CaptureIssue
}

func (a *analyzer) inspectClosure(id ClosureID, st *absState, visiting map[ClosureID]bool, dead map[int]bool) []closureIssue {
	if visiting[id] {
		return nil
	}
	visiting[id] = true
	defer delete(visiting, id)
	op := a.p.ClosureOp(id)
	if op == nil {
		a.report.addUnknown("", "closure %q has no MakeClosure", id)
		return nil
	}
	caps := op.Captures
	if len(caps) == 0 {
		if c, ok := op.Value.(Closure); ok {
			caps = c.Captures
		} else if c, ok := op.Value.(*Closure); ok {
			caps = c.Captures
		}
	}
	var out []closureIssue
	for capIndex, c := range caps {
		if c.Kind == CaptureByValue {
			continue
		}
		var src valueState
		haveSrc := false
		if c.Source != "" {
			src, haveSrc = st.values[c.Source]
		}
		if haveSrc && src.kinds[ClosureKind] {
			nested := make([]string, 0, len(src.closures))
			for nid := range src.closures {
				nested = append(nested, string(nid))
			}
			sort.Strings(nested)
			for _, nid := range nested {
				nestedDead := make(map[int]bool)
				if haveSrc {
					for i := range src.deadCaptures {
						nestedDead[i] = true
					}
				}
				out = append(out, a.inspectClosure(ClosureID(nid), st, visiting, nestedDead)...)
			}
			if len(src.locations) == 0 {
				continue
			}
		}
		var locs []Location
		hasFrameRef := false
		if haveSrc {
			locs = append(locs, src.locationsSorted()...)
			hasFrameRef = src.kinds[FrameRefKind]
		}
		if len(locs) == 0 && c.Frame != "" {
			locs = append(locs, Location{Frame: c.Frame, CallSite: c.CallSite, Slot: c.Slot})
			hasFrameRef = true
		}
		if !haveSrc && c.Source != "" && c.Frame == "" {
			a.report.addUnknown(op.ID, "capture source %q is unavailable while inspecting %s", c.Source, id)
			continue
		}
		deadHere := dead[capIndex]
		if haveSrc && hasFrameRef && src.afterReturn {
			deadHere = true
		}
		if len(locs) == 0 && deadHere {
			if c.Frame != "" {
				locs = append(locs, Location{Frame: c.Frame, CallSite: c.CallSite, Slot: c.Slot})
			} else if loc, ok := a.staticRefLocation([]ValueID{c.Source}); ok {
				locs = append(locs, loc)
			}
		}
		if !hasFrameRef && !deadHere {
			continue
		}
		captureAt := op.ID
		if c.Source != "" {
			if srcOp := a.p.ValueOp(c.Source); srcOp != nil {
				captureAt = srcOp.ID
			}
		}
		for _, loc := range locs {
			p := st.promotion(loc)
			if !p.unpromoted && !deadHere {
				continue
			}
			reason := MissingPromotion
			if deadHere {
				reason = ReturnedFrame
			}
			originSet := make(map[OpID]bool)
			for o := range src.origins {
				originSet[o] = true
			}
			if len(originSet) == 0 && captureAt != "" {
				originSet[captureAt] = true
			}
			origins := make([]OpID, 0, len(originSet))
			for o := range originSet {
				origins = append(origins, o)
			}
			sort.Slice(origins, func(i, j int) bool { return origins[i] < origins[j] })
			out = append(out, closureIssue{Capture: CaptureIssue{
				Closure:   id,
				Frame:     loc.Frame,
				CallSite:  loc.CallSite,
				Slot:      loc.Slot,
				Mutable:   c.Mutable,
				CaptureAt: captureAt,
				Reason:    reason,
				Origins:   origins,
			}})
		}
	}
	return out
}

func (a *analyzer) allocEvidencePath(issue CaptureIssue, escape OpID) []OpID {
	best := []OpID(nil)
	for _, alloc := range a.allocOps[issue.Frame] {
		if alloc == nil {
			continue
		}
		if issue.CallSite != "" && alloc.CallSite != "" && issue.CallSite != alloc.CallSite {
			continue
		}
		if issue.CallSite != "" && alloc.CallSite == "" {
			continue
		}
		path := a.shortestPath(alloc.ID, escape, a.pathBudget)
		if len(path) == 0 {
			continue
		}
		if best == nil || len(path) < len(best) || (len(path) == len(best) && pathLess(path, best)) {
			best = path
		}
	}
	if best == nil {
		// Some graphs omit FrameAlloc as a graph node. Keep a usable shortest
		// capture evidence rather than inventing an allocation.
		if issue.CaptureAt != "" {
			best = a.shortestPath(issue.CaptureAt, escape, a.pathBudget)
		}
	}
	return best
}

func (a *analyzer) unsafeLeakPath(issue CaptureIssue, escape OpID) []OpID {
	loc := Location{Frame: issue.Frame, CallSite: issue.CallSite, Slot: issue.Slot}
	best := []OpID(nil)
	for _, alloc := range a.allocOps[issue.Frame] {
		if alloc == nil {
			continue
		}
		if issue.CallSite != "" && alloc.CallSite != "" && issue.CallSite != alloc.CallSite {
			continue
		}
		path := a.shortestUnpromotedPath(alloc.ID, issue.CaptureAt, escape, loc, a.pathBudget)
		if len(path) == 0 {
			continue
		}
		if best == nil || len(path) < len(best) || (len(path) == len(best) && pathLess(path, best)) {
			best = path
		}
	}
	if best == nil && issue.CaptureAt != "" {
		best = a.shortestPath(issue.CaptureAt, escape, a.pathBudget)
	}
	return best
}

func (a *analyzer) missingPromotionPaths(issue CaptureIssue, escape OpID) [][]OpID {
	loc := Location{Frame: issue.Frame, CallSite: issue.CallSite, Slot: issue.Slot}
	if issue.CaptureAt == "" {
		return nil
	}
	paths := a.unpromotedPaths(issue.CaptureAt, escape, loc, a.pathBudget, a.maxMissing)
	return paths
}

func (a *analyzer) promotesLocation(op *Op, loc Location) bool {
	if op == nil || op.Kind != OpHeapPromote {
		return false
	}
	if a.promotedAt[op.ID] != nil && a.promotedAt[op.ID][loc] {
		return true
	}
	for _, l := range a.opPromotions[op.ID] {
		if l == loc {
			return true
		}
	}
	if len(a.opPromotions[op.ID]) > 0 {
		return false
	}
	if op.Frame == loc.Frame {
		if op.CallSite == "" || op.CallSite == loc.CallSite {
			if op.Frame != "" && op.Slot == loc.Slot {
				return true
			}
		}
	}
	if static, ok := a.staticRefLocation(op.Inputs); ok {
		return static == loc
	}
	return false
}

func (a *analyzer) shortestPath(from, to OpID, budget int) []OpID {
	if from == "" || to == "" {
		return nil
	}
	type node struct {
		id   OpID
		path []OpID
	}
	q := []node{{id: from, path: []OpID{from}}}
	seen := map[OpID]bool{from: true}
	count := 0
	for len(q) > 0 {
		n := q[0]
		q = q[1:]
		if n.id == to {
			return n.path
		}
		count++
		if budget >= 0 && count >= budget {
			return nil
		}
		for _, s := range a.succs[n.id] {
			if seen[s] {
				continue
			}
			seen[s] = true
			np := append(append([]OpID(nil), n.path...), s)
			q = append(q, node{id: s, path: np})
		}
	}
	return nil
}

type leakNode struct {
	op          OpID
	promoted    bool
	seenCapture bool
	path        []OpID
}

func (a *analyzer) shortestUnpromotedPath(from, captureAt, to OpID, loc Location, budget int) []OpID {
	if from == "" || to == "" {
		return nil
	}
	start := leakNode{op: from, path: []OpID{from}, seenCapture: from == captureAt}
	if a.promotesLocation(a.p.Op(from), loc) {
		start.promoted = true
	}
	q := []leakNode{start}
	seen := map[string]bool{}
	count := 0
	for len(q) > 0 {
		n := q[0]
		q = q[1:]
		key := fmt.Sprintf("%s|%t|%t", n.op, n.promoted, n.seenCapture)
		if seen[key] {
			continue
		}
		seen[key] = true
		if n.op == to && n.seenCapture && !n.promoted {
			return n.path
		}
		count++
		if budget >= 0 && count >= budget {
			return nil
		}
		for _, sid := range a.succs[n.op] {
			next := leakNode{
				op:          sid,
				promoted:    n.promoted,
				seenCapture: n.seenCapture || sid == captureAt,
				path:        append(append([]OpID(nil), n.path...), sid),
			}
			if a.promotesLocation(a.p.Op(sid), loc) {
				next.promoted = true
			}
			q = append(q, next)
		}
	}
	return nil
}

func (a *analyzer) unpromotedPaths(from, to OpID, loc Location, budget, limit int) [][]OpID {
	if from == "" || to == "" || limit <= 0 {
		return nil
	}
	q := []leakNode{{op: from, path: []OpID{from}, seenCapture: true}}
	if a.promotesLocation(a.p.Op(from), loc) {
		q[0].promoted = true
	}
	seen := map[OpID]bool{}
	var out [][]OpID
	count := 0
	for len(q) > 0 && len(out) < limit {
		n := q[0]
		q = q[1:]
		if n.promoted {
			continue
		}
		key := n.op
		if seen[key] {
			continue
		}
		seen[key] = true
		if n.op == to {
			out = append(out, n.path)
			continue
		}
		count++
		if budget >= 0 && count >= budget {
			break
		}
		for _, sid := range a.succs[n.op] {
			next := leakNode{
				op:       sid,
				promoted: n.promoted,
				path:     append(append([]OpID(nil), n.path...), sid),
			}
			if a.promotesLocation(a.p.Op(sid), loc) {
				next.promoted = true
			}
			q = append(q, next)
		}
	}
	return out
}

func kindsString(kinds map[ValueKind]bool) string {
	if len(kinds) == 0 {
		return "unknown"
	}
	out := make([]string, 0, len(kinds))
	for k := range kinds {
		out = append(out, k.String())
	}
	sort.Strings(out)
	return strings.Join(out, "|")
}

func scalarKey(s Scalar) string {
	return s.Tenant + "\x00" + s.Data + "\x00" + string(s.Bytes)
}

func pathLess(a, b []OpID) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

func sortReport(r *Report) {
	sort.SliceStable(r.Findings, func(i, j int) bool {
		a, b := r.Findings[i], r.Findings[j]
		if a.EscapeAt != b.EscapeAt {
			return a.EscapeAt < b.EscapeAt
		}
		if a.Closure != b.Closure {
			return a.Closure < b.Closure
		}
		if a.Capture.CaptureAt != b.Capture.CaptureAt {
			return a.Capture.CaptureAt < b.Capture.CaptureAt
		}
		if a.Capture.Frame != b.Capture.Frame {
			return a.Capture.Frame < b.Capture.Frame
		}
		if a.Capture.Slot != b.Capture.Slot {
			return a.Capture.Slot < b.Capture.Slot
		}
		return a.Capture.Reason < b.Capture.Reason
	})
	sort.SliceStable(r.Unknowns, func(i, j int) bool {
		if r.Unknowns[i].At != r.Unknowns[j].At {
			return r.Unknowns[i].At < r.Unknowns[j].At
		}
		return r.Unknowns[i].Reason < r.Unknowns[j].Reason
	})
	sort.Strings(r.Errors)
}

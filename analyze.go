package netsec

import (
	"fmt"
	"math"
	"sort"
)

// Analyze classifies every managed local object in g.  It intentionally
// accepts both Graph and *Graph, so compiler hosts can pass either representation.
func Analyze(g any, opts ...Option) *Report {
	graph, ok := coerceGraph(g)
	if !ok {
		return &Report{
			Objects:         map[ObjectID]Decision{},
			Decisions:       map[ObjectID]Decision{},
			Classes:         map[ObjectID]StackClass{},
			BudgetExhausted: true,
			Warnings:        []string{"invalid or unsupported graph value"},
		}
	}
	return analyzeGraph(graph, normalizeOptions(opts))
}

// Classify is an alias of Analyze.
func Classify(g any, opts ...Option) *Report { return Analyze(g, opts...) }

func coerceGraph(g any) (Graph, bool) {
	switch x := g.(type) {
	case Graph:
		return x, true
	case *Graph:
		if x == nil {
			return Graph{}, false
		}
		return *x, true
	default:
		return Graph{}, false
	}
}

type pointerRegion struct {
	object    ObjectID
	min       int64
	max       int64
	whole     bool
	unbounded bool
}

func (r pointerRegion) key() string {
	return fmt.Sprintf("%s|%d|%d|%t|%t", r.object, r.min, r.max, r.whole, r.unbounded)
}

func (r pointerRegion) aliasesWholeObject() bool { return r.whole || r.unbounded }

type pointerInfo struct {
	origins []pointerRegion
	unknown bool
	escaped bool
	tainted bool
}

func (p pointerInfo) clone() pointerInfo {
	p.origins = append([]pointerRegion(nil), p.origins...)
	return p
}

func (p *pointerInfo) addOrigin(r pointerRegion) bool {
	for _, old := range p.origins {
		if old == r {
			return false
		}
	}
	p.origins = append(p.origins, r)
	return true
}

func (p *pointerInfo) merge(q pointerInfo) bool {
	changed := false
	for _, r := range q.origins {
		if p.addOrigin(r) {
			changed = true
		}
	}
	if q.unknown && !p.unknown {
		p.unknown = true
		changed = true
	}
	if q.escaped && !p.escaped {
		p.escaped = true
		changed = true
	}
	if q.tainted && !p.tainted {
		p.tainted = true
		changed = true
	}
	return changed
}

func (p pointerInfo) empty() bool {
	return len(p.origins) == 0 && !p.unknown && !p.escaped && !p.tainted
}

type valueRecord struct {
	value     Value
	kind      OpKind
	function  FunctionID
	synthetic bool
}

type accessSpan struct {
	function  FunctionID
	object    ObjectID
	min       int64
	max       int64
	width     uint64
	whole     bool
	unbounded bool
}

func (s accessSpan) alias(other accessSpan) bool {
	if s.object != other.object || s.function != other.function {
		return false
	}
	if s.whole || other.whole || s.unbounded || other.unbounded {
		return true
	}
	// Empty pointer ranges are still pointer-valued; an access of width w at
	// offset x covers [x, x+w).  Width is at least one for memory operations.
	return s.min < other.max && other.min < s.max
}

type memoryEntry struct {
	span   accessSpan
	stored pointerInfo
	source ValueID
}

type clobberEntry struct {
	span   accessSpan
	source ValueID
}

type reasonSet map[string]Reason

func (s reasonSet) add(r Reason) bool {
	key := fmt.Sprintf("%s|%s|%s|%s|%s", r.Code, r.Object, r.Value, r.Source, r.Detail)
	if _, ok := s[key]; ok {
		return false
	}
	s[key] = r
	return true
}

func (s reasonSet) slice() []Reason {
	out := make([]Reason, 0, len(s))
	for _, r := range s {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Code != out[j].Code {
			return out[i].Code < out[j].Code
		}
		if out[i].Value != out[j].Value {
			return out[i].Value < out[j].Value
		}
		return out[i].Source < out[j].Source
	})
	return out
}

type engine struct {
	graph Graph
	opts  Options

	objects   map[ObjectID]Object
	objectSeq []ObjectID
	values    map[ValueID]valueRecord
	valueSeq  []ValueID
	functions map[FunctionID]Function
	summaries map[FunctionID]CallSummary

	ptr         map[ValueID]pointerInfo
	ints        map[ValueID]pointerInfo
	escapedObj  map[ObjectID]bool
	taintedObj  map[ObjectID]bool
	reasons     map[ObjectID]reasonSet
	accesses    map[ObjectID]map[string]Access
	conversions map[ObjectID]bool

	memory   []memoryEntry
	clobbers []clobberEntry

	steps      int
	iterations int
	budgetHit  bool
	dirty      bool

	warnings []string
}

func analyzeGraph(g Graph, opts Options) *Report {
	e := newEngine(g, opts)
	e.prepare()
	e.run()
	return e.report()
}

func newEngine(g Graph, opts Options) *engine {
	if g.PointerSize != 0 && !opts.PointerSizeSet {
		opts.PointerSize = g.PointerSize
	}
	if opts.PointerSize == 0 {
		opts.PointerSize = g.PointerSize
	}
	if opts.PointerSize == 0 {
		opts.PointerSize = 8
	}
	return &engine{
		graph:       g,
		opts:        opts,
		objects:     map[ObjectID]Object{},
		values:      map[ValueID]valueRecord{},
		functions:   map[FunctionID]Function{},
		summaries:   g.summaryMap(),
		ptr:         map[ValueID]pointerInfo{},
		ints:        map[ValueID]pointerInfo{},
		escapedObj:  map[ObjectID]bool{},
		taintedObj:  map[ObjectID]bool{},
		reasons:     map[ObjectID]reasonSet{},
		accesses:    map[ObjectID]map[string]Access{},
		conversions: map[ObjectID]bool{},
	}
}

func (e *engine) prepare() {
	for _, obj := range e.graph.Objects {
		id := obj.normalizedID()
		if id == "" {
			e.warnings = append(e.warnings, "object without an ID ignored")
			continue
		}
		if _, exists := e.objects[id]; exists {
			e.warnings = append(e.warnings, "duplicate object ID: "+string(id))
			continue
		}
		e.objects[id] = obj
		e.objectSeq = append(e.objectSeq, id)
		e.reasons[id] = reasonSet{}
		e.accesses[id] = map[string]Access{}
	}

	fnValues := map[FunctionID][]ValueID{}
	for _, fn := range e.graph.Functions {
		if fn.ID == "" {
			continue
		}
		e.functions[fn.ID] = fn
		fnValues[fn.ID] = fn.bodyValues()
	}
	valueFunction := map[ValueID]FunctionID{}
	for id, vals := range fnValues {
		for _, vid := range vals {
			if _, exists := valueFunction[vid]; !exists {
				valueFunction[vid] = id
			}
		}
	}

	for _, val := range e.graph.Values {
		id := val.normalizedID()
		if id == "" {
			e.warnings = append(e.warnings, "value without an ID ignored")
			continue
		}
		if _, exists := e.values[id]; exists {
			e.warnings = append(e.warnings, "duplicate value ID: "+string(id))
			continue
		}
		fn := val.Function
		if fn == "" {
			fn = valueFunction[id]
		}
		rec := valueRecord{value: val, kind: val.kind(), function: fn}
		e.values[id] = rec
		e.valueSeq = append(e.valueSeq, id)
	}

	// If a function body references values not present in Graph.Values, still
	// make them visible to a later, conservative pass.
	for _, fn := range e.graph.Functions {
		for _, id := range fn.paramValues() {
			if _, ok := e.values[id]; !ok {
				e.values[id] = valueRecord{
					value:     Value{ID: id, Kind: OpParam},
					kind:      OpParam,
					function:  fn.ID,
					synthetic: true,
				}
				e.valueSeq = append(e.valueSeq, id)
			}
		}
	}

	if e.opts.InferSummaries {
		e.inferSummaries()
	}
}

func (e *engine) tick(n int) bool {
	if e.budgetHit {
		return false
	}
	e.steps += n
	if e.steps > e.opts.MaxSteps {
		e.budgetHit = true
		return false
	}
	return true
}

func (e *engine) run() {
	for e.iterations < e.opts.MaxIterations && !e.budgetHit {
		e.iterations++
		e.dirty = false
		e.pass()
		if !e.dirty {
			break
		}
	}
	if !e.budgetHit && e.iterations >= e.opts.MaxIterations && e.dirty {
		e.budgetHit = true
	}
	e.closeEscape()
}

func (e *engine) pass() {
	for _, id := range e.valueSeq {
		if !e.tick(1) {
			return
		}
		rec := e.values[id]
		switch rec.kind {
		case OpAddrOf:
			e.passAddrOf(id, rec)
		case OpGEP:
			e.passGEP(id, rec)
		case OpPhi, OpSelect:
			e.passMerge(id, rec)
		case OpLoadPtr:
			e.passLoad(id, rec)
		case OpStorePtr:
			e.passStore(id, rec)
		case OpPassCall:
			e.passCall(id, rec)
		case OpPtrToInt:
			e.passPtrToInt(id, rec)
		case OpIntToPtr:
			e.passIntToPtr(id, rec)
		case OpReturnPtr:
			e.passReturn(id, rec)
		}
	}
	e.propagateMemoryEscape()
}

func (e *engine) passAddrOf(id ValueID, rec valueRecord) {
	objID := rec.value.Object
	if objID == "" {
		return
	}
	if _, ok := e.objects[objID]; !ok {
		e.mergePtr(id, pointerInfo{unknown: true})
		return
	}
	info := pointerInfo{origins: []pointerRegion{{object: objID}}}
	e.mergePtr(id, info)
}

func (e *engine) passGEP(id ValueID, rec valueRecord) {
	baseID := rec.value.pointerInput()
	base := e.ptr[baseID]
	if len(base.origins) == 0 && !base.unknown {
		return
	}
	var info pointerInfo
	info.unknown = base.unknown
	info.escaped = base.escaped
	info.tainted = base.tainted
	for _, region := range base.origins {
		nr, ok := e.deriveRegion(region, rec.value)
		if !ok {
			nr = pointerRegion{object: region.object, unbounded: true, whole: true}
			obj := e.objects[region.object]
			if size := obj.sizeBytes(); size > 0 && size <= math.MaxInt64 {
				nr.max = int64(size) - 1
			}
		}
		info.origins = append(info.origins, nr)
	}
	e.mergePtr(id, info)
}

func (e *engine) deriveRegion(base pointerRegion, v Value) (pointerRegion, bool) {
	r := base
	if r.unbounded {
		r.whole = true
		return r, true
	}
	components := make([]IndexRange, 0, 1+len(v.Indices))
	if v.Index != nil {
		components = append(components, *v.Index)
	}
	components = append(components, v.Indices...)
	if len(components) == 0 {
		var ok bool
		r.min, ok = addInt64(r.min, v.offset())
		if !ok {
			return r, false
		}
		r.max, ok = addInt64(r.max, v.offset())
		if !ok {
			return r, false
		}
		return r, true
	}
	for _, c := range components {
		if c.Unknown {
			r.whole = true
			r.unbounded = true
			if obj := e.objects[r.object]; obj.sizeBytes() > 0 && obj.sizeBytes() <= math.MaxInt64 {
				r.min = 0
				r.max = int64(obj.sizeBytes()) - 1
			}
			return r, true
		}
		if c.Max < c.Min {
			return r, false
		}
		lo, ok1 := mulInt64(c.Min, c.stride())
		hi, ok2 := mulInt64(c.Max, c.stride())
		if !ok1 || !ok2 {
			return r, false
		}
		if lo > hi {
			lo, hi = hi, lo
		}
		var ok bool
		r.min, ok = addInt64(r.min, lo)
		if !ok {
			return r, false
		}
		r.max, ok = addInt64(r.max, hi)
		if !ok {
			return r, false
		}
	}
	if v.offset() != 0 {
		var ok bool
		r.min, ok = addInt64(r.min, v.offset())
		if !ok {
			return r, false
		}
		r.max, ok = addInt64(r.max, v.offset())
		if !ok {
			return r, false
		}
	}
	if r.max < r.min {
		r.min, r.max = r.max, r.min
	}
	return r, true
}

func (e *engine) passMerge(id ValueID, rec valueRecord) {
	if rec.kind == OpPhi {
		inputs := rec.value.Inputs
		if len(inputs) == 0 {
			inputs = rec.value.Args
		}
		for _, in := range inputs {
			info := e.ptr[in]
			e.mergePtr(id, info)
		}
		return
	}
	for _, in := range []ValueID{rec.value.thenValue(), rec.value.elseValue()} {
		if in == "" {
			continue
		}
		info := e.ptr[in]
		e.mergePtr(id, info)
	}
}

func (e *engine) passLoad(id ValueID, rec valueRecord) {
	ptrID := rec.value.pointerInput()
	base := e.ptr[ptrID]
	width := rec.value.accessSize(e.opts.PointerSize)
	spans := e.spansFor(base, rec.function, width)
	var out pointerInfo
	for _, entry := range e.memory {
		for _, span := range spans {
			if entry.span.alias(span) {
				out.merge(entry.stored)
			}
		}
	}
	for _, c := range e.clobbers {
		for _, span := range spans {
			if c.span.alias(span) {
				out.unknown = true
			}
		}
	}
	e.mergePtr(id, out)
	e.checkMemoryAccess(rec, id, ptrID, AccessLoad, width)
}

func (e *engine) passStore(id ValueID, rec valueRecord) {
	destID := rec.value.storeDest()
	srcID := rec.value.storeSrc()
	dest := e.ptr[destID]
	src := e.ptr[srcID]
	width := rec.value.accessSize(e.opts.PointerSize)

	if len(dest.origins) == 0 || dest.unknown {
		// The pointer is written into memory not provably owned by one of the
		// graph's local objects.  That is an escape of the stored pointer.
		if !src.empty() {
			e.escapeInfoAt(id, srcID, src)
		}
	}
	for _, region := range dest.origins {
		obj, ok := e.objects[region.object]
		if !ok || obj.isControl() {
			if !src.empty() {
				e.escapeInfoAt(id, srcID, src)
			}
			continue
		}
		span := makeSpan(rec.function, region, width)
		stored := src.clone()
		if e.escapedObj[region.object] {
			stored.escaped = true
		}
		e.addMemory(span, stored, srcID)
	}
	e.checkMemoryAccess(rec, id, destID, AccessStore, width)
}

func (e *engine) passCall(id ValueID, rec valueRecord) {
	summary, known := e.callSummary(rec.value)
	if !known || summary.Unknown {
		for _, argID := range rec.value.Args {
			info := e.ptr[argID]
			if info.empty() {
				continue
			}
			e.escapeInfoAt(id, argID, info)
			e.checkAllRegions(rec, id, argID, AccessCall, 0, math.MaxInt64, true)
			e.markUnknownOrigins(argID, info)
		}
		return
	}
	for i, argID := range rec.value.Args {
		param := i
		if i < len(rec.value.ParamIndices) {
			param = rec.value.ParamIndices[i]
		}
		ps, ok := summary.lookup(param)
		info := e.ptr[argID]
		if info.empty() {
			continue
		}
		if !ok || ps.maxOffset() < 0 {
			e.escapeInfoAt(id, argID, info)
			e.checkAllRegions(rec, id, argID, AccessCall, 0, math.MaxInt64, true)
			e.markUnknownOrigins(argID, info)
			continue
		}
		if !ps.NoCapture {
			e.escapeInfoWithReason(id, argID, info, ReasonEscape, "call captures argument")
		}
		e.checkAllRegions(rec, id, argID, AccessCall, 0, ps.maxOffset(), true)
		if !ps.ReadOnly {
			for _, region := range info.origins {
				obj, ok := e.objects[region.object]
				if !ok || obj.isControl() {
					continue
				}
				span := makeSpan(rec.function, region, 0)
				span.max = addSaturated(span.max, ps.maxOffset())
				e.addClobber(span, id)
			}
			if info.unknown {
				e.markUnknownOrigins(argID, info)
			}
		}
	}
}

func (e *engine) passPtrToInt(id ValueID, rec valueRecord) {
	ptrID := rec.value.pointerInput()
	info := e.ptr[ptrID].clone()
	info.tainted = true
	if e.mergeInt(id, info) {
		e.dirty = true
	}
	for _, region := range info.origins {
		e.taintedObj[region.object] = true
		e.addReason(region.object, Reason{
			Code:   ReasonPointerInteger,
			Object: region.object,
			Value:  id,
			Source: ptrID,
			Detail: "pointer passed through PtrToInt",
		})
	}
	if info.unknown {
		e.markUnknownOrigins(ptrID, info)
	}
}

func (e *engine) passIntToPtr(id ValueID, rec valueRecord) {
	srcID := rec.value.pointerInput()
	info := e.ints[srcID].clone()
	info.tainted = true
	if info.empty() {
		info.unknown = true
	}
	e.mergePtr(id, info)
	for _, region := range info.origins {
		e.taintedObj[region.object] = true
		e.addReason(region.object, Reason{
			Code:   ReasonPointerInteger,
			Object: region.object,
			Value:  id,
			Source: srcID,
			Detail: "integer converted back to a pointer",
		})
	}
}

func (e *engine) passReturn(id ValueID, rec valueRecord) {
	ptrID := rec.value.pointerInput()
	info := e.ptr[ptrID]
	if !info.empty() {
		e.escapeInfoWithReason(id, ptrID, info, ReasonEscape, "pointer returned from function")
	}
}

func (e *engine) callSummary(v Value) (CallSummary, bool) {
	if s := v.Summary; s != nil {
		return *s, true
	}
	if s := v.CallSummary; s != nil {
		return *s, true
	}
	if v.Callee == "" {
		return CallSummary{}, false
	}
	s, ok := e.summaries[v.Callee]
	return s, ok
}

func (e *engine) escapeInfo(source ValueID, info pointerInfo) {
	e.escapeInfoAt(source, source, info)
}

func (e *engine) escapeInfoAt(valueID, source ValueID, info pointerInfo) {
	e.escapeInfoWithReason(valueID, source, info, ReasonEscape, "pointer escapes to external code")
}

func (e *engine) escapeInfoWithReason(valueID, source ValueID, info pointerInfo, code ReasonCode, detail string) {
	escaped := e.ptr[source]
	if !escaped.escaped {
		escaped.escaped = true
		e.ptr[source] = escaped
		e.dirty = true
	}
	for _, region := range info.origins {
		e.escapedObj[region.object] = true
		e.addReason(region.object, Reason{
			Code:   code,
			Object: region.object,
			Value:  valueID,
			Source: source,
			Detail: detail,
		})
	}
	if len(info.origins) == 0 && info.unknown {
		e.markUnknownOrigins(source, info)
	}
}

func (e *engine) markUnknownOrigins(source ValueID, info pointerInfo) {
	for _, region := range info.origins {
		e.addReason(region.object, Reason{
			Code:   ReasonUnknownPointer,
			Object: region.object,
			Value:  source,
			Source: source,
			Detail: "a pointer with partially unknown provenance is dereferenced",
		})
	}
}

func (e *engine) checkMemoryAccess(rec valueRecord, opID, ptrID ValueID, kind AccessKind, width uint64) {
	info := e.ptr[ptrID]
	if info.unknown {
		e.markUnknownOrigins(ptrID, info)
	}
	for _, region := range info.origins {
		e.checkRegion(rec, opID, ptrID, kind, region, width, false, 0)
	}
}

func (e *engine) checkAllRegions(rec valueRecord, opID, ptrID ValueID, kind AccessKind, width uint64, maxOffset int64, inclusive bool) {
	info := e.ptr[ptrID]
	if info.unknown {
		e.markUnknownOrigins(ptrID, info)
	}
	for _, region := range info.origins {
		e.checkRegion(rec, opID, ptrID, kind, region, width, inclusive, maxOffset)
	}
}

func (e *engine) checkRegion(rec valueRecord, opID, ptrID ValueID, kind AccessKind, region pointerRegion, width uint64, maxOffsetMode bool, maxOffset int64) {
	obj, ok := e.objects[region.object]
	if !ok {
		return
	}
	var offsetMax int64
	inBounds := !region.unbounded
	if maxOffsetMode {
		offsetMax = addSaturated(region.max, maxOffset)
		size := obj.sizeBytes()
		if inBounds && size > 0 && size <= math.MaxInt64 {
			inBounds = region.min >= 0 && offsetMax < int64(size)
		} else {
			inBounds = false
		}
	} else {
		offsetMax = addSaturated(region.max, int64(width))
		size := obj.sizeBytes()
		if inBounds && size > 0 && size <= math.MaxInt64 {
			inBounds = region.min >= 0 && region.max >= 0 &&
				uint64(region.max) <= size && width <= size &&
				offsetMax <= int64(size)
		} else {
			inBounds = false
		}
	}
	access := Access{
		Object:    region.object,
		Value:     opID,
		Source:    ptrID,
		Kind:      kind,
		OffsetMin: region.min,
		OffsetMax: offsetMax,
		Width:     width,
		InBounds:  inBounds,
	}
	key := fmt.Sprintf("%s|%s|%s|%d|%d|%d|%t", access.Object, access.Value, access.Source,
		access.OffsetMin, access.OffsetMax, access.Width, access.InBounds)
	if _, exists := e.accesses[region.object][key]; !exists {
		e.accesses[region.object][key] = access
		e.dirty = true
	}
	if !inBounds {
		e.addReason(region.object, Reason{
			Code:   ReasonOutOfBounds,
			Object: region.object,
			Value:  opID,
			Source: ptrID,
			Detail: fmt.Sprintf("%s range [%d,%d] width %d is outside object size %d", kind, region.min, offsetMax, width, obj.sizeBytes()),
		})
	}
}

func (e *engine) spansFor(info pointerInfo, fn FunctionID, width uint64) []accessSpan {
	spans := make([]accessSpan, 0, len(info.origins))
	for _, region := range info.origins {
		spans = append(spans, makeSpan(fn, region, width))
	}
	return spans
}

func makeSpan(fn FunctionID, region pointerRegion, width uint64) accessSpan {
	return accessSpan{
		function:  fn,
		object:    region.object,
		min:       region.min,
		max:       addSaturated(region.max, int64(width)),
		width:     width,
		whole:     region.whole,
		unbounded: region.unbounded,
	}
}

func (e *engine) addMemory(span accessSpan, stored pointerInfo, source ValueID) {
	for i := range e.memory {
		if e.memory[i].span == span {
			if e.memory[i].stored.merge(stored) {
				e.dirty = true
			}
			if e.memory[i].source == "" {
				e.memory[i].source = source
			}
			return
		}
	}
	e.memory = append(e.memory, memoryEntry{span: span, stored: stored, source: source})
	e.dirty = true
}

func (e *engine) addClobber(span accessSpan, source ValueID) {
	for _, old := range e.clobbers {
		if old.span == span {
			return
		}
	}
	e.clobbers = append(e.clobbers, clobberEntry{span: span, source: source})
	e.dirty = true
}

func (e *engine) propagateMemoryEscape() {
	for i := range e.memory {
		entry := &e.memory[i]
		if e.escapedObj[entry.span.object] {
			if !entry.stored.escaped {
				entry.stored.escaped = true
				e.dirty = true
			}
			for _, region := range entry.stored.origins {
				obj, managed := e.objects[region.object]
				if !managed || obj.isControl() || e.escapedObj[region.object] {
					continue
				}
				e.escapedObj[region.object] = true
				e.addReason(region.object, Reason{
					Code:   ReasonEscape,
					Object: region.object,
					Value:  entry.source,
					Source: entry.source,
					Detail: "pointer is stored in memory of an escaped object",
				})
				e.dirty = true
			}
			if entry.source != "" {
				stored := e.ptr[entry.source]
				if !stored.escaped {
					stored.escaped = true
					e.ptr[entry.source] = stored
					e.dirty = true
				}
			}
		}
	}
}

func (e *engine) mergeInt(id ValueID, info pointerInfo) bool {
	cur := e.ints[id]
	if cur.merge(info) {
		e.ints[id] = cur
		return true
	}
	e.ints[id] = cur
	return false
}

func (e *engine) mergePtr(id ValueID, info pointerInfo) bool {
	cur := e.ptr[id]
	if cur.merge(info) {
		e.ptr[id] = cur
		e.dirty = true
		return true
	}
	return false
}

func (e *engine) addReason(id ObjectID, reason Reason) {
	obj, ok := e.objects[id]
	if !ok {
		return
	}
	if obj.isControl() {
		return
	}
	set := e.reasons[id]
	if set == nil {
		set = reasonSet{}
		e.reasons[id] = set
	}
	if set.add(reason) {
		e.dirty = true
	}
}

func (e *engine) closeEscape() {
	// Escape is monotone.  Re-run a bounded closure so a value that becomes
	// escaped only after a memory update is reflected in every object origin.
	for i := 0; i < e.opts.MaxIterations && !e.budgetHit && e.dirty; i++ {
		e.iterations++
		e.dirty = false
		e.pass()
	}
	if e.dirty && !e.budgetHit {
		e.budgetHit = true
	}
}

func (e *engine) report() *Report {
	rep := &Report{
		Objects:         map[ObjectID]Decision{},
		Decisions:       map[ObjectID]Decision{},
		Classes:         map[ObjectID]StackClass{},
		Summaries:       map[FunctionID]CallSummary{},
		Iterations:      e.iterations,
		BudgetExhausted: e.budgetHit,
		Warnings:        append([]string(nil), e.warnings...),
	}
	for id, summary := range e.summaries {
		rep.Summaries[id] = summary
	}

	// A variable-length layout contaminates its whole sharing group.  The
	// classifier itself never lays out control metadata.
	variadicGroups := map[string]bool{}
	for _, id := range e.objectSeq {
		obj := e.objects[id]
		if group := obj.layoutGroup(); obj.isVariableLength() && group != "" {
			variadicGroups[group] = true
		}
	}

	for _, id := range e.objectSeq {
		obj := e.objects[id]
		class := ClassSafe
		reasons := e.reasons[id].slice()
		if obj.isControl() {
			class = ClassUnmanaged
			reasons = append(reasons, Reason{
				Code:   ReasonUnmanagedMetadata,
				Object: id,
				Detail: "control metadata is not laid out by this package",
			})
		} else {
			if obj.sizeBytes() == 0 {
				class = ClassUnsafe
				reasons = append(reasons, Reason{Code: ReasonInvalidObject, Object: id, Detail: "object size is zero"})
			}
			if obj.normalizedAlign() == 0 || (obj.normalizedAlign()&(obj.normalizedAlign()-1)) != 0 {
				class = ClassUnsafe
				reasons = append(reasons, Reason{Code: ReasonInvalidObject, Object: id, Detail: "alignment is not a power of two"})
			}
			if obj.isVariableLength() {
				class = ClassUnsafe
				reasons = append(reasons, Reason{Code: ReasonVariableLayout, Object: id, Detail: "object has variable-length layout"})
			} else if group := obj.layoutGroup(); group != "" && variadicGroups[group] {
				class = ClassUnsafe
				reasons = append(reasons, Reason{Code: ReasonVariableLayout, Object: id, Detail: "object shares a layout group with a variable-length object"})
			}
			if e.escapedObj[id] {
				class = ClassUnsafe
				if len(reasonsForCode(reasons, ReasonEscape)) == 0 {
					reasons = append(reasons, Reason{Code: ReasonEscape, Object: id, Detail: "a derived pointer escapes"})
				}
			}
			if e.taintedObj[id] || e.conversions[id] {
				class = ClassUnsafe
				if len(reasonsForCode(reasons, ReasonPointerInteger)) == 0 {
					reasons = append(reasons, Reason{Code: ReasonPointerInteger, Object: id, Detail: "pointer-integer conversion is present"})
				}
			}
			for _, r := range reasons {
				if r.Code == ReasonOutOfBounds || r.Code == ReasonUnknownPointer || r.Code == ReasonPointerInteger {
					class = ClassUnsafe
				}
			}
			if e.budgetHit {
				class = ClassUnsafe
				reasons = append(reasons, Reason{Code: ReasonBudgetExhausted, Object: id, Detail: "fixed-point budget exhausted; classification is conservative"})
			}
		}

		decision := Decision{Object: id, Class: class, Reasons: reasons}
		seenAccess := map[string]struct{}{}
		for _, a := range e.accesses[id] {
			key := fmt.Sprintf("%s|%d|%d|%d|%t", a.Value, a.OffsetMin, a.OffsetMax, a.Width, a.InBounds)
			if _, ok := seenAccess[key]; ok {
				continue
			}
			seenAccess[key] = struct{}{}
			decision.Accesses = append(decision.Accesses, a)
			if !a.InBounds {
				size := a.Width
				if size == 0 && a.OffsetMax >= a.OffsetMin {
					size = uint64(a.OffsetMax-a.OffsetMin) + 1
				}
				rep.Writes = append(rep.Writes, UnsafeWrite{
					Object:    a.Object,
					Offset:    a.OffsetMin,
					OffsetMin: a.OffsetMin,
					OffsetMax: a.OffsetMax,
					Size:      size,
					Source:    a.Source,
				})
			}
		}
		sort.Slice(decision.Accesses, func(i, j int) bool {
			a, b := decision.Accesses[i], decision.Accesses[j]
			if a.OffsetMin != b.OffsetMin {
				return a.OffsetMin < b.OffsetMin
			}
			return a.Value < b.Value
		})
		rep.Objects[id] = decision
		rep.Decisions[id] = decision
		rep.Classes[id] = class
		switch class {
		case ClassSafe:
			rep.Safe = append(rep.Safe, id)
		case ClassUnsafe:
			rep.Unsafe = append(rep.Unsafe, id)
		case ClassUnmanaged:
			rep.Unmanaged = append(rep.Unmanaged, id)
		}
	}
	return rep
}

func reasonsForCode(reasons []Reason, code ReasonCode) []Reason {
	var out []Reason
	for _, r := range reasons {
		if r.Code == code {
			out = append(out, r)
		}
	}
	return out
}

func addInt64(a, b int64) (int64, bool) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, false
	}
	if b < 0 && a < math.MinInt64-b {
		return 0, false
	}
	return a + b, true
}

func mulInt64(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	if a == math.MinInt64 || b == math.MinInt64 {
		return 0, false
	}
	if a > 0 && b > 0 && a > math.MaxInt64/b {
		return 0, false
	}
	if a < 0 && b < 0 && a < math.MaxInt64/b {
		return 0, false
	}
	if a > 0 && b < 0 && b < math.MinInt64/a {
		return 0, false
	}
	if a < 0 && b > 0 && a < math.MinInt64/b {
		return 0, false
	}
	return a * b, true
}

func addSaturated(a, b int64) int64 {
	c, ok := addInt64(a, b)
	if ok {
		return c
	}
	if b >= 0 {
		return math.MaxInt64
	}
	return math.MinInt64
}

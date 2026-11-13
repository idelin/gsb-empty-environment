package netsec

import (
	"fmt"
	"math"
	"sort"
)

// Summarize infers per-parameter call summaries from finite SSA function
// bodies.  The iteration starts from the optimistic element (no capture,
// read-only, zero offset) and monotonically moves upward.  Recursive calls
// therefore reach a fixed point without recursive Go calls.
func Summarize(g any, opts ...Option) map[FunctionID]CallSummary {
	graph, ok := coerceGraph(g)
	if !ok {
		return nil
	}
	o := normalizeOptions(opts)
	e := newEngine(graph, o)
	e.prepareWithoutSummaryInference()
	e.inferSummaries()
	out := make(map[FunctionID]CallSummary, len(e.summaries))
	for id, s := range e.summaries {
		out[id] = s
	}
	return out
}

// InferSummaries is an alias of Summarize.
func InferSummaries(g any, opts ...Option) map[FunctionID]CallSummary {
	return Summarize(g, opts...)
}

func (e *engine) prepareWithoutSummaryInference() {
	old := e.opts.InferSummaries
	e.opts.InferSummaries = false
	e.prepare()
	e.opts.InferSummaries = old
}

type paramFact struct {
	escaped   bool
	write     bool
	unbounded bool
	maxOffset int64
}

type summaryMemory struct {
	span   accessSpan
	stored pointerInfo
}

type summaryEval struct {
	e        *engine
	fn       Function
	params   []ValueID
	paramOf  map[ValueID]int
	ptr      map[ValueID]pointerInfo
	ints     map[ValueID]pointerInfo
	memory   []summaryMemory
	escaped  map[ObjectID]bool
	facts    []paramFact
	localIDs map[ValueID]bool
	changed  bool
}

func paramObject(index int) ObjectID {
	return ObjectID(fmt.Sprintf("\x00param/%d", index))
}

func paramIndex(id ObjectID) (int, bool) {
	var idx int
	if n, err := fmt.Sscanf(string(id), "\x00param/%d", &idx); err == nil && n == 1 && idx >= 0 {
		return idx, true
	}
	return 0, false
}

func (e *engine) bodyValuesFor(fn Function) []ValueID {
	if body := fn.bodyValues(); len(body) != 0 {
		return body
	}
	body := make([]ValueID, 0)
	for _, id := range e.valueSeq {
		if rec, ok := e.values[id]; ok && rec.function == fn.ID && !rec.synthetic {
			body = append(body, id)
		}
	}
	return body
}

func (e *engine) paramValuesFor(fn Function) []ValueID {
	if params := fn.paramValues(); len(params) != 0 {
		return params
	}
	maxIndex := -1
	for _, id := range e.valueSeq {
		rec, ok := e.values[id]
		if !ok || rec.function != fn.ID || rec.kind != OpParam {
			continue
		}
		if rec.value.Param > maxIndex {
			maxIndex = rec.value.Param
		}
	}
	if maxIndex < 0 {
		return nil
	}
	params := make([]ValueID, maxIndex+1)
	for _, id := range e.valueSeq {
		rec, ok := e.values[id]
		if !ok || rec.function != fn.ID || rec.kind != OpParam {
			continue
		}
		params[rec.value.Param] = id
	}
	return params
}

func (e *engine) inferSummaries() {
	if len(e.functions) == 0 {
		return
	}
	explicit := map[FunctionID]bool{}
	for id := range e.summaries {
		explicit[id] = true
	}
	candidate := map[FunctionID]bool{}

	for _, fn := range e.functions {
		if explicit[fn.ID] {
			continue
		}
		body := e.bodyValuesFor(fn)
		if len(body) == 0 {
			// A Function entry without a body is a declaration, not evidence
			// that the call is side-effect free.
			continue
		}
		complete := true
		for _, vid := range body {
			if _, ok := e.values[vid]; !ok {
				complete = false
				break
			}
		}
		if !complete {
			continue
		}
		params := e.paramValuesFor(fn)
		count := len(params)
		for _, vid := range body {
			if rec, ok := e.values[vid]; ok && rec.kind == OpParam && rec.value.Param+1 > count {
				count = rec.value.Param + 1
			}
		}
		s := CallSummary{Function: fn.ID, Params: make([]ParamSummary, count)}
		for i := 0; i < count; i++ {
			s.Params[i] = ParamSummary{
				Index:     i,
				NoCapture: true,
				ReadOnly:  true,
				MaxOffset: 0,
			}
		}
		e.summaries[fn.ID] = s
		candidate[fn.ID] = true
	}

	order := make([]FunctionID, 0, len(candidate))
	for id := range e.functions {
		if candidate[id] {
			order = append(order, id)
		}
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })

	for iter := 0; iter < e.opts.MaxIterations; iter++ {
		if !e.tick(1) {
			break
		}
		changed := false
		for _, id := range order {
			fn := e.functions[id]
			ev := newSummaryEval(e, fn)
			ev.run()
			next := ev.summary()
			old := e.summaries[id]
			merged := mergeSummary(old, next)
			if !equalSummary(old, merged) {
				e.summaries[id] = merged
				changed = true
			}
		}
		if !changed {
			return
		}
	}
	// A non-converged summary is deliberately top: any argument may be
	// captured and accessed arbitrarily.
	for _, id := range order {
		s := e.summaries[id]
		s.Unknown = true
		for i := range s.Params {
			s.Params[i].NoCapture = false
			s.Params[i].ReadOnly = false
			s.Params[i].MaxOffset = -1
		}
		e.summaries[id] = s
	}
	e.budgetHit = true
}

func mergeSummary(a, b CallSummary) CallSummary {
	out := a
	out.Function = b.Function
	out.Unknown = a.Unknown || b.Unknown
	if len(out.Params) < len(b.Params) {
		out.Params = append(out.Params, make([]ParamSummary, len(b.Params)-len(out.Params))...)
	}
	for i := range b.Params {
		bp := b.Params[i]
		if i >= len(out.Params) {
			out.Params = append(out.Params, bp)
			continue
		}
		ap := out.Params[i]
		ap.Index = i
		ap.NoCapture = ap.NoCapture && bp.NoCapture
		ap.ReadOnly = ap.ReadOnly && bp.ReadOnly
		ap.MaxOffset = maxOffset(ap.MaxOffset, bp.MaxOffset)
		out.Params[i] = ap
	}
	return out
}

func maxOffset(a, b int64) int64 {
	if a < 0 || b < 0 {
		return -1
	}
	if a > b {
		return a
	}
	return b
}

func equalSummary(a, b CallSummary) bool {
	if a.Unknown != b.Unknown || len(a.Params) != len(b.Params) {
		return false
	}
	for i := range a.Params {
		if a.Params[i].NoCapture != b.Params[i].NoCapture ||
			a.Params[i].ReadOnly != b.Params[i].ReadOnly ||
			a.Params[i].MaxOffset != b.Params[i].MaxOffset {
			return false
		}
	}
	return true
}

func newSummaryEval(e *engine, fn Function) *summaryEval {
	params := e.paramValuesFor(fn)
	body := e.bodyValuesFor(fn)
	ev := &summaryEval{
		e:        e,
		fn:       fn,
		params:   params,
		paramOf:  map[ValueID]int{},
		ptr:      map[ValueID]pointerInfo{},
		ints:     map[ValueID]pointerInfo{},
		escaped:  map[ObjectID]bool{},
		facts:    make([]paramFact, len(params)),
		localIDs: map[ValueID]bool{},
	}
	for i, id := range params {
		ev.paramOf[id] = i
		if rec, ok := e.values[id]; ok && rec.kind == OpParam {
			ev.paramOf[id] = rec.value.Param
			for len(ev.facts) <= rec.value.Param {
				ev.facts = append(ev.facts, paramFact{})
			}
		}
	}
	for _, id := range body {
		ev.localIDs[id] = true
		if rec, ok := e.values[id]; ok && rec.kind == OpParam {
			idx := rec.value.Param
			ev.paramOf[id] = idx
			for len(ev.facts) <= idx {
				ev.facts = append(ev.facts, paramFact{})
			}
			if _, exists := ev.ptr[id]; !exists {
				ev.ptr[id] = pointerInfo{origins: []pointerRegion{{object: paramObject(idx)}}}
			}
		}
	}
	for i, id := range params {
		idx := i
		if rec, ok := e.values[id]; ok && rec.kind == OpParam {
			idx = rec.value.Param
			ev.paramOf[id] = idx
			for len(ev.facts) <= idx {
				ev.facts = append(ev.facts, paramFact{})
			}
		}
		for len(ev.facts) <= idx {
			ev.facts = append(ev.facts, paramFact{})
		}
		ev.ptr[id] = pointerInfo{origins: []pointerRegion{{object: paramObject(idx)}}}
	}
	return ev
}

func (ev *summaryEval) run() {
	for iter := 0; iter < 64; iter++ {
		ev.changed = false
		changed := false
		for _, id := range ev.e.bodyValuesFor(ev.fn) {
			rec, ok := ev.e.values[id]
			if !ok {
				continue
			}
			switch rec.kind {
			case OpAddrOf:
				obj := rec.value.Object
				if _, ok := ev.e.objects[obj]; ok {
					changed = ev.merge(id, pointerInfo{origins: []pointerRegion{{object: obj}}}) || changed
				}
			case OpGEP:
				base := ev.ptr[rec.value.pointerInput()]
				var out pointerInfo
				out.unknown = base.unknown
				out.tainted = base.tainted
				for _, region := range base.origins {
					nr, ok := ev.e.deriveRegion(region, rec.value)
					if !ok {
						nr = pointerRegion{object: region.object, whole: true, unbounded: true}
					}
					out.origins = append(out.origins, nr)
				}
				changed = ev.merge(id, out) || changed
			case OpPhi:
				var out pointerInfo
				inputs := rec.value.Inputs
				if len(inputs) == 0 {
					inputs = rec.value.Args
				}
				for _, in := range inputs {
					out.merge(ev.ptr[in])
				}
				changed = ev.merge(id, out) || changed
			case OpSelect:
				var out pointerInfo
				out.merge(ev.ptr[rec.value.thenValue()])
				out.merge(ev.ptr[rec.value.elseValue()])
				changed = ev.merge(id, out) || changed
			case OpLoadPtr:
				ptr := ev.ptr[rec.value.pointerInput()]
				width := rec.value.accessSize(ev.e.opts.PointerSize)
				var out pointerInfo
				for _, entry := range ev.memory {
					for _, region := range ptr.origins {
						span := makeSpan(rec.function, region, width)
						if entry.span.alias(span) {
							out.merge(entry.stored)
						}
					}
				}
				changed = ev.merge(id, out) || changed
				ev.observe(rec, id, ptr, width)
			case OpStorePtr:
				dest := ev.ptr[rec.value.storeDest()]
				src := ev.ptr[rec.value.storeSrc()]
				width := rec.value.accessSize(ev.e.opts.PointerSize)
				if len(dest.origins) == 0 || dest.unknown {
					ev.capture(src, dest)
				}
				for _, region := range dest.origins {
					if _, isParam := paramIndex(region.object); isParam {
						ev.noteWrite(region, int64(width))
						continue
					}
					obj, ok := ev.e.objects[region.object]
					if !ok || obj.isControl() {
						ev.capture(src, dest)
						continue
					}
					span := makeSpan(rec.function, region, width)
					ev.addMemory(span, src)
				}
				ev.observe(rec, id, dest, width)
			case OpPassCall:
				summary, known := ev.e.callSummary(rec.value)
				for i, argID := range rec.value.Args {
					param := i
					if i < len(rec.value.ParamIndices) {
						param = rec.value.ParamIndices[i]
					}
					info := ev.ptr[argID]
					if info.empty() {
						continue
					}
					if !known || summary.Unknown {
						ev.capture(info, info)
						ev.noteTop(info)
						continue
					}
					ps, ok := summary.lookup(param)
					if !ok || ps.maxOffset() < 0 {
						ev.capture(info, info)
						ev.noteTop(info)
						continue
					}
					if !ps.NoCapture {
						ev.capture(info, info)
					}
					if !ps.ReadOnly {
						for _, region := range info.origins {
							if _, isParam := paramIndex(region.object); isParam {
								ev.observeRegion(region, ps.maxOffset())
							}
						}
					}
					ev.observeCall(info, ps.maxOffset(), true)
				}
			case OpPtrToInt:
				ptr := ev.ptr[rec.value.pointerInput()]
				val := ptr.clone()
				val.tainted = true
				curInt := ev.ints[id]
				curInt.merge(val)
				ev.ints[id] = curInt
				ev.noteTop(ptr)
			case OpIntToPtr:
				info := ev.ints[rec.value.pointerInput()].clone()
				info.tainted = true
				if info.empty() {
					info.unknown = true
				}
				changed = ev.merge(id, info) || changed
				ev.noteTop(info)
			case OpReturnPtr:
				info := ev.ptr[rec.value.pointerInput()]
				ev.capture(info, info)
			}
		}
		ev.propagateLocalEscapes()
		if !changed && !ev.changed {
			return
		}
	}
	// Local fixed point failure is also conservative.
	for i := range ev.facts {
		ev.facts[i].escaped = true
		ev.facts[i].write = true
		ev.facts[i].unbounded = true
	}
}

func (ev *summaryEval) merge(id ValueID, info pointerInfo) bool {
	cur := ev.ptr[id]
	if cur.merge(info) {
		ev.ptr[id] = cur
		ev.changed = true
		return true
	}
	return false
}

func (ev *summaryEval) capture(info, dest pointerInfo) {
	for _, region := range info.origins {
		idx, ok := paramIndex(region.object)
		if !ok {
			if obj, managed := ev.e.objects[region.object]; managed && !obj.isControl() {
				if !ev.escaped[region.object] {
					ev.escaped[region.object] = true
					ev.changed = true
				}
			}
			continue
		}
		for len(ev.facts) <= idx {
			ev.facts = append(ev.facts, paramFact{})
		}
		if !ev.facts[idx].escaped {
			ev.facts[idx].escaped = true
			ev.changed = true
		}
	}
	if info.unknown {
		for i := range ev.facts {
			// An unknown address cannot be tied to a particular parameter.
			// Keeping the other parameters optimistic here is safe only if the
			// unknown did not come from them; callers that do so mark top
			// explicitly.  To stay conservative for a shared unknown value we
			// mark every parameter.
			if !ev.facts[i].escaped {
				ev.facts[i].escaped = true
				ev.changed = true
			}
		}
	}
}

func (ev *summaryEval) noteTop(info pointerInfo) {
	for _, region := range info.origins {
		idx, ok := paramIndex(region.object)
		if !ok {
			continue
		}
		for len(ev.facts) <= idx {
			ev.facts = append(ev.facts, paramFact{})
		}
		if !ev.facts[idx].escaped || !ev.facts[idx].write || !ev.facts[idx].unbounded {
			ev.facts[idx].escaped = true
			ev.facts[idx].write = true
			ev.facts[idx].unbounded = true
			ev.changed = true
		}
	}
}

func (ev *summaryEval) observe(rec valueRecord, opID ValueID, info pointerInfo, width uint64) {
	for _, region := range info.origins {
		if _, ok := paramIndex(region.object); ok {
			ev.observeRegion(region, int64(width)-1)
		}
	}
}

func (ev *summaryEval) observeCall(info pointerInfo, offset int64, inclusive bool) {
	for _, region := range info.origins {
		if _, ok := paramIndex(region.object); ok {
			ev.observeRegion(region, offset)
		}
	}
}

func (ev *summaryEval) observeRegion(region pointerRegion, offset int64) {
	idx, ok := paramIndex(region.object)
	if !ok {
		return
	}
	for len(ev.facts) <= idx {
		ev.facts = append(ev.facts, paramFact{})
	}
	if region.unbounded || region.whole && region.max == math.MaxInt64 {
		if !ev.facts[idx].unbounded {
			ev.facts[idx].unbounded = true
			ev.changed = true
		}
		return
	}
	n := addSaturated(region.max, offset)
	if n < 0 {
		n = 0
	}
	if n > ev.facts[idx].maxOffset {
		ev.facts[idx].maxOffset = n
		ev.changed = true
	}
}

func (ev *summaryEval) noteWrite(region pointerRegion, width int64) {
	idx, ok := paramIndex(region.object)
	if !ok {
		return
	}
	for len(ev.facts) <= idx {
		ev.facts = append(ev.facts, paramFact{})
	}
	if !ev.facts[idx].write {
		ev.facts[idx].write = true
		ev.changed = true
	}
	ev.observeRegion(region, width-1)
}

func (ev *summaryEval) addMemory(span accessSpan, stored pointerInfo) {
	for i := range ev.memory {
		if ev.memory[i].span == span {
			if ev.memory[i].stored.merge(stored) {
				ev.changed = true
			}
			return
		}
	}
	ev.memory = append(ev.memory, summaryMemory{span: span, stored: stored})
	ev.changed = true
}

func (ev *summaryEval) propagateLocalEscapes() {
	for i := range ev.memory {
		entry := &ev.memory[i]
		if ev.escaped[entry.span.object] {
			ev.capture(entry.stored, entry.stored)
		}
	}
}

func (ev *summaryEval) summary() CallSummary {
	ps := make([]ParamSummary, len(ev.facts))
	for i, f := range ev.facts {
		ps[i] = ParamSummary{
			Index:     i,
			NoCapture: !f.escaped,
			ReadOnly:  !f.write,
			MaxOffset: f.maxOffset,
		}
		if f.unbounded {
			ps[i].MaxOffset = -1
		}
	}
	return CallSummary{Function: ev.fn.ID, Params: ps}
}

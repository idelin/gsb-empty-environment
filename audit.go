package netsec

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Audit compares a pending classifier with the reference model.  A false-safe
// object is one that the callback puts on the safe stack while Analyze marks it
// unsafe.  Audit finds the shortest provenance witness for every such object.
// If WithLayout is supplied, out-of-bounds writes are also simulated; only
// impacts that reach control metadata or another reference-safe object are
// reported.
func Audit(g any, pending any, opts ...Option) *AuditReport {
	graph, ok := coerceGraph(g)
	if !ok {
		return &AuditReport{
			Reference:       &Report{Objects: map[ObjectID]Decision{}, Decisions: map[ObjectID]Decision{}, Classes: map[ObjectID]StackClass{}, BudgetExhausted: true},
			BudgetExhausted: true,
			Warnings:        []string{"invalid or unsupported graph value"},
		}
	}
	o := normalizeOptions(opts)
	if o.Layout == nil && graph.Layout != nil {
		o.Layout = graph.Layout
	}
	e := newEngine(graph, o)
	e.prepare()
	e.run()
	ref := e.report()

	classify, warnings, err := adaptPending(pending)
	audit := &AuditReport{
		Reference:       ref,
		BudgetExhausted: ref.BudgetExhausted,
		Warnings:        append(append([]string(nil), ref.Warnings...), warnings...),
	}
	if err != nil {
		audit.Warnings = append(audit.Warnings, err.Error())
	}
	if classify == nil {
		classify = func(id ObjectID, _ Object) StackClass {
			return ref.ClassOf(id)
		}
	}
	initialWarnings := len(warnings)

	provenance := buildProvenance(e)
	for _, id := range e.objectSeq {
		obj := e.objects[id]
		pendingClass := classify(id, obj)
		referenceClass := ref.ClassOf(id)
		if pendingClass == referenceClass {
			continue
		}
		m := Mismatch{Object: id, Pending: pendingClass, Reference: referenceClass}
		if d, ok := ref.DecisionOf(id); ok {
			m.Reasons = append([]Reason(nil), d.Reasons...)
		}
		m.Dangerous = pendingClass == ClassSafe && referenceClass == ClassUnsafe
		if m.Dangerous {
			m.Path, m.Distance = provenance.shortest(id, m.Reasons, o.MaxPath)
			for i := len(m.Path) - 1; i >= 0; i-- {
				m.SourcePath = append(m.SourcePath, m.Path[i])
			}
			audit.FalseSafe = append(audit.FalseSafe, m)
		} else if pendingClass == ClassUnsafe && referenceClass == ClassSafe {
			audit.FalseUnsafe = append(audit.FalseUnsafe, m)
		}
		audit.Mismatches = append(audit.Mismatches, m)
	}
	if len(warnings) > initialWarnings {
		audit.Warnings = append(audit.Warnings, warnings[initialWarnings:]...)
	}

	if o.Layout != nil {
		for _, m := range audit.FalseSafe {
			writes := make([]UnsafeWrite, 0)
			for _, w := range ref.Writes {
				if w.Object == m.Object {
					writes = append(writes, w)
				}
			}
			if len(writes) == 0 {
				continue
			}
			impacts := SimulateLayout(*o.Layout, ref.Classes, writes)
			for _, impact := range impacts {
				if impact.Source == m.Object && impact.Serious() {
					audit.Impacts = append(audit.Impacts, impact)
				}
			}
		}
	}
	return audit
}

func adaptPending(p any) (func(ObjectID, Object) StackClass, []string, error) {
	if p == nil {
		return nil, nil, nil
	}
	switch m := p.(type) {
	case map[ObjectID]StackClass:
		return func(id ObjectID, _ Object) StackClass { return m[id] }, nil, nil
	case map[string]StackClass:
		return func(id ObjectID, _ Object) StackClass { return m[string(id)] }, nil, nil
	}
	rv := reflect.ValueOf(p)
	if rv.Kind() != reflect.Func {
		return nil, nil, fmt.Errorf("pending classifier must be a function or class map, got %T", p)
	}
	rt := rv.Type()
	if rt.NumIn() < 1 || rt.NumIn() > 2 || rt.NumOut() < 1 {
		return nil, nil, fmt.Errorf("unsupported pending classifier signature %s", rt)
	}
	var warnings []string
	classify := func(id ObjectID, obj Object) (result StackClass) {
		defer func() {
			if recover() != nil {
				warnings = append(warnings, "pending classifier panicked for object "+string(id))
				result = ClassUnsafe
			}
		}()
		args := make([]reflect.Value, 0, rt.NumIn())
		if rt.NumIn() == 1 {
			v, ok := pendingArgument(rt.In(0), id, obj)
			if !ok {
				warnings = append(warnings, "unsupported pending classifier argument type "+rt.In(0).String())
				return ClassUnsafe
			}
			args = append(args, v)
		} else {
			v1, ok1 := pendingArgument(rt.In(0), id, obj)
			v2, ok2 := pendingArgument(rt.In(1), id, obj)
			if !ok1 || !ok2 || rt.In(0) == rt.In(1) {
				// For two identical argument types prefer (ObjectID, Object)
				// when possible; otherwise the callback is ambiguous.
				a, aok := pendingArgument(rt.In(0), id, obj)
				b, bok := pendingArgument(rt.In(1), id, obj)
				if !aok || !bok {
					warnings = append(warnings, "unsupported pending classifier argument types")
					return ClassUnsafe
				}
				v1, v2 = a, b
			}
			args = append(args, v1, v2)
		}
		out := rv.Call(args)
		return pendingResult(out[0])
	}
	return classify, warnings, nil
}

func pendingArgument(t reflect.Type, id ObjectID, obj Object) (reflect.Value, bool) {
	objType := reflect.TypeOf(Object{})
	idType := reflect.TypeOf(ObjectID(""))
	if t == objType {
		return reflect.ValueOf(obj), true
	}
	if t == idType {
		return reflect.ValueOf(id), true
	}
	if t.Kind() == reflect.String {
		return reflect.ValueOf(string(id)).Convert(t), true
	}
	if t.Kind() == reflect.Interface {
		v := reflect.ValueOf(id)
		if v.Type().AssignableTo(t) {
			return v, true
		}
		v = reflect.ValueOf(obj)
		if v.Type().AssignableTo(t) {
			return v, true
		}
	}
	if t.Kind() == reflect.Int || t.Kind() == reflect.Int64 {
		// Numeric IDs are not the native representation; accept them as an
		// ordinal only when explicitly convertible is impossible.  Returning
		// false here keeps accidental callbacks from producing a false-safe
		// answer.
		return reflect.Value{}, false
	}
	return reflect.Value{}, false
}

func pendingResult(v reflect.Value) StackClass {
	if !v.IsValid() {
		return ClassUnsafe
	}
	if v.Type() == reflect.TypeOf(StackClass(0)) {
		return StackClass(v.Uint())
	}
	switch v.Kind() {
	case reflect.Bool:
		if v.Bool() {
			return ClassSafe
		}
		return ClassUnsafe
	case reflect.String:
		switch strings.ToLower(strings.TrimSpace(v.String())) {
		case "safe", "safestack", "classsafe":
			return ClassSafe
		case "unsafe", "unsafestack", "classunsafe":
			return ClassUnsafe
		case "unmanaged", "control":
			return ClassUnmanaged
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		switch v.Int() {
		case int64(ClassSafe):
			return ClassSafe
		case int64(ClassUnmanaged):
			return ClassUnmanaged
		default:
			return ClassUnsafe
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		switch v.Uint() {
		case uint64(ClassSafe):
			return ClassSafe
		case uint64(ClassUnmanaged):
			return ClassUnmanaged
		default:
			return ClassUnsafe
		}
	}
	return ClassUnsafe
}

type provenanceEdge struct {
	to   string
	step ProvenanceStep
}

type provenanceGraph struct {
	edges map[string][]provenanceEdge
}

func nodeObject(id ObjectID) string { return "o:" + string(id) }
func nodeValue(id ValueID) string   { return "v:" + string(id) }
func nodeOp(id ValueID) string      { return "p:" + string(id) }
func nodeLayout(id ObjectID) string { return "l:" + string(id) }

func buildProvenance(e *engine) *provenanceGraph {
	pg := &provenanceGraph{edges: map[string][]provenanceEdge{}}
	add := func(from string, edge provenanceEdge) {
		pg.edges[from] = append(pg.edges[from], edge)
	}
	valueEdge := func(from string, id ValueID) {
		if from == "" || id == "" {
			return
		}
		rec, _ := e.values[id]
		step := ProvenanceStep{
			Kind:      NodeValue,
			ID:        string(id),
			Value:     id,
			Operation: rec.kind,
		}
		add(from, provenanceEdge{to: nodeValue(id), step: step})
	}
	opEdge := func(from string, id ValueID, kind OpKind, detail string) {
		if from == "" || id == "" {
			return
		}
		step := ProvenanceStep{
			Kind:      NodeOperation,
			ID:        string(id),
			Value:     id,
			Operation: kind,
			Detail:    detail,
		}
		add(from, provenanceEdge{to: nodeOp(id), step: step})
	}

	for _, id := range e.objectSeq {
		step := ProvenanceStep{
			Kind:   NodeLayout,
			ID:     string(id),
			Object: id,
			Detail: "external layout or object invariant",
		}
		add(nodeObject(id), provenanceEdge{to: nodeLayout(id), step: step})
	}
	for _, vid := range e.valueSeq {
		rec := e.values[vid]
		switch rec.kind {
		case OpAddrOf:
			obj := rec.value.Object
			if obj == "" {
				continue
			}
			step := ProvenanceStep{Kind: NodeValue, ID: string(vid), Object: obj, Value: vid, Operation: OpAddrOf}
			add(nodeObject(obj), provenanceEdge{to: nodeValue(vid), step: step})
		case OpGEP:
			in := rec.value.pointerInput()
			opEdge(nodeValue(in), vid, rec.kind, "")
			valueEdge(nodeOp(vid), vid)
		case OpPhi:
			inputs := rec.value.Inputs
			if len(inputs) == 0 {
				inputs = rec.value.Args
			}
			for _, in := range inputs {
				opEdge(nodeValue(in), vid, rec.kind, "")
			}
			valueEdge(nodeOp(vid), vid)
		case OpSelect:
			opEdge(nodeValue(rec.value.thenValue()), vid, rec.kind, "")
			opEdge(nodeValue(rec.value.elseValue()), vid, rec.kind, "")
			valueEdge(nodeOp(vid), vid)
		case OpLoadPtr:
			opEdge(nodeValue(rec.value.pointerInput()), vid, rec.kind, "")
			valueEdge(nodeOp(vid), vid)
		case OpStorePtr:
			opEdge(nodeValue(rec.value.storeSrc()), vid, rec.kind, "pointer stored into local memory")
			opEdge(nodeValue(rec.value.storeDest()), vid, rec.kind, "")
		case OpPtrToInt:
			opEdge(nodeValue(rec.value.pointerInput()), vid, rec.kind, "")
			valueEdge(nodeOp(vid), vid)
		case OpIntToPtr:
			opEdge(nodeValue(rec.value.pointerInput()), vid, rec.kind, "")
			valueEdge(nodeOp(vid), vid)
		case OpReturnPtr:
			opEdge(nodeValue(rec.value.pointerInput()), vid, rec.kind, "pointer returned")
		case OpPassCall:
			for _, arg := range rec.value.Args {
				opEdge(nodeValue(arg), vid, rec.kind, "")
			}
		}
	}

	// Add memory edges after the value graph is known.  A path through local
	// storage is therefore represented as source -> store -> load -> use.
	for _, sid := range e.valueSeq {
		srec := e.values[sid]
		if srec.kind != OpStorePtr {
			continue
		}
		dest := e.ptr[srec.value.storeDest()]
		if len(dest.origins) == 0 {
			continue
		}
		for _, lid := range e.valueSeq {
			lrec := e.values[lid]
			if lrec.kind != OpLoadPtr || lrec.function != srec.function {
				continue
			}
			loadPtr := e.ptr[lrec.value.pointerInput()]
			width := lrec.value.accessSize(e.opts.PointerSize)
			if memoryAlias(dest, loadPtr, lrec.function, width) {
				step := ProvenanceStep{
					Kind:      NodeMemory,
					ID:        string(sid) + "->" + string(lid),
					Value:     lid,
					Operation: OpLoadPtr,
					Detail:    "pointer reloaded from field-sensitive memory",
				}
				add(nodeOp(sid), provenanceEdge{to: nodeOp(lid), step: step})
			}
		}
	}
	return pg
}

func memoryAlias(dest, load pointerInfo, fn FunctionID, width uint64) bool {
	for _, d := range dest.origins {
		ds := makeSpan(fn, d, width)
		for _, l := range load.origins {
			ls := makeSpan(fn, l, width)
			if ds.alias(ls) {
				return true
			}
		}
	}
	return false
}

func (pg *provenanceGraph) shortest(obj ObjectID, reasons []Reason, maxPath int) ([]ProvenanceStep, int) {
	if maxPath <= 0 {
		maxPath = 1
	}
	start := nodeObject(obj)
	dist := map[string]int{start: 0}
	prevNode := map[string]string{}
	prevEdge := map[string]ProvenanceStep{}
	queue := []string{start}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		d := dist[n]
		if d >= maxPath {
			continue
		}
		for _, edge := range pg.edges[n] {
			if _, seen := dist[edge.to]; seen {
				continue
			}
			dist[edge.to] = d + 1
			prevNode[edge.to] = n
			prevEdge[edge.to] = edge.step
			queue = append(queue, edge.to)
		}
	}

	sorted := append([]Reason(nil), reasons...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Code != sorted[j].Code {
			return sorted[i].Code < sorted[j].Code
		}
		return sorted[i].Value < sorted[j].Value
	})

	var best []string
	for _, r := range sorted {
		if r.Value == "" {
			continue
		}
		candidates := []string{nodeOp(r.Value), nodeValue(r.Value)}
		for _, cand := range candidates {
			if _, ok := dist[cand]; !ok {
				continue
			}
			path := reconstruct(cand, prevNode)
			if len(best) == 0 || len(path) < len(best) || (len(path) == len(best) && strings.Join(path, "\x00") < strings.Join(best, "\x00")) {
				best = path
			}
		}
	}
	if len(best) == 0 {
		layout := nodeLayout(obj)
		if _, ok := dist[layout]; ok {
			best = reconstruct(layout, prevNode)
		}
	}
	if len(best) == 0 {
		return nil, 0
	}
	steps := make([]ProvenanceStep, 0, len(best))
	steps = append(steps, ProvenanceStep{Kind: NodeObject, ID: string(obj), Object: obj})
	for i, n := range best {
		if i == 0 {
			continue
		}
		if edge, ok := prevEdge[n]; ok {
			steps = append(steps, edge)
		}
	}
	return steps, len(steps) - 1
}

func reconstruct(target string, prev map[string]string) []string {
	var rev []string
	for n := target; n != ""; n = prev[n] {
		rev = append(rev, n)
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

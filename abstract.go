package netsec

import (
	"sort"
	"strings"
)

// EvidenceKind classifies one step in a data-flow witness.
type EvidenceKind string

const (
	EvidenceAllocate         EvidenceKind = "allocate"
	EvidenceAlias            EvidenceKind = "alias"
	EvidenceFieldRead        EvidenceKind = "field-read"
	EvidencePrototypeRead    EvidenceKind = "prototype-read"
	EvidenceConstructorRead  EvidenceKind = "constructor-read"
	EvidencePrototypeFollow  EvidenceKind = "follow-prototype"
	EvidenceFieldWrite       EvidenceKind = "field-write"
	EvidenceExternalString   EvidenceKind = "external-string"
	EvidenceStringLiteral    EvidenceKind = "string-literal"
	EvidenceDefaultPrototype EvidenceKind = "default-prototype"
)

// EvidenceStep is one operation in a data-flow witness. NodeID is the
// deterministic syntax-node ID assigned during analysis (0 for semantic
// defaults).
type EvidenceStep struct {
	NodeID int
	Kind   EvidenceKind
	Detail string
}

func (s EvidenceStep) String() string {
	if s.NodeID == 0 {
		return string(s.Kind) + ": " + s.Detail
	}
	return strings.Join([]string{
		string(s.Kind),
		"@",
		itoa(s.NodeID),
		": ",
		s.Detail,
	}, "")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [24]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

const maxTraceSteps = 32

type trace []EvidenceStep

func (t trace) clone() trace {
	if len(t) == 0 {
		return nil
	}
	out := make(trace, len(t))
	copy(out, t)
	return out
}

func appendTrace(t trace, s EvidenceStep) trace {
	if len(t) > 0 {
		last := t[len(t)-1]
		if last == s {
			return t.clone()
		}
	}
	if len(t) >= maxTraceSteps {
		// Keep the root of the chain and the most recent operations.
		half := maxTraceSteps / 2
		out := make(trace, 0, maxTraceSteps)
		out = append(out, t[:half]...)
		out = append(out, t[len(t)-(maxTraceSteps-half-1):]...)
		out = append(out, s)
		return out
	}
	out := make(trace, 0, len(t)+1)
	out = append(out, t...)
	out = append(out, s)
	return out
}

func joinTrace(a, b trace) trace {
	if len(a) == 0 {
		return b.clone()
	}
	if len(b) == 0 {
		return a.clone()
	}
	if traceLess(b, a) {
		return b.clone()
	}
	return a.clone()
}

func traceLess(a, b trace) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	for i := range a {
		if a[i] == b[i] {
			continue
		}
		if a[i].NodeID != b[i].NodeID {
			return a[i].NodeID < b[i].NodeID
		}
		if a[i].Kind != b[i].Kind {
			return a[i].Kind < b[i].Kind
		}
		return a[i].Detail < b[i].Detail
	}
	return false
}

// absVal is a finite disjunction of objects, strings, and an "other" value
// (undefined or a value outside the modeled string/object domain). Traces are
// witnesses only; they do not affect semantic state equality.
type absVal struct {
	objs  map[ObjectID]trace
	strs  map[string]trace
	other bool
}

func emptyVal() absVal { return absVal{} }

func otherVal() absVal { return absVal{other: true} }

func objectVal(id ObjectID, t trace) absVal {
	return absVal{objs: map[ObjectID]trace{id: t.clone()}}
}

func stringVal(s string, t trace) absVal {
	return absVal{strs: map[string]trace{s: t.clone()}}
}

func (v absVal) clone() absVal {
	out := absVal{other: v.other}
	if len(v.objs) > 0 {
		out.objs = make(map[ObjectID]trace, len(v.objs))
		for id, t := range v.objs {
			out.objs[id] = t.clone()
		}
	}
	if len(v.strs) > 0 {
		out.strs = make(map[string]trace, len(v.strs))
		for s, t := range v.strs {
			out.strs[s] = t.clone()
		}
	}
	return out
}

func (v *absVal) addObj(id ObjectID, t trace) {
	if v.objs == nil {
		v.objs = make(map[ObjectID]trace)
	}
	if old, ok := v.objs[id]; ok {
		v.objs[id] = joinTrace(old, t)
	} else {
		v.objs[id] = t.clone()
	}
}

func (v *absVal) addStr(s string, t trace) {
	if v.strs == nil {
		v.strs = make(map[string]trace)
	}
	if old, ok := v.strs[s]; ok {
		v.strs[s] = joinTrace(old, t)
	} else {
		v.strs[s] = t.clone()
	}
}

func (v absVal) withStep(s EvidenceStep) absVal {
	out := v.clone()
	for id, t := range out.objs {
		out.objs[id] = appendTrace(t, s)
	}
	for str, t := range out.strs {
		out.strs[str] = appendTrace(t, s)
	}
	return out
}

func (v absVal) isEmpty() bool {
	return len(v.objs) == 0 && len(v.strs) == 0 && !v.other
}

func (v absVal) onlyObject(id ObjectID) bool {
	if v.other || len(v.strs) != 0 || len(v.objs) != 1 {
		return false
	}
	_, ok := v.objs[id]
	return ok
}

func (v absVal) objects() []ObjectID {
	out := make([]ObjectID, 0, len(v.objs))
	for id := range v.objs {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (v absVal) strings() []string {
	out := make([]string, 0, len(v.strs))
	for s := range v.strs {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func joinVal(a, b absVal) absVal {
	out := absVal{other: a.other || b.other}
	if len(a.objs) > 0 || len(b.objs) > 0 {
		out.objs = make(map[ObjectID]trace, len(a.objs)+len(b.objs))
		for id, t := range a.objs {
			out.objs[id] = t.clone()
		}
		for id, t := range b.objs {
			if old, ok := out.objs[id]; ok {
				out.objs[id] = joinTrace(old, t)
			} else {
				out.objs[id] = t.clone()
			}
		}
	}
	if len(a.strs) > 0 || len(b.strs) > 0 {
		out.strs = make(map[string]trace, len(a.strs)+len(b.strs))
		for s, t := range a.strs {
			out.strs[s] = t.clone()
		}
		for s, t := range b.strs {
			if old, ok := out.strs[s]; ok {
				out.strs[s] = joinTrace(old, t)
			} else {
				out.strs[s] = t.clone()
			}
		}
	}
	return out
}

func sameVal(a, b absVal) bool {
	if a.other != b.other || len(a.objs) != len(b.objs) || len(a.strs) != len(b.strs) {
		return false
	}
	for id := range a.objs {
		if _, ok := b.objs[id]; !ok {
			return false
		}
	}
	for s := range a.strs {
		if _, ok := b.strs[s]; !ok {
			return false
		}
	}
	return true
}

func defaultProtoTrace() trace {
	return trace{{
		Kind:   EvidenceDefaultPrototype,
		Detail: "default shared prototype",
	}}
}

// objectState is the abstract heap record for one object.
type objectState struct {
	fields       map[string]absVal
	definite     map[string]bool
	unknownField absVal
	hasUnknown   bool
	protos       map[ObjectID]trace
	multi        bool
}

func newObjectState() *objectState {
	return &objectState{
		fields:   make(map[string]absVal),
		definite: make(map[string]bool),
		protos:   make(map[ObjectID]trace),
	}
}

func (o *objectState) clone() *objectState {
	out := newObjectState()
	out.multi = o.multi
	out.unknownField = o.unknownField.clone()
	out.hasUnknown = o.hasUnknown
	for k, v := range o.fields {
		out.fields[k] = v.clone()
	}
	for k, v := range o.definite {
		out.definite[k] = v
	}
	for id, t := range o.protos {
		out.protos[id] = t.clone()
	}
	return out
}

func (o *objectState) field(k string) (absVal, bool) {
	v, ok := o.fields[k]
	return v, ok
}

func (o *objectState) setFieldStrong(k string, v absVal) {
	o.fields[k] = v.clone()
	o.definite[k] = true
}

func (o *objectState) setFieldWeak(k string, v absVal) {
	if old, ok := o.fields[k]; ok {
		o.fields[k] = joinVal(old, v)
	} else {
		o.fields[k] = v.clone()
	}
	if _, ok := o.definite[k]; !ok {
		o.definite[k] = false
	}
}

func (o *objectState) addUnknown(v absVal) {
	if o.hasUnknown {
		o.unknownField = joinVal(o.unknownField, v)
	} else {
		o.unknownField = v.clone()
		o.hasUnknown = true
	}
}

func (o *objectState) addProto(id ObjectID, t trace) {
	if old, ok := o.protos[id]; ok {
		o.protos[id] = joinTrace(old, t)
	} else {
		o.protos[id] = t.clone()
	}
}

func (o *objectState) makeMulti() {
	if o.multi {
		return
	}
	o.multi = true
	// A second allocation of the same site introduces an instance without
	// any of the previous strong fields.
	for k := range o.definite {
		o.definite[k] = false
	}
}

// absState is a flow-sensitive abstract environment plus heap.
type absState struct {
	env  map[string]absVal
	heap map[ObjectID]*objectState
}

func newAbsState() *absState {
	st := &absState{
		env:  make(map[string]absVal),
		heap: make(map[ObjectID]*objectState),
	}
	shared := newObjectState()
	shared.setFieldStrong("constructor", objectVal(ConstructorID, nil))
	st.heap[SharedPrototypeID] = shared
	ctor := newObjectState()
	ctor.setFieldStrong("prototype", objectVal(SharedPrototypeID, nil))
	ctor.addProto(SharedPrototypeID, defaultProtoTrace())
	st.heap[ConstructorID] = ctor
	return st
}

func (st *absState) clone() *absState {
	out := &absState{
		env:  make(map[string]absVal, len(st.env)),
		heap: make(map[ObjectID]*objectState, len(st.heap)),
	}
	for k, v := range st.env {
		out.env[k] = v.clone()
	}
	for id, o := range st.heap {
		out.heap[id] = o.clone()
	}
	return out
}

func (st *absState) readVar(name string) absVal {
	if v, ok := st.env[name]; ok {
		return v.clone()
	}
	return otherVal()
}

func (st *absState) writeVar(name string, v absVal) {
	st.env[name] = v.clone()
}

func (st *absState) object(id ObjectID) *objectState {
	o := st.heap[id]
	if o == nil {
		o = newObjectState()
		st.heap[id] = o
	}
	return o
}

func joinStates(a, b *absState) *absState {
	if a == nil {
		return b.clone()
	}
	if b == nil {
		return a.clone()
	}
	out := &absState{
		env:  make(map[string]absVal, len(a.env)+len(b.env)),
		heap: make(map[ObjectID]*objectState, len(a.heap)+len(b.heap)),
	}
	for k, v := range a.env {
		if vb, ok := b.env[k]; ok {
			out.env[k] = joinVal(v, vb)
		} else {
			out.env[k] = joinVal(v, otherVal())
		}
	}
	for k, v := range b.env {
		if _, ok := a.env[k]; !ok {
			out.env[k] = joinVal(otherVal(), v)
		}
	}
	for id, oa := range a.heap {
		if ob, ok := b.heap[id]; ok {
			out.heap[id] = joinObjectStates(oa, ob)
		} else {
			out.heap[id] = oa.clone()
		}
	}
	for id, ob := range b.heap {
		if _, ok := out.heap[id]; !ok {
			out.heap[id] = ob.clone()
		}
	}
	return out
}

func joinObjectStates(a, b *objectState) *objectState {
	out := newObjectState()
	out.multi = a.multi || b.multi
	if a.hasUnknown || b.hasUnknown {
		out.hasUnknown = true
		switch {
		case a.hasUnknown && b.hasUnknown:
			out.unknownField = joinVal(a.unknownField, b.unknownField)
		case a.hasUnknown:
			out.unknownField = a.unknownField.clone()
		default:
			out.unknownField = b.unknownField.clone()
		}
	}
	for k, va := range a.fields {
		if vb, ok := b.fields[k]; ok {
			out.fields[k] = joinVal(va, vb)
			out.definite[k] = a.definite[k] && b.definite[k]
		} else {
			out.fields[k] = va.clone()
			out.definite[k] = false
		}
	}
	for k, vb := range b.fields {
		if _, ok := out.fields[k]; ok {
			continue
		}
		out.fields[k] = vb.clone()
		out.definite[k] = false
	}
	for id, t := range a.protos {
		out.protos[id] = t.clone()
	}
	for id, t := range b.protos {
		if old, ok := out.protos[id]; ok {
			out.protos[id] = joinTrace(old, t)
		} else {
			out.protos[id] = t.clone()
		}
	}
	return out
}

func sameState(a, b *absState) bool {
	if len(a.env) != len(b.env) || len(a.heap) != len(b.heap) {
		return false
	}
	for k, va := range a.env {
		vb, ok := b.env[k]
		if !ok || !sameVal(va, vb) {
			return false
		}
	}
	for id, oa := range a.heap {
		ob, ok := b.heap[id]
		if !ok || !sameObjectState(oa, ob) {
			return false
		}
	}
	return true
}

func sameObjectState(a, b *objectState) bool {
	if a.multi != b.multi || a.hasUnknown != b.hasUnknown ||
		len(a.fields) != len(b.fields) || len(a.protos) != len(b.protos) {
		return false
	}
	if a.hasUnknown && !sameVal(a.unknownField, b.unknownField) {
		return false
	}
	for k, va := range a.fields {
		vb, ok := b.fields[k]
		if !ok || !sameVal(va, vb) || a.definite[k] != b.definite[k] {
			return false
		}
	}
	for id := range a.protos {
		if _, ok := b.protos[id]; !ok {
			return false
		}
	}
	return true
}

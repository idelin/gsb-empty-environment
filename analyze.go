package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// Certainty describes whether a reported write is guaranteed to target the
// shared prototype whenever the write executes. MayWrite is used for
// conservative branch/loop merges and for receivers that are only sometimes
// the shared prototype.
type Certainty string

const (
	MustWrite Certainty = "must"
	MayWrite  Certainty = "may"
)

// Witness is one concrete data-flow explanation for a finding.
type Witness struct {
	Certainty Certainty
	Keys      []string
	Steps     []EvidenceStep
}

// Finding reports one reachable potentially dangerous field write.
type Finding struct {
	Node      *AssignStmt
	NodeID    int
	Certainty Certainty
	Targets   []ObjectRef
	Keys      []string
	Witnesses []Witness
}

// Report is the result of Analyze.
type Report struct {
	Findings []Finding
}

// HasFinding reports whether the syntax node with id was flagged.
func (r *Report) HasFinding(id int) bool {
	if r == nil {
		return false
	}
	for _, f := range r.Findings {
		if f.NodeID == id {
			return true
		}
	}
	return false
}

// NodeIDs returns all flagged syntax-node IDs in ascending order.
func (r *Report) NodeIDs() []int {
	if r == nil {
		return nil
	}
	out := make([]int, 0, len(r.Findings))
	for _, f := range r.Findings {
		out = append(out, f.NodeID)
	}
	sort.Ints(out)
	return out
}

// Analyze checks a restricted script for reachable writes to the shared
// prototype. It performs flow-sensitive alias and string-set propagation and
// reports data-flow witnesses. Branch joins deliberately over-approximate;
// such findings are marked MayWrite.
func Analyze(p *Program) (*Report, error) {
	if err := Validate(p); err != nil {
		return nil, err
	}
	ix := indexProgram(p)
	a := &analyzer{
		ix:       ix,
		findings: make(map[*AssignStmt]*findingAgg),
	}
	st := newAbsState()
	for _, s := range p.Stmts {
		a.stmt(s, st)
	}
	return a.report(), nil
}

type analyzer struct {
	ix       *programIndex
	inLoop   int
	findings map[*AssignStmt]*findingAgg
}

type findingAgg struct {
	node   *AssignStmt
	nodeID int

	anyDangerous bool
	allMust      bool
	sawSafe      bool
	keys         map[string]struct{}
	witnesses    []Witness
}

func (a *analyzer) report() *Report {
	aggs := make([]*findingAgg, 0, len(a.findings))
	for _, agg := range a.findings {
		if agg.anyDangerous {
			aggs = append(aggs, agg)
		}
	}
	sort.Slice(aggs, func(i, j int) bool { return aggs[i].nodeID < aggs[j].nodeID })

	rep := &Report{Findings: make([]Finding, 0, len(aggs))}
	for _, agg := range aggs {
		certainty := MustWrite
		if agg.sawSafe || !agg.allMust {
			certainty = MayWrite
		}
		keys := sortedStringSet(agg.keys)
		sort.Slice(agg.witnesses, func(i, j int) bool {
			if agg.witnesses[i].Certainty != agg.witnesses[j].Certainty {
				return agg.witnesses[i].Certainty == MustWrite
			}
			return witnessText(agg.witnesses[i]) < witnessText(agg.witnesses[j])
		})
		if len(agg.witnesses) > 8 {
			agg.witnesses = agg.witnesses[:8]
		}
		rep.Findings = append(rep.Findings, Finding{
			Node:      agg.node,
			NodeID:    agg.nodeID,
			Certainty: certainty,
			Targets: []ObjectRef{{
				ID:    SharedPrototypeID,
				Kind:  KindSharedPrototype,
				Label: "shared prototype",
			}},
			Keys:      keys,
			Witnesses: agg.witnesses,
		})
	}
	return rep
}

func witnessText(w Witness) string {
	var b strings.Builder
	b.WriteString(string(w.Certainty))
	b.WriteString("|")
	b.WriteString(strings.Join(w.Keys, ","))
	for _, s := range w.Steps {
		b.WriteString("|")
		b.WriteString(s.String())
	}
	return b.String()
}

func (a *analyzer) nodeID(n Node) int {
	if n == nil {
		return 0
	}
	return a.ix.nodeIDs[n]
}

func (a *analyzer) stmt(s Stmt, st *absState) {
	if s == nil {
		return
	}
	switch x := s.(type) {
	case *AssignStmt:
		a.assign(x, st)
	case *ExprStmt:
		a.expr(x.X, st)
	case *IfStmt:
		a.ifStmt(x, st)
	case *WhileStmt:
		a.whileStmt(x, st)
	case *RepeatStmt:
		a.repeatStmt(x, st)
	}
}

func (a *analyzer) block(stmts []Stmt, st *absState) {
	for _, s := range stmts {
		a.stmt(s, st)
	}
}

func (a *analyzer) assign(x *AssignStmt, st *absState) {
	switch target := x.Target.(type) {
	case *VarExpr:
		v := a.expr(x.Value, st)
		step := EvidenceStep{
			NodeID: a.nodeID(x),
			Kind:   EvidenceAlias,
			Detail: fmt.Sprintf("alias %s = %s", target.Name, ExprString(x.Value)),
		}
		st.writeVar(target.Name, v.withStep(step))
	case *FieldExpr:
		base := a.expr(target.Base, st)
		key := a.expr(target.Key, st)
		value := a.expr(x.Value, st)

		dangerous, must := dangerousBase(base)
		if dangerous {
			a.recordFinding(x, base, key, must)
		} else {
			a.recordSafeVisit(x)
		}
		a.writeField(st, base, key, value)
	}
}

func dangerousBase(base absVal) (dangerous, must bool) {
	if _, ok := base.objs[SharedPrototypeID]; !ok {
		return false, false
	}
	must = len(base.objs) == 1 && !base.other && len(base.strs) == 0
	return true, must
}

func (a *analyzer) recordSafeVisit(x *AssignStmt) {
	agg := a.agg(x)
	agg.sawSafe = true
}

func (a *analyzer) agg(x *AssignStmt) *findingAgg {
	agg := a.findings[x]
	if agg == nil {
		agg = &findingAgg{
			node:    x,
			nodeID:  a.nodeID(x),
			allMust: true,
			keys:    make(map[string]struct{}),
		}
		a.findings[x] = agg
	}
	return agg
}

func (a *analyzer) recordFinding(x *AssignStmt, base, key absVal, must bool) {
	agg := a.agg(x)
	agg.anyDangerous = true
	if !must {
		agg.allMust = false
	}

	keys := key.strings()
	if len(keys) == 0 || key.other || len(key.objs) > 0 {
		keys = append(keys, "<unknown>")
	}
	for _, k := range keys {
		agg.keys[k] = struct{}{}
	}

	certainty := MayWrite
	if must {
		certainty = MustWrite
	}

	baseTrace := base.objs[SharedPrototypeID].clone()
	var keyTrace trace
	best := -1
	for _, k := range key.strings() {
		t := key.strs[k]
		if best < 0 || traceLess(t, keyTrace) {
			best = 0
			keyTrace = t.clone()
		}
	}
	steps := make([]EvidenceStep, 0, len(baseTrace)+len(keyTrace)+1)
	steps = append(steps, baseTrace...)
	steps = append(steps, keyTrace...)
	steps = append(steps, EvidenceStep{
		NodeID: a.nodeID(x),
		Kind:   EvidenceFieldWrite,
		Detail: fmt.Sprintf("%s (receiver is shared prototype)", StmtString(x)),
	})
	w := Witness{Certainty: certainty, Keys: keys, Steps: steps}
	if !a.hasWitness(agg, w) {
		agg.witnesses = append(agg.witnesses, w)
	}
}

func (a *analyzer) hasWitness(agg *findingAgg, w Witness) bool {
	text := witnessText(w)
	for _, old := range agg.witnesses {
		if witnessText(old) == text {
			return true
		}
	}
	return false
}

func (a *analyzer) ifStmt(x *IfStmt, st *absState) {
	truth := a.cond(x.Cond, st)
	condState := st.clone()

	var thenOut, elseOut *absState
	if truth.maybeTrue {
		thenState := condState.clone()
		a.block(x.Then, thenState)
		thenOut = thenState
	}
	if truth.maybeFalse {
		elseState := condState.clone()
		a.block(x.Else, elseState)
		elseOut = elseState
	}
	switch {
	case thenOut != nil && elseOut != nil:
		replaceState(st, joinStates(thenOut, elseOut))
	case thenOut != nil:
		replaceState(st, thenOut)
	case elseOut != nil:
		replaceState(st, elseOut)
	default:
		replaceState(st, condState)
	}
}

func (a *analyzer) whileStmt(x *WhileStmt, st *absState) {
	if x.MaxIterations <= 0 {
		return
	}
	// The loop body is analyzed outside-in, one bounded iteration at a time.
	// Weak updates below prevent an iteration from discarding facts that
	// another concrete iteration may still need.
	a.inLoop++
	defer func() { a.inLoop-- }()

	condState := st.clone()
	truth := a.cond(x.Cond, condState)

	var result *absState
	if truth.maybeFalse {
		result = condState.clone()
	}
	if !truth.maybeTrue {
		replaceState(st, condState)
		return
	}

	current := condState.clone()
	for i := 0; i < x.MaxIterations; i++ {
		bodyOut := current.clone()
		a.block(x.Body, bodyOut)
		result = joinStates(result, bodyOut)

		next := bodyOut.clone()
		nextTruth := a.cond(x.Cond, next)
		if !nextTruth.maybeTrue {
			break
		}
		if nextTruth.maybeFalse {
			result = joinStates(result, next.clone())
		}
		current = next
	}
	if result == nil {
		result = st.clone()
	}
	replaceState(st, result)
}

func (a *analyzer) repeatStmt(x *RepeatStmt, st *absState) {
	a.inLoop++
	defer func() { a.inLoop-- }()
	for i := 0; i < x.Times; i++ {
		a.block(x.Body, st)
	}
}

func replaceState(dst, src *absState) {
	dst.env = src.env
	dst.heap = src.heap
}

// truth is a conservative set of possible boolean outcomes.
type truth struct {
	maybeTrue  bool
	maybeFalse bool
}

func (a *analyzer) cond(e Expr, st *absState) truth {
	if e == nil {
		return truth{maybeTrue: true, maybeFalse: true}
	}
	switch x := e.(type) {
	case *EqExpr:
		left := a.expr(x.Left, st)
		right := a.expr(x.Right, st)
		return equalityPossibilities(left, right, st)
	case *NotExpr:
		t := a.cond(x.X, st)
		return truth{maybeTrue: t.maybeFalse, maybeFalse: t.maybeTrue}
	case *AndExpr:
		left := a.cond(x.Left, st)
		right := a.cond(x.Right, st)
		return truth{
			maybeTrue:  left.maybeTrue && right.maybeTrue,
			maybeFalse: left.maybeFalse || right.maybeFalse,
		}
	case *OrExpr:
		left := a.cond(x.Left, st)
		right := a.cond(x.Right, st)
		return truth{
			maybeTrue:  left.maybeTrue || right.maybeTrue,
			maybeFalse: left.maybeFalse && right.maybeFalse,
		}
	default:
		v := a.expr(e, st)
		return truthFromValue(v)
	}
}

func truthFromValue(v absVal) truth {
	t := truth{}
	if len(v.objs) > 0 || len(v.strs) > 0 || v.other {
		t.maybeTrue = true
	}
	if v.other || v.isEmpty() {
		t.maybeFalse = true
	}
	if !t.maybeTrue && !t.maybeFalse {
		t.maybeTrue = true
		t.maybeFalse = true
	}
	return t
}

func equalityPossibilities(a, b absVal, st *absState) truth {
	if a.isEmpty() || b.isEmpty() {
		return truth{maybeTrue: true, maybeFalse: true}
	}
	// "other" includes undefined and boolean values. Since their exact
	// equality is not modeled, keep both outcomes possible.
	if a.other || b.other {
		return truth{maybeTrue: true, maybeFalse: true}
	}
	t := truth{}
	for sa := range a.strs {
		for sb := range b.strs {
			if sa == sb {
				t.maybeTrue = true
			} else {
				t.maybeFalse = true
			}
		}
	}
	for oa := range a.objs {
		for ob := range b.objs {
			if oa == ob {
				t.maybeTrue = true
				// One allocation site may have produced several runtime
				// instances (for example, a New inside a loop). Two
				// references to the same abstract object are then not
				// necessarily the same concrete object.
				if o := st.heap[oa]; o == nil || o.multi {
					t.maybeFalse = true
				}
			} else {
				t.maybeFalse = true
			}
		}
	}
	// Values of different kinds are unequal.
	if len(a.strs) > 0 && (len(b.objs) > 0 || b.other) {
		t.maybeFalse = true
	}
	if len(b.strs) > 0 && (len(a.objs) > 0 || a.other) {
		t.maybeFalse = true
	}
	if len(a.objs) > 0 && len(b.strs) > 0 {
		t.maybeFalse = true
	}
	if len(b.objs) > 0 && len(a.strs) > 0 {
		t.maybeFalse = true
	}
	if !t.maybeTrue && !t.maybeFalse {
		// Should only happen for impossible/empty values; be conservative.
		t.maybeTrue = true
		t.maybeFalse = true
	}
	return t
}

func (a *analyzer) expr(e Expr, st *absState) absVal {
	if e == nil {
		return otherVal()
	}
	switch x := e.(type) {
	case *VarExpr:
		return st.readVar(x.Name)
	case *NewExpr:
		id, ok := a.ix.objects[x]
		if !ok {
			return otherVal()
		}
		obj, existed := st.heap[id]
		if existed {
			obj.makeMulti()
			obj.addProto(SharedPrototypeID, defaultProtoTrace())
		} else {
			obj = newObjectState()
			obj.addProto(SharedPrototypeID, defaultProtoTrace())
			st.heap[id] = obj
		}
		step := EvidenceStep{
			NodeID: a.nodeID(x),
			Kind:   EvidenceAllocate,
			Detail: fmt.Sprintf("allocate %s", a.ix.objectRef(id).Label),
		}
		return objectVal(id, trace{step})
	case *StringExpr:
		step := EvidenceStep{
			NodeID: a.nodeID(x),
			Kind:   EvidenceStringLiteral,
			Detail: fmt.Sprintf("string %q", x.Value),
		}
		return stringVal(x.Value, trace{step})
	case *ExternalStringExpr:
		v := emptyVal()
		label := x.Label
		if label == "" {
			label = fmt.Sprintf("external@%d", a.nodeID(x))
		}
		for _, s := range stringDomain {
			step := EvidenceStep{
				NodeID: a.nodeID(x),
				Kind:   EvidenceExternalString,
				Detail: fmt.Sprintf("external %s may be %q", label, s),
			}
			v.addStr(s, trace{step})
		}
		return v
	case *FieldExpr:
		base := a.expr(x.Base, st)
		key := a.expr(x.Key, st)
		return a.readField(st, base, key, a.nodeID(x))
	case *EqExpr, *NotExpr, *AndExpr, *OrExpr:
		// Boolean expressions are consumed by cond. If used as a value, the
		// precise bool is outside the string/object domain.
		return otherVal()
	default:
		return otherVal()
	}
}

func (a *analyzer) readField(st *absState, base, key absVal, nodeID int) absVal {
	out := emptyVal()

	for id, bt := range base.objs {
		for _, k := range key.strings() {
			out = joinVal(out, a.readObjectKey(st, id, k, bt, nodeID))
		}
		if key.other || len(key.objs) > 0 || len(key.strings()) == 0 {
			out = joinVal(out, a.readObjectUnknown(st, id, bt, nodeID))
		}
	}
	if base.other || len(base.strs) > 0 {
		out.other = true
	}
	if out.isEmpty() {
		out.other = true
	}
	return out
}

func (a *analyzer) readObjectKey(st *absState, id ObjectID, key string, bt trace, nodeID int) absVal {
	obj := st.heap[id]
	if obj == nil {
		return otherVal()
	}

	if key == "__proto__" {
		out := emptyVal()
		for proto := range obj.protos {
			step := EvidenceStep{
				NodeID: nodeID,
				Kind:   EvidencePrototypeRead,
				Detail: "read __proto__",
			}
			out.addObj(proto, appendTrace(bt, step))
		}
		if out.isEmpty() {
			out.other = true
		}
		return out
	}

	out := emptyVal()
	missing := false
	type item struct {
		id ObjectID
		tr trace
	}
	queue := []item{{id: id, tr: bt}}
	visited := make(map[ObjectID]bool)

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if visited[cur.id] {
			missing = true
			continue
		}
		visited[cur.id] = true
		o := st.heap[cur.id]
		if o == nil {
			missing = true
			continue
		}

		field, hasField := o.field(key)
		readStep := EvidenceStep{
			NodeID: nodeID,
			Kind:   EvidenceFieldRead,
			Detail: fmt.Sprintf("read [%q]", key),
		}
		if key == "constructor" {
			readStep.Kind = EvidenceConstructorRead
			readStep.Detail = "read constructor"
		}
		if hasField {
			if field.isEmpty() {
				out.other = true
			} else {
				out = joinVal(out, field.withStep(readStep))
			}
		}

		if cur.id == SharedPrototypeID && key == "constructor" {
			// The default constructor is an own property of the shared
			// prototype. An explicit write may shadow it, but a may-write
			// still leaves executions in which the default is visible.
			if !hasField || !o.definite[key] {
				step := EvidenceStep{
					NodeID: nodeID,
					Kind:   EvidenceConstructorRead,
					Detail: "read constructor -> fixed constructor object",
				}
				out.addObj(ConstructorID, appendTrace(cur.tr, step))
			}
			continue
		}

		if hasField && o.definite[key] {
			continue
		}
		if len(o.protos) == 0 {
			missing = true
			continue
		}
		for proto := range o.protos {
			step := EvidenceStep{
				NodeID: nodeID,
				Kind:   EvidencePrototypeFollow,
				Detail: fmt.Sprintf("follow prototype to %s", a.ix.objectRef(proto).Label),
			}
			queue = append(queue, item{id: proto, tr: appendTrace(cur.tr, step)})
		}
	}
	if missing {
		out.other = true
	}
	if out.isEmpty() {
		out.other = true
	}
	return out
}

func (a *analyzer) readObjectUnknown(st *absState, id ObjectID, bt trace, nodeID int) absVal {
	out := emptyVal()
	type item struct {
		id ObjectID
		tr trace
	}
	queue := []item{{id: id, tr: bt}}
	visited := make(map[ObjectID]bool)
	missing := false
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if visited[cur.id] {
			missing = true
			continue
		}
		visited[cur.id] = true
		o := st.heap[cur.id]
		if o == nil {
			missing = true
			continue
		}
		if o.hasUnknown {
			step := EvidenceStep{
				NodeID: nodeID,
				Kind:   EvidenceFieldRead,
				Detail: "read non-domain property key",
			}
			if o.unknownField.isEmpty() {
				out.other = true
			} else {
				out = joinVal(out, o.unknownField.withStep(step))
			}
		}
		for proto := range o.protos {
			step := EvidenceStep{
				NodeID: nodeID,
				Kind:   EvidencePrototypeFollow,
				Detail: fmt.Sprintf("follow prototype to %s", a.ix.objectRef(proto).Label),
			}
			queue = append(queue, item{id: proto, tr: appendTrace(cur.tr, step)})
		}
	}
	if missing || out.isEmpty() {
		out.other = true
	}
	return out
}

func (a *analyzer) writeField(st *absState, base, key, value absVal) {
	known := key.strings()
	unknown := key.other || len(key.objs) > 0 || len(known) == 0

	for id := range base.objs {
		obj := st.object(id)
		for _, k := range known {
			if k == "__proto__" {
				a.writeProto(st, id, base, key, value, known)
				continue
			}
			if a.canStrong(st, base, key, id) {
				obj.setFieldStrong(k, value)
			} else {
				obj.setFieldWeak(k, value)
			}
		}
		if unknown {
			obj.addUnknown(value)
		}
	}
}

func (a *analyzer) writeProto(st *absState, id ObjectID, base, key, value absVal, known []string) {
	obj := st.object(id)
	if len(value.objs) == 0 {
		return
	}
	onlyThisKey := len(known) == 1 && known[0] == "__proto__" &&
		!key.other && len(key.objs) == 0
	onlyObjects := !value.other && len(value.strs) == 0
	if onlyThisKey && onlyObjects && a.canStrong(st, base, key, id) {
		obj.protos = make(map[ObjectID]trace, len(value.objs))
		for proto, pt := range value.objs {
			obj.protos[proto] = pt.clone()
		}
		return
	}
	for proto, pt := range value.objs {
		obj.addProto(proto, pt)
	}
}

func (a *analyzer) canStrong(st *absState, base, key absVal, id ObjectID) bool {
	if a.inLoop > 0 {
		return false
	}
	if !base.onlyObject(id) {
		return false
	}
	if key.other || len(key.objs) > 0 || len(key.strs) != 1 {
		return false
	}
	if obj := st.heap[id]; obj != nil && obj.multi {
		return false
	}
	return true
}

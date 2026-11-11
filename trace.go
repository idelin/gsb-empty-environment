package netsec

import (
	"fmt"
	"sort"
)

// AccessKind classifies how an event touches an object.
type AccessKind int

const (
	Read AccessKind = iota
	Write
)

// Access is a single event-to-object interaction.
//
// Secret marks security-relevant state. A write to a secret object conflicts
// exactly like any other write: any two events that touch the same object and
// include at least one write conflict, including writes to secret state. Two
// pure reads of the same object never conflict, secret object or not.
type Access struct {
	Object string
	Kind   AccessKind
	Secret bool
}

// Event is one step of a finite trace: a Subject performing reads and writes.
// EventID must be unique within a trace.
type Event struct {
	ID      EventID
	Subject string
	Access  []Access
}

// Edge is an explicit send/receive causality edge From -> To. Both endpoints
// must refer to events inside the same trace.
type Edge struct {
	From EventID
	To   EventID
}

// Trace is a finite, ordered execution. Events are listed in execution order;
// Edges carry the send/receive causality structure.
type Trace struct {
	Events []Event
	Edges  []Edge
}

// Swap records one adjacent swap of two independent events.
type Swap struct {
	Step     int
	Position int
	Left     EventID
	Right    EventID
	Reason   IndependenceCheck
}

// IndependenceCheck is the machine-checkable proof that two events may swap:
// no shared conflict and neither happens-before direction.
type IndependenceCheck struct {
	A               EventID
	B               EventID
	SharedConflict  bool
	HappensBeforeAB bool
	HappensBeforeBA bool
	SameSubject     bool
	Independent     bool
}

// Certificate witnesses that one ordering can be transformed into another using
// only adjacent swaps of independent events.
type Certificate struct {
	Source        []EventID
	Target        []EventID
	Swaps         []Swap
	FinalOrder    []EventID
	Valid         bool
	InvalidStep   int
	InvalidReason string
}

// ViolationKind enumerates the ways two traces can fail equivalence.
type ViolationKind int

const (
	ViolationEventSet ViolationKind = iota
	ViolationSubject
	ViolationEdges
	ViolationConflictOrder
	ViolationCausalOrder
)

// Violation localizes one necessary-order or structural difference. Witness is
// the happens-before chain forcing the order, when one exists.
type Violation struct {
	Kind    ViolationKind
	A       EventID
	B       EventID
	Object  string
	Witness []EventID
	Detail  string
}

// Report is the result of comparing two finite traces.
type Report struct {
	Equivalent  bool
	Violations  []Violation
	Certificate *Certificate
}

type eventData struct {
	event   Event
	index   int
	subject int
}

type compiledTrace struct {
	trace    Trace
	byID     map[EventID]*eventData
	order    []EventID
	hb       map[EventID]map[EventID]bool
	conflict map[EventID]map[EventID]string
	adj      map[EventID][]EventID
}

func compile(t Trace) (*compiledTrace, error) {
	c := &compiledTrace{
		trace:    t,
		byID:     make(map[EventID]*eventData),
		order:    make([]EventID, 0, len(t.Events)),
		hb:       make(map[EventID]map[EventID]bool),
		conflict: make(map[EventID]map[EventID]string),
		adj:      make(map[EventID][]EventID),
	}

	subjectRank := make(map[string]int)
	for i, ev := range t.Events {
		if _, dup := c.byID[ev.ID]; dup {
			return nil, fmt.Errorf("trace: duplicate event id %q", ev.ID)
		}
		rank, ok := subjectRank[ev.Subject]
		if !ok {
			rank = len(subjectRank)
			subjectRank[ev.Subject] = rank
		}
		c.byID[ev.ID] = &eventData{event: ev, index: i, subject: rank}
		c.order = append(c.order, ev.ID)
		c.hb[ev.ID] = make(map[EventID]bool)
		c.conflict[ev.ID] = make(map[EventID]string)
	}

	for _, e := range t.Edges {
		if _, ok := c.byID[e.From]; !ok {
			return nil, fmt.Errorf("trace: edge from unknown event %q", e.From)
		}
		if _, ok := c.byID[e.To]; !ok {
			return nil, fmt.Errorf("trace: edge to unknown event %q", e.To)
		}
		if e.From == e.To {
			return nil, fmt.Errorf("trace: self loop on event %q", e.From)
		}
		c.adj[e.From] = append(c.adj[e.From], e.To)
		c.hb[e.From][e.To] = true
	}

	// Per-subject program order: consecutive events of one subject form a chain
	// that can never be reordered.
	lastBySubject := make(map[string]EventID)
	for _, ev := range t.Events {
		if prev, ok := lastBySubject[ev.Subject]; ok {
			if !c.hb[prev][ev.ID] {
				c.adj[prev] = append(c.adj[prev], ev.ID)
			}
			c.hb[prev][ev.ID] = true
		}
		lastBySubject[ev.Subject] = ev.ID
	}

	// Transitive closure over program order and send/receive edges.
	for _, k := range c.order {
		for _, i := range c.order {
			if !c.hb[i][k] {
				continue
			}
			for _, j := range c.order {
				if c.hb[k][j] {
					c.hb[i][j] = true
				}
			}
		}
	}
	for _, id := range c.order {
		if c.hb[id][id] {
			return nil, fmt.Errorf("trace: causality cycle involving event %q", id)
		}
	}

	// Conflicts: shared object with at least one write. Secret-state writes are
	// never special-cased away; they conflict just as non-secret writes do.
	for x := 0; x < len(c.order); x++ {
		for y := x + 1; y < len(c.order); y++ {
			idA, idB := c.order[x], c.order[y]
			if obj, shared := sharedConflictObject(t.Events[x], t.Events[y]); shared {
				c.conflict[idA][idB] = obj
				c.conflict[idB][idA] = obj
			}
		}
	}
	return c, nil
}

func sharedConflictObject(a, b Event) (string, bool) {
	access := func(ev Event) map[string]AccessKind {
		m := make(map[string]AccessKind)
		for _, acc := range ev.Access {
			if acc.Kind == Write {
				m[acc.Object] = Write
			} else if _, seen := m[acc.Object]; !seen {
				m[acc.Object] = Read
			}
		}
		return m
	}
	aa, bb := access(a), access(b)
	objects := make([]string, 0, len(aa))
	for obj := range aa {
		objects = append(objects, obj)
	}
	sort.Strings(objects)
	for _, obj := range objects {
		kb, ok := bb[obj]
		if !ok {
			continue
		}
		if aa[obj] == Write || kb == Write {
			return obj, true
		}
	}
	return "", false
}

// Independent reports whether two events may be swapped in an interleaving.
// Events are independent exactly when they belong to different subjects, do
// not conflict on any object, and neither happens-before the other.
func Independent(t Trace, a, b EventID) (IndependenceCheck, error) {
	c, err := compile(t)
	if err != nil {
		return IndependenceCheck{}, err
	}
	if _, ok := c.byID[a]; !ok {
		return IndependenceCheck{}, fmt.Errorf("trace: unknown event id %q", a)
	}
	if _, ok := c.byID[b]; !ok {
		return IndependenceCheck{}, fmt.Errorf("trace: unknown event id %q", b)
	}
	return c.independent(a, b), nil
}

func (c *compiledTrace) independent(a, b EventID) IndependenceCheck {
	_, conflicted := c.conflict[a][b]
	check := IndependenceCheck{
		A:               a,
		B:               b,
		SharedConflict:  conflicted,
		HappensBeforeAB: c.hb[a][b],
		HappensBeforeBA: c.hb[b][a],
		SameSubject:     c.byID[a].event.Subject == c.byID[b].event.Subject,
	}
	check.Independent = !check.SharedConflict &&
		!check.HappensBeforeAB && !check.HappensBeforeBA && !check.SameSubject
	return check
}

func (c *compiledTrace) hbPath(from, to EventID) []EventID {
	if !c.hb[from][to] {
		return nil
	}
	parent := map[EventID]EventID{from: from}
	queue := []EventID{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == to {
			break
		}
		for _, next := range c.adj[cur] {
			if _, seen := parent[next]; seen {
				continue
			}
			parent[next] = cur
			queue = append(queue, next)
		}
	}
	if _, ok := parent[to]; !ok {
		return nil
	}
	path := []EventID{to}
	for path[len(path)-1] != from {
		path = append(path, parent[path[len(path)-1]])
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

// Compare decides whether two finite traces are Mazurkiewicz equivalent: they
// contain the same events and causality structure, and every pair of events
// that conflicts or is related by happens-before keeps the same relative order
// in both traces. Equivalent traces additionally receive a swap certificate.
func Compare(a, b Trace) (*Report, error) {
	ca, err := compile(a)
	if err != nil {
		return nil, fmt.Errorf("first trace invalid: %w", err)
	}
	cb, err := compile(b)
	if err != nil {
		return nil, fmt.Errorf("second trace invalid: %w", err)
	}

	report := &Report{}

	if len(ca.order) != len(cb.order) || !sameIDSet(ca.order, cb.order) {
		onlyA, onlyB := setDifference(ca.order, cb.order)
		for _, id := range onlyA {
			report.Violations = append(report.Violations, Violation{
				Kind: ViolationEventSet, A: id,
				Detail: "event only present in first trace",
			})
		}
		for _, id := range onlyB {
			report.Violations = append(report.Violations, Violation{
				Kind: ViolationEventSet, B: id,
				Detail: "event only present in second trace",
			})
		}
		return report, nil
	}

	for _, id := range ca.order {
		sa := ca.byID[id].event.Subject
		sb := cb.byID[id].event.Subject
		if sa != sb {
			report.Violations = append(report.Violations, Violation{
				Kind: ViolationSubject, A: id,
				Detail: fmt.Sprintf("subject %q vs %q", sa, sb),
			})
		}
		if !sameAccesses(ca.byID[id].event.Access, cb.byID[id].event.Access) {
			report.Violations = append(report.Violations, Violation{
				Kind: ViolationSubject, A: id,
				Detail: "event access set differs between traces",
			})
		}
	}

	if !sameEdges(a.Edges, b.Edges) {
		onlyA, onlyB := edgeDifference(a.Edges, b.Edges)
		for _, e := range onlyA {
			report.Violations = append(report.Violations, Violation{
				Kind: ViolationEdges, A: e.From, B: e.To,
				Detail: "causality edge only present in first trace",
			})
		}
		for _, e := range onlyB {
			report.Violations = append(report.Violations, Violation{
				Kind: ViolationEdges, A: e.From, B: e.To,
				Detail: "causality edge only present in second trace",
			})
		}
	}

	posA := positions(ca.order)
	posB := positions(cb.order)

	// Conflict pairs must keep their order regardless of causality. Compare
	// positions symmetrically because a conflicting pair may itself be
	// happens-before ordered in either direction.
	for _, pair := range orderedPairs(ca, func(x, y EventID) bool {
		_, ok := ca.conflict[x][y]
		return ok
	}) {
		x, y := pair[0], pair[1]
		if (posA[x] < posA[y]) != (posB[x] < posB[y]) {
			first, second := x, y
			if posA[x] > posA[y] {
				first, second = y, x
			}
			report.Violations = append(report.Violations, Violation{
				Kind: ViolationConflictOrder, A: first, B: second,
				Object: ca.conflict[x][y],
				Detail: "conflicting events appear in reverse order",
			})
		}
	}

	// Happens-before pairs (program order and send/receive edges, transitively).
	for _, pair := range orderedPairs(ca, func(x, y EventID) bool { return ca.hb[x][y] }) {
		x, y := pair[0], pair[1]
		if posA[x] < posA[y] && posB[x] > posB[y] {
			kind := ViolationCausalOrder
			if _, isConflict := ca.conflict[x][y]; isConflict {
				kind = ViolationConflictOrder
			}
			report.Violations = append(report.Violations, Violation{
				Kind: kind, A: x, B: y,
				Object:  ca.conflict[x][y],
				Witness: ca.hbPath(x, y),
				Detail:  "happens-before order reversed",
			})
		}
	}

	if len(report.Violations) == 0 {
		report.Equivalent = true
		report.Certificate = buildCertificate(ca, cb)
	}
	return report, nil
}

func orderedPairs(c *compiledTrace, pred func(x, y EventID) bool) [][2]EventID {
	var pairs [][2]EventID
	for i, x := range c.order {
		for _, y := range c.order[i+1:] {
			if pred(x, y) {
				pairs = append(pairs, [2]EventID{x, y})
			}
		}
	}
	return pairs
}

func buildCertificate(ca, cb *compiledTrace) *Certificate {
	cert := &Certificate{
		Source: append([]EventID(nil), ca.order...),
		Target: append([]EventID(nil), cb.order...),
		Valid:  true,
	}
	current := append([]EventID(nil), ca.order...)
	target := append([]EventID(nil), cb.order...)
	step := 1

	for targetPos := 0; targetPos < len(target); targetPos++ {
		want := target[targetPos]
		curPos := indexOf(current, want)
		for curPos > targetPos {
			left, right := current[curPos-1], current[curPos]
			check := ca.independent(left, right)
			if !check.Independent {
				cert.Valid = false
				cert.InvalidStep = step
				cert.InvalidReason = fmt.Sprintf(
					"events %q and %q are not swappable", left, right)
				cert.FinalOrder = append([]EventID(nil), current...)
				return cert
			}
			cert.Swaps = append(cert.Swaps, Swap{
				Step: step, Position: curPos - 1,
				Left: left, Right: right, Reason: check,
			})
			current[curPos-1], current[curPos] = current[curPos], current[curPos-1]
			curPos--
			step++
		}
	}
	cert.FinalOrder = append([]EventID(nil), current...)
	return cert
}

// Verify replays a certificate against a source trace, checking every adjacent
// swap against the independence relation of the current ordering.
func Verify(t Trace, cert *Certificate) error {
	if cert == nil {
		return fmt.Errorf("verify: nil certificate")
	}
	c, err := compile(t)
	if err != nil {
		return err
	}
	current := append([]EventID(nil), c.order...)
	for _, swap := range cert.Swaps {
		if swap.Position < 0 || swap.Position+1 >= len(current) {
			return fmt.Errorf("verify: swap %d out of range", swap.Step)
		}
		left, right := current[swap.Position], current[swap.Position+1]
		if left != swap.Left || right != swap.Right {
			return fmt.Errorf("verify: swap %d does not match trace order", swap.Step)
		}
		check := c.independent(left, right)
		if !check.Independent {
			return fmt.Errorf("verify: swap %d exchanges dependent events %q and %q",
				swap.Step, left, right)
		}
		current[swap.Position], current[swap.Position+1] =
			current[swap.Position+1], current[swap.Position]
	}
	if cert.Target != nil && !sameIDSet(current, cert.Target) {
		return fmt.Errorf("verify: resulting event set differs from target")
	}
	return nil
}

func positions(order []EventID) map[EventID]int {
	m := make(map[EventID]int, len(order))
	for i, id := range order {
		m[id] = i
	}
	return m
}

func indexOf(order []EventID, id EventID) int {
	for i, v := range order {
		if v == id {
			return i
		}
	}
	return -1
}

func sameIDSet(a, b []EventID) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[EventID]int, len(a))
	for _, id := range a {
		set[id]++
	}
	for _, id := range b {
		set[id]--
		if set[id] < 0 {
			return false
		}
	}
	return true
}

func setDifference(a, b []EventID) (onlyA, onlyB []EventID) {
	countB := make(map[EventID]int, len(b))
	for _, id := range b {
		countB[id]++
	}
	seenA := make(map[EventID]int, len(a))
	for _, id := range a {
		seenA[id]++
		if seenA[id] > countB[id] {
			onlyA = append(onlyA, id)
		}
	}
	countA := make(map[EventID]int, len(a))
	for _, id := range a {
		countA[id]++
	}
	seenB := make(map[EventID]int, len(b))
	for _, id := range b {
		seenB[id]++
		if seenB[id] > countA[id] {
			onlyB = append(onlyB, id)
		}
	}
	return onlyA, onlyB
}

func sameAccesses(a, b []Access) bool {
	key := func(acc Access) string {
		return fmt.Sprintf("%s|%d|%t", acc.Object, acc.Kind, acc.Secret)
	}
	count := make(map[string]int, len(a)+len(b))
	for _, acc := range a {
		count[key(acc)]++
	}
	for _, acc := range b {
		count[key(acc)]--
		if count[key(acc)] < 0 {
			return false
		}
	}
	for _, v := range count {
		if v != 0 {
			return false
		}
	}
	return true
}

func edgeKey(e Edge) string {
	return string(e.From) + "->" + string(e.To)
}

func sameEdges(a, b []Edge) bool {
	if len(a) != len(b) {
		return false
	}
	count := make(map[string]int, len(a))
	for _, e := range a {
		count[edgeKey(e)]++
	}
	for _, e := range b {
		count[edgeKey(e)]--
		if count[edgeKey(e)] < 0 {
			return false
		}
	}
	for _, v := range count {
		if v != 0 {
			return false
		}
	}
	return true
}

func edgeDifference(a, b []Edge) (onlyA, onlyB []Edge) {
	countB := make(map[string]int, len(b))
	for _, e := range b {
		countB[edgeKey(e)]++
	}
	seenA := make(map[string]int, len(a))
	for _, e := range a {
		k := edgeKey(e)
		seenA[k]++
		if seenA[k] > countB[k] {
			onlyA = append(onlyA, e)
		}
	}
	countA := make(map[string]int, len(a))
	for _, e := range a {
		countA[edgeKey(e)]++
	}
	seenB := make(map[string]int, len(b))
	for _, e := range b {
		k := edgeKey(e)
		seenB[k]++
		if seenB[k] > countA[k] {
			onlyB = append(onlyB, e)
		}
	}
	return onlyA, onlyB
}

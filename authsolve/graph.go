package authsolve

import (
	"fmt"

	"prescreen.local/ps-e39099b0dc/clockwire"
)

// diffGraph is a system of difference constraints x[to]-x[from] <= weight.
// Such systems are solved exactly with Bellman-Ford; no offset value is
// enumerated.
type diffGraph struct {
	keys  []string
	index map[string]int
	edges []diffEdge
}

type diffEdge struct {
	from   int
	to     int
	weight int64
	tag    string
}

func newDiffGraph() *diffGraph {
	return &diffGraph{index: make(map[string]int)}
}

func (g *diffGraph) node(key string) int {
	if i, ok := g.index[key]; ok {
		return i
	}
	i := len(g.keys)
	g.keys = append(g.keys, key)
	g.index[key] = i
	return i
}

func (g *diffGraph) add(from, to string, weight int64, tag string) {
	g.edges = append(g.edges, diffEdge{
		from:   g.node(from),
		to:     g.node(to),
		weight: weight,
		tag:    tag,
	})
}

func (g *diffGraph) clone() *diffGraph {
	out := &diffGraph{
		keys:  append([]string(nil), g.keys...),
		index: make(map[string]int, len(g.index)),
		edges: append([]diffEdge(nil), g.edges...),
	}
	for k, v := range g.index {
		out.index[k] = v
	}
	return out
}

// feasible reports whether the system has a solution.  If it does not, the
// returned edges form a directed cycle of negative total weight.
func (g *diffGraph) feasible() (bool, []ProofEdge, error) {
	n := len(g.keys)
	if n == 0 {
		return true, nil, nil
	}
	dist := make([]int64, n)
	pred := make([]int, n)
	predEdge := make([]int, n)
	for i := range pred {
		pred[i] = -1
		predEdge[i] = -1
	}

	last := -1
	// Initialising every distance to zero is equivalent to adding a super
	// source with zero-weight edges to every node.
	for round := 0; round < n; round++ {
		changed := false
		for ei := range g.edges {
			e := &g.edges[ei]
			cand, err := clockwire.Add(dist[e.from], e.weight)
			if err != nil {
				return false, nil, fmt.Errorf("%w while relaxing %s", ErrSignedOverflow, e.tag)
			}
			if cand < dist[e.to] {
				dist[e.to] = cand
				pred[e.to] = e.from
				predEdge[e.to] = ei
				last = e.to
				changed = true
			}
		}
		if !changed {
			return true, nil, nil
		}
	}
	if last < 0 {
		return true, nil, nil
	}

	// Move n steps along predecessor pointers; this lands on a cycle.
	x := last
	for i := 0; i < n; i++ {
		if pred[x] < 0 {
			return false, nil, fmt.Errorf("%w: malformed negative cycle", ErrInvalidInput)
		}
		x = pred[x]
	}

	var reversed []ProofEdge
	start := x
	cur := x
	for i := 0; i <= n; i++ {
		ei := predEdge[cur]
		if ei < 0 {
			return false, nil, fmt.Errorf("%w: malformed negative cycle", ErrInvalidInput)
		}
		e := g.edges[ei]
		reversed = append(reversed, ProofEdge{
			From:   g.keys[e.from],
			To:     g.keys[e.to],
			Weight: e.weight,
		})
		cur = e.from
		if cur == start {
			break
		}
		if i == n {
			return false, nil, fmt.Errorf("%w: malformed negative cycle", ErrInvalidInput)
		}
	}

	// The witness was collected backwards; reverse it into traversal order.
	out := make([]ProofEdge, len(reversed))
	for i := range reversed {
		out[i] = reversed[len(reversed)-1-i]
	}
	return false, out, nil
}

func referenceDevice(p Problem) (string, error) {
	if p.ReferenceDevice != "" {
		return p.ReferenceDevice, nil
	}
	for _, d := range p.Devices {
		if d.Reference {
			return d.ID, nil
		}
	}
	if len(p.Devices) > 0 {
		// A host that does not name its reference still needs a zero-offset
		// anchor; the first device is the conventional choice.
		return p.Devices[0].ID, nil
	}
	// An unnamed reference still has offset zero.  It is usable by clock
	// constraints that leave Other empty.
	return "", nil
}

func buildOffsetGraph(p Problem) (*diffGraph, error) {
	ref, err := referenceDevice(p)
	if err != nil {
		return nil, err
	}
	g := newDiffGraph()
	g.node(ref)

	known := make(map[string]bool, len(p.Devices)+1)
	known[ref] = true
	for _, d := range p.Devices {
		if d.ID == "" {
			return nil, fmt.Errorf("%w: device with empty ID", ErrInvalidInput)
		}
		if d.ID == ref {
			continue
		}
		if known[d.ID] {
			return nil, fmt.Errorf("%w: duplicate device %q", ErrInvalidInput, d.ID)
		}
		known[d.ID] = true
		g.node(d.ID)
	}

	for i, c := range p.Clock {
		from := c.Device
		to := c.Other
		if to == "" {
			to = ref
		}
		if !known[from] {
			return nil, fmt.Errorf("%w: clock constraint %d uses unknown device %q", ErrInvalidInput, i, from)
		}
		if !known[to] {
			return nil, fmt.Errorf("%w: clock constraint %d uses unknown device %q", ErrInvalidInput, i, to)
		}
		if c.Bound.Empty() {
			return nil, fmt.Errorf("%w: clock constraint %d has empty bound [%d,%d]",
				ErrClockInconsistent, i, c.Bound.Lo, c.Bound.Hi)
		}
		// lo <= o_from - o_to <= hi
		// o_from - o_to <= hi          => edge to -> from, weight hi
		// o_to   - o_from <= -lo        => edge from -> to, weight -lo
		g.add(to, from, c.Bound.Hi, fmt.Sprintf("clock[%d].hi", i))
		negLo, err := clockwire.Neg(c.Bound.Lo)
		if err != nil {
			return nil, fmt.Errorf("%w in clock[%d]", ErrSignedOverflow, i)
		}
		g.add(from, to, negLo, fmt.Sprintf("clock[%d].lo", i))
	}
	return g, nil
}

func checkClock(p Problem) error {
	g, err := buildOffsetGraph(p)
	if err != nil {
		return err
	}
	ok, _, err := g.feasible()
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: negative cycle in offset evidence", ErrClockInconsistent)
	}
	return nil
}

// selectedDeviceBounds collapses all selected credentials on a device to that
// device's common real-time validity interval at offset zero:
//
//	[A_d - o_d, B_d - o_d]
func selectedDeviceBounds(p Problem, selected []Credential) (map[string]deviceBound, error) {
	out := make(map[string]deviceBound)
	devices := make(map[string]bool, len(p.Devices))
	for _, d := range p.Devices {
		devices[d.ID] = true
	}
	for _, c := range selected {
		if !devices[c.Device] {
			return nil, fmt.Errorf("%w: credential %q uses unknown device %q", ErrInvalidInput, c.ID, c.Device)
		}
		last, err := clockwire.Sub(c.NotAfter, 1)
		if err != nil {
			return nil, fmt.Errorf("%w in credential %q validity", ErrSignedOverflow, c.ID)
		}
		b, ok := out[c.Device]
		if !ok {
			out[c.Device] = deviceBound{A: c.NotBefore, B: last}
			continue
		}
		if c.NotBefore > b.A {
			b.A = c.NotBefore
		}
		if last < b.B {
			b.B = last
		}
		out[c.Device] = b
	}
	return out, nil
}

type deviceBound struct {
	A int64 // maximum NotBefore
	B int64 // minimum NotAfter-1
}

type failureAtoms struct {
	left  []FailureAtom
	gap   []FailureAtom
	right []FailureAtom
	empty []FailureAtom
}

func enumerateFailureAtoms(bounds map[string]deviceBound) failureAtoms {
	var a failureAtoms
	for d := range bounds {
		a.left = append(a.left, FailureAtom{Kind: FailureLeft, Device: d})
		a.right = append(a.right, FailureAtom{Kind: FailureRight, Device: d})
		a.gap = append(a.gap, FailureAtom{Kind: FailureGap, Device: d})
		for e := range bounds {
			a.empty = append(a.empty, FailureAtom{Kind: FailureEmpty, Device: d, Second: e})
		}
	}
	sortAtoms(a.left)
	sortAtoms(a.right)
	sortAtoms(a.gap)
	sortAtoms(a.empty)
	return a
}

func sortAtoms(in []FailureAtom) {
	sortSlice(in, func(a, b FailureAtom) bool {
		if a.Device != b.Device {
			return a.Device < b.Device
		}
		return a.Second < b.Second
	})
}

// sortSlice is a tiny generic-free insertion sort; atom lists are at most
// 12*12 elements and insertion order is otherwise stable enough.
func sortSlice[T any](in []T, less func(T, T) bool) {
	for i := 1; i < len(in); i++ {
		v := in[i]
		j := i - 1
		for j >= 0 && less(v, in[j]) {
			in[j+1] = in[j]
			j--
		}
		in[j+1] = v
	}
}

func atomKey(a FailureAtom) string {
	return string(a.Kind) + "\x00" + a.Device + "\x00" + a.Second
}

func addAtomConstraints(g *diffGraph, ref string, p Problem, bounds map[string]deviceBound, atom FailureAtom, w Window, w2 Window) error {
	addLe := func(from, to string, weight int64) {
		g.add(from, to, weight, "atom")
	}
	// Recompute all constants locally so every arithmetic operation is
	// checked for signed overflow.
	addLower := func(device string, value int64) error {
		// o_device >= value  <=>  o_ref - o_device <= -value
		neg, err := clockwire.Neg(value)
		if err != nil {
			return fmt.Errorf("%w in failure atom", ErrSignedOverflow)
		}
		addLe(device, ref, neg)
		return nil
	}
	addUpper := func(device string, value int64) {
		// o_device <= value  <=>  o_device - o_ref <= value
		addLe(ref, device, value)
	}

	switch atom.Kind {
	case FailureLeft:
		// U = min(B_e - o_e) < w.Start  =>  exists e: o_e >= B_e-w.Start+1
		v, err := clockwire.Sub(bounds[atom.Device].B, w.Start)
		if err != nil {
			return fmt.Errorf("%w in left atom", ErrSignedOverflow)
		}
		v, err = clockwire.Add(v, 1)
		if err != nil {
			return fmt.Errorf("%w in left atom", ErrSignedOverflow)
		}
		return addLower(atom.Device, v)

	case FailureRight:
		// L = max(A_d - o_d) >= w2.End  =>  exists d: o_d <= A_d-w2.End
		v, err := clockwire.Sub(bounds[atom.Device].A, w2.End)
		if err != nil {
			return fmt.Errorf("%w in right atom", ErrSignedOverflow)
		}
		addUpper(atom.Device, v)
		return nil

	case FailureGap:
		// L >= w.End and U < w2.Start.
		for d, b := range bounds {
			v, err := clockwire.Sub(b.A, w.End)
			if err != nil {
				return fmt.Errorf("%w in gap atom", ErrSignedOverflow)
			}
			addUpper(d, v)
		}
		v, err := clockwire.Sub(bounds[atom.Device].B, w2.Start)
		if err != nil {
			return fmt.Errorf("%w in gap atom", ErrSignedOverflow)
		}
		v, err = clockwire.Add(v, 1)
		if err != nil {
			return fmt.Errorf("%w in gap atom", ErrSignedOverflow)
		}
		return addLower(atom.Device, v)

	case FailureEmpty:
		// A_d-o_d > B_e-o_e  =>  o_d-o_e <= A_d-B_e-1
		v, err := clockwire.Sub(bounds[atom.Device].A, bounds[atom.Second].B)
		if err != nil {
			return fmt.Errorf("%w in empty atom", ErrSignedOverflow)
		}
		v, err = clockwire.Sub(v, 1)
		if err != nil {
			return fmt.Errorf("%w in empty atom", ErrSignedOverflow)
		}
		addLe(atom.Second, atom.Device, v)
		return nil

	default:
		return fmt.Errorf("%w: unknown failure atom %q", ErrInvalidInput, atom.Kind)
	}
}

func windowsOf(p Problem) (Window, Window, error) {
	if len(p.Windows) != 2 {
		return Window{}, Window{}, fmt.Errorf("%w: expected exactly two release windows, got %d",
			ErrInvalidInput, len(p.Windows))
	}
	w1, w2 := p.Windows[0], p.Windows[1]
	if w1.Start >= w1.End || w2.Start >= w2.End {
		return Window{}, Window{}, fmt.Errorf("%w: release windows must be non-empty half-open intervals", ErrInvalidInput)
	}
	if w1.Start > w2.Start {
		w1, w2 = w2, w1
	}
	if w1.End > w2.Start {
		return Window{}, Window{}, fmt.Errorf("%w: release windows overlap", ErrInvalidInput)
	}
	return w1, w2, nil
}

// witnessRobust checks the fixed-witness universal condition exactly.
//
// For a fixed selected set, the set of all possible common release times is
//
//	J(o) = [ max_d(A_d-o_d), min_d(B_d-o_d) ].
//
// J(o) misses the union of two windows only in one of four primitive ways:
// left of both, in the gap between them, right of both, or empty.  Each of
// those is a finite union of difference-constraint systems over the offsets.
// Testing every atom with Bellman-Ford decides the universal statement
// without enumerating offsets, and gives a Farkas negative cycle for every
// impossible atom.
func witnessRobust(p Problem, selected []Credential) (bool, []InfeasibilityCertificate, error) {
	w1, w2, err := windowsOf(p)
	if err != nil {
		return false, nil, err
	}
	base, err := buildOffsetGraph(p)
	if err != nil {
		return false, nil, err
	}
	ref, err := referenceDevice(p)
	if err != nil {
		return false, nil, err
	}
	bounds, err := selectedDeviceBounds(p, selected)
	if err != nil {
		return false, nil, err
	}
	if len(bounds) == 0 {
		// J is the whole integer line; the windows are non-empty.
		return true, nil, nil
	}

	atoms := enumerateFailureAtoms(bounds)
	all := make([]FailureAtom, 0, len(atoms.left)+len(atoms.gap)+len(atoms.right)+len(atoms.empty))
	all = append(all, atoms.left...)
	all = append(all, atoms.gap...)
	all = append(all, atoms.right...)
	all = append(all, atoms.empty...)

	certs := make([]InfeasibilityCertificate, 0, len(all))
	for _, atom := range all {
		g := base.clone()
		if err := addAtomConstraints(g, ref, p, bounds, atom, w1, w2); err != nil {
			return false, nil, err
		}
		ok, cycle, err := g.feasible()
		if err != nil {
			return false, nil, err
		}
		if ok {
			// Some allowed offset assignment makes the witness miss all
			// release windows.
			return false, nil, nil
		}
		certs = append(certs, InfeasibilityCertificate{Atom: atom, Edges: cycle})
	}
	return true, certs, nil
}

func verifyInfeasibilityCertificate(p Problem, bounds map[string]deviceBound, w1, w2 Window, cert InfeasibilityCertificate) error {
	if len(cert.Edges) == 0 {
		return fmt.Errorf("%w: empty negative-cycle certificate", ErrInvalidInput)
	}
	ref, err := referenceDevice(p)
	if err != nil {
		return err
	}
	base, err := buildOffsetGraph(p)
	if err != nil {
		return err
	}
	g := base.clone()
	if err := addAtomConstraints(g, ref, p, bounds, cert.Atom, w1, w2); err != nil {
		return err
	}

	// The certificate edges must be a closed directed walk, and each edge
	// must occur in the generated system with the stated weight.
	used := make([]bool, len(g.edges))
	var total int64
	for i, pe := range cert.Edges {
		next := cert.Edges[(i+1)%len(cert.Edges)]
		if pe.To != next.From {
			return fmt.Errorf("%w: certificate edges do not form a cycle", ErrInvalidInput)
		}
		found := false
		for ei, e := range g.edges {
			if used[ei] {
				continue
			}
			if g.keys[e.from] == pe.From && g.keys[e.to] == pe.To && e.weight == pe.Weight {
				used[ei] = true
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: certificate edge %q -> %q weight %d is not in the system",
				ErrInvalidInput, pe.From, pe.To, pe.Weight)
		}
		total, err = clockwire.Add(total, pe.Weight)
		if err != nil {
			return fmt.Errorf("%w in certificate sum", ErrSignedOverflow)
		}
	}
	if total >= 0 {
		return fmt.Errorf("%w: certificate cycle has non-negative weight %d", ErrInvalidInput, total)
	}
	return nil
}

// AtomCount returns the number of primitive time-failure atoms for n
// selected devices.  It is exposed mainly for diagnostics and tests.
func AtomCount(n int) int {
	return 3*n + n*n
}

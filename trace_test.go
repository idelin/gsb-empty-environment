package netsec

import (
	"fmt"
	"reflect"
	"testing"
)

// richSpec is a five-event spec exercising every dependency kind:
// per-subject program order, an explicit send/receive edge, read/write and
// write/write conflicts on ordinary objects, and write/write plus read/write
// conflicts on a secret object (s1).
var richSpec = []Event{
	{ID: "e1", Subject: "cli", Access: []Access{
		{Object: "x", Kind: Write},
		{Object: "s1", Kind: Write, Secret: true},
	}},
	{ID: "e2", Subject: "net", Access: []Access{
		{Object: "y", Kind: Read},
		{Object: "x", Kind: Read},
	}},
	{ID: "e3", Subject: "srv", Access: []Access{
		{Object: "y", Kind: Write},
		{Object: "z", Kind: Write},
	}},
	{ID: "e4", Subject: "cli", Access: []Access{
		{Object: "z", Kind: Read},
	}},
	{ID: "e5", Subject: "srv", Access: []Access{
		{Object: "s1", Kind: Write, Secret: true},
		{Object: "q", Kind: Read},
	}},
}

var richEdges = []Edge{{From: "e1", To: "e3"}}

// sparseSpec is a six-event spec with mostly independent events: three
// subjects, two shared objects and a single send/receive edge. It yields a
// large equivalence class so permutation tests exercise multi-swap
// certificates, including one secret write (k1) conflicting with a secret
// read/write on object key.
var sparseSpec = []Event{
	{ID: "a1", Subject: "alice", Access: []Access{
		{Object: "x", Kind: Write}}},
	{ID: "a2", Subject: "alice", Access: []Access{
		{Object: "key", Kind: Write, Secret: true}}},
	{ID: "b1", Subject: "bob", Access: []Access{
		{Object: "y", Kind: Write}}},
	{ID: "b2", Subject: "bob", Access: []Access{
		{Object: "x", Kind: Read}}},
	{ID: "c1", Subject: "carol", Access: []Access{
		{Object: "key", Kind: Read, Secret: true}}},
	{ID: "c2", Subject: "carol", Access: []Access{
		{Object: "y", Kind: Read}}},
}

var sparseEdges = []Edge{{From: "b1", To: "c2"}}

func permutedTrace(spec []Event, edges []Edge, perm []int) Trace {
	events := make([]Event, len(perm))
	for i, p := range perm {
		events[i] = spec[p]
	}
	return Trace{Events: events, Edges: edges}
}

func permutations(n int) [][]int {
	var out [][]int
	used := make([]bool, n)
	cur := make([]int, 0, n)
	var rec func()
	rec = func() {
		if len(cur) == n {
			out = append(out, append([]int(nil), cur...))
			return
		}
		for i := 0; i < n; i++ {
			if used[i] {
				continue
			}
			used[i] = true
			cur = append(cur, i)
			rec()
			cur = cur[:len(cur)-1]
			used[i] = false
		}
	}
	rec()
	return out
}

func canonicalKey(perm []int) string {
	key := ""
	for i, v := range perm {
		if i > 0 {
			key += ","
		}
		key += fmt.Sprintf("%d", v)
	}
	return key
}

// reachableClass performs BFS over all permutations reachable from the
// identity permutation by adjacent swaps of independent events, using the
// exact independence relation (conflict + happens-before + subject) exposed
// by the package.
func reachableClass(t *testing.T, spec []Event, edges []Edge, n int) map[string]bool {
	t.Helper()
	basePerm := make([]int, n)
	for i := range basePerm {
		basePerm[i] = i
	}
	visited := map[string]bool{canonicalKey(basePerm): true}
	queue := [][]int{basePerm}
	for len(queue) > 0 {
		perm := queue[0]
		queue = queue[1:]
		cur := permutedTrace(spec, edges, perm)
		for i := 0; i+1 < n; i++ {
			a, b := cur.Events[i].ID, cur.Events[i+1].ID
			check, err := Independent(cur, a, b)
			if err != nil {
				t.Fatalf("Independent(%q,%q): %v", a, b, err)
			}
			if !check.Independent {
				continue
			}
			next := append([]int(nil), perm...)
			next[i], next[i+1] = next[i+1], next[i]
			key := canonicalKey(next)
			if !visited[key] {
				visited[key] = true
				queue = append(queue, next)
			}
		}
	}
	return visited
}

func assertPermutationEquivalence(t *testing.T, spec []Event, edges []Edge, n int) {
	t.Helper()
	base := permutedTrace(spec, edges, identityPerm(n))
	klass := reachableClass(t, spec, edges, n)
	all := permutations(n)

	equivCount := 0
	maxSwaps := 0
	for _, perm := range all {
		other := permutedTrace(spec, edges, perm)
		report, err := Compare(base, other)
		if err != nil {
			t.Fatalf("Compare failed for perm %v: %v", perm, err)
		}
		gotEquiv := klass[canonicalKey(perm)]
		if report.Equivalent != gotEquiv {
			t.Fatalf("perm %v: Compare equivalence=%v want %v; violations=%+v",
				perm, report.Equivalent, gotEquiv, report.Violations)
		}
		if gotEquiv {
			equivCount++
			if report.Certificate == nil || !report.Certificate.Valid {
				t.Fatalf("perm %v: missing or invalid certificate", perm)
			}
			if err := Verify(base, report.Certificate); err != nil {
				t.Fatalf("perm %v: certificate replay failed: %v", perm, err)
			}
			if !reflect.DeepEqual(report.Certificate.FinalOrder,
				eventIDs(other.Events)) {
				t.Fatalf("perm %v: certificate reaches wrong order", perm)
			}
			if len(report.Certificate.Swaps) > maxSwaps {
				maxSwaps = len(report.Certificate.Swaps)
			}
		} else if report.Certificate != nil || len(report.Violations) == 0 {
			t.Fatalf("perm %v: non-equivalent trace misreported", perm)
		}

		reverse, err := Compare(other, base)
		if err != nil || reverse.Equivalent != report.Equivalent {
			t.Fatalf("perm %v: symmetry check failed: %v", perm, err)
		}

		// Reflexivity for every permutation.
		self, err := Compare(other, other)
		if err != nil || !self.Equivalent || len(self.Certificate.Swaps) != 0 {
			t.Fatalf("perm %v: reflexivity check failed", perm)
		}
	}
	if equivCount <= 1 {
		t.Fatalf("sanity: equivalence class too small (%d permutations)", equivCount)
	}
	t.Logf("n=%d: equivalence class contains %d of %d permutations; longest certificate %d swaps",
		n, equivCount, len(all), maxSwaps)
}

func identityPerm(n int) []int {
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	return perm
}

func eventIDs(events []Event) []EventID {
	ids := make([]EventID, len(events))
	for i, ev := range events {
		ids[i] = ev.ID
	}
	return ids
}

// TestFullPermutationsRich enumerates every permutation of a five-event trace
// dense in dependencies and checks Compare against an independently computed
// Mazurkiewicz reachability class.
func TestFullPermutationsRich(t *testing.T) {
	all := permutations(5)
	if len(all) != 120 {
		t.Fatalf("expected 120 permutations, got %d", len(all))
	}
	assertPermutationEquivalence(t, richSpec, richEdges, 5)
}

// TestFullPermutationsSparse enumerates all 720 permutations of a six-event
// trace whose large equivalence class forces multi-swap certificates.
func TestFullPermutationsSparse(t *testing.T) {
	if len(permutations(6)) != 720 {
		t.Fatal("permutation generator mismatch")
	}
	assertPermutationEquivalence(t, sparseSpec, sparseEdges, 6)
}

// TestTransitivityInsideClass verifies transitivity by comparing every pair of
// permutations inside each BFS reachability class.
func TestTransitivityInsideClass(t *testing.T) {
	specs := []struct {
		spec  []Event
		edges []Edge
		n     int
	}{
		{richSpec, richEdges, 5},
		{sparseSpec, sparseEdges, 6},
	}
	for _, s := range specs {
		all := permutations(s.n)
		klass := reachableClass(t, s.spec, s.edges, s.n)
		var members [][]int
		for _, perm := range all {
			if klass[canonicalKey(perm)] {
				members = append(members, perm)
			}
		}
		for i := range members {
			for j := i + 1; j < len(members); j++ {
				ri, err := Compare(
					permutedTrace(s.spec, s.edges, members[i]),
					permutedTrace(s.spec, s.edges, members[j]))
				if err != nil {
					t.Fatalf("pair Compare: %v", err)
				}
				if !ri.Equivalent {
					t.Fatalf("two BFS-reachable permutations reported non-equivalent: %v vs %v",
						members[i], members[j])
				}
			}
		}
	}
}

// TestSecretWriteConflict verifies that swapping two writes to the same secret
// object is rejected even when nothing else relates the two events.
func TestSecretWriteConflict(t *testing.T) {
	spec := []Event{
		{ID: "a", Subject: "p", Access: []Access{
			{Object: "key", Kind: Write, Secret: true}}},
		{ID: "b", Subject: "q", Access: []Access{
			{Object: "key", Kind: Write, Secret: true}}},
	}
	t1 := Trace{Events: []Event{spec[0], spec[1]}}
	t2 := Trace{Events: []Event{spec[1], spec[0]}}

	check, err := Independent(t1, "a", "b")
	if err != nil {
		t.Fatal(err)
	}
	if check.Independent || !check.SharedConflict {
		t.Fatalf("secret write/write must conflict, got %+v", check)
	}
	report, err := Compare(t1, t2)
	if err != nil {
		t.Fatal(err)
	}
	if report.Equivalent {
		t.Fatal("reversed secret writes must not be equivalent")
	}
	if len(report.Violations) != 1 ||
		report.Violations[0].Kind != ViolationConflictOrder ||
		report.Violations[0].Object != "key" {
		t.Fatalf("want one secret conflict-order violation on key, got %+v",
			report.Violations)
	}
}

// TestSecretReadWriteConflict covers read/write conflict on secret state,
// while two pure reads of a secret object remain swappable.
func TestSecretReadWriteConflict(t *testing.T) {
	rw := []Event{
		{ID: "w", Subject: "p", Access: []Access{
			{Object: "token", Kind: Write, Secret: true}}},
		{ID: "r", Subject: "q", Access: []Access{
			{Object: "token", Kind: Read, Secret: true}}},
	}
	report, err := Compare(
		Trace{Events: []Event{rw[0], rw[1]}},
		Trace{Events: []Event{rw[1], rw[0]}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Equivalent {
		t.Fatal("secret read/write reversal must not be equivalent")
	}

	rr := []Event{
		{ID: "r1", Subject: "p", Access: []Access{
			{Object: "token", Kind: Read, Secret: true}}},
		{ID: "r2", Subject: "q", Access: []Access{
			{Object: "token", Kind: Read, Secret: true}}},
	}
	check, err := Independent(Trace{Events: rr}, "r1", "r2")
	if err != nil {
		t.Fatal(err)
	}
	if !check.Independent {
		t.Fatalf("two pure reads of a secret object should be independent: %+v", check)
	}
	ok, err := Compare(
		Trace{Events: []Event{rr[0], rr[1]}},
		Trace{Events: []Event{rr[1], rr[0]}})
	if err != nil {
		t.Fatal(err)
	}
	if !ok.Equivalent || ok.Certificate == nil || len(ok.Certificate.Swaps) != 1 {
		t.Fatalf("secret read/read reorder must be equivalent with one swap: %+v", ok)
	}
}

// TestCausalReversal checks send/receive edge violations and their witnesses.
func TestCausalReversal(t *testing.T) {
	spec := []Event{
		{ID: "send", Subject: "p"},
		{ID: "mid", Subject: "q"},
		{ID: "recv", Subject: "r"},
	}
	edges := []Edge{{From: "send", To: "recv"}}
	base := Trace{Events: spec, Edges: edges}

	check, err := Independent(base, "send", "recv")
	if err != nil {
		t.Fatal(err)
	}
	if check.Independent || !check.HappensBeforeAB {
		t.Fatalf("send must happen-before recv: %+v", check)
	}

	reversed := Trace{
		Events: []Event{spec[2], spec[1], spec[0]},
		Edges:  edges,
	}
	report, err := Compare(base, reversed)
	if err != nil {
		t.Fatal(err)
	}
	if report.Equivalent {
		t.Fatal("reversed causality must not be equivalent")
	}
	var found bool
	for _, v := range report.Violations {
		if v.Kind == ViolationCausalOrder && v.A == "send" && v.B == "recv" {
			if !reflect.DeepEqual(v.Witness, []EventID{"send", "recv"}) {
				t.Fatalf("causal witness mismatch: %v", v.Witness)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("want causal-order violation with witness, got %+v", report.Violations)
	}
}

// TestConflictReversedInBase covers a conflicting pair whose order in the
// base trace is already "reversed" relative to event IDs: equivalence must be
// decided by relative positions in both traces, not by ID ordering.
func TestConflictReversedInBase(t *testing.T) {
	spec := []Event{
		{ID: "writer", Subject: "p", Access: []Access{
			{Object: "state", Kind: Write, Secret: true}}},
		{ID: "reader", Subject: "q", Access: []Access{
			{Object: "state", Kind: Read, Secret: true}}},
	}
	base := Trace{Events: []Event{spec[1], spec[0]}}
	sameOrder := Trace{Events: []Event{spec[1], spec[0]}}
	swapped := Trace{Events: []Event{spec[0], spec[1]}}

	report, err := Compare(base, sameOrder)
	if err != nil || !report.Equivalent {
		t.Fatalf("identical orderings must be equivalent: %v %+v", err, report)
	}
	report, err = Compare(base, swapped)
	if err != nil {
		t.Fatal(err)
	}
	if report.Equivalent {
		t.Fatal("reversing a read/write conflict must not be equivalent")
	}
	if len(report.Violations) != 1 ||
		report.Violations[0].Kind != ViolationConflictOrder ||
		report.Violations[0].A != "reader" ||
		report.Violations[0].B != "writer" {
		t.Fatalf("violation must preserve base order reader->writer: %+v",
			report.Violations)
	}
}

// TestTransitiveCausalityWitness verifies the happens-before closure and a
// multi-edge witness chain.
func TestTransitiveCausalityWitness(t *testing.T) {
	spec := []Event{
		{ID: "a", Subject: "p"},
		{ID: "b", Subject: "q"},
		{ID: "c", Subject: "r"},
	}
	edges := []Edge{{From: "a", To: "b"}, {From: "b", To: "c"}}
	base := Trace{Events: spec, Edges: edges}
	other := Trace{
		Events: []Event{spec[2], spec[1], spec[0]},
		Edges:  edges,
	}
	report, err := Compare(base, other)
	if err != nil {
		t.Fatal(err)
	}
	if report.Equivalent {
		t.Fatal("trace violating transitive causality must not be equivalent")
	}
	var found bool
	for _, v := range report.Violations {
		if v.A == "a" && v.B == "c" &&
			reflect.DeepEqual(v.Witness, []EventID{"a", "b", "c"}) {
			found = true
		}
	}
	if !found {
		t.Fatalf("want transitive witness a->b->c, got %+v", report.Violations)
	}
}

// TestSameSubjectNotSwappable ensures per-subject program order is respected
// even for events with disjoint object accesses.
func TestSameSubjectNotSwappable(t *testing.T) {
	spec := []Event{
		{ID: "a", Subject: "p", Access: []Access{{Object: "x", Kind: Read}}},
		{ID: "b", Subject: "p", Access: []Access{{Object: "y", Kind: Read}}},
	}
	check, err := Independent(Trace{Events: spec}, "a", "b")
	if err != nil {
		t.Fatal(err)
	}
	if check.Independent || !check.SameSubject {
		t.Fatalf("same-subject events must not be independent: %+v", check)
	}
	report, err := Compare(
		Trace{Events: []Event{spec[0], spec[1]}},
		Trace{Events: []Event{spec[1], spec[0]}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Equivalent {
		t.Fatal("same-subject reorder must not be equivalent")
	}
}

// TestStructuralDifferences covers event set, edge set and subject mismatch.
func TestStructuralDifferences(t *testing.T) {
	base := Trace{
		Events: []Event{
			{ID: "a", Subject: "p"},
			{ID: "b", Subject: "q"},
		},
		Edges: []Edge{{From: "a", To: "b"}},
	}

	missingEvent := Trace{Events: []Event{base.Events[0]}}
	report, err := Compare(base, missingEvent)
	if err != nil {
		t.Fatal(err)
	}
	if report.Equivalent || len(report.Violations) != 1 ||
		report.Violations[0].Kind != ViolationEventSet {
		t.Fatalf("want event-set violation, got %+v", report)
	}

	dropEdge := Trace{Events: base.Events}
	report, err = Compare(base, dropEdge)
	if err != nil {
		t.Fatal(err)
	}
	if report.Equivalent || len(report.Violations) != 1 ||
		report.Violations[0].Kind != ViolationEdges {
		t.Fatalf("want edge violation, got %+v", report)
	}

	changedSubject := Trace{
		Events: []Event{
			base.Events[0],
			{ID: "b", Subject: "r"},
		},
		Edges: base.Edges,
	}
	report, err = Compare(base, changedSubject)
	if err != nil {
		t.Fatal(err)
	}
	if report.Equivalent || len(report.Violations) != 1 ||
		report.Violations[0].Kind != ViolationSubject {
		t.Fatalf("want subject violation, got %+v", report)
	}
}

// TestIndependentReorderingCertificate builds a trace where two cross-subject
// events on disjoint objects commute, and checks the produced certificate.
func TestIndependentReorderingCertificate(t *testing.T) {
	spec := []Event{
		{ID: "a", Subject: "p", Access: []Access{{Object: "x", Kind: Write}}},
		{ID: "b", Subject: "q", Access: []Access{{Object: "y", Kind: Write}}},
		{ID: "c", Subject: "p", Access: []Access{{Object: "z", Kind: Read}}},
	}
	t1 := Trace{Events: []Event{spec[0], spec[1], spec[2]}}
	t2 := Trace{Events: []Event{spec[1], spec[0], spec[2]}}
	report, err := Compare(t1, t2)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Equivalent {
		t.Fatalf("disjoint-object cross-subject swap must be equivalent: %+v",
			report.Violations)
	}
	cert := report.Certificate
	if !cert.Valid || len(cert.Swaps) != 1 {
		t.Fatalf("want exactly one swap, got %+v", cert)
	}
	swap := cert.Swaps[0]
	if swap.Left != "a" || swap.Right != "b" || !swap.Reason.Independent {
		t.Fatalf("unexpected swap payload: %+v", swap)
	}
	if err := Verify(t1, cert); err != nil {
		t.Fatalf("certificate replay: %v", err)
	}
}

// TestEmptyAndSingle covers degenerate finite traces.
func TestEmptyAndSingle(t *testing.T) {
	report, err := Compare(Trace{}, Trace{})
	if err != nil || !report.Equivalent {
		t.Fatalf("empty traces must be equivalent: %v %+v", err, report)
	}
	one := Trace{Events: []Event{{ID: "solo", Subject: "p"}}}
	report, err = Compare(one, one)
	if err != nil || !report.Equivalent ||
		report.Certificate == nil || len(report.Certificate.Swaps) != 0 {
		t.Fatalf("single-event trace must be equivalent with empty certificate: %v %+v",
			err, report)
	}
	if err := Verify(one, report.Certificate); err != nil {
		t.Fatalf("empty certificate replay: %v", err)
	}
}

// TestMalformedTraces checks input validation errors.
func TestMalformedTraces(t *testing.T) {
	dup := Trace{Events: []Event{
		{ID: "a", Subject: "p"},
		{ID: "a", Subject: "q"},
	}}
	if _, err := Compare(dup, dup); err == nil {
		t.Fatal("duplicate event ids must be rejected")
	}
	badEdge := Trace{
		Events: []Event{{ID: "a", Subject: "p"}},
		Edges:  []Edge{{From: "a", To: "ghost"}},
	}
	if _, err := Compare(badEdge, badEdge); err == nil {
		t.Fatal("dangling edge must be rejected")
	}
	cyclic := Trace{
		Events: []Event{
			{ID: "a", Subject: "p"},
			{ID: "b", Subject: "q"},
		},
		Edges: []Edge{{From: "a", To: "b"}, {From: "b", To: "a"}},
	}
	if _, err := Compare(cyclic, cyclic); err == nil {
		t.Fatal("causality cycle must be rejected")
	}
	if _, err := Independent(
		Trace{Events: []Event{{ID: "a", Subject: "p"}}}, "a", "missing"); err == nil {
		t.Fatal("unknown event id in independence query must be rejected")
	}
}

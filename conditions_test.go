package netsec

import (
	"testing"
	"time"
)

func intPtr(value int) *int { return &value }

func mustFindEvidence(t *testing.T, report *Report, kind, path string) *Evidence {
	t.Helper()
	var ev *Evidence
	for index := range report.Evidences {
		candidate := &report.Evidences[index]
		if candidate.Kind != kind {
			continue
		}
		if path != "" && candidate.Path != path {
			continue
		}
		ev = candidate
		break
	}
	if ev == nil {
		t.Fatalf("missing evidence kind=%q path=%q in %+v", kind, path, report.Evidences)
	}
	return ev
}

func countEvidence(report *Report, kind string) int {
	total := 0
	for _, ev := range report.Evidences {
		if ev.Kind == kind {
			total++
		}
	}
	return total
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func subsetAudience(universe []string, mask int) []string {
	var audiences []string
	for index, audience := range universe {
		if mask&(1<<index) != 0 {
			audiences = append(audiences, audience)
		}
	}
	return audiences
}

func nodeName(level int) string {
	return "n" + string(rune('0'+level))
}

func chainTree(depth int) *Node {
	root := &Node{ID: "root"}
	current := root
	for level := 1; level <= depth; level++ {
		child := &Node{ID: nodeName(level)}
		current.Children = append(current.Children, child)
		current = child
	}
	return root
}

func rootWithCap(root *Node, capValue int) *Node {
	clone := *root
	clone.Conditions = []Condition{&ProxyRestriction{Count: intPtr(capValue)}}
	return &clone
}

// TestAudienceExhaustive enumerates every entry audience over a small
// universe and every subset of audiences inside one restriction, proving
// restriction semantics are OR membership rather than a union merge across
// separate restrictions.
func TestAudienceExhaustive(t *testing.T) {
	universe := []string{"", "a", "b", "c", "d"}
	now := time.Unix(1_000_000, 0)

	for _, entry := range universe {
		for mask := 0; mask < 1<<len(universe); mask++ {
			audiences := subsetAudience(universe, mask)
			root := &Node{
				ID:         "root",
				Conditions: []Condition{&AudienceRestriction{Audiences: audiences}},
			}
			report := Evaluate(root, entry, now, 0)

			want := false
			for _, audience := range audiences {
				if audience != "" && audience == entry {
					want = true
					break
				}
			}
			if report.Accepted != want {
				t.Fatalf("entry=%q audiences=%v: got accepted=%v want %v",
					entry, audiences, report.Accepted, want)
			}
		}
	}
}

// TestSeparateRestrictionsAreAND shows two restrictions on one node are
// AND-ed: matching only one must deny. A naive union-merge would accept it.
func TestSeparateRestrictionsAreAND(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	root := &Node{
		ID: "root",
		Conditions: []Condition{
			&AudienceRestriction{Audiences: []string{"a", "b"}},
			&AudienceRestriction{Audiences: []string{"b", "c"}},
		},
	}

	if Evaluate(root, "a", now, 0).Accepted {
		t.Fatalf("audience a satisfies only the first restriction; union-merge bug would accept")
	}

	shared := Evaluate(root, "b", now, 0)
	if !shared.Accepted {
		t.Fatalf("audience b belongs to both restrictions and must be accepted")
	}
	if countEvidence(shared, KindAudience) != 2 {
		t.Fatalf("want 2 audience evidences, got %d", countEvidence(shared, KindAudience))
	}
}

// TestNestedConditionsExhaustive enumerates all audience assignments for a
// nested All(Any(...), Any(...)) structure.
func TestNestedConditionsExhaustive(t *testing.T) {
	universe := []string{"a", "b", "c"}
	now := time.Unix(1_000_000, 0)

	for _, entry := range universe {
		for maskA := 0; maskA < 1<<len(universe); maskA++ {
			for maskB := 0; maskB < 1<<len(universe); maskB++ {
				groupA := &Any{Conditions: []Condition{
					&AudienceRestriction{Audiences: subsetAudience(universe, maskA)},
				}}
				groupB := &Any{Conditions: []Condition{
					&AudienceRestriction{Audiences: subsetAudience(universe, maskB)},
				}}
				root := &Node{
					ID:         "root",
					Conditions: []Condition{&All{Conditions: []Condition{groupA, groupB}}},
				}

				report := Evaluate(root, entry, now, 0)
				want := contains(subsetAudience(universe, maskA), entry) &&
					contains(subsetAudience(universe, maskB), entry)
				if report.Accepted != want {
					t.Fatalf("entry=%q maskA=%b maskB=%b: got %v want %v",
						entry, maskA, maskB, report.Accepted, want)
				}

				allEvidence := mustFindEvidence(t, report, KindAll, "root")
				if allEvidence.Satisfied != want {
					t.Fatalf("All evidence satisfaction=%v want %v", allEvidence.Satisfied, want)
				}
			}
		}
	}
}

// TestProxyExhaustive exercises every root cap against chains of increasing
// depth. A chain is accepted only when its forwarding depth is within the cap.
func TestProxyExhaustive(t *testing.T) {
	now := time.Unix(1_000_000, 0)

	for capValue := 0; capValue <= 4; capValue++ {
		for depth := 0; depth <= 5; depth++ {
			root := chainTree(depth)
			root.Conditions = append(root.Conditions, &ProxyRestriction{Count: intPtr(capValue)})

			report := Evaluate(root, "rp", now, 0)
			want := depth <= capValue
			if report.Accepted != want {
				t.Fatalf("cap=%d depth=%d: got accepted=%v want %v",
					capValue, depth, report.Accepted, want)
			}

			if !want {
				denied := findDeniedForward(report)
				if denied == nil {
					t.Fatalf("cap=%d depth=%d: want a denied forwarding evidence", capValue, depth)
				}
			}
		}
	}
}

func findDeniedForward(report *Report) *Evidence {
	for index := range report.Evidences {
		ev := &report.Evidences[index]
		if ev.Kind == KindProxyForward && !ev.Satisfied {
			return ev
		}
	}
	return nil
}

// TestMultiTargetProxy fans one parent out to several targets. Each branch
// consumes its own hops independently under the shared root budget.
func TestMultiTargetProxy(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	root := &Node{
		ID:         "root",
		Conditions: []Condition{&ProxyRestriction{Count: intPtr(1)}},
		Children: []*Node{
			{ID: "target1"},
			{ID: "target2", Children: []*Node{{ID: "target2-child"}}},
		},
	}

	report := Evaluate(root, "rp", now, 0)
	if report.Accepted {
		t.Fatalf("one branch needs two forwards under cap 1; tree must deny")
	}

	firstForward := mustFindEvidence(t, report, KindProxyForward, "root")
	if !firstForward.Satisfied {
		t.Fatalf("the first forwarding hop under cap 1 must be allowed")
	}
	secondForward := mustFindEvidence(t, report, KindProxyForward, "root/target2")
	if secondForward.Satisfied {
		t.Fatalf("forwarding to target2-child must exhaust the remaining budget")
	}

	allowed := Evaluate(rootWithCap(root, 2), "rp", now, 0)
	if !allowed.Accepted {
		t.Fatalf("cap 2 must permit both branches")
	}
}

// TestZeroForwardRestriction verifies Count=0 rejects any child even though
// the root node itself remains valid.
func TestZeroForwardRestriction(t *testing.T) {
	now := time.Unix(1_000_000, 0)

	leaf := &Node{
		ID:         "root",
		Conditions: []Condition{&ProxyRestriction{Count: intPtr(0)}},
	}
	if !Evaluate(leaf, "rp", now, 0).Accepted {
		t.Fatalf("Count=0 on a leaf imposes no forwarding and must accept")
	}

	parent := &Node{
		ID:         "root",
		Conditions: []Condition{&ProxyRestriction{Count: intPtr(0)}},
		Children:   []*Node{{ID: "child"}},
	}
	report := Evaluate(parent, "rp", now, 0)
	if report.Accepted {
		t.Fatalf("Count=0 must reject forwarding to a child")
	}
	denied := mustFindEvidence(t, report, KindProxyForward, "root")
	if denied.Satisfied || denied.NodeID != "root" {
		t.Fatalf("want root-level denied forwarding evidence, got %+v", denied)
	}
}

// TestStricterProxyWins ensures nested restrictions combine by minimum: the
// tightest cap along a path governs the remaining hops.
func TestStricterProxyWins(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	root := &Node{
		ID:         "root",
		Conditions: []Condition{&ProxyRestriction{Count: intPtr(3)}},
		Children: []*Node{{
			ID:         "n1",
			Conditions: []Condition{&ProxyRestriction{Count: intPtr(0)}},
			Children:   []*Node{{ID: "n2"}},
		}},
	}

	report := Evaluate(root, "rp", now, 0)
	if report.Accepted {
		t.Fatalf("n1 Count=0 must block n2 regardless of the root cap 3")
	}
	if ev := mustFindEvidence(t, report, KindProxyForward, "root/n1"); ev.Satisfied {
		t.Fatalf("forward from n1 must be denied by its zero cap")
	}
}

// TestValidityWindowHalfOpen proves both interval bounds are half-open and the
// single skew allowance is applied at entry, not accumulated per node.
func TestValidityWindowHalfOpen(t *testing.T) {
	base := time.Unix(1_000_000, 0)
	skew := time.Minute

	accepted := Evaluate(&Node{
		ID: "root",
		Conditions: []Condition{&ValidityWindow{
			NotBefore:    base,
			NotOnOrAfter: base.Add(time.Hour),
		}},
	}, "rp", base, 0)
	if !accepted.Accepted {
		t.Fatalf("now == NotBefore must be accepted by the half-open lower bound")
	}

	expired := Evaluate(&Node{
		ID: "root",
		Conditions: []Condition{&ValidityWindow{
			NotBefore:    base,
			NotOnOrAfter: base,
		}},
	}, "rp", base, 0)
	if expired.Accepted {
		t.Fatalf("now == NotOnOrAfter must remain denied in a half-open interval")
	}

	// Skew widens acceptance toward NotBefore, but cannot make the
	// NotOnOrAfter boundary inclusive.
	skewExpired := Evaluate(&Node{
		ID: "root",
		Conditions: []Condition{&ValidityWindow{
			NotBefore:    base.Add(-time.Hour),
			NotOnOrAfter: base,
		}},
	}, "rp", base.Add(skew), skew)
	if skewExpired.Accepted {
		t.Fatalf("now-skew == NotOnOrAfter must still be denied")
	}

	// now is exactly skew before NotBefore: entry-level grace admits it.
	edge := Evaluate(&Node{
		ID:         "root",
		Conditions: []Condition{&ValidityWindow{NotBefore: base}},
	}, "rp", base.Add(-skew), skew)
	if !edge.Accepted {
		t.Fatalf("now+skew == NotBefore must be accepted")
	}

	// One nanosecond beyond the grace window is still too early.
	tooEarly := Evaluate(&Node{
		ID:         "root",
		Conditions: []Condition{&ValidityWindow{NotBefore: base}},
	}, "rp", base.Add(-skew).Add(-time.Nanosecond), skew)
	if tooEarly.Accepted {
		t.Fatalf("a claim outside the single skew allowance must be denied")
	}
}

// TestSkewAppliedOnce shows nested windows share the entry-level adjusted
// reference time; a child window does not widen the allowance again.
func TestSkewAppliedOnce(t *testing.T) {
	base := time.Unix(1_000_000, 0)
	skew := time.Minute
	window := func() Condition { return &ValidityWindow{NotBefore: base} }
	tree := &Node{
		ID:         "root",
		Conditions: []Condition{window()},
		Children: []*Node{{
			ID:         "child",
			Conditions: []Condition{window()},
		}},
	}

	report := Evaluate(tree, "rp", base.Add(-skew), skew)
	if !report.Accepted {
		t.Fatalf("both windows share the entry grace and should be satisfied")
	}
	if countEvidence(report, KindTime) != 2 {
		t.Fatalf("want 2 time evidences, got %d", countEvidence(report, KindTime))
	}

	denied := Evaluate(tree, "rp", base.Add(-skew).Add(-time.Nanosecond), skew)
	if denied.Accepted {
		t.Fatalf("one extra nanosecond cannot be rescued by a second skew allowance")
	}
}

// TestUnknownConditionAlwaysDenies covers unknown rules at the node, inside an
// All group and inside an Any group; none may be accepted.
func TestUnknownConditionAlwaysDenies(t *testing.T) {
	now := time.Unix(1_000_000, 0)

	cases := []struct {
		name string
		node *Node
	}{
		{
			name: "node level",
			node: &Node{ID: "root", Conditions: []Condition{UnknownCondition("CustomXACML")}},
		},
		{
			name: "inside all",
			node: &Node{ID: "root", Conditions: []Condition{&All{Conditions: []Condition{
				&AudienceRestriction{Audiences: []string{"rp"}},
				UnknownCondition("CustomXACML"),
			}}}},
		},
		{
			name: "inside any with satisfied sibling",
			node: &Node{ID: "root", Conditions: []Condition{&Any{Conditions: []Condition{
				&AudienceRestriction{Audiences: []string{"rp"}},
				UnknownCondition("CustomXACML"),
			}}}},
		},
		{
			name: "nested deep",
			node: &Node{ID: "root", Conditions: []Condition{&Any{Conditions: []Condition{
				&All{Conditions: []Condition{UnknownCondition("Deep")}},
			}}}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := Evaluate(tc.node, "rp", now, 0)
			if report.Accepted {
				t.Fatalf("%s: unknown condition must force denial", tc.name)
			}
			unknown := mustFindEvidence(t, report, KindUnknown, "root")
			if unknown.Satisfied {
				t.Fatalf("%s: unknown evidence must be unsatisfied", tc.name)
			}
		})
	}
}

// TestEvidencePathsTraceTree verifies each evidence carries the owning node and
// slash-joined path, and a nil root or empty entry audience is a denial.
func TestEvidencePathsTraceTree(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	tree := &Node{
		ID:         "root",
		Conditions: []Condition{&AudienceRestriction{Audiences: []string{"rp"}}},
		Children: []*Node{{
			ID:         "child",
			Conditions: []Condition{&AudienceRestriction{Audiences: []string{"rp"}}},
		}},
	}

	report := Evaluate(tree, "rp", now, 0)
	if !report.Accepted {
		t.Fatalf("both nodes accept rp")
	}
	if ev := mustFindEvidence(t, report, KindAudience, "root/child"); ev.NodeID != "child" {
		t.Fatalf("child evidence has wrong NodeID %q", ev.NodeID)
	}

	if Evaluate(tree, "", now, 0).Accepted {
		t.Fatalf("an empty entry audience must be denied")
	}

	nilReport := Evaluate(nil, "rp", now, 0)
	if nilReport.Accepted || mustFindEvidence(t, nilReport, KindTree, "").Satisfied {
		t.Fatalf("nil tree root must produce a tree denial evidence")
	}
}

// TestMalformedTreesRejected covers cycles, empty groups and nil conditions,
// all of which must fail closed with evidence.
func TestMalformedTreesRejected(t *testing.T) {
	now := time.Unix(1_000_000, 0)

	child := &Node{ID: "child"}
	root := &Node{ID: "root", Children: []*Node{child}}
	child.Children = []*Node{root}
	cycleReport := Evaluate(root, "rp", now, 0)
	if cycleReport.Accepted {
		t.Fatalf("a cyclic claim tree must be rejected")
	}
	if ev := mustFindEvidence(t, cycleReport, KindTree, "root/child/root"); ev.Satisfied {
		t.Fatalf("cycle evidence must be unsatisfied")
	}

	emptyGroups := &Node{ID: "root", Conditions: []Condition{
		&Any{Conditions: nil},
		&All{Conditions: nil},
	}}
	if Evaluate(emptyGroups, "rp", now, 0).Accepted {
		t.Fatalf("empty Any/All groups must deny")
	}

	nilMember := &Node{ID: "root", Conditions: []Condition{
		&All{Conditions: []Condition{nil}},
	}}
	nilReport := Evaluate(nilMember, "rp", now, 0)
	if nilReport.Accepted || mustFindEvidence(t, nilReport, KindUnknown, "root").Satisfied {
		t.Fatalf("a nil nested condition must be reported as an unknown denial")
	}
}

// TestNilProxyCountMeansUnlimited verifies an absent numeric cap never blocks
// a deep chain.
func TestNilProxyCountMeansUnlimited(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	root := chainTree(4)
	root.Conditions = append(root.Conditions, &ProxyRestriction{Count: nil})
	if !Evaluate(root, "rp", now, 0).Accepted {
		t.Fatalf("nil proxy count must allow arbitrary forwarding depth")
	}
}

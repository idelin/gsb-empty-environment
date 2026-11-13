package netsec

import (
	"errors"
	"strings"
	"testing"
)

func TestInheritFixedOverrideAndProvenance(t *testing.T) {
	cur := map[string]string{
		"project":    "alpha",
		"department": "eng",
		"secret":     "drop",
	}
	req := HopRequest{
		CurrentLabels:       cur,
		CurrentTransitive:   []string{"project"},
		RequestedLabels:     map[string]string{"project": "beta"},
		RequestedTransitive: []string{"project"},
		FixedLabels:         map[string]string{"department": "eng"},
		OverrideKeys:        []string{"project"},
		Depth:               1,
		RoleID:              "role-b",
	}
	res, err := ApplyHop(req)
	if err != nil {
		t.Fatalf("ApplyHop: %v", err)
	}
	if got := res.Values["project"]; got != "beta" {
		t.Fatalf("project = %q, want beta", got)
	}
	if _, ok := res.Values["secret"]; ok {
		t.Fatal("non-transitive secret was inherited")
	}
	if got := res.Values["department"]; got != "eng" {
		t.Fatalf("department = %q", got)
	}
	l := res.Labels["project"]
	if len(l.Origins) != 3 {
		t.Fatalf("project origins = %d, want initial+inherit+override", len(l.Origins))
	}
	if l.Origins[0].Kind != OriginInitial {
		t.Fatalf("oldest origin = %s, want initial", l.Origins[0].Kind)
	}
	if l.Origins[1].Kind != OriginInherited || l.Origins[2].Kind != OriginOverride {
		t.Fatalf("origins = %#v", l.Origins)
	}
	if !res.TransitiveSet["project"] {
		t.Fatal("project should be transitive")
	}
	if res.TransitiveSet["department"] {
		t.Fatal("target-fixed department cannot be caller-upgraded")
	}
}

func TestFixedConflictAlwaysRejects(t *testing.T) {
	// Fixed collides with an inherited key, even with an equal value.
	_, err := ApplyHop(HopRequest{
		CurrentLabels:     map[string]string{"department": "eng"},
		CurrentTransitive: []string{"department"},
		FixedLabels:       map[string]string{"department": "eng"},
		Depth:             1,
		RoleID:            "role-b",
	})
	if !errors.Is(err, ErrFixedConflict) {
		t.Fatalf("err = %v, want fixed conflict", err)
	}

	// Fixed collides with a caller request, even if the target also (wrongly)
	// lists the key as overridable.
	_, err = ApplyHop(HopRequest{
		RequestedLabels: map[string]string{"department": "admin"},
		FixedLabels:     map[string]string{"department": "eng"},
		OverrideKeys:    []string{"department"},
		Depth:           1,
		RoleID:          "role-b",
	})
	if !errors.Is(err, ErrFixedConflict) {
		t.Fatalf("err = %v, want fixed conflict", err)
	}

	_, err = ApplyHop(HopRequest{
		FixedLabels:         map[string]string{"department": "eng"},
		RequestedTransitive: []string{"department"},
		Depth:               1,
		RoleID:              "role-b",
	})
	if !errors.Is(err, ErrTransitiveUp) {
		t.Fatalf("fixed transitive err = %v, want transitive-upgrade rejection", err)
	}
}

func TestOnlyExactCaseMatches(t *testing.T) {
	// Distinct case spellings are distinct keys.  Both may be inherited and
	// both may be explicitly overridden.
	res, err := ApplyHop(HopRequest{
		CurrentLabels: map[string]string{
			"project": "lower",
			"Project": "upper",
		},
		CurrentTransitive: []string{"project", "Project"},
		RequestedLabels: map[string]string{
			"project": "L2",
			"Project": "U2",
		},
		RequestedTransitive: []string{"project", "Project"},
		OverrideKeys:        []string{"project", "Project"},
		Depth:               1,
		RoleID:              "role-b",
	})
	if err != nil {
		t.Fatalf("ApplyHop: %v", err)
	}
	if res.Values["project"] != "L2" || res.Values["Project"] != "U2" {
		t.Fatalf("case variants were folded: %#v", res.Values)
	}

	// A case-approximate spelling is not covered by the lower-case override
	// key.
	_, err = ApplyHop(HopRequest{
		FixedLabels:     map[string]string{"project": "locked"},
		RequestedLabels: map[string]string{"Project": "open"},
		OverrideKeys:    []string{"project"},
		Depth:           1,
		RoleID:          "role-b",
	})
	if !errors.Is(err, ErrNotOverridable) {
		t.Fatalf("err = %v, want not-overridable", err)
	}
}

func TestNonTransitiveLabelCannotBeUpgraded(t *testing.T) {
	_, err := ApplyHop(HopRequest{
		CurrentLabels:       map[string]string{"project": "old"},
		RequestedLabels:     map[string]string{"project": "new"},
		RequestedTransitive: []string{"project"},
		OverrideKeys:        []string{"project"},
		Depth:               1,
		RoleID:              "role-b",
	})
	if !errors.Is(err, ErrTransitiveUp) {
		t.Fatalf("err = %v, want transitive-upgrade rejection", err)
	}

	// Removing a non-transitive label and re-adding it is allowed only as a
	// non-transitive label, and the old provenance is retained.
	res, err := ApplyHop(HopRequest{
		CurrentLabels:   map[string]string{"project": "old"},
		RequestedLabels: map[string]string{"project": "new"},
		OverrideKeys:    []string{"project"},
		Depth:           1,
		RoleID:          "role-b",
	})
	if err != nil {
		t.Fatalf("re-add: %v", err)
	}
	if res.TransitiveSet["project"] {
		t.Fatal("re-added non-transitive label became transitive")
	}
	if len(res.Labels["project"].Origins) != 2 {
		t.Fatalf("re-add lost provenance: %#v", res.Labels["project"].Origins)
	}
}

func TestDroppedNonTransitiveLabelStaysTainted(t *testing.T) {
	s := NewSession(map[string]string{"project": "old"})
	if !s.NonTransitive["project"] {
		t.Fatal("initial non-transitive label was not tainted")
	}
	// Drop project by not listing it as transitive on the next hop.
	next, err := s.Advance(HopRequest{RoleID: "role-b"})
	if err != nil {
		t.Fatalf("drop hop: %v", err)
	}
	if _, ok := next.Values()["project"]; ok {
		t.Fatal("non-transitive project was inherited")
	}
	if !next.NonTransitive["project"] {
		t.Fatal("dropped key lost its non-transitive taint")
	}
	// Re-add it and try to request transitivity.  The old taint must still
	// reject the upgrade.
	_, err = next.Advance(HopRequest{
		RequestedLabels:     map[string]string{"project": "new"},
		RequestedTransitive: []string{"project"},
		OverrideKeys:        []string{"project"},
		RoleID:              "role-c",
	})
	if !errors.Is(err, ErrTransitiveUp) {
		t.Fatalf("re-add upgrade err = %v, want transitive-upgrade rejection", err)
	}
}

func TestNextTransitiveSetIsIntersectionWithRequest(t *testing.T) {
	// project is currently transitive, but the caller does not request it in
	// the next transitive set.  It is retained as an ordinary label and loses
	// transitive status.
	res, err := ApplyHop(HopRequest{
		CurrentLabels:     map[string]string{"project": "a"},
		CurrentTransitive: []string{"project"},
		Depth:             1,
		RoleID:            "role-b",
	})
	if err != nil {
		t.Fatalf("ApplyHop: %v", err)
	}
	if _, ok := res.Values["project"]; !ok {
		t.Fatal("transitive label should still be inherited")
	}
	if res.TransitiveSet["project"] {
		t.Fatal("label was transitive even though it was not requested")
	}
	if !res.Session.NonTransitive["project"] {
		t.Fatal("label that lost transitivity was not tainted")
	}
}

func TestRequestedTransitiveAbsentKeyIsDropped(t *testing.T) {
	res, err := ApplyHop(HopRequest{
		CurrentLabels:       map[string]string{"project": "a"},
		CurrentTransitive:   []string{"project"},
		RequestedTransitive: []string{"project", "missing"},
		Depth:               1,
		RoleID:              "role-b",
	})
	if err != nil {
		t.Fatalf("ApplyHop: %v", err)
	}
	if !res.TransitiveSet["project"] {
		t.Fatal("present requested key should be transitive")
	}
	if _, ok := res.TransitiveSet["missing"]; ok {
		t.Fatal("absent key should not be in the intersection")
	}
}

func TestRequestMustUseExactOverrideKey(t *testing.T) {
	_, err := ApplyHop(HopRequest{
		CurrentLabels:     map[string]string{"project": "a"},
		CurrentTransitive: []string{"project"},
		RequestedLabels:   map[string]string{"project": "b"},
		OverrideKeys:      []string{"other"},
		Depth:             1,
		RoleID:            "role-b",
	})
	if !errors.Is(err, ErrNotOverridable) {
		t.Fatalf("err = %v, want not-overridable", err)
	}
}

func TestTargetTransitivePolicyForNewLabels(t *testing.T) {
	base := HopRequest{
		RequestedLabels: map[string]string{"project": "x"},
		OverrideKeys:    []string{"project"},
		Depth:           1,
		RoleID:          "role-b",
	}
	base.RequestedTransitive = []string{"project"}
	if res, err := ApplyHop(base); err != nil || !res.TransitiveSet["project"] {
		t.Fatalf("default target policy should permit new transitive label: res=%v err=%v", res, err)
	}
	base.TargetTransitiveKeys = []string{}
	if _, err := ApplyHop(base); !errors.Is(err, ErrTransitiveUp) {
		t.Fatalf("err = %v, want transitive-upgrade rejection", err)
	}
	base.TargetTransitiveKeys = []string{"project"}
	if res, err := ApplyHop(base); err != nil || !res.TransitiveSet["project"] {
		t.Fatalf("explicit target permission failed: res=%v err=%v", res, err)
	}
}

func TestRoleCycleAndDepth(t *testing.T) {
	_, err := ApplyHop(HopRequest{
		CurrentRoleIDs: []string{"role-a"},
		Depth:          2,
		RoleID:         "role-a",
	})
	if !errors.Is(err, ErrRoleCycle) {
		t.Fatalf("err = %v, want role cycle", err)
	}
	_, err = ApplyHop(HopRequest{Depth: 5, RoleID: "role-e"})
	if !errors.Is(err, ErrDepthExceeded) {
		t.Fatalf("err = %v, want depth exceeded", err)
	}
}

func TestSequentialChainDepthAndSources(t *testing.T) {
	s := NewSession(map[string]string{"project": "a"}, "project")
	for i, role := range []string{"r1", "r2", "r3", "r4"} {
		next, err := s.Advance(HopRequest{
			RequestedLabels:     map[string]string{"project": string(rune('b' + i))},
			RequestedTransitive: []string{"project"},
			OverrideKeys:        []string{"project"},
			RoleID:              role,
		})
		if err != nil {
			t.Fatalf("hop %s: %v", role, err)
		}
		s = next
	}
	if s.Depth != 4 {
		t.Fatalf("depth = %d", s.Depth)
	}
	if len(s.Labels["project"].Origins) != 1+2*4 {
		t.Fatalf("origins = %d, want 9", len(s.Labels["project"].Origins))
	}
	if s.Labels["project"].Origins[0].Kind != OriginInitial {
		t.Fatal("initial source was lost")
	}
	if _, err := s.Advance(HopRequest{RoleID: "r5"}); !errors.Is(err, ErrDepthExceeded) {
		t.Fatalf("5th hop err = %v, want depth exceeded", err)
	}
}

func TestValidationAndDuplicateSources(t *testing.T) {
	_, err := ApplyHop(HopRequest{RequestedPairs: []Pair{{Key: "a", Value: "1"}, {Key: "a", Value: "2"}}, Depth: 1, RoleID: "r"})
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("duplicate requested key err = %v", err)
	}
	_, err = ApplyHop(HopRequest{OverrideKeys: []string{"a", "a"}, Depth: 1, RoleID: "r"})
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("duplicate override key err = %v", err)
	}
	_, err = ApplyHop(HopRequest{RequestedTransitive: []string{"a", "a"}, Depth: 1, RoleID: "r"})
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("duplicate transitive key err = %v", err)
	}
	_, err = ApplyHop(HopRequest{CurrentLabels: map[string]string{"": "x"}, Depth: 1, RoleID: "r"})
	if !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("empty key err = %v", err)
	}
	_, err = ApplyHop(HopRequest{CurrentLabels: map[string]string{"x": ""}, Depth: 1, RoleID: "r"})
	if !errors.Is(err, ErrEmptyValue) {
		t.Fatalf("empty value err = %v", err)
	}
	_, err = ApplyHop(HopRequest{CurrentLabels: map[string]string{string([]byte{0xff}): "x"}, Depth: 1, RoleID: "r"})
	if !errors.Is(err, ErrInvalidUTF8) {
		t.Fatalf("invalid UTF-8 err = %v", err)
	}
	_, err = ApplyHop(HopRequest{CurrentLabels: map[string]string{"x": strings.Repeat("a", MaxValueBytes+1)}, Depth: 1, RoleID: "r"})
	if !errors.Is(err, ErrValueTooLong) {
		t.Fatalf("long value err = %v", err)
	}
}

func TestPolicyViewIsImmutableSnapshot(t *testing.T) {
	sess := NewSession(map[string]string{"department": "guest"})
	var seen map[string]string
	p := PolicyFunc(func(labels map[string]string) bool {
		seen = labels
		labels["department"] = "admin"
		labels["injected"] = "yes"
		return true
	})
	if got := Decide(p, sess.Values()); got != Allow {
		t.Fatalf("decision = %s", got)
	}
	if seen["department"] != "admin" {
		t.Fatal("simulated mutation was not applied to the callback copy")
	}
	if got := sess.Values()["department"]; got != "guest" {
		t.Fatalf("policy mutated session: %q", got)
	}
	if _, ok := sess.Values()["injected"]; ok {
		t.Fatal("policy injected a session label")
	}

	view := NewLabelView(map[string]string{"x": "y"})
	cp := view.Map()
	cp["x"] = "changed"
	if got, _ := view.Get("x"); got != "y" {
		t.Fatalf("view changed through Map copy: %q", got)
	}
}

func TestProofsAreStableAndBoundToValue(t *testing.T) {
	req := HopRequest{
		CurrentLabels:     map[string]string{"project": "a"},
		CurrentTransitive: []string{"project"},
		RequestedLabels:   map[string]string{"project": "b"},
		OverrideKeys:      []string{"project"},
		Depth:             1,
		RoleID:            "r1",
	}
	a, err := ApplyHop(req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ApplyHop(req)
	if err != nil {
		t.Fatal(err)
	}
	if a.Proofs["project"].Digest == "" || a.Proofs["project"].Digest != b.Proofs["project"].Digest {
		t.Fatal("proof digest is not stable")
	}
	req.RequestedLabels["project"] = "c"
	c, err := ApplyHop(req)
	if err != nil {
		t.Fatal(err)
	}
	if c.Proofs["project"].Digest == a.Proofs["project"].Digest {
		t.Fatal("proof digest did not bind the value")
	}
	if !VerifyResultProofs(a) || !VerifySessionProofs(a.Session) {
		t.Fatal("valid proofs failed verification")
	}
	tampered := a.Proofs["project"]
	tampered.Value = "evil"
	if VerifyProof(tampered) {
		t.Fatal("tampered proof passed verification")
	}
}

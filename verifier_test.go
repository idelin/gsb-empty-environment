package taskpkg

import (
	"strings"
	"testing"
	"time"
)

var (
	testNow  = time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	testFrom = testNow.Add(-time.Hour)
	testTo   = testNow.Add(time.Hour)
)

func aliasProof(id, owner, target, zone string) Proof {
	return Proof{
		ID:         id,
		Kind:       ProofAlias,
		Name:       owner,
		Target:     target,
		Zone:       zone,
		Type:       TypeCNAME,
		ValidFrom:  testFrom,
		ValidUntil: testTo,
	}
}

func nxProof(id, name, zone string) Proof {
	return Proof{
		ID:         id,
		Kind:       ProofNXDomain,
		Name:       name,
		Zone:       zone,
		ValidFrom:  testFrom,
		ValidUntil: testTo,
	}
}

func nxTypedProof(id, name, zone string, typ RRType) Proof {
	p := nxProof(id, name, zone)
	p.Type = typ
	return p
}

func nodataProof(id, name, zone string, typ RRType) Proof {
	return Proof{
		ID:         id,
		Kind:       ProofNoData,
		Name:       name,
		Zone:       zone,
		Type:       typ,
		ValidFrom:  testFrom,
		ValidUntil: testTo,
	}
}

func answerProof(id, name, zone string, typ RRType, value string) Proof {
	return Proof{
		ID:         id,
		Kind:       ProofAnswer,
		Name:       name,
		Zone:       zone,
		Type:       typ,
		Value:      value,
		ValidFrom:  testFrom,
		ValidUntil: testTo,
	}
}

func newResolver(zones ...string) *Resolver {
	return NewResolver(zones, testNow)
}

func TestAuthorityUsesDeepestConfiguredZone(t *testing.T) {
	zs, err := NewZoneSet(".", "Example.COM", "sub.example.com", "leaf.sub.example.com")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		want string
		ok   bool
	}{
		{"example.com", "example.com", true},
		{"www.example.com", "example.com", true},
		{"a.sub.example.com", "sub.example.com", true},
		{"x.leaf.sub.example.com", "leaf.sub.example.com", true},
		{"anything.else", ".", true},
		{"EXAMPLE.COM.", "example.com", true},
	}
	for _, tc := range cases {
		got, ok := zs.Authority(tc.name)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("Authority(%q) = %q, %v; want %q, %v", tc.name, got, ok, tc.want, tc.ok)
		}
	}

	zs, err = NewZoneSet("example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := zs.Authority("x.other.com"); ok {
		t.Fatal("there must be no implicit root authority")
	}
}

func TestCrossZoneForgedDenialCannotNegateVictimName(t *testing.T) {
	res := newResolver("attacker.test", "victim.test")
	proofs := []Proof{
		aliasProof("jump", "evil.attacker.test", "www.victim.test", "attacker.test"),
		nxProof("forged", "www.victim.test", "attacker.test"),
	}

	got := res.Resolve(Query{Name: "evil.attacker.test", Type: TypeA}, proofs)
	if got.Status != StatusUnknown {
		t.Fatalf("cross-zone denial produced %s, want unknown", got.Status)
	}
	if got.Name == "www.victim.test" && got.Status.IsDefinitive() {
		t.Fatal("forged denial was treated as a definitive negative")
	}
	if !hasRejection(got.Rejections, "forged", RejectZoneMismatch) {
		t.Fatalf("forged proof was not rejected for zone mismatch: %#v", got.Rejections)
	}
	if !strings.Contains(got.Reason, "not signed by its authority") {
		t.Fatalf("reason does not explain missing authority: %q", got.Reason)
	}

	// The same forged denial must also fail when the victim name is queried
	// directly rather than reached through the attacker's alias.
	direct := res.Resolve(Query{Name: "www.victim.test", Type: TypeA}, proofs)
	if direct.Status != StatusUnknown {
		t.Fatalf("direct query with forged denial produced %s, want unknown", direct.Status)
	}

	// A denial signed by the real authority is accepted, but only at the
	// endpoint name reached by the alias.
	legit := append(proofs, nxProof("legit", "www.victim.test", "victim.test"))
	got = res.Resolve(Query{Name: "evil.attacker.test", Type: TypeA}, legit)
	if got.Status != StatusNXDomain {
		t.Fatalf("legitimate endpoint denial produced %s, want nxdomain", got.Status)
	}
	if got.Name != "www.victim.test" {
		t.Fatalf("negative scope name = %q, want endpoint www.victim.test", got.Name)
	}
	if got.Query.Name != "evil.attacker.test" {
		t.Fatalf("query name was rewritten: %q", got.Query.Name)
	}
	scope, ok := got.NegativeScope()
	if !ok {
		t.Fatal("negative scope missing")
	}
	if scope.Name != "www.victim.test" || scope.Type != TypeA || scope.Zone != "victim.test" || !scope.NameWide {
		t.Fatalf("bad negative scope: %#v", scope)
	}
	key, ok := got.CacheKey()
	if !ok || key.Name != "www.victim.test" || key.Type != TypeA {
		t.Fatalf("negative cache key = %#v, %v; must not be keyed by start name", key, ok)
	}
	if len(got.Path) != 2 || got.Path[0].Kind != StepAlias || got.Path[1].Kind != StepDenial {
		t.Fatalf("incomplete authorization path: %#v", got.Path)
	}
}

func TestForgedNoDataAtAnotherZoneIsUnknown(t *testing.T) {
	res := newResolver("attacker.test", "victim.test")
	proofs := []Proof{
		aliasProof("jump", "evil.attacker.test", "www.victim.test", "attacker.test"),
		{
			ID:         "forged-nodata",
			Kind:       ProofNoData,
			Name:       "www.victim.test",
			Zone:       "attacker.test",
			Type:       TypeA,
			ValidFrom:  testFrom,
			ValidUntil: testTo,
		},
	}
	got := res.Resolve(Query{Name: "evil.attacker.test", Type: TypeA}, proofs)
	if got.Status != StatusUnknown {
		t.Fatalf("forged nodata produced %s, want unknown", got.Status)
	}
}

func TestEveryAliasHopNeedsOwnerAuthority(t *testing.T) {
	res := newResolver("attacker.test", "victim.test")
	proofs := []Proof{
		aliasProof("hop1", "a.victim.test", "b.victim.test", "victim.test"),
		aliasProof("forged-hop", "b.victim.test", "c.attacker.test", "attacker.test"),
		nxProof("end", "c.attacker.test", "attacker.test"),
	}
	got := res.Resolve(Query{Name: "a.victim.test", Type: TypeA}, proofs)
	if got.Status != StatusUnknown {
		t.Fatalf("forged intermediate hop produced %s, want unknown", got.Status)
	}
	if !hasRejection(got.Rejections, "forged-hop", RejectZoneMismatch) {
		t.Fatalf("forged hop was not rejected: %#v", got.Rejections)
	}
	if len(got.Path) != 1 || got.Path[0].Name != "a.victim.test" {
		t.Fatalf("path should stop at b.victim.test, got %#v", got.Path)
	}
}

func TestDelegatedChildMustSignItsOwnNames(t *testing.T) {
	res := newResolver("example.com", "sub.example.com")
	parentSigned := []Proof{
		aliasProof("parent-alias", "a.sub.example.com", "b.example.com", "example.com"),
		nxProof("end", "b.example.com", "example.com"),
	}
	got := res.Resolve(Query{Name: "a.sub.example.com", Type: TypeA}, parentSigned)
	if got.Status != StatusUnknown {
		t.Fatalf("parent-signed child alias produced %s, want unknown", got.Status)
	}

	childSigned := []Proof{
		aliasProof("child-alias", "a.sub.example.com", "b.example.com", "sub.example.com"),
		nxProof("end", "b.example.com", "example.com"),
	}
	got = res.Resolve(Query{Name: "a.sub.example.com", Type: TypeA}, childSigned)
	if got.Status != StatusNXDomain || got.Name != "b.example.com" {
		t.Fatalf("child-signed alias produced %s at %q", got.Status, got.Name)
	}
}

func TestExpiredIntermediateAliasYieldsUnknown(t *testing.T) {
	res := newResolver("attacker.test")
	expired := aliasProof("middle", "b.attacker.test", "c.attacker.test", "attacker.test")
	expired.ValidUntil = testNow
	proofs := []Proof{
		aliasProof("first", "a.attacker.test", "b.attacker.test", "attacker.test"),
		expired,
		nxProof("end", "c.attacker.test", "attacker.test"),
	}

	got := res.Resolve(Query{Name: "a.attacker.test", Type: TypeA}, proofs)
	if got.Status != StatusUnknown {
		t.Fatalf("expired intermediate hop produced %s, want unknown", got.Status)
	}
	if got.Name != "b.attacker.test" {
		t.Fatalf("resolution should stop at b.attacker.test, got %q", got.Name)
	}
	if !strings.Contains(got.Reason, "expired") {
		t.Fatalf("reason does not mention expiry: %q", got.Reason)
	}
	if !hasRejection(got.Rejections, "middle", RejectExpired) {
		t.Fatalf("expired proof was not rejected: %#v", got.Rejections)
	}
	if len(got.Path) != 1 {
		t.Fatalf("invalid hop appears in authorization path: %#v", got.Path)
	}

	expired.ValidUntil = testTo
	proofs[1] = expired
	got = res.Resolve(Query{Name: "a.attacker.test", Type: TypeA}, proofs)
	if got.Status != StatusNXDomain || got.Name != "c.attacker.test" {
		t.Fatalf("refreshed chain produced %s at %q", got.Status, got.Name)
	}
	if len(got.Path) != 3 {
		t.Fatalf("complete path length = %d, want 3", len(got.Path))
	}
	for _, step := range got.Path {
		if step.Zone != "attacker.test" {
			t.Fatalf("path step has wrong authority: %#v", step)
		}
	}
}

func TestValidityWindowBoundaries(t *testing.T) {
	res := newResolver("example.com")

	p := nxProof("at-boundary", "end.example.com", "example.com")
	p.ValidFrom = testNow
	p.ValidUntil = testTo
	got := res.Resolve(Query{Name: "end.example.com", Type: TypeA}, []Proof{p})
	if got.Status != StatusNXDomain {
		t.Fatalf("ValidFrom == now should be valid, got %s", got.Status)
	}

	p.ValidFrom = testNow.Add(time.Nanosecond)
	got = res.Resolve(Query{Name: "end.example.com", Type: TypeA}, []Proof{p})
	if got.Status != StatusUnknown || !hasRejection(got.Rejections, "at-boundary", RejectNotYetValid) {
		t.Fatalf("future proof produced %s, %#v", got.Status, got.Rejections)
	}

	p.ValidFrom = testFrom
	p.ValidUntil = testNow
	got = res.Resolve(Query{Name: "end.example.com", Type: TypeA}, []Proof{p})
	if got.Status != StatusUnknown || !hasRejection(got.Rejections, "at-boundary", RejectExpired) {
		t.Fatalf("ValidUntil == now should be expired, got %s, %#v", got.Status, got.Rejections)
	}
}

func TestAliasCyclesTerminateAsUnknown(t *testing.T) {
	res := newResolver("example.com")
	proofs := []Proof{
		aliasProof("a-b", "a.example.com", "b.example.com", "example.com"),
		aliasProof("b-a", "b.example.com", "a.example.com", "example.com"),
	}
	got := res.Resolve(Query{Name: "a.example.com", Type: TypeA}, proofs)
	if got.Status != StatusUnknown || !got.Cycle {
		t.Fatalf("two-node cycle produced %s cycle=%v, want unknown cyclic", got.Status, got.Cycle)
	}
	if !strings.Contains(got.Reason, "alias cycle") {
		t.Fatalf("cycle reason = %q", got.Reason)
	}
	if len(got.Path) != 2 {
		t.Fatalf("cycle path length = %d, want 2", len(got.Path))
	}

	self := []Proof{aliasProof("self", "loop.example.com", "loop.example.com", "example.com")}
	got = res.Resolve(Query{Name: "loop.example.com", Type: TypeA}, self)
	if got.Status != StatusUnknown || !got.Cycle || len(got.Path) != 1 {
		t.Fatalf("self cycle produced %s cycle=%v path=%#v", got.Status, got.Cycle, got.Path)
	}

	three := []Proof{
		aliasProof("1", "a.example.com", "b.example.com", "example.com"),
		aliasProof("2", "b.example.com", "c.example.com", "example.com"),
		aliasProof("3", "c.example.com", "a.example.com", "example.com"),
	}
	got = res.Resolve(Query{Name: "a.example.com", Type: TypeA}, three)
	if got.Status != StatusUnknown || !got.Cycle || len(got.Path) != 3 {
		t.Fatalf("three-node cycle produced %s cycle=%v path=%#v", got.Status, got.Cycle, got.Path)
	}
}

func TestNegativeTypeScopeIsTerminalOnly(t *testing.T) {
	res := newResolver("victim.test")
	proofs := []Proof{
		aliasProof("jump", "a.victim.test", "b.victim.test", "victim.test"),
		nodataProof("nodata-a", "b.victim.test", "victim.test", TypeA),
	}

	got := res.Resolve(Query{Name: "a.victim.test", Type: TypeA}, proofs)
	if got.Status != StatusNoData || got.Name != "b.victim.test" || got.Type != TypeA {
		t.Fatalf("A query produced %s at %q type %s", got.Status, got.Name, got.Type)
	}
	if key, ok := got.CacheKey(); !ok || key.Name != "b.victim.test" || key.Type != TypeA {
		t.Fatalf("cache key leaked the start name: %#v, %v", key, ok)
	}

	got = res.Resolve(Query{Name: "a.victim.test", Type: TypeAAAA}, proofs)
	if got.Status != StatusUnknown {
		t.Fatalf("nodata for A was reused for AAAA: %s", got.Status)
	}

	typedNX := []Proof{
		aliasProof("jump2", "c.victim.test", "d.victim.test", "victim.test"),
		nxTypedProof("nx-a", "d.victim.test", "victim.test", TypeA),
	}
	got = res.Resolve(Query{Name: "c.victim.test", Type: TypeA}, typedNX)
	if got.Status != StatusNXDomain {
		t.Fatalf("typed nxdomain for A produced %s", got.Status)
	}
	got = res.Resolve(Query{Name: "c.victim.test", Type: TypeAAAA}, typedNX)
	if got.Status != StatusUnknown {
		t.Fatalf("typed nxdomain for A was reused for AAAA: %s", got.Status)
	}

	// A QType binding also limits a name-wide NXDOMAIN proof.
	qbound := nxProof("nx-qbound", "e.victim.test", "victim.test")
	qbound.QType = TypeA
	got = res.Resolve(Query{Name: "e.victim.test", Type: TypeA}, []Proof{qbound})
	if got.Status != StatusNXDomain {
		t.Fatalf("query-bound nxdomain for A produced %s", got.Status)
	}
	got = res.Resolve(Query{Name: "e.victim.test", Type: TypeAAAA}, []Proof{qbound})
	if got.Status != StatusUnknown {
		t.Fatalf("query-bound nxdomain leaked to AAAA: %s", got.Status)
	}
}

func TestCompleteAuthorizationPathAndPositiveAnswer(t *testing.T) {
	res := newResolver("example.com")
	proofs := []Proof{
		aliasProof("one", "a.example.com", "b.example.com", "example.com"),
		aliasProof("two", "b.example.com", "c.example.com", "example.com"),
		answerProof("data", "c.example.com", "example.com", TypeA, "192.0.2.1"),
	}
	got := res.Resolve(Query{Name: "a.example.com", Type: TypeA}, proofs)
	if got.Status != StatusAnswer || got.Name != "c.example.com" || got.Value != "192.0.2.1" {
		t.Fatalf("positive result = %#v", got)
	}
	if len(got.Path) != 3 {
		t.Fatalf("path length = %d, want 3", len(got.Path))
	}
	if !got.CompletePath() || len(got.AuthorizationPath()) != 3 {
		t.Fatalf("result does not expose a complete authorization path: %#v", got.AuthorizationPath())
	}
	if got.Path[0].Kind != StepAlias || got.Path[1].Kind != StepAlias || got.Path[2].Kind != StepAnswer {
		t.Fatalf("bad step kinds: %#v", got.Path)
	}
	if key, ok := got.CacheKey(); !ok || key.Name != "c.example.com" || key.Type != TypeA {
		t.Fatalf("positive cache key = %#v, %v", key, ok)
	}

	cname := res.Resolve(Query{Name: "a.example.com", Type: TypeCNAME}, proofs)
	if cname.Status != StatusAnswer || cname.Type != TypeCNAME || cname.Value != "b.example.com" {
		t.Fatalf("CNAME query result = %#v", cname)
	}
	if len(cname.Path) != 1 || cname.Path[0].Kind != StepAlias {
		t.Fatalf("CNAME answer followed the alias: %#v", cname.Path)
	}
}

func TestEnumerateReturnsEveryTrustedPathIndependently(t *testing.T) {
	res := newResolver("example.com", "victim.test", "attacker.test")
	proofs := []Proof{
		aliasProof("to-b", "a.example.com", "b.example.com", "example.com"),
		aliasProof("to-c", "a.example.com", "c.example.com", "example.com"),
		nxProof("end-b", "b.example.com", "example.com"),
		answerProof("end-c", "c.example.com", "example.com", TypeA, "192.0.2.2"),
	}

	rep := res.Enumerate(Query{Name: "a.example.com", Type: TypeA}, proofs)
	defs := rep.DefinitivePaths()
	if len(defs) != 2 {
		t.Fatalf("enumerated %d definitive paths, want 2: %#v", len(defs), rep.Paths)
	}
	var sawNX, sawAnswer bool
	for _, p := range defs {
		switch p.Status {
		case StatusNXDomain:
			sawNX = true
			if p.Name != "b.example.com" || len(p.Steps) != 2 {
				t.Fatalf("bad nxdomain path: %#v", p)
			}
		case StatusAnswer:
			sawAnswer = true
			if p.Name != "c.example.com" || p.Value != "192.0.2.2" || len(p.Steps) != 2 {
				t.Fatalf("bad answer path: %#v", p)
			}
		default:
			t.Fatalf("unexpected definitive status %s", p.Status)
		}
		verifyPathAuthority(t, p)
	}
	if !sawNX || !sawAnswer {
		t.Fatalf("missing an independently enumerated path: %#v", defs)
	}
	if len(rep.Ambiguities) == 0 {
		t.Fatal("multiple aliases were not reported as ambiguous")
	}
	if got := res.Resolve(Query{Name: "a.example.com", Type: TypeA}, proofs); got.Status != StatusUnknown {
		t.Fatalf("ambiguous evidence resolved to %s, want unknown", got.Status)
	}
}

func TestFiniteTreeEnumerationMatchesExpectedResults(t *testing.T) {
	res := newResolver("example.com", "leaf.example.com", "attacker.test", "victim.test")
	proofs := []Proof{
		aliasProof("a-b", "a.example.com", "b.example.com", "example.com"),
		aliasProof("b-c", "b.example.com", "c.example.com", "example.com"),
		nxProof("c-end", "c.example.com", "example.com"),
		aliasProof("d-e", "d.example.com", "e.leaf.example.com", "example.com"),
		nodataProof("e-nodata", "e.leaf.example.com", "leaf.example.com", TypeA),
		aliasProof("x-y", "x.attacker.test", "y.victim.test", "attacker.test"),
		nxProof("forged-y", "y.victim.test", "attacker.test"),
	}

	cases := []struct {
		start  string
		status Status
		name   string
		steps  int
	}{
		{"a.example.com", StatusNXDomain, "c.example.com", 3},
		{"b.example.com", StatusNXDomain, "c.example.com", 2},
		{"d.example.com", StatusNoData, "e.leaf.example.com", 2},
		{"x.attacker.test", StatusUnknown, "y.victim.test", 1},
		{"z.example.com", StatusUnknown, "z.example.com", 0},
	}
	for _, tc := range cases {
		t.Run(tc.start, func(t *testing.T) {
			rep := res.Enumerate(Query{Name: tc.start, Type: TypeA}, proofs)
			got := res.Resolve(Query{Name: tc.start, Type: TypeA}, proofs)
			if got.Status != tc.status || got.Name != tc.name || len(got.Path) != tc.steps {
				t.Fatalf("Resolve = %s at %q, %d steps; want %s at %q, %d steps (reason %q)",
					got.Status, got.Name, len(got.Path), tc.status, tc.name, tc.steps, got.Reason)
			}
			if len(rep.Paths) == 0 {
				t.Fatal("enumeration produced no path")
			}
			for _, p := range rep.Paths {
				verifyPathAuthority(t, p)
			}
			if tc.status == StatusUnknown {
				if _, ok := got.CacheKey(); ok {
					t.Fatal("unknown result must not be cacheable")
				}
			}
		})
	}

	// The forged cross-zone denial must not appear as a trusted end path.
	rep := res.Enumerate(Query{Name: "x.attacker.test", Type: TypeA}, proofs)
	for _, p := range rep.Paths {
		if p.Status == StatusNXDomain || p.Name == "y.victim.test" && p.Definitive() {
			t.Fatalf("forged denial became a trusted path: %#v", p)
		}
		for _, s := range p.Steps {
			if s.ProofID == "forged-y" {
				t.Fatalf("forged proof appears in an authorization path: %#v", s)
			}
		}
	}
}

func TestMissingAndOutOfTreeProofsAreUnknown(t *testing.T) {
	res := newResolver("example.com")
	got := res.Resolve(Query{Name: "a.example.com", Type: TypeA}, nil)
	if got.Status != StatusUnknown || !strings.Contains(got.Reason, "missing") {
		t.Fatalf("empty evidence = %s, %q", got.Status, got.Reason)
	}

	out := Proof{
		ID:         "outside",
		Kind:       ProofNXDomain,
		Name:       "a.other.test",
		Zone:       "other.test",
		ValidFrom:  testFrom,
		ValidUntil: testTo,
	}
	got = res.Resolve(Query{Name: "a.other.test", Type: TypeA}, []Proof{out})
	if got.Status != StatusUnknown || !hasRejection(got.Rejections, "outside", RejectNoAuthority) {
		t.Fatalf("out-of-tree proof = %s, %#v", got.Status, got.Rejections)
	}
}

func TestWrongZoneProofCannotBeRescuedByAnotherGoodProof(t *testing.T) {
	res := newResolver("example.com")
	proofs := []Proof{
		aliasProof("ok-hop", "a.example.com", "b.example.com", "example.com"),
		{
			ID:         "wrong-zone-answer",
			Kind:       ProofAnswer,
			Name:       "b.example.com",
			Zone:       "evil.test",
			Type:       TypeA,
			Value:      "192.0.2.66",
			ValidFrom:  testFrom,
			ValidUntil: testTo,
		},
	}
	got := res.Resolve(Query{Name: "a.example.com", Type: TypeA}, proofs)
	if got.Status != StatusUnknown {
		t.Fatalf("wrong-zone answer produced %s, want unknown", got.Status)
	}
}

func TestConflictingTrustedEvidenceIsUnknown(t *testing.T) {
	res := newResolver("example.com")

	aliasAndDenial := []Proof{
		aliasProof("alias-at-a", "a.example.com", "b.example.com", "example.com"),
		nxProof("denial-at-a", "a.example.com", "example.com"),
	}
	got := res.Resolve(Query{Name: "a.example.com", Type: TypeA}, aliasAndDenial)
	if got.Status != StatusUnknown || !strings.Contains(got.Reason, "ambiguous") {
		t.Fatalf("alias plus terminal denial = %s, %q", got.Status, got.Reason)
	}

	nxAndNodata := []Proof{
		nxProof("nx", "e.example.com", "example.com"),
		nodataProof("nodata", "e.example.com", "example.com", TypeA),
	}
	got = res.Resolve(Query{Name: "e.example.com", Type: TypeA}, nxAndNodata)
	if got.Status != StatusUnknown || !strings.Contains(got.Reason, "ambiguous") {
		t.Fatalf("nxdomain plus nodata = %s, %q", got.Status, got.Reason)
	}

	answerA := answerProof("answer-a", "f.example.com", "example.com", TypeA, "192.0.2.10")
	answerB := answerProof("answer-b", "f.example.com", "example.com", TypeA, "192.0.2.11")
	got = res.Resolve(Query{Name: "f.example.com", Type: TypeA}, []Proof{answerA, answerB})
	if got.Status != StatusUnknown || !strings.Contains(got.Reason, "ambiguous") {
		t.Fatalf("two positive answers = %s, %q", got.Status, got.Reason)
	}
}

func TestNamesAndZonesAreNormalized(t *testing.T) {
	res := newResolver("Example.COM.")
	proofs := []Proof{
		aliasProof("jump", "WWW.Example.COM.", "API.Example.com.", "EXAMPLE.com"),
		nxProof("end", "api.example.com", "example.com."),
	}
	got := res.Resolve(Query{Name: "WWW.Example.COM.", Type: "a"}, proofs)
	if got.Status != StatusNXDomain || got.Name != "api.example.com" {
		t.Fatalf("normalized names produced %s at %q", got.Status, got.Name)
	}
	if got.Query.Name != "www.example.com" || got.Query.Type != TypeA {
		t.Fatalf("query was not normalized: %#v", got.Query)
	}
	key, ok := got.CacheKey()
	if !ok || key.Name != "api.example.com" || key.Type != TypeA {
		t.Fatalf("normalized cache key = %#v, %v", key, ok)
	}
}

func TestAliasQueryTypeBinding(t *testing.T) {
	res := newResolver("example.com")
	alias := aliasProof("typed-alias", "a.example.com", "b.example.com", "example.com")
	alias.QType = TypeA
	nx := nxProof("end", "b.example.com", "example.com")

	got := res.Resolve(Query{Name: "a.example.com", Type: TypeA}, []Proof{alias, nx})
	if got.Status != StatusNXDomain {
		t.Fatalf("A-bound alias did not apply to A: %s", got.Status)
	}
	got = res.Resolve(Query{Name: "a.example.com", Type: TypeAAAA}, []Proof{alias, nx})
	if got.Status != StatusUnknown || !hasRejection(got.Rejections, "typed-alias", RejectTypeMismatch) {
		t.Fatalf("A-bound alias leaked to AAAA: %s, %#v", got.Status, got.Rejections)
	}
}

func TestNoDataWithoutAnyTypeBindingIsRejected(t *testing.T) {
	res := newResolver("example.com")
	p := Proof{
		ID:         "unbound-nodata",
		Kind:       ProofNoData,
		Name:       "a.example.com",
		Zone:       "example.com",
		ValidFrom:  testFrom,
		ValidUntil: testTo,
	}
	got := res.Resolve(Query{Name: "a.example.com", Type: TypeA}, []Proof{p})
	if got.Status != StatusUnknown || !hasRejection(got.Rejections, "unbound-nodata", RejectInvalidProof) {
		t.Fatalf("unbound nodata produced %s, %#v", got.Status, got.Rejections)
	}
}

func TestEmptyNamesAndZonesAreRejected(t *testing.T) {
	if _, err := NewZoneSet(""); err == nil {
		t.Fatal("empty zone name must be rejected")
	}
	res := newResolver("example.com")
	p := Proof{
		ID:         "empty-zone",
		Kind:       ProofNXDomain,
		Name:       "a.example.com",
		ValidFrom:  testFrom,
		ValidUntil: testTo,
	}
	got := res.Resolve(Query{Name: "a.example.com", Type: TypeA}, []Proof{p})
	if got.Status != StatusUnknown || !hasRejection(got.Rejections, "empty-zone", RejectMalformed) {
		t.Fatalf("empty proof zone produced %s, %#v", got.Status, got.Rejections)
	}

	got = res.Resolve(Query{Name: "  ", Type: TypeA}, nil)
	if got.Status != StatusUnknown {
		t.Fatalf("empty query name produced %s", got.Status)
	}
}

func verifyPathAuthority(t *testing.T, p Path) {
	t.Helper()
	if p.Query.Name == "" {
		t.Fatalf("path has empty query: %#v", p)
	}
	zs, err := NewZoneSet("example.com", "leaf.example.com", "attacker.test", "victim.test")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range p.Steps {
		name, err := CanonicalName(s.Name)
		if err != nil {
			t.Fatalf("bad step name: %v", err)
		}
		want, ok := zs.Authority(name)
		if !ok || s.Zone != want {
			t.Fatalf("step %s uses zone %q, authority is %q (ok=%v)", name, s.Zone, want, ok)
		}
		if s.Kind == StepAlias {
			if _, err := CanonicalName(s.Target); err != nil {
				t.Fatalf("bad alias target in path: %v", err)
			}
		}
		if name == "" {
			t.Fatalf("empty step name: %#v", s)
		}
	}
}

func hasRejection(rejections []Rejection, id string, code RejectionCode) bool {
	for _, r := range rejections {
		if r.ProofID == id && r.Code == code {
			return true
		}
	}
	return false
}

package netsec

import (
	"errors"
	"strings"
	"testing"
)

const (
	oldHost   = "git.example.net"
	newHost   = "modules.example.corp"
	pubHost   = "proxy.golang.org"
	moduleA   = "git.example.net/team/lib"
	moduleB   = "git.example.net/team/lib/sub"
	moduleX   = "git.example.net/team-evil"
	modulePub = "git.example.net/team"
)

func testFingerprint(t *testing.T) Fingerprint {
	t.Helper()
	fp, err := ParseFingerprint("ab:cd:00:11:22:33:44:55:66:77:88:99:aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99:aa:bb:cc:dd")
	if err != nil {
		t.Fatal(err)
	}
	return fp
}

func testDomain(t *testing.T) *VersionDomain {
	t.Helper()
	d := NewVersionDomain()
	mustAdd(t, d, moduleA, "0.9.0", "1.0.0-alpha", "1.0.0", "1.5.0", "2.0.0-alpha", "2.0.0", "2.1.0", "3.0.0")
	mustAdd(t, d, moduleB, "1.0.0", "1.2.0")
	mustAdd(t, d, moduleX, "1.0.0")
	mustAdd(t, d, modulePub, "1.0.0")
	return d
}

func mustAdd(t *testing.T, d *VersionDomain, modulePath string, versions ...string) {
	t.Helper()
	if err := d.Add(modulePath, versions...); err != nil {
		t.Fatalf("domain.Add(%s, %v): %v", modulePath, versions, err)
	}
}

func baseRule(t *testing.T, id, scope, rng string) Rule {
	t.Helper()
	vr, err := ParseVersionRange(rng)
	if err != nil {
		t.Fatal(err)
	}
	return Rule{
		ID:        id,
		OldSource: SourceIdentity{Host: oldHost},
		Scope:     PackageScope{Prefix: scope},
		Versions:  vr,
		Target:    Target{NewHost: newHost, Fingerprint: testFingerprint(t)},
	}
}

func compileOrFail(t *testing.T, rules []Rule, domain *VersionDomain) *Snapshot {
	t.Helper()
	snap, err := Compile("snap-1", rules, domain)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}
	if snap.ID() != "snap-1" {
		t.Fatalf("snapshot ID = %q, want snap-1", snap.ID())
	}
	return snap
}

func TestResolveApprovedBoundaryAndNoFallback(t *testing.T) {
	snap := compileOrFail(t,
		[]Rule{baseRule(t, "r1", moduleA, ">=1.0.0 <2.0.0")},
		testDomain(t),
	)

	fp := testFingerprint(t)

	for _, v := range []string{"1.0.0", "1.5.0", "2.0.0-alpha"} {
		got, err := snap.ResolveModule(SourceIdentity{Host: oldHost}, moduleA, v)
		if err != nil {
			t.Fatalf("ResolveModule(%s) unexpected error: %v", v, err)
		}
		if got.NewHost != newHost || !got.Fingerprint.equal(fp) {
			t.Fatalf("ResolveModule(%s) = %+v, want pinned target", v, got)
		}
	}

	denied := []struct {
		name   string
		source SourceIdentity
		module string
		ver    string
		want   error
	}{
		{"lower prerelease excluded", SourceIdentity{Host: oldHost}, moduleA, "1.0.0-alpha", ErrNoRule},
		{"upper boundary excluded", SourceIdentity{Host: oldHost}, moduleA, "2.0.0", ErrNoRule},
		{"below range", SourceIdentity{Host: oldHost}, moduleA, "0.9.0", ErrNoRule},
		{"above range", SourceIdentity{Host: oldHost}, moduleA, "2.1.0", ErrNoRule},
		{"outside segment prefix", SourceIdentity{Host: oldHost}, moduleX, "1.0.0", ErrNoRule},
		{"unknown version", SourceIdentity{Host: oldHost}, moduleA, "1.7.7", ErrUnknownVersion},
		{"unknown module", SourceIdentity{Host: oldHost}, "git.example.net/team/ghost", "1.0.0", ErrUnknownModule},
		{"wrong source identity", SourceIdentity{Host: pubHost}, moduleA, "1.0.0", ErrNoRule},
		{"public host same-named package", SourceIdentity{Host: pubHost}, modulePub, "1.0.0", ErrNoRule},
	}
	for _, tc := range denied {
		t.Run(tc.name, func(t *testing.T) {
			_, err := snap.ResolveModule(tc.source, tc.module, tc.ver)
			if !errors.Is(err, tc.want) {
				t.Fatalf("ResolveModule(%s@%s) err = %v, want %v", tc.module, tc.ver, err, tc.want)
			}
		})
	}
}

func TestOverlappingRulesSameTargetAllowed(t *testing.T) {
	r1 := baseRule(t, "r1", "git.example.net/team/", ">=1.0.0 <2.0.0")
	r2 := baseRule(t, "r2", moduleA, ">=1.2.0 <=2.1.0")
	snap := compileOrFail(t, []Rule{r1, r2}, testDomain(t))

	for _, v := range []string{"1.0.0", "1.5.0", "2.0.0", "2.1.0"} {
		if _, err := snap.ResolveModule(SourceIdentity{Host: oldHost}, moduleA, v); err != nil {
			t.Fatalf("version %s should be approved by converging rules: %v", v, err)
		}
	}
}

func TestOverlappingRulesTargetMismatchReportsVersion(t *testing.T) {
	r1 := baseRule(t, "r1", moduleA, ">=1.0.0 <2.0.0")
	r2 := baseRule(t, "r2", moduleA, ">=1.5.0 <=2.0.0")
	r2.Target.NewHost = "other.example.corp"

	_, err := Compile("snap-1", []Rule{r1, r2}, testDomain(t))
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected ConflictError, got %v", err)
	}
	found := false
	for _, c := range conflict.Conflicts {
		if c.Reason == ReasonTargetMismatch && c.ModulePath == moduleA && c.Version == "1.5.0" {
			found = true
			joined := strings.Join(c.RuleIDs, ",")
			if !strings.Contains(joined, "r1") || !strings.Contains(joined, "r2") {
				t.Fatalf("conflict must name both rules, got %v", c.RuleIDs)
			}
		}
	}
	if !found {
		t.Fatalf("expected a 1.5.0 target-mismatch conflict, got %v", conflict.Conflicts)
	}
	if !strings.Contains(err.Error(), "1.5.0") {
		t.Fatalf("error message must name the conflicting version: %v", err)
	}
}

func TestFingerprintMismatchIsConflict(t *testing.T) {
	r1 := baseRule(t, "r1", moduleA, ">=1.0.0 <=1.5.0")
	r2 := baseRule(t, "r2", moduleA, ">=1.5.0 <2.0.0")
	otherFP, err := ParseFingerprint("00:11:22:33:44:55:66:77:88:99:aa:bb:cc:dd:ee:ff:00:11:22:33:44:55:66:77:88:99:aa:bb:cc:dd:ee:ff")
	if err != nil {
		t.Fatal(err)
	}
	r2.Target.Fingerprint = otherFP

	_, err = Compile("snap-1", []Rule{r1, r2}, testDomain(t))
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected fingerprint conflict, got %v", err)
	}
	if conflict.Conflicts[0].Version != "1.5.0" {
		t.Fatalf("conflict should pinpoint shared version 1.5.0, got %s", conflict.Conflicts[0].Version)
	}
}

func TestRevokedVersionDeniedWithoutFallback(t *testing.T) {
	r1 := baseRule(t, "r1", "git.example.net/team/", ">=1.0.0 <2.0.0")
	r2 := baseRule(t, "r2", moduleA, ">=2.0.0 <=2.0.0")
	r2.Revoked = true
	r2.Target = Target{}
	snap := compileOrFail(t, []Rule{r1, r2}, testDomain(t))

	if _, err := snap.ResolveModule(SourceIdentity{Host: oldHost}, moduleA, "2.0.0"); !errors.Is(err, ErrRevoked) {
		t.Fatalf("revoked version must fail with ErrRevoked, got %v", err)
	}
	for _, v := range []string{"1.0.0", "1.5.0"} {
		if _, err := snap.ResolveModule(SourceIdentity{Host: oldHost}, moduleA, v); err != nil {
			t.Fatalf("non-revoked version %s should still resolve: %v", v, err)
		}
	}
	if _, err := snap.ResolveModule(SourceIdentity{Host: oldHost}, moduleA, "2.1.0"); !errors.Is(err, ErrNoRule) {
		t.Fatalf("version beyond approval and revocation must have no rule, got %v", err)
	}
}

func TestRevocationOverlappingApprovalIsConflict(t *testing.T) {
	r1 := baseRule(t, "r1", moduleA, ">=1.0.0 <=2.0.0")
	r2 := baseRule(t, "r2", moduleA, ">=1.5.0 <=1.5.0")
	r2.Revoked = true
	r2.Target = Target{}

	_, err := Compile("snap-1", []Rule{r1, r2}, testDomain(t))
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	if conflict.Conflicts[0].Reason != ReasonRevokedAndApproved {
		t.Fatalf("reason = %v, want ReasonRevokedAndApproved", conflict.Conflicts[0].Reason)
	}
	if conflict.Conflicts[0].Version != "1.5.0" {
		t.Fatalf("conflict version = %s, want 1.5.0", conflict.Conflicts[0].Version)
	}
}

func TestIneffectiveRuleRejected(t *testing.T) {
	r1 := baseRule(t, "r1", moduleA, ">=4.0.0 <5.0.0")
	_, err := Compile("snap-1", []Rule{r1}, testDomain(t))
	if err == nil || !strings.Contains(err.Error(), "matches no version") {
		t.Fatalf("expected ineffective-rule error, got %v", err)
	}
}

func TestRuleValidation(t *testing.T) {
	valid := baseRule(t, "r1", moduleA, ">=1.0.0 <2.0.0")
	cases := []struct {
		name string
		mut  func(*Rule)
	}{
		{"missing old source", func(r *Rule) { r.OldSource = SourceIdentity{} }},
		{"empty scope", func(r *Rule) { r.Scope = PackageScope{} }},
		{"missing new host", func(r *Rule) { r.Target.NewHost = "" }},
		{"missing fingerprint", func(r *Rule) { r.Target.Fingerprint = nil }},
		{"revoked carrying target", func(r *Rule) { r.Revoked = true; r.Target = Target{NewHost: newHost} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := valid
			tc.mut(&r)
			if _, err := Compile("snap-1", []Rule{r}, testDomain(t)); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestDuplicateRuleIDRejected(t *testing.T) {
	r1 := baseRule(t, "dup", moduleA, ">=1.0.0 <1.5.0")
	r2 := baseRule(t, "dup", moduleA, ">=1.5.0 <2.0.0")
	if _, err := Compile("snap-1", []Rule{r1, r2}, testDomain(t)); err == nil {
		t.Fatal("expected duplicate rule ID error")
	}
}

func TestSnapshotImmuneToInputMutation(t *testing.T) {
	rules := []Rule{baseRule(t, "r1", moduleA, ">=1.0.0 <2.0.0")}
	domain := testDomain(t)
	snap := compileOrFail(t, rules, domain)

	rules[0].Target.NewHost = "mutated.example"
	rules[0].Target.Fingerprint[0] = 0xFF
	rules[0].OldSource.Host = "mutated.example"
	delete(domain.modules, moduleA)

	target, err := snap.ResolveModule(SourceIdentity{Host: oldHost}, moduleA, "1.0.0")
	if err != nil {
		t.Fatalf("snapshot must be immutable, got error: %v", err)
	}
	if target.NewHost != newHost {
		t.Fatalf("target host mutated to %q", target.NewHost)
	}
	if target.Fingerprint[0] == 0xFF {
		t.Fatal("target fingerprint mutated through caller slice")
	}
	if _, err := snap.ResolveModule(SourceIdentity{Host: oldHost}, moduleA, "0.9.0"); !errors.Is(err, ErrNoRule) {
		t.Fatalf("snapshot domain changed unexpectedly: %v", err)
	}

	target.Fingerprint[0] = 0x11
	again, err := snap.ResolveModule(SourceIdentity{Host: oldHost}, moduleA, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if again.Fingerprint[0] == 0x11 {
		t.Fatal("returned target must not alias snapshot memory")
	}
}

func TestParseFingerprint(t *testing.T) {
	fp, err := ParseFingerprint("ABCD00112233445566778899AABBCCDDEEFF00112233445566778899AABBCCDD")
	if err != nil {
		t.Fatal(err)
	}
	if len(fp) != 32 || fp[0] != 0xAB || fp[31] != 0xDD {
		t.Fatalf("unexpected decoded fingerprint: %x", fp)
	}
	bad := []string{"not-hex", "abcd", strings.Repeat("ab", 33)}
	for _, in := range bad {
		if _, err := ParseFingerprint(in); err == nil {
			t.Fatalf("ParseFingerprint(%q) should fail", in)
		}
	}
}

func TestSourceIdentityAndScope(t *testing.T) {
	scope := PackageScope{Prefix: "git.example.net/team/"}
	if !scope.contains(moduleA) || !scope.contains(moduleB) {
		t.Fatal("segment prefix should contain nested module paths")
	}
	if scope.contains(moduleX) {
		t.Fatal("segment prefix must not match a sibling with a different first segment")
	}

	exact := PackageScope{Prefix: moduleA}
	if !exact.contains(moduleA) || exact.contains(moduleB) {
		t.Fatal("exact scope must match only the exact path")
	}

	snap := compileOrFail(t,
		[]Rule{baseRule(t, "r1", moduleA, ">=1.0.0 <2.0.0")},
		testDomain(t),
	)
	if _, err := snap.ResolveModule(SourceIdentity{Host: "GIT.Example.NET."}, moduleA, "1.0.0"); err != nil {
		t.Fatalf("canonicalized source identity should match: %v", err)
	}
	withInstance := baseRule(t, "r2", moduleB, ">=1.0.0 <=1.2.0")
	withInstance.OldSource.InstanceID = "vcs-7"
	snap2, err := Compile("snap-2", []Rule{withInstance}, testDomain(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := snap2.ResolveModule(SourceIdentity{Host: oldHost}, moduleB, "1.0.0"); !errors.Is(err, ErrNoRule) {
		t.Fatalf("empty instance ID must not match an instance-scoped rule: %v", err)
	}
	if _, err := snap2.ResolveModule(SourceIdentity{Host: oldHost, InstanceID: "vcs-7"}, moduleB, "1.2.0"); err != nil {
		t.Fatalf("matching instance ID should resolve: %v", err)
	}
}

func TestCompileRequiresDomain(t *testing.T) {
	r1 := baseRule(t, "r1", moduleA, ">=1.0.0 <2.0.0")
	if _, err := Compile("snap", []Rule{r1}, nil); err == nil {
		t.Fatal("Compile without a version domain must fail")
	}
	if _, err := Compile("snap", []Rule{r1}, NewVersionDomain()); err == nil {
		t.Fatal("Compile against an empty domain must fail")
	}
}

func TestConflictErrorIs(t *testing.T) {
	err := &ConflictError{Conflicts: []Conflict{{Reason: ReasonTargetMismatch, Version: "1.0.0"}}}
	if !errors.Is(err, &ConflictError{}) {
		t.Fatal("ConflictError should satisfy errors.Is with the error type")
	}
	if errors.Is(err, ErrNoRule) {
		t.Fatal("ConflictError must not masquerade as ErrNoRule")
	}
}

func TestEmptyRuleSetNeverApproves(t *testing.T) {
	snap, err := Compile("empty", nil, testDomain(t))
	if err != nil {
		t.Fatalf("empty rule set should compile: %v", err)
	}
	_, err = snap.ResolveModule(SourceIdentity{Host: oldHost}, moduleA, "1.0.0")
	if !errors.Is(err, ErrNoRule) {
		t.Fatalf("snapshot without rules must fail closed with ErrNoRule, got %v", err)
	}
}

func TestVersionDomainDeduplicatesAndCopies(t *testing.T) {
	d := NewVersionDomain()
	if err := d.Add(moduleA, "1.0.0", "v1.0.0", "1.1.0"); err != nil {
		t.Fatal(err)
	}
	dup := d.modules[moduleA]
	if len(dup) != 2 {
		t.Fatalf("expected two distinct versions, got %d", len(dup))
	}

	if err := d.Add("", "1.0.0"); err == nil {
		t.Fatal("empty module path must be rejected")
	}
	if err := d.Add(moduleA, "not-a-version"); err == nil {
		t.Fatal("invalid versions must be rejected")
	}
}

func TestResolveSemVerEntryPoint(t *testing.T) {
	snap := compileOrFail(t,
		[]Rule{baseRule(t, "r1", moduleA, ">=1.0.0 <2.0.0")},
		testDomain(t),
	)
	v := mustVersion(t, "v1.5.0")
	if _, err := snap.Resolve(SourceIdentity{Host: oldHost}, moduleA, v); err != nil {
		t.Fatalf("Resolve with parsed SemVer failed: %v", err)
	}
	unknown := mustVersion(t, "1.5.99")
	if _, err := snap.Resolve(SourceIdentity{Host: oldHost}, moduleA, unknown); !errors.Is(err, ErrUnknownVersion) {
		t.Fatalf("unknown version must fail closed, got %v", err)
	}

	if _, err := snap.ResolveModule(SourceIdentity{Host: oldHost}, moduleA, "not-a-version"); err == nil {
		t.Fatal("ResolveModule must reject unparseable versions")
	}
}

func TestVersionDomainNilReceiverRejected(t *testing.T) {
	var d *VersionDomain
	if err := d.Add(moduleA, "1.0.0"); err == nil {
		t.Fatal("Add on a nil domain must return an error")
	}
}

func TestSourceIdentityEqualityCanonicalizes(t *testing.T) {
	a := SourceIdentity{Host: "Git.Example.NET.", InstanceID: "x"}
	b := SourceIdentity{Host: "git.example.net", InstanceID: " x "}
	if !a.equal(b) {
		t.Fatal("canonicalized source identities should compare equal")
	}
	c := SourceIdentity{Host: "git.example.net", InstanceID: "y"}
	if a.equal(c) {
		t.Fatal("different instance IDs must not be equal")
	}

	empty := PackageScope{Prefix: " / "}
	if empty.contains(moduleA) {
		t.Fatal("blank scope must not contain anything")
	}
}

package devrule

import (
	"testing"
)

func mustCompile(t *testing.T, rules ...Rule) *Diagram {
	t.Helper()
	d, err := Compile(rules)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return d
}

func mustParse(t *testing.T, effect Effect, spec string) Rule {
	t.Helper()
	r, err := ParseRule(effect, spec)
	if err != nil {
		t.Fatalf("ParseRule(%q): %v", spec, err)
	}
	return r
}

func TestAllowAllThenDenyDisk(t *testing.T) {
	d := mustCompile(t,
		mustParse(t, Allow, "a *:* rwm"),
		mustParse(t, Deny, "b 8:0 rw"),
	)
	if got := d.Lookup(Block, 8, 0); got != PermMknod {
		t.Errorf("disk b 8:0 = %v, want m (r/w removed, mknod untouched)", got)
	}
	if got := d.Lookup(Block, 8, 1); got != PermAll {
		t.Errorf("neighbor b 8:1 = %v, want rwm", got)
	}
	if got := d.Lookup(Char, 8, 0); got != PermAll {
		t.Errorf("char c 8:0 = %v, want rwm (deny only matched block)", got)
	}
}

func TestDenyThenExactAllow(t *testing.T) {
	// Deny before allow: the later exact allow wins.
	d := mustCompile(t,
		mustParse(t, Allow, "a *:* rwm"),
		mustParse(t, Deny, "b 8:0 w"),
		mustParse(t, Allow, "b 8:0 w"),
	)
	if got := d.Lookup(Block, 8, 0); got != PermAll {
		t.Errorf("b 8:0 = %v, want rwm (exact allow after deny)", got)
	}
	// Reverse order: deny wins.
	d2 := mustCompile(t,
		mustParse(t, Allow, "b 8:0 w"),
		mustParse(t, Deny, "b 8:0 w"),
	)
	if got := d2.Lookup(Block, 8, 0); got != 0 {
		t.Errorf("b 8:0 = %v, want - (deny after allow)", got)
	}
	// Deny on a fresh device is a no-op.
	d3 := mustCompile(t, mustParse(t, Deny, "c 1:1 r"))
	if got := d3.Lookup(Char, 1, 1); got != 0 {
		t.Errorf("c 1:1 = %v, want - (initial set is empty)", got)
	}
}

func TestTypeAllMatchesBothKinds(t *testing.T) {
	d := mustCompile(t, mustParse(t, Allow, "a 10:20 r"))
	if got := d.Lookup(Char, 10, 20); got != PermRead {
		t.Errorf("c 10:20 = %v, want r", got)
	}
	if got := d.Lookup(Block, 10, 20); got != PermRead {
		t.Errorf("b 10:20 = %v, want r", got)
	}
	if got := d.Lookup(Char, 10, 21); got != 0 {
		t.Errorf("c 10:21 = %v, want -", got)
	}
}

func TestPermissionSubsets(t *testing.T) {
	d := mustCompile(t,
		mustParse(t, Allow, "b 1:2 rwm"),
		mustParse(t, Deny, "b 1:2 rw"),
	)
	if got := d.Lookup(Block, 1, 2); got != PermMknod {
		t.Errorf("b 1:2 = %v, want m (deny removes only the named subset)", got)
	}
	// m controls node creation but never implies r/w.
	d2 := mustCompile(t, mustParse(t, Allow, "c 3:4 m"))
	if got := d2.Lookup(Char, 3, 4); got != PermMknod {
		t.Errorf("c 3:4 = %v, want exactly m", got)
	}
	if got := d2.Lookup(Char, 3, 4) & (PermRead | PermWrite); got != 0 {
		t.Errorf("mknod leaked read/write: %v", got)
	}
}

func TestBoundary4095(t *testing.T) {
	d := mustCompile(t,
		mustParse(t, Allow, "a *:* w"),
		mustParse(t, Deny, "b 4095:4095 w"),
	)
	if got := d.Lookup(Block, 4095, 4095); got != 0 {
		t.Errorf("b 4095:4095 = %v, want -", got)
	}
	if got := d.Lookup(Block, 4095, 4094); got != PermWrite {
		t.Errorf("b 4095:4094 = %v, want w", got)
	}
	if got := d.Lookup(Block, 4094, 4095); got != PermWrite {
		t.Errorf("b 4094:4095 = %v, want w", got)
	}
	if got := d.Lookup(Char, 4095, 4095); got != PermWrite {
		t.Errorf("c 4095:4095 = %v, want w", got)
	}
	// Out-of-range numbers are rejected.
	if _, err := ParseRule(Allow, "b 4096:0 r"); err == nil {
		t.Error("major 4096 accepted, want range error")
	}
	if _, err := ParseRule(Allow, "b 0:4096 r"); err == nil {
		t.Error("minor 4096 accepted, want range error")
	}
	if _, err := Compile([]Rule{{Effect: Allow, Type: SelBlock, Major: 0, Minor: 0, Perms: 0}}); err == nil {
		t.Error("empty permission set accepted, want error")
	}
}

// TestRegionInvariant checks the diagram stays disjoint and covers the
// whole device space after a mixed rule sequence.
func TestRegionInvariant(t *testing.T) {
	d := mustCompile(t,
		mustParse(t, Allow, "a *:* rwm"),
		mustParse(t, Deny, "b 8:0 rw"),
		mustParse(t, Allow, "c 5:* w"),
		mustParse(t, Deny, "a 4095:4095 m"),
		mustParse(t, Allow, "b 1:1 r"),
	)
	var total int64
	for i, r := range d.Regions {
		area := int64(r.MajHi-r.MajLo+1) * int64(r.MinHi-r.MinLo+1)
		if area <= 0 {
			t.Fatalf("region %d has non-positive area: %v", i, r)
		}
		total += area
		for j := i + 1; j < len(d.Regions); j++ {
			if _, ok := overlap(r, d.Regions[j]); ok {
				t.Fatalf("regions %d and %d overlap: %v vs %v", i, j, r, d.Regions[j])
			}
		}
	}
	want := int64(2) * (MaxDeviceNumber + 1) * (MaxDeviceNumber + 1)
	if total != want {
		t.Fatalf("regions cover %d devices, want %d", total, want)
	}
}

func TestVerifyNotInTargetAllow(t *testing.T) {
	target := Target{
		Allowed: []TargetEntry{{Type: SelChar, Major: 1, Minor: 3, Perms: PermRead}},
	}
	d := mustCompile(t, mustParse(t, Allow, "b 5:5 r"))
	res := Verify(d, target)
	if res.Sound {
		t.Fatal("expected unsound result")
	}
	v := res.Violation
	if v.Kind != Block || v.Major != 5 || v.Minor != 5 || v.Perms != PermRead || v.Reason != NotInTargetAllow {
		t.Errorf("violation = %v, want b 5:5 r (not-in-target-allow)", v)
	}
	// The target-allowed c 1:3 r is wrongly denied: completeness hint.
	if res.Completeness == nil {
		t.Fatal("missing completeness hint")
	}
	h := res.Completeness
	if h.Kind != Char || h.Major != 1 || h.Minor != 3 || h.Perm != PermRead {
		t.Errorf("hint = %v, want c 1:3 r", h)
	}
}

func TestCompletenessHintOrdering(t *testing.T) {
	target := Target{Allowed: []TargetEntry{
		{Type: SelBlock, Major: 0, Minor: 0, Perms: PermRead},
		{Type: SelChar, Major: 9, Minor: 9, Perms: PermRead},
	}}
	// c < b: the char device wins even at a higher major.
	res := Verify(mustCompile(t), target)
	if res.Completeness == nil || res.Completeness.Kind != Char || res.Completeness.Major != 9 {
		t.Errorf("hint = %v, want c 9:9 r (type c sorts before b)", res.Completeness)
	}
	// Grant the char device; the block device becomes the hint.
	res = Verify(mustCompile(t, mustParse(t, Allow, "c 9:9 r")), target)
	if res.Completeness == nil || res.Completeness.Kind != Block {
		t.Errorf("hint = %v, want b 0:0 r", res.Completeness)
	}
	// Permission bit order r < w < m.
	target2 := Target{Allowed: []TargetEntry{
		{Type: SelChar, Major: 1, Minor: 1, Perms: PermWrite | PermMknod},
	}}
	res = Verify(mustCompile(t), target2)
	if res.Completeness == nil || res.Completeness.Perm != PermWrite {
		t.Errorf("hint = %v, want c 1:1 w (w before m)", res.Completeness)
	}
	// Fully covered target: no hint.
	res = Verify(mustCompile(t, mustParse(t, Allow, "a *:* rwm")), target)
	if res.Completeness != nil {
		t.Errorf("hint = %v, want nil (target fully covered)", res.Completeness)
	}
}

func TestCheckOptimizer(t *testing.T) {
	rules := []Rule{
		mustParse(t, Allow, "b 8:0 w"),
		mustParse(t, Deny, "b 8:0 w"),
	}
	// A bad optimizer that swaps rule order changes semantics.
	diff, err := CheckOptimizer(rules, func(rs []Rule) []Rule {
		return []Rule{rs[1], rs[0]}
	})
	if err != nil {
		t.Fatalf("CheckOptimizer: %v", err)
	}
	if diff == nil {
		t.Fatal("expected semantic difference")
	}
	if diff.Kind != Block || diff.Major != 8 || diff.Minor != 0 ||
		diff.Before != 0 || diff.After != PermWrite {
		t.Errorf("diff = %v, want b 8:0 before=- after=w", diff)
	}
	// A sound optimizer that drops a duplicate rule keeps semantics.
	dup := []Rule{
		mustParse(t, Allow, "a *:* r"),
		mustParse(t, Allow, "a *:* r"),
		mustParse(t, Deny, "b 1:1 r"),
	}
	diff, err = CheckOptimizer(dup, func(rs []Rule) []Rule { return rs[1:] })
	if err != nil {
		t.Fatalf("CheckOptimizer: %v", err)
	}
	if diff != nil {
		t.Errorf("diff = %v, want nil (duplicate removal is sound)", diff)
	}
}

// TestHostDiskWriteWitness is the end-to-end scenario: a tenant rule chain
// that re-acquires write permission on the host disk b 8:0 must be caught by
// the verifier, and the minimal witnessing chain must be extractable.
func TestHostDiskWriteWitness(t *testing.T) {
	target := Target{
		Allowed: []TargetEntry{
			{Type: SelAll, Major: Wildcard, Minor: Wildcard, Perms: PermAll},
		},
		Denied: []TargetEntry{
			{Type: SelBlock, Major: 8, Minor: 0, Perms: PermAll}, // host disk
		},
	}
	rules := []Rule{
		mustParse(t, Allow, "a *:* rwm"),
		mustParse(t, Deny, "b 8:0 w"),
		mustParse(t, Allow, "b 8:0 w"),
	}
	if !Grants(rules, Block, 8, 0, PermWrite) {
		t.Fatal("chain should grant host disk write")
	}
	res := Verify(mustCompile(t, rules...), target)
	if res.Sound {
		t.Fatal("verifier accepted a chain that opens the host disk")
	}
	v := res.Violation
	if v.Kind != Block || v.Major != 8 || v.Minor != 0 ||
		v.Perms&PermWrite == 0 || v.Reason != HitsMandatoryDeny {
		t.Errorf("violation = %v, want b 8:0 with w (hits-mandatory-deny)", v)
	}

	// Extract the minimal rule chain that still obtains host disk write.
	chain := MinimizeChain(rules, func(rs []Rule) bool {
		return Grants(rs, Block, 8, 0, PermWrite)
	})
	if len(chain) != 1 {
		t.Fatalf("minimal chain = %v, want a single rule", chain)
	}
	want := Rule{Effect: Allow, Type: SelBlock, Major: 8, Minor: 0, Perms: PermWrite}
	if chain[0] != want {
		t.Errorf("minimal chain rule = %v, want %v", chain[0], want)
	}
	t.Logf("minimal witness chain for host disk w: %v", chain)

	// The minimized chain alone still violates the target policy.
	res = Verify(mustCompile(t, chain...), target)
	if res.Sound || res.Violation.Reason != HitsMandatoryDeny ||
		res.Violation.Perms&PermWrite == 0 {
		t.Errorf("witness chain no longer violates: %+v", res)
	}
}

func TestParseRule(t *testing.T) {
	r := mustParse(t, Allow, "a *:* rwm")
	if r.Type != SelAll || r.Major != Wildcard || r.Minor != Wildcard || r.Perms != PermAll {
		t.Errorf("parsed %v, want allow a *:* rwm", r)
	}
	r = mustParse(t, Deny, "b 8:0 rw")
	if r.Type != SelBlock || r.Major != 8 || r.Minor != 0 || r.Perms != PermRead|PermWrite {
		t.Errorf("parsed %v, want deny b 8:0 rw", r)
	}
	for _, bad := range []string{"x 1:2 r", "b 1:2 x", "b 1 r", "b -1:2 r"} {
		if _, err := ParseRule(Allow, bad); err == nil {
			t.Errorf("ParseRule(%q) accepted, want error", bad)
		}
	}
}

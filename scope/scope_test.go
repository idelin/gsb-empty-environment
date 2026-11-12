package scope

import (
	"errors"
	"testing"
)

// testTree builds the domain hierarchy used across the tests:
//
//	root
//	├── a
//	│   └── a1
//	└── b
type testTree struct {
	root, a, a1, b *Domain
}

func newTestTree() testTree {
	root := NewDomain("root", nil)
	a := NewDomain("a", root)
	return testTree{
		root: root,
		a:    a,
		a1:   NewDomain("a1", a),
		b:    NewDomain("b", root),
	}
}

func scopedRuleset(t *testing.T) *Ruleset {
	t.Helper()
	rs, err := Compile(Manifest{ABI: MinScopeABI, ScopeSignal: true, ScopeAbstractUnixSocket: true})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return rs
}

func TestCompileOldABIRefused(t *testing.T) {
	cases := []Manifest{
		{ABI: 5, ScopeSignal: true},
		{ABI: 5, ScopeAbstractUnixSocket: true},
		{ABI: 1, ScopeSignal: true, ScopeAbstractUnixSocket: true},
	}
	for _, m := range cases {
		rs, err := Compile(m)
		if !errors.Is(err, ErrUnsupportedABI) {
			t.Errorf("Compile(%+v): want ErrUnsupportedABI, got %v", m, err)
		}
		if rs != nil {
			t.Errorf("Compile(%+v): ruleset must be nil, no downgrade", m)
		}
	}

	// Old ABI without scoping requests is fine.
	if _, err := Compile(Manifest{ABI: 5}); err != nil {
		t.Errorf("Compile(ABI 5, no scopes): %v", err)
	}
	// ABI 6 with scopes compiles and keeps both flags.
	rs, err := Compile(Manifest{ABI: 6, ScopeSignal: true, ScopeAbstractUnixSocket: true})
	if err != nil {
		t.Fatalf("Compile(ABI 6, scoped): %v", err)
	}
	if !rs.SignalScoped() || !rs.AbstractUnixScoped() {
		t.Error("scoped flags must survive compilation")
	}
}

func TestCrossDomainSIGKILLDenied(t *testing.T) {
	rs := scopedRuleset(t)
	tree := newTestTree()
	paths := []DeliveryPath{PathKill, PathTgkill, PathPidfdSendSignal}

	attacker := &Process{PID: 100, Domain: tree.a}
	daemon := &Process{PID: 1, Domain: tree.b} // unrelated domain

	for _, p := range paths {
		if err := rs.SendSignal(attacker, daemon, SIGKILL, p); !errors.Is(err, ErrCrossScope) {
			t.Errorf("path %v: cross-domain SIGKILL must be denied, got %v", p, err)
		}
	}

	// Signaling an ancestor domain is also denied.
	ancestor := &Process{PID: 2, Domain: tree.root}
	if err := rs.SendSignal(attacker, ancestor, SIGKILL, PathKill); !errors.Is(err, ErrCrossScope) {
		t.Errorf("ancestor-domain SIGKILL must be denied, got %v", err)
	}

	// Signaling an unscoped process is denied as well.
	unscoped := &Process{PID: 3}
	if err := rs.SendSignal(attacker, unscoped, SIGKILL, PathKill); !errors.Is(err, ErrCrossScope) {
		t.Errorf("SIGKILL to unscoped target must be denied, got %v", err)
	}
}

func TestSameAndDescendantDomainAllowed(t *testing.T) {
	rs := scopedRuleset(t)
	tree := newTestTree()
	paths := []DeliveryPath{PathKill, PathTgkill, PathPidfdSendSignal}

	sender := &Process{PID: 100, Domain: tree.a}
	sameDomain := &Process{PID: 101, Domain: tree.a}
	descendant := &Process{PID: 102, Domain: tree.a1}

	for _, p := range paths {
		if err := rs.SendSignal(sender, sameDomain, SIGKILL, p); err != nil {
			t.Errorf("path %v: same-domain signal must be allowed, got %v", p, err)
		}
		if err := rs.SendSignal(sender, descendant, SIGKILL, p); err != nil {
			t.Errorf("path %v: descendant-domain signal must be allowed, got %v", p, err)
		}
	}

	// An unscoped sender is unrestricted.
	unscoped := &Process{PID: 200}
	if err := rs.SendSignal(unscoped, sameDomain, SIGKILL, PathKill); err != nil {
		t.Errorf("unscoped sender must be allowed, got %v", err)
	}
}

func TestFSetOwnAuthorizedAtSetTime(t *testing.T) {
	rs := scopedRuleset(t)
	tree := newTestTree()

	caller := &Process{PID: 100, Domain: tree.root}
	owner := &Process{PID: 101, Domain: tree.a}

	// F_SETOWN while the caller is in root: root contains a, so allowed.
	fd, err := rs.FSetOwn(caller, owner)
	if err != nil {
		t.Fatalf("FSetOwn: %v", err)
	}

	// The caller then restricts itself into domain b, which is unrelated
	// to the owner's domain a.
	caller.Domain = tree.b

	// Delivery must still be authorized by the set-time domain (root),
	// not the caller's current domain (b).
	for _, tc := range []struct {
		sig  Signal
		path DeliveryPath
	}{
		{SIGIO, PathFSetOwnSIGIO},
		{SIGURG, PathFSetOwnSIGURG},
	} {
		if err := rs.Deliver(fd, tc.sig, tc.path); err != nil {
			t.Errorf("Deliver(%v) after domain change must use set-time domain, got %v", tc.sig, err)
		}
	}

	// Sanity: a synchronous kill from the caller's new domain b to the
	// owner in a is denied, proving the domains really are unrelated.
	if err := rs.SendSignal(caller, owner, SIGIO, PathKill); !errors.Is(err, ErrCrossScope) {
		t.Errorf("kill from new domain must be denied, got %v", err)
	}
}

func TestFSetOwnCrossScopeDeniedAtSetTime(t *testing.T) {
	rs := scopedRuleset(t)
	tree := newTestTree()

	attacker := &Process{PID: 100, Domain: tree.a}
	daemon := &Process{PID: 1, Domain: tree.b}

	// Setting the owner to a process in an unrelated domain fails
	// immediately; no deferred delivery is ever authorized.
	if _, err := rs.FSetOwn(attacker, daemon); !errors.Is(err, ErrCrossScope) {
		t.Errorf("F_SETOWN to unrelated domain must be denied, got %v", err)
	}

	// Same for an ancestor domain.
	ancestor := &Process{PID: 2, Domain: tree.root}
	if _, err := rs.FSetOwn(attacker, ancestor); !errors.Is(err, ErrCrossScope) {
		t.Errorf("F_SETOWN to ancestor domain must be denied, got %v", err)
	}
}

func TestUnscopedRulesetAllowsCrossDomain(t *testing.T) {
	rs, err := Compile(Manifest{ABI: 6})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	tree := newTestTree()
	sender := &Process{PID: 100, Domain: tree.a}
	target := &Process{PID: 101, Domain: tree.b}
	if err := rs.SendSignal(sender, target, SIGKILL, PathKill); err != nil {
		t.Errorf("unscoped ruleset must not restrict signals, got %v", err)
	}
}

func TestAbstractUnixSocketScope(t *testing.T) {
	rs := scopedRuleset(t)
	tree := newTestTree()

	connector := &Process{PID: 100, Domain: tree.a}
	outsideCreator := &Process{PID: 1, Domain: tree.b}
	insideCreator := &Process{PID: 101, Domain: tree.a1}

	if err := rs.CheckAbstractUnixConnect(connector, outsideCreator); !errors.Is(err, ErrCrossScope) {
		t.Errorf("connect to socket created in unrelated domain must be denied, got %v", err)
	}
	if err := rs.CheckAbstractUnixConnect(connector, insideCreator); err != nil {
		t.Errorf("connect to socket created in descendant domain must be allowed, got %v", err)
	}
}

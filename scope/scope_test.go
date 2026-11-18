package scope

import (
	"errors"
	"testing"
)

// testHierarchy builds:
//
//	root
//	├── sandboxA
//	│   └── sandboxA-child
//	└── daemonDomain
func testHierarchy() (root, sandboxA, sandboxAChild, daemon *Domain) {
	root = NewDomain("root", nil)
	sandboxA = NewDomain("sandbox-a", root)
	sandboxAChild = NewDomain("sandbox-a-child", sandboxA)
	daemon = NewDomain("daemon", root)
	return
}

func scopedRuleset(t *testing.T) *Ruleset {
	t.Helper()
	r, err := Compile(Manifest{ScopeSignal: true, ScopeAbstractSocket: true}, MinScopedABI)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return r
}

func TestCompileOldABIRefusesWithoutDowngrade(t *testing.T) {
	manifests := []Manifest{
		{ScopeSignal: true},
		{ScopeAbstractSocket: true},
		{ScopeSignal: true, ScopeAbstractSocket: true},
	}
	for _, m := range manifests {
		for _, abi := range []ABI{1, 3, 5} {
			r, err := Compile(m, abi)
			if !errors.Is(err, ErrABITooOld) {
				t.Errorf("Compile(%+v, ABI %d): want ErrABITooOld, got %v", m, abi, err)
			}
			if r != nil {
				t.Errorf("Compile(%+v, ABI %d): ruleset must be nil on refusal (no downgrade)", m, abi)
			}
		}
	}

	// No scopes requested: old ABI is fine.
	if _, err := Compile(Manifest{}, 1); err != nil {
		t.Errorf("Compile without scopes on ABI 1: %v", err)
	}
	// Exactly ABI 6 with scopes: accepted.
	if _, err := Compile(Manifest{ScopeSignal: true}, MinScopedABI); err != nil {
		t.Errorf("Compile with scopes on ABI 6: %v", err)
	}
}

func TestCrossDomainSIGKILLDenied(t *testing.T) {
	r := scopedRuleset(t)
	_, sandboxA, _, daemonDomain := testHierarchy()

	attacker := &Process{PID: 100, Name: "attacker", Domain: sandboxA}
	daemon := &Process{PID: 1, Name: "daemon", Domain: daemonDomain}
	init := &Process{PID: 2, Name: "init", Domain: nil}

	paths := []Path{Kill, Tgkill, PidfdSendSignal}
	for _, p := range paths {
		if err := r.CheckSignal(attacker, daemon, SIGKILL, p); !errors.Is(err, ErrCrossScope) {
			t.Errorf("%s: scoped -> unrelated domain: want ErrCrossScope, got %v", p, err)
		}
		if err := r.CheckSignal(attacker, init, SIGKILL, p); !errors.Is(err, ErrCrossScope) {
			t.Errorf("%s: scoped -> unrestricted: want ErrCrossScope, got %v", p, err)
		}
	}

	// Signaling an ancestor domain is also denied.
	rootProc := &Process{PID: 3, Name: "root-proc", Domain: NewDomain("root", nil)}
	nested := &Process{PID: 4, Name: "nested", Domain: NewDomain("child", rootProc.Domain)}
	if err := r.CheckSignal(nested, rootProc, SIGKILL, Kill); !errors.Is(err, ErrCrossScope) {
		t.Errorf("descendant -> ancestor: want ErrCrossScope, got %v", err)
	}
}

func TestSameAndDescendantDomainSignalAllowed(t *testing.T) {
	r := scopedRuleset(t)
	_, sandboxA, sandboxAChild, _ := testHierarchy()

	a1 := &Process{PID: 10, Name: "a1", Domain: sandboxA}
	a2 := &Process{PID: 11, Name: "a2", Domain: sandboxA}
	child := &Process{PID: 12, Name: "child", Domain: sandboxAChild}

	for _, p := range []Path{Kill, Tgkill, PidfdSendSignal} {
		if err := r.CheckSignal(a1, a2, SIGKILL, p); err != nil {
			t.Errorf("%s: same domain: %v", p, err)
		}
		if err := r.CheckSignal(a1, child, SIGKILL, p); err != nil {
			t.Errorf("%s: parent -> descendant: %v", p, err)
		}
	}

	// Unrestricted sender is never scoped.
	if err := r.CheckSignal(&Process{PID: 1, Name: "init"}, a1, SIGKILL, Kill); err != nil {
		t.Errorf("unrestricted sender: %v", err)
	}
	// Ruleset without signal scoping does not block anything.
	plain, err := Compile(Manifest{}, MinScopedABI)
	if err != nil {
		t.Fatal(err)
	}
	other := &Process{PID: 99, Name: "other", Domain: NewDomain("other", nil)}
	if err := plain.CheckSignal(a1, other, SIGKILL, Kill); err != nil {
		t.Errorf("unscoped ruleset: %v", err)
	}
}

func TestFSetOwnAuthorizedAtSetTime(t *testing.T) {
	r := scopedRuleset(t)
	_, sandboxA, _, daemonDomain := testHierarchy()

	setter := &Process{PID: 20, Name: "setter", Domain: sandboxA}
	owner := &Process{PID: 21, Name: "owner", Domain: sandboxA}

	// Same domain at set time: authorized, grant frozen.
	g, err := r.SetOwn(setter, owner, SIGIO)
	if err != nil {
		t.Fatalf("SetOwn same domain: %v", err)
	}

	// Both processes move out of scope afterwards: the owner is re-sandboxed
	// into an unrelated domain, the setter is unsandboxed entirely.
	owner.Domain = daemonDomain
	setter.Domain = nil

	// Delivery still succeeds: authorization was judged at set time.
	if err := r.Deliver(g, owner); err != nil {
		t.Errorf("Deliver after domain change: %v", err)
	}

	// SIGURG follows the same path.
	g2, err := r.SetOwn(&Process{PID: 22, Name: "s2", Domain: sandboxA}, &Process{PID: 23, Name: "o2", Domain: sandboxA}, SIGURG)
	if err != nil {
		t.Fatalf("SetOwn SIGURG: %v", err)
	}
	if err := r.Deliver(g2, &Process{PID: 23, Name: "o2", Domain: daemonDomain}); err != nil {
		t.Errorf("Deliver SIGURG after domain change: %v", err)
	}
}

func TestFSetOwnDeniedAtSetTimeStaysDenied(t *testing.T) {
	r := scopedRuleset(t)
	_, sandboxA, _, daemonDomain := testHierarchy()

	attacker := &Process{PID: 30, Name: "attacker", Domain: sandboxA}
	daemon := &Process{PID: 31, Name: "daemon", Domain: daemonDomain}

	// Cross-domain at set time: fcntl fails immediately, no grant issued.
	if _, err := r.SetOwn(attacker, daemon, SIGIO); !errors.Is(err, ErrCrossScope) {
		t.Fatalf("SetOwn cross-domain: want ErrCrossScope, got %v", err)
	}

	// Even though the daemon later joins the attacker's domain (where a
	// delivery-time check would pass), no grant exists to deliver under.
	daemon.Domain = sandboxA
	if err := r.Deliver(nil, daemon); err == nil {
		t.Error("Deliver without grant must fail")
	}

	// A grant cannot be replayed to a different process.
	victim := &Process{PID: 32, Name: "victim", Domain: sandboxA}
	g, err := r.SetOwn(attacker, victim, SIGIO)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Deliver(g, daemon); !errors.Is(err, ErrGrantMismatch) {
		t.Errorf("Deliver to non-owner: want ErrGrantMismatch, got %v", err)
	}
}

func TestAbstractSocketScope(t *testing.T) {
	r := scopedRuleset(t)
	_, sandboxA, sandboxAChild, daemonDomain := testHierarchy()
	conn := &Process{PID: 40, Name: "conn", Domain: sandboxA}

	if err := r.CheckAbstractConnect(conn, sandboxA); err != nil {
		t.Errorf("same-domain socket: %v", err)
	}
	if err := r.CheckAbstractConnect(conn, sandboxAChild); err != nil {
		t.Errorf("descendant-domain socket: %v", err)
	}
	if err := r.CheckAbstractConnect(conn, daemonDomain); !errors.Is(err, ErrCrossScope) {
		t.Errorf("unrelated-domain socket: want ErrCrossScope, got %v", err)
	}
	if err := r.CheckAbstractConnect(conn, nil); !errors.Is(err, ErrCrossScope) {
		t.Errorf("socket created outside any domain: want ErrCrossScope, got %v", err)
	}
}

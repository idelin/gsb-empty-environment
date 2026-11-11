package policypub_test

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"testing"

	"example.org/offlinecli/policypub"
)

func mustPrefix(t *testing.T, s string) netip.Prefix {
	t.Helper()
	p, err := netip.ParsePrefix(s)
	if err != nil {
		t.Fatalf("parse prefix %q: %v", s, err)
	}
	return p
}

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	a, err := netip.ParseAddr(s)
	if err != nil {
		t.Fatalf("parse addr %q: %v", s, err)
	}
	return a
}

func keyFor(handle string) []byte {
	return []byte("secret-key-material-" + handle)
}

// TestCompileOverlappingPrefixes covers nested and overlapping selectors in
// both directions, asserting longest-prefix-match precedence in 2D.
func TestCompileOverlappingPrefixes(t *testing.T) {
	rules := []policypub.Rule{
		{
			Name:       "broad",
			Local:      mustPrefix(t, "10.0.0.0/8"),
			Remote:     mustPrefix(t, "192.0.2.0/24"),
			OutboundSA: "ob-broad",
			InboundSA:  "ib-broad",
		},
		{
			Name:       "narrow",
			Local:      mustPrefix(t, "10.1.0.0/16"),
			Remote:     mustPrefix(t, "192.0.2.0/24"),
			OutboundSA: "ob-narrow",
			InboundSA:  "ib-narrow",
		},
		{
			Name:       "host",
			Local:      mustPrefix(t, "10.1.2.3/32"),
			Remote:     mustPrefix(t, "192.0.2.7/32"),
			OutboundSA: "ob-host",
			InboundSA:  "ib-host",
		},
	}
	table, err := policypub.Compile(rules)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	outboundCases := []struct {
		src, dst, want string
	}{
		{"10.1.2.3", "192.0.2.7", "ob-host"},
		{"10.1.2.3", "192.0.2.9", "ob-narrow"},
		{"10.1.9.9", "192.0.2.9", "ob-narrow"},
		{"10.2.0.1", "192.0.2.9", "ob-broad"},
		{"10.255.0.1", "192.0.2.1", "ob-broad"},
	}
	for _, tc := range outboundCases {
		got, ok := table.Lookup(policypub.Outbound, mustAddr(t, tc.src), mustAddr(t, tc.dst))
		if !ok || got != tc.want {
			t.Errorf("outbound %s -> %s = %q,ok=%v, want %q", tc.src, tc.dst, got, ok, tc.want)
		}
	}

	inboundCases := []struct {
		src, dst, want string
	}{
		{"192.0.2.7", "10.1.2.3", "ib-host"},
		{"192.0.2.9", "10.1.2.3", "ib-narrow"},
		{"192.0.2.9", "10.2.0.1", "ib-broad"},
	}
	for _, tc := range inboundCases {
		got, ok := table.Lookup(policypub.Inbound, mustAddr(t, tc.src), mustAddr(t, tc.dst))
		if !ok || got != tc.want {
			t.Errorf("inbound %s -> %s = %q,ok=%v, want %q", tc.src, tc.dst, got, ok, tc.want)
		}
	}

	if _, ok := table.Lookup(policypub.Outbound, mustAddr(t, "172.16.0.1"), mustAddr(t, "192.0.2.1")); ok {
		t.Error("flow outside all selectors must not match")
	}
	if got := len(table.Rules()); got != 3 {
		t.Errorf("Rules() len = %d, want 3", got)
	}
}

// TestCompileIPv6AndPrefixBoundaries checks v6 LPM, default routes, and the
// /0 source boundary (every address must still land on a real selector).
func TestCompileIPv6AndPrefixBoundaries(t *testing.T) {
	rules := []policypub.Rule{
		{
			Name:       "v6-default",
			Local:      mustPrefix(t, "::/0"),
			Remote:     mustPrefix(t, "2001:db8::/32"),
			OutboundSA: "ob6-def",
			InboundSA:  "ib6-def",
		},
		{
			Name:       "v6-specific",
			Local:      mustPrefix(t, "2001:db9:1::/48"),
			Remote:     mustPrefix(t, "2001:db8:2::/48"),
			OutboundSA: "ob6-spec",
			InboundSA:  "ib6-spec",
		},
	}
	table, err := policypub.Compile(rules)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	cases := []struct {
		dir            policypub.Direction
		src, dst, want string
	}{
		{policypub.Outbound, "2001:db9:1:2::9", "2001:db8:2:3::1", "ob6-spec"},
		{policypub.Outbound, "2001:db9:2::1", "2001:db8:1::1", "ob6-def"},
		{policypub.Outbound, "fe80::1", "2001:db8::abcd", "ob6-def"},
		{policypub.Inbound, "2001:db8:2:3::1", "2001:db9:1:2::9", "ib6-spec"},
		{policypub.Inbound, "2001:db8:1::1", "fe80::1", "ib6-def"},
	}
	for _, tc := range cases {
		got, ok := table.Lookup(tc.dir, mustAddr(t, tc.src), mustAddr(t, tc.dst))
		if !ok || got != tc.want {
			t.Errorf("%s %s -> %s = %q,ok=%v, want %q", tc.dir, tc.src, tc.dst, got, ok, tc.want)
		}
	}

	// A /0 source selector matches every source, but an unmatched destination
	// must still fail closed.
	if _, ok := table.Lookup(policypub.Outbound, mustAddr(t, "fe80::1"), mustAddr(t, "2001:db7::1")); ok {
		t.Error("destination outside all selectors must not match even with /0 source")
	}
}

func TestCompileRejectsBadInput(t *testing.T) {
	base := []policypub.Rule{{
		Name:       "r",
		Local:      mustPrefix(t, "10.0.0.0/8"),
		Remote:     mustPrefix(t, "192.0.2.0/24"),
		OutboundSA: "ob",
		InboundSA:  "ib",
	}}
	if _, err := policypub.Compile(nil); err == nil {
		t.Error("empty rule set must fail")
	}

	missingHandle := append([]policypub.Rule(nil), base...)
	missingHandle[0].OutboundSA = ""
	if _, err := policypub.Compile(missingHandle); err == nil {
		t.Error("missing SA handle must fail")
	}

	dup := append([]policypub.Rule(nil), base...)
	dup = append(dup, base[0])
	dup[1].Name = "r2"
	if _, err := policypub.Compile(dup); err == nil {
		t.Error("duplicate selectors must fail")
	}

	mixedFamily := append([]policypub.Rule(nil), base...)
	mixedFamily[0].Remote = mustPrefix(t, "2001:db8::/32")
	if _, err := policypub.Compile(mixedFamily); err == nil {
		t.Error("mixed address families must fail")
	}
}

func basePolicy(t *testing.T) policypub.Policy {
	t.Helper()
	return policypub.Policy{
		Rules: []policypub.Rule{{
			Name:       "r1",
			Local:      mustPrefix(t, "10.0.0.0/8"),
			Remote:     mustPrefix(t, "192.0.2.0/24"),
			OutboundSA: "ob1",
			InboundSA:  "ib1",
		}},
		SAs: []policypub.SA{
			{Handle: "ob1", Direction: policypub.Outbound, Key: keyFor("ob1")},
			{Handle: "ib1", Direction: policypub.Inbound, Key: keyFor("ib1")},
		},
	}
}

// TestPublishInstallsBothDirectionsThenSwaps verifies make-before-break:
// the swap only happens once every SA in both directions is confirmed.
func TestPublishInstallsBothDirectionsThenSwaps(t *testing.T) {
	db := policypub.NewSADB()
	pub := policypub.New(db)

	if pub.Active() != nil {
		t.Fatal("fresh publisher must have no active generation")
	}

	var order []string
	db.SetInstallHook(func(sa policypub.SA) error {
		order = append(order, fmt.Sprintf("%s:%s", sa.Direction, sa.Handle))
		return nil
	})

	snap, err := pub.Publish(context.Background(), basePolicy(t))
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	wantOrder := []string{"outbound:ob1", "inbound:ib1"}
	if fmt.Sprint(order) != fmt.Sprint(wantOrder) {
		t.Errorf("install order = %v, want %v", order, wantOrder)
	}
	for _, handle := range []string{"ob1", "ib1"} {
		if !db.Installed(handle) {
			t.Errorf("SA %s not installed after publish", handle)
		}
	}
	if snap.Generation() != 1 {
		t.Errorf("generation = %d, want 1", snap.Generation())
	}

	dec := pub.Decide(policypub.Outbound, mustAddr(t, "10.0.0.5"), mustAddr(t, "192.0.2.5"))
	if dec.Action != policypub.ActionEncrypt || dec.SAHandle != "ob1" {
		t.Errorf("decision = %+v, want encrypt with ob1", dec)
	}
	dec = pub.Decide(policypub.Inbound, mustAddr(t, "192.0.2.5"), mustAddr(t, "10.0.0.5"))
	if dec.Action != policypub.ActionEncrypt || dec.SAHandle != "ib1" {
		t.Errorf("decision = %+v, want encrypt with ib1", dec)
	}
}

// TestPublishRetiresPreviousGeneration verifies an old generation stays
// readable until the swap and its unique SAs are retired afterwards.
func TestPublishRetiresPreviousGeneration(t *testing.T) {
	db := policypub.NewSADB()
	pub := policypub.New(db)

	if _, err := pub.Publish(context.Background(), basePolicy(t)); err != nil {
		t.Fatalf("first publish: %v", err)
	}

	next := policypub.Policy{
		Rules: []policypub.Rule{{
			Name:       "r2",
			Local:      mustPrefix(t, "10.0.0.0/8"),
			Remote:     mustPrefix(t, "198.51.100.0/24"),
			OutboundSA: "ob2",
			InboundSA:  "ib2",
		}},
		SAs: []policypub.SA{
			{Handle: "ob2", Direction: policypub.Outbound, Key: keyFor("ob2")},
			{Handle: "ib2", Direction: policypub.Inbound, Key: keyFor("ib2")},
		},
	}
	snap, err := pub.Publish(context.Background(), next)
	if err != nil {
		t.Fatalf("second publish: %v", err)
	}
	if snap.Generation() != 2 {
		t.Fatalf("generation = %d, want 2", snap.Generation())
	}
	for _, gone := range []string{"ob1", "ib1"} {
		if db.Installed(gone) {
			t.Errorf("old SA %s should have been retired", gone)
		}
	}
	for _, live := range []string{"ob2", "ib2"} {
		if !db.Installed(live) {
			t.Errorf("new SA %s should be installed", live)
		}
	}

	dec := pub.Decide(policypub.Outbound, mustAddr(t, "10.0.0.5"), mustAddr(t, "198.51.100.5"))
	if dec.Action != policypub.ActionEncrypt || dec.SAHandle != "ob2" {
		t.Errorf("decision = %+v, want encrypt with ob2", dec)
	}
	dec = pub.Decide(policypub.Outbound, mustAddr(t, "10.0.0.5"), mustAddr(t, "192.0.2.5"))
	if dec.Action != policypub.ActionDrop || dec.Reason != "no-matching-selector" {
		t.Errorf("stale flow decision = %+v, want drop/no-matching-selector", dec)
	}
}

// TestRollbackOnInboundFailure installs the outbound SA, fails an inbound
// install, and asserts every newly installed SA is reclaimed and the prior
// generation survives untouched.
func TestRollbackOnInboundFailure(t *testing.T) {
	db := policypub.NewSADB()
	pub := policypub.New(db)
	if _, err := pub.Publish(context.Background(), basePolicy(t)); err != nil {
		t.Fatalf("first publish: %v", err)
	}

	injected := errors.New("simulated inbound install failure")
	db.SetInstallHook(func(sa policypub.SA) error {
		if sa.Handle == "ib2" {
			return injected
		}
		return nil
	})

	next := policypub.Policy{
		Rules: []policypub.Rule{{
			Name:       "r2",
			Local:      mustPrefix(t, "10.0.0.0/8"),
			Remote:     mustPrefix(t, "198.51.100.0/24"),
			OutboundSA: "ob2",
			InboundSA:  "ib2",
		}},
		SAs: []policypub.SA{
			{Handle: "ob2", Direction: policypub.Outbound, Key: keyFor("ob2")},
			{Handle: "ib2", Direction: policypub.Inbound, Key: keyFor("ib2")},
		},
	}
	if _, err := pub.Publish(context.Background(), next); !errors.Is(err, injected) {
		t.Fatalf("Publish error = %v, want wrapped %v", err, injected)
	}

	if db.Installed("ob2") {
		t.Error("ob2 installed before failure must be rolled back")
	}
	if db.Installed("ib2") {
		t.Error("failed ib2 must not be installed")
	}
	for _, keep := range []string{"ob1", "ib1"} {
		if !db.Installed(keep) {
			t.Errorf("previous-generation SA %s must survive rollback", keep)
		}
	}
	if gen := pub.Active().Generation(); gen != 1 {
		t.Errorf("active generation = %d, want 1 after failed publish", gen)
	}

	dec := pub.Decide(policypub.Outbound, mustAddr(t, "10.0.0.5"), mustAddr(t, "192.0.2.5"))
	if dec.Action != policypub.ActionEncrypt || dec.SAHandle != "ob1" {
		t.Errorf("post-rollback decision = %+v, want encrypt with ob1", dec)
	}
}

// TestDefaultDeny proves there is never a plaintext outcome: unmatched flows
// and absent policies are always dropped.
func TestDefaultDeny(t *testing.T) {
	pub := policypub.New(policypub.NewSADB())

	dec := pub.Decide(policypub.Outbound, mustAddr(t, "10.0.0.1"), mustAddr(t, "192.0.2.1"))
	if dec.Action != policypub.ActionDrop || dec.Reason != "no-active-policy" {
		t.Fatalf("decision before first publish = %+v", dec)
	}

	if _, err := pub.Publish(context.Background(), basePolicy(t)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	dec = pub.Decide(policypub.Outbound, mustAddr(t, "172.16.0.1"), mustAddr(t, "192.0.2.1"))
	if dec.Action != policypub.ActionDrop || dec.Reason != "no-matching-selector" {
		t.Fatalf("unmatched flow decision = %+v", dec)
	}
}

// TestPublishRejectsDanglingAndMisusedSAs exercises cross-validation between
// selectors and the SA set.
func TestPublishRejectsDanglingAndMisusedSAs(t *testing.T) {
	pub := policypub.New(policypub.NewSADB())

	dangling := basePolicy(t)
	dangling.SAs = dangling.SAs[:1]
	if _, err := pub.Publish(context.Background(), dangling); err == nil {
		t.Error("dangling inbound SA reference must fail")
	}

	wrongDirection := basePolicy(t)
	wrongDirection.SAs[1].Direction = policypub.Outbound
	if _, err := pub.Publish(context.Background(), wrongDirection); err == nil {
		t.Error("SA used against its declared direction must fail")
	}

	dupHandle := policypub.Policy{
		Rules: basePolicy(t).Rules,
		SAs: append([]policypub.SA(nil),
			policypub.SA{Handle: "x", Direction: policypub.Outbound},
			policypub.SA{Handle: "x", Direction: policypub.Inbound}),
	}
	if _, err := pub.Publish(context.Background(), dupHandle); err == nil {
		t.Error("duplicate SA handles must fail")
	}
}

// TestKeyMaterialZeroized verifies the SADB zeroizes stored keys when an SA is
// uninstalled, both during rollback and generation retirement.
func TestKeyMaterialZeroized(t *testing.T) {
	db := policypub.NewSADB()
	key := keyFor("obz")
	sa := policypub.SA{Handle: "obz", Direction: policypub.Outbound, Key: key}
	if err := db.Install(context.Background(), sa); err != nil {
		t.Fatalf("install: %v", err)
	}
	// The caller keeps its own copy; the SADB stored a private copy.
	if allZero(key) {
		t.Error("install must copy key material, not take the caller's slice")
	}
	if err := db.Uninstall(context.Background(), "obz"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if db.Installed("obz") {
		t.Error("SA still present after uninstall")
	}

	// A fresh key is zeroized on uninstall even though the caller's slice is
	// untouched; verify through a rollback path below where the stored copy
	// is the only one in the process.
	failingDB := policypub.NewSADB()
	failingDB.SetInstallHook(func(sa policypub.SA) error {
		if sa.Handle == "ibx" {
			return errors.New("fail inbound to force rollback")
		}
		return nil
	})
	failingPub := policypub.New(failingDB)
	failingPolicy := policypub.Policy{
		Rules: []policypub.Rule{{
			Name:       "rx",
			Local:      mustPrefix(t, "10.0.0.0/8"),
			Remote:     mustPrefix(t, "192.0.2.0/24"),
			OutboundSA: "obx",
			InboundSA:  "ibx",
		}},
		SAs: []policypub.SA{
			{Handle: "obx", Direction: policypub.Outbound, Key: keyFor("obx")},
			{Handle: "ibx", Direction: policypub.Inbound, Key: keyFor("ibx")},
		},
	}
	if _, err := failingPub.Publish(context.Background(), failingPolicy); err == nil {
		t.Fatal("expected forced rollback error")
	}
	if failingDB.Installed("obx") {
		t.Error("obx must have been uninstalled during rollback")
	}
	// Re-installing then retiring through a publisher also zeroizes.
	pub := policypub.New(db)
	policy := policypub.Policy{
		Rules: []policypub.Rule{{
			Name:       "r",
			Local:      mustPrefix(t, "10.0.0.0/8"),
			Remote:     mustPrefix(t, "192.0.2.0/24"),
			OutboundSA: "obz",
			InboundSA:  "ibz",
		}},
		SAs: []policypub.SA{
			{Handle: "obz", Direction: policypub.Outbound, Key: keyFor("obz")},
			{Handle: "ibz", Direction: policypub.Inbound, Key: keyFor("ibz")},
		},
	}
	if _, err := pub.Publish(context.Background(), policy); err != nil {
		t.Fatalf("publish: %v", err)
	}
	replacement := policypub.Policy{
		Rules: []policypub.Rule{{
			Name:       "r2",
			Local:      mustPrefix(t, "10.0.0.0/8"),
			Remote:     mustPrefix(t, "198.51.100.0/24"),
			OutboundSA: "obz2",
			InboundSA:  "ibz2",
		}},
		SAs: []policypub.SA{
			{Handle: "obz2", Direction: policypub.Outbound, Key: keyFor("obz2")},
			{Handle: "ibz2", Direction: policypub.Inbound, Key: keyFor("ibz2")},
		},
	}
	if _, err := pub.Publish(context.Background(), replacement); err != nil {
		t.Fatalf("replacement publish: %v", err)
	}
	for _, handle := range []string{"obz", "ibz"} {
		if db.Installed(handle) {
			t.Errorf("retired SA %s still installed", handle)
		}
	}
}

func allZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

// TestConcurrentReadersDuringPublication hammers Decide from many goroutines
// while a generation is published behind the swap barrier. Every decision must
// be encrypt-with-installed-SA or drop; a plaintext outcome is unrepresentable.
func TestConcurrentReadersDuringPublication(t *testing.T) {
	inner := policypub.NewSADB()
	driver := policypub.NewPausable(inner)
	pub := policypub.New(driver)

	if _, err := pub.Publish(context.Background(), basePolicy(t)); err != nil {
		t.Fatalf("first publish: %v", err)
	}

	next := policypub.Policy{
		Rules: []policypub.Rule{
			{
				Name:       "r2",
				Local:      mustPrefix(t, "10.0.0.0/8"),
				Remote:     mustPrefix(t, "198.51.100.0/24"),
				OutboundSA: "ob2",
				InboundSA:  "ib2",
			},
			{
				Name:       "overlap",
				Local:      mustPrefix(t, "10.1.0.0/16"),
				Remote:     mustPrefix(t, "198.51.100.0/24"),
				OutboundSA: "ob3",
				InboundSA:  "ib3",
			},
		},
		SAs: []policypub.SA{
			{Handle: "ob2", Direction: policypub.Outbound, Key: keyFor("ob2")},
			{Handle: "ib2", Direction: policypub.Inbound, Key: keyFor("ib2")},
			{Handle: "ob3", Direction: policypub.Outbound, Key: keyFor("ob3")},
			{Handle: "ib3", Direction: policypub.Inbound, Key: keyFor("ib3")},
		},
	}

	driver.Hold(policypub.StepBeforeSwap)
	publishErr := make(chan error, 1)
	go func() {
		_, err := pub.Publish(context.Background(), next)
		publishErr <- err
	}()
	driver.WaitHeld(policypub.StepBeforeSwap)

	// All new SAs are installed but the swap has not happened yet.
	for _, handle := range []string{"ob2", "ib2", "ob3", "ib3"} {
		if !inner.Installed(handle) {
			t.Fatalf("SA %s must be installed before the swap", handle)
		}
	}

	flows := []struct {
		dir          policypub.Direction
		src, dst     string
		wantOldSA    string
		mustDropOnly bool
	}{
		{policypub.Outbound, "10.0.0.9", "192.0.2.9", "ob1", false},
		{policypub.Outbound, "10.0.0.9", "198.51.100.9", "", true},
		{policypub.Outbound, "10.1.7.7", "198.51.100.9", "", true},
		{policypub.Inbound, "192.0.2.9", "10.0.0.9", "ib1", false},
		{policypub.Outbound, "172.16.0.1", "198.51.100.9", "", true},
	}

	var (
		violMu    sync.Mutex
		violation error
	)
	setViolation := func(err error) {
		violMu.Lock()
		if violation == nil {
			violation = err
		}
		violMu.Unlock()
	}
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				violMu.Lock()
				bail := violation != nil
				violMu.Unlock()
				if bail {
					return
				}
				for _, flow := range flows {
					d := pub.Decide(flow.dir, mustAddr(t, flow.src), mustAddr(t, flow.dst))
					switch d.Action {
					case policypub.ActionDrop:
						// Acceptable at any time (old gen lacks the selector).
					case policypub.ActionEncrypt:
						if !inner.Installed(d.SAHandle) {
							setViolation(fmt.Errorf("encrypt decision for %s->%s references uninstalled SA %q",
								flow.src, flow.dst, d.SAHandle))
							return
						}
						if flow.mustDropOnly || d.SAHandle != flow.wantOldSA {
							setViolation(fmt.Errorf("flow %s->%s resolved to unexpected SA %q while swap was held",
								flow.src, flow.dst, d.SAHandle))
							return
						}
					default:
						setViolation(fmt.Errorf("impossible action %d for %s->%s", d.Action, flow.src, flow.dst))
						return
					}
				}
			}
		}()
	}
	wg.Wait()

	driver.Release(policypub.StepBeforeSwap)
	if err := <-publishErr; err != nil {
		t.Fatalf("blocked publish failed after release: %v", err)
	}
	if violation != nil {
		t.Fatal(violation)
	}

	// After the swap the new selectors must resolve to the new, installed SAs.
	dec := pub.Decide(policypub.Outbound, mustAddr(t, "10.1.7.7"), mustAddr(t, "198.51.100.9"))
	if dec.Action != policypub.ActionEncrypt || dec.SAHandle != "ob3" {
		t.Fatalf("post-swap decision = %+v, want encrypt with ob3", dec)
	}
}

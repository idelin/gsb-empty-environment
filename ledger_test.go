package netsec

import (
	"errors"
	"testing"
	"time"
)

func testLedger(t *testing.T, restart time.Duration, cfg Config) (*Ledger, *VirtualClock) {
	t.Helper()
	clk := NewVirtualClockAt(time.Unix(1000, 0).UTC())
	cfg.Clock = clk
	cfg.RestartTime = restart
	return NewLedger(cfg), clk
}

func mustOpen(t *testing.T, l *Ledger, args ...any) *Session {
	t.Helper()
	s, err := l.OpenSession(args...)
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	return s
}

func onlyStale(t *testing.T, l *Ledger) StaleRoute {
	t.Helper()
	stale := l.StaleRoutes()
	if len(stale) != 1 {
		t.Fatalf("want exactly one stale route, got %#v", stale)
	}
	return stale[0]
}

func TestGracefulDisconnectMarksStaleAndRecordsAudit(t *testing.T) {
	l, clk := testLedger(t, 30*time.Second, Config{})
	l.UpdatePolicy(7, func(Route) bool { return true })
	l.UpdateVRP(9, func(Route) bool { return true })

	s := mustOpen(t, l, Peer{NeighborAS: 65001, RouterID: "192.0.2.1"}, uint64(1), Capabilities{GracefulRestart: true})
	if err := s.Announce(Route{ID: "r1", Prefix: "10.0.0.0/8", AttrsDigest: "attrs-a"}); err != nil {
		t.Fatal(err)
	}
	if !l.CanForward("r1") {
		t.Fatal("fresh route should be forwardable")
	}
	if err := s.Disconnect(DisconnectTransport); err != nil {
		t.Fatal(err)
	}

	st := onlyStale(t, l)
	if st.RouteID != "r1" || st.Prefix != "10.0.0.0/8" || st.AttrsDigest != "attrs-a" {
		t.Fatalf("stale record lost identity: %#v", st)
	}
	if st.OriginalPolicyVersion != 7 || st.OriginalVRPVersion != 9 {
		t.Fatalf("original versions not recorded: %#v", st)
	}
	if !st.Deadline.Equal(clk.Now().Add(30 * time.Second)) {
		t.Fatalf("deadline = %v, want %v", st.Deadline, clk.Now().Add(30*time.Second))
	}
	if !l.CanForward("r1") {
		t.Fatal("stale route should be forwardable inside restart window")
	}
	if l.CanExport("r1") {
		t.Fatal("stale route must not be exportable")
	}
	if l.CanFlowSpec("r1") {
		t.Fatal("stale route must not participate in FlowSpec validation")
	}
	if err := l.ValidateFlowSpec("r1"); !errors.Is(err, ErrStaleRouteForFlowSpec) {
		t.Fatalf("FlowSpec validation error = %v", err)
	}
}

func TestPolicyUpdateRevokesStaleImmediately(t *testing.T) {
	l, _ := testLedger(t, 30*time.Second, Config{})
	l.UpdatePolicy(1, func(Route) bool { return true })
	s := mustOpen(t, l, Peer{NeighborAS: 65002, RouterID: "192.0.2.2"}, uint64(1), true)
	if err := s.Announce(Route{ID: "r1", Prefix: "10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if !l.CanForward("r1") {
		t.Fatal("expected stale route before policy change")
	}
	removed := l.UpdatePolicy(2, func(r Route) bool { return false })
	if len(removed) != 1 || removed[0] != "r1" {
		t.Fatalf("removed = %v", removed)
	}
	if l.CanForward("r1") {
		t.Fatal("revoked stale route is still forwardable")
	}
	// The revocation must not wait for the restart timer.
	if len(l.StaleRoutes()) != 0 {
		t.Fatal("stale route still present after policy revocation")
	}
}

func TestVRPUpdateRevokesStaleImmediately(t *testing.T) {
	l, _ := testLedger(t, 30*time.Second, Config{})
	l.UpdateVRP(1, func(Route) bool { return true })
	s := mustOpen(t, l, Peer{NeighborAS: 65003, RouterID: "192.0.2.3"}, uint64(1), true)
	if err := s.Announce(Route{ID: "r1", Prefix: "10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Disconnect(); err != nil {
		t.Fatal(err)
	}
	removed := l.UpdateVRP(2, func(r Route) bool { return false })
	if len(removed) != 1 || removed[0] != "r1" {
		t.Fatalf("removed = %v", removed)
	}
	if l.CanForward("r1") || l.CanExport("r1") || l.CanFlowSpec("r1") {
		t.Fatal("VRP-revoked stale route remains usable")
	}
}

func TestReconnectRefreshBeforeEOR(t *testing.T) {
	l, _ := testLedger(t, 30*time.Second, Config{})
	peer := Peer{NeighborAS: 65004, RouterID: "192.0.2.4"}
	s1 := mustOpen(t, l, peer, uint64(1), true)
	if err := s1.Announce(Route{ID: "old", Prefix: "10.0.0.0/8", AttrsDigest: "old-attrs"}); err != nil {
		t.Fatal(err)
	}
	if err := s1.Disconnect(DisconnectTransport); err != nil {
		t.Fatal(err)
	}
	s2 := mustOpen(t, l, peer, uint64(2), true)
	if err := s2.Announce(Route{ID: "new", Prefix: "10.0.0.0/8", AttrsDigest: "new-attrs"}); err != nil {
		t.Fatal(err)
	}
	if len(l.StaleRoutes()) != 0 {
		t.Fatal("successful refresh should remove stale record")
	}
	rv, ok := l.Route("new")
	if !ok || rv.State != StateFresh {
		t.Fatalf("fresh replacement missing: %#v %v", rv, ok)
	}
	if l.CanExport("new") == false {
		t.Fatal("refreshed route should be exportable")
	}
}

func TestFailedRefreshDoesNotReplaceStale(t *testing.T) {
	l, _ := testLedger(t, 30*time.Second, Config{})
	peer := Peer{NeighborAS: 65005, RouterID: "192.0.2.5"}
	s1 := mustOpen(t, l, peer, uint64(1), true)
	if err := s1.Announce(Route{ID: "old", Prefix: "10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
	if err := s1.Disconnect(); err != nil {
		t.Fatal(err)
	}
	l.UpdatePolicy(2, func(Route) bool { return false })
	s2 := mustOpen(t, l, peer, uint64(2), true)
	err := s2.Announce(Route{ID: "new", Prefix: "10.0.0.0/8"})
	if !errors.Is(err, ErrPolicyRejected) {
		t.Fatalf("announce error = %v", err)
	}
	if len(l.StaleRoutes()) != 0 {
		t.Fatal("a policy update should already have revoked the stale route")
	}

	// If the current policy rejects only the *new* attributes while the old
	// stale attributes remain valid, the stale route stays until EOR/timer.
	l2, _ := testLedger(t, 30*time.Second, Config{})
	peer2 := Peer{NeighborAS: 65006, RouterID: "192.0.2.6"}
	a := mustOpen(t, l2, peer2, uint64(1), true)
	if err := a.Announce(Route{ID: "old", Prefix: "10.0.0.0/8", AttrsDigest: "good"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Disconnect(); err != nil {
		t.Fatal(err)
	}
	l2.UpdatePolicy(1, func(r Route) bool { return r.AttrsDigest == "good" })
	b := mustOpen(t, l2, peer2, uint64(2), true)
	if err := b.Announce(Route{ID: "new", Prefix: "10.0.0.0/8", AttrsDigest: "bad"}); !errors.Is(err, ErrPolicyRejected) {
		t.Fatalf("announce error = %v", err)
	}
	if !l2.CanForward("old") {
		t.Fatal("valid stale route should remain until EOR or deadline")
	}
}

func TestEndOfRIBWithdrawsUnrefreshedAndDuplicateEORIsIdempotent(t *testing.T) {
	l, _ := testLedger(t, 30*time.Second, Config{})
	peer := Peer{NeighborAS: 65007, RouterID: "192.0.2.7"}
	s1 := mustOpen(t, l, peer, uint64(1), true)
	if err := s1.Announce(Route{ID: "stay", Prefix: "10.1.0.0/16"}); err != nil {
		t.Fatal(err)
	}
	if err := s1.Announce(Route{ID: "drop", Prefix: "10.2.0.0/16"}); err != nil {
		t.Fatal(err)
	}
	if err := s1.Disconnect(); err != nil {
		t.Fatal(err)
	}
	s2 := mustOpen(t, l, peer, uint64(2), true)
	if err := s2.Announce(Route{ID: "stay-new", Prefix: "10.1.0.0/16"}); err != nil {
		t.Fatal(err)
	}
	if err := s2.EndOfRIB(); err != nil {
		t.Fatal(err)
	}
	if l.CanForward("drop") {
		t.Fatal("unrefreshed stale route survived End-of-RIB")
	}
	if !l.CanForward("stay-new") {
		t.Fatal("refreshed route was withdrawn by End-of-RIB")
	}
	if err := s2.EndOfRIB(); err != nil {
		t.Fatalf("duplicate EOR: %v", err)
	}
	if !l.CanForward("stay-new") {
		t.Fatal("duplicate EOR changed live routes")
	}
}

func TestStaleDeadlineBoundary(t *testing.T) {
	l, _ := testLedger(t, 10*time.Second, Config{})
	s := mustOpen(t, l, Peer{NeighborAS: 65008, RouterID: "192.0.2.8"}, uint64(1), true)
	if err := s.Announce(Route{ID: "r", Prefix: "10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Disconnect(); err != nil {
		t.Fatal(err)
	}
	l.Advance(9*time.Second + 999*time.Millisecond)
	if !l.CanForward("r") {
		t.Fatal("stale route expired before deadline")
	}
	l.Advance(time.Millisecond) // exactly at deadline
	if l.CanForward("r") {
		t.Fatal("stale route is forwardable at the deadline boundary")
	}
	if len(l.StaleRoutes()) != 0 {
		t.Fatal("stale route not removed at deadline")
	}
}

func TestPolicyUpdateAtExactDeadlineDoesNotResurrect(t *testing.T) {
	l, _ := testLedger(t, 10*time.Second, Config{})
	s := mustOpen(t, l, Peer{NeighborAS: 65009, RouterID: "192.0.2.9"}, uint64(1), true)
	if err := s.Announce(Route{ID: "r", Prefix: "10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Disconnect(); err != nil {
		t.Fatal(err)
	}
	l.Advance(10 * time.Second)
	l.UpdatePolicy(100, func(Route) bool { return true })
	if l.CanForward("r") || len(l.StaleRoutes()) != 0 {
		t.Fatal("policy update at deadline resurrected stale route")
	}
}

func TestAuthenticationFailureNeverCreatesStale(t *testing.T) {
	l, _ := testLedger(t, 30*time.Second, Config{})
	s := mustOpen(t, l, Peer{NeighborAS: 65010, RouterID: "192.0.2.10"}, uint64(1), true)
	if err := s.Announce(Route{ID: "r", Prefix: "10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Disconnect(DisconnectAuthenticationFailure); err != nil {
		t.Fatal(err)
	}
	if len(l.StaleRoutes()) != 0 {
		t.Fatal("authentication failure must withdraw, not stale, routes")
	}
	if l.CanForward("r") {
		t.Fatal("route from failed-authentication session still forwardable")
	}
}

func TestFailedReauthenticationRevokesOlderStale(t *testing.T) {
	l, _ := testLedger(t, 30*time.Second, Config{})
	peer := Peer{NeighborAS: 65011, RouterID: "192.0.2.11"}
	s1 := mustOpen(t, l, peer, uint64(1), true)
	if err := s1.Announce(Route{ID: "r", Prefix: "10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
	if err := s1.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if !l.CanForward("r") {
		t.Fatal("expected stale route")
	}
	s2 := mustOpen(t, l, peer, uint64(2), true)
	if err := s2.Disconnect(DisconnectAuthenticationFailure); err != nil {
		t.Fatal(err)
	}
	if l.CanForward("r") || len(l.StaleRoutes()) != 0 {
		t.Fatal("failed reauthentication left stale route alive")
	}
}

func TestReconnectIdentityMustMatchAndGenerationMustIncrease(t *testing.T) {
	l, _ := testLedger(t, 30*time.Second, Config{})
	peer := Peer{NeighborAS: 65012, RouterID: "192.0.2.12"}
	s1 := mustOpen(t, l, peer, uint64(1), true)
	if err := s1.Announce(Route{ID: "r", Prefix: "10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
	if err := s1.Disconnect(); err != nil {
		t.Fatal(err)
	}

	// Same generation is not a reconnect.
	if _, err := l.OpenSession(peer, uint64(1), true); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("same generation error = %v", err)
	}
	// Same router ID but different AS cannot refresh the stale route.
	otherAS := mustOpen(t, l, Peer{NeighborAS: 65099, RouterID: peer.RouterID}, uint64(1), true)
	if err := otherAS.Announce(Route{ID: "evil", Prefix: "10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
	if len(l.StaleRoutes()) != 1 || !l.CanForward("r") {
		t.Fatal("different AS refreshed or displaced the original peer's stale route")
	}
	// Same AS but a different router ID cannot refresh it either.
	otherRID := mustOpen(t, l, Peer{NeighborAS: peer.NeighborAS, RouterID: "192.0.2.99"}, uint64(1), true)
	if err := otherRID.Announce(Route{ID: "evil2", Prefix: "10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
	if len(l.StaleRoutes()) != 1 || !l.CanForward("r") {
		t.Fatal("different router ID refreshed or displaced the original peer's stale route")
	}
}

func TestRouterIDReuseEORDoesNotFlushOtherPeer(t *testing.T) {
	l, _ := testLedger(t, 30*time.Second, Config{})
	peer := Peer{NeighborAS: 65013, RouterID: "192.0.2.13"}
	s1 := mustOpen(t, l, peer, uint64(1), true)
	if err := s1.Announce(Route{ID: "r", Prefix: "10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
	if err := s1.Disconnect(); err != nil {
		t.Fatal(err)
	}
	reuser := mustOpen(t, l, Peer{NeighborAS: 65098, RouterID: peer.RouterID}, uint64(1), true)
	if err := reuser.EndOfRIB(); err != nil {
		t.Fatal(err)
	}
	if !l.CanForward("r") || len(l.StaleRoutes()) != 1 {
		t.Fatal("EOR from a different AS/router identity flushed the stale route")
	}
}

func TestSelectorNeverSeesStaleForExportOrFlowSpec(t *testing.T) {
	var uses []RouteUse
	var counts []int
	selector := func(use RouteUse, candidates []RouteView) (RouteView, bool) {
		uses = append(uses, use)
		counts = append(counts, len(candidates))
		for _, c := range candidates {
			if c.State == StateStale && use != UseForward {
				panic("stale candidate offered for export/flowspec")
			}
		}
		if len(candidates) == 0 {
			return RouteView{}, false
		}
		return candidates[0], true
	}
	l, _ := testLedger(t, 30*time.Second, Config{Select: selector})
	s := mustOpen(t, l, Peer{NeighborAS: 65014, RouterID: "192.0.2.14"}, uint64(1), true)
	if err := s.Announce(Route{ID: "r", Prefix: "10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if _, ok := l.SelectBest("10.0.0.0/8", UseForward); !ok {
		t.Fatal("stale route should be selectable for forwarding")
	}
	if _, ok := l.SelectBest("10.0.0.0/8", UseExport); ok {
		t.Fatal("stale route selected for export")
	}
	if _, ok := l.SelectBest("10.0.0.0/8", UseFlowSpec); ok {
		t.Fatal("stale route selected for FlowSpec")
	}
	if len(uses) != 1 || uses[0] != UseForward || counts[0] != 1 {
		t.Fatalf("selector calls = %v counts=%v", uses, counts)
	}
	if got := l.ExportCandidates(); len(got) != 0 {
		t.Fatalf("stale route appeared in export candidates: %#v", got)
	}
	if got := l.FlowSpecCandidates(); len(got) != 0 {
		t.Fatalf("stale route appeared in FlowSpec candidates: %#v", got)
	}
}

func TestFreshWinsDefaultSelectionOverStale(t *testing.T) {
	l, _ := testLedger(t, 30*time.Second, Config{})
	peer1 := Peer{NeighborAS: 65015, RouterID: "192.0.2.15"}
	s1 := mustOpen(t, l, peer1, uint64(1), true)
	if err := s1.Announce(Route{ID: "aaa", Prefix: "10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
	if err := s1.Disconnect(); err != nil {
		t.Fatal(err)
	}
	peer2 := Peer{NeighborAS: 65016, RouterID: "192.0.2.16"}
	s2 := mustOpen(t, l, peer2, uint64(1), true)
	if err := s2.Announce(Route{ID: "zzz", Prefix: "10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
	chosen, ok := l.SelectBest("10.0.0.0/8", UseForward)
	if !ok || chosen.State != StateFresh {
		t.Fatalf("default selector chose %#v", chosen)
	}
}

func TestRouteIDIsScopedByPrefix(t *testing.T) {
	l, _ := testLedger(t, 30*time.Second, Config{})
	peer := Peer{NeighborAS: 65021, RouterID: "192.0.2.21"}
	s1 := mustOpen(t, l, peer, uint64(1), true)
	if err := s1.Announce(Route{ID: "x", Prefix: "10.10.0.0/16"}); err != nil {
		t.Fatal(err)
	}
	if err := s1.Announce(Route{ID: "x", Prefix: "10.11.0.0/16"}); err != nil {
		t.Fatal(err)
	}
	if err := s1.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if got := len(l.StaleRoutes()); got != 2 {
		t.Fatalf("same route ID on two prefixes produced %d stale records, want 2", got)
	}
	s2 := mustOpen(t, l, peer, uint64(2), true)
	if err := s2.Announce(Route{ID: "x-new", Prefix: "10.10.0.0/16"}); err != nil {
		t.Fatal(err)
	}
	if got := len(l.StaleRoutes()); got != 1 {
		t.Fatalf("refresh of one prefix left %d stale records, want 1", got)
	}
	if l.CanExport("x") {
		t.Fatal("ambiguous export of an ID that still names a stale route must be denied")
	}
	if err := s2.EndOfRIB(); err != nil {
		t.Fatal(err)
	}
	if l.CanExport("x-new") != true {
		t.Fatal("refreshed route should be exportable after EOR")
	}
}

func TestSameRouteIDDifferentPrefixDoesNotRefreshStale(t *testing.T) {
	l, _ := testLedger(t, 30*time.Second, Config{})
	peer := Peer{NeighborAS: 65022, RouterID: "192.0.2.22"}
	s1 := mustOpen(t, l, peer, uint64(1), true)
	if err := s1.Announce(Route{ID: "r", Prefix: "10.12.0.0/16"}); err != nil {
		t.Fatal(err)
	}
	if err := s1.Disconnect(); err != nil {
		t.Fatal(err)
	}
	s2 := mustOpen(t, l, peer, uint64(2), true)
	if err := s2.Announce(Route{ID: "r", Prefix: "10.13.0.0/16"}); err != nil {
		t.Fatal(err)
	}
	stale := l.StaleRoutes()
	if len(stale) != 1 || stale[0].Prefix != "10.12.0.0/16" {
		t.Fatalf("cross-prefix route ID reused the stale slot: %#v", stale)
	}
	if !l.CanForward("r") {
		t.Fatal("stale route should still be forwardable")
	}
}

func TestZeroRestartTimeNeverStales(t *testing.T) {
	l, _ := testLedger(t, 0, Config{})
	s := mustOpen(t, l, Peer{NeighborAS: 65017, RouterID: "192.0.2.17"}, uint64(1), true)
	if err := s.Announce(Route{ID: "r", Prefix: "10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if len(l.StaleRoutes()) != 0 || l.CanForward("r") {
		t.Fatal("zero restart time produced a stale route")
	}
}

func TestNoGracefulCapabilityWithdraws(t *testing.T) {
	l, _ := testLedger(t, 30*time.Second, Config{})
	s := mustOpen(t, l, Peer{NeighborAS: 65018, RouterID: "192.0.2.18"}, uint64(1), false)
	if err := s.Announce(Route{ID: "r", Prefix: "10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if len(l.StaleRoutes()) != 0 || l.CanForward("r") {
		t.Fatal("session without restart capability produced stale route")
	}
}

func TestWithdrawalOfStaleByReconnectedSession(t *testing.T) {
	l, _ := testLedger(t, 30*time.Second, Config{})
	peer := Peer{NeighborAS: 65019, RouterID: "192.0.2.19"}
	s1 := mustOpen(t, l, peer, uint64(1), true)
	if err := s1.Announce(Route{ID: "r", Prefix: "10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
	if err := s1.Disconnect(); err != nil {
		t.Fatal(err)
	}
	s2 := mustOpen(t, l, peer, uint64(2), true)
	if !s2.Withdraw("r") {
		t.Fatal("explicit withdraw did not remove stale route")
	}
	if l.CanForward("r") {
		t.Fatal("explicitly withdrawn stale route still forwardable")
	}
}

func TestPolicyVersionTracksCurrentOnSession(t *testing.T) {
	l, _ := testLedger(t, 30*time.Second, Config{})
	s := mustOpen(t, l, Peer{NeighborAS: 65020, RouterID: "192.0.2.20"}, uint64(1), true)
	l.UpdatePolicy(42, func(Route) bool { return true })
	if got := s.Identity().PolicyVersion; got != 42 {
		t.Fatalf("session policy version = %d, want 42", got)
	}
	l.UpdateVRP(43, func(Route) bool { return true })
	if got := s.Identity().VRPVersion; got != 43 {
		t.Fatalf("session VRP version = %d, want 43", got)
	}
}

func TestValidPolicyRevalidationPreservesOriginalVersionsAndDeadline(t *testing.T) {
	l, clk := testLedger(t, 30*time.Second, Config{})
	l.UpdatePolicy(3, func(Route) bool { return true })
	l.UpdateVRP(4, func(Route) bool { return true })
	s := mustOpen(t, l, Peer{NeighborAS: 65023, RouterID: "192.0.2.23"}, uint64(1), true)
	if err := s.Announce(Route{ID: "r", Prefix: "10.20.0.0/16"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Disconnect(); err != nil {
		t.Fatal(err)
	}
	before := onlyStale(t, l)
	l.UpdatePolicy(5, func(Route) bool { return true })
	l.UpdateVRP(6, func(Route) bool { return true })
	after := onlyStale(t, l)
	if after.OriginalPolicyVersion != 3 || after.OriginalVRPVersion != 4 {
		t.Fatalf("original versions changed: %#v", after)
	}
	if after.LastValidatedPolicyVersion != 5 || after.LastValidatedVRPVersion != 6 {
		t.Fatalf("revalidation versions not recorded: %#v", after)
	}
	if !after.Deadline.Equal(before.Deadline) || !after.Deadline.Equal(clk.Now().Add(30*time.Second)) {
		t.Fatalf("deadline moved: before=%v after=%v", before.Deadline, after.Deadline)
	}
}

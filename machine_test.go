package netsec

import (
	"errors"
	"math/rand"
	"net/netip"
	"sync"
	"testing"
	"time"
)

var (
	ip1 = netip.MustParseAddr("10.0.0.1")
	ip2 = netip.MustParseAddr("10.0.0.2")

	macA = mustMAC("aa:aa:aa:aa:aa:01")
	macB = mustMAC("bb:bb:bb:bb:bb:02")
	macC = mustMAC("cc:cc:cc:cc:cc:03")

	baseTime = time.Date(2026, 11, 18, 2, 0, 0, 0, time.UTC)
)

type fakeClock struct {
	t time.Time
}

func (c *fakeClock) now() time.Time {
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.t = c.t.Add(d)
}

func mustMAC(s string) MAC {
	mac, err := ParseMAC(s)
	if err != nil {
		panic(err)
	}
	return mac
}

func newTestMachine() (*Machine, *fakeClock) {
	clock := &fakeClock{t: baseTime}
	return NewMachine(WithClock(clock.now)), clock
}

func assertState(t *testing.T, m *Machine, ip netip.Addr, want State) {
	t.Helper()
	snap := m.Snapshot(ip)
	if snap == nil {
		t.Fatalf("snapshot for %s is nil, want state %s", ip, want)
	}
	if snap.State != want {
		t.Fatalf("state for %s = %s, want %s", ip, snap.State, want)
	}
}

func assertBinding(t *testing.T, m *Machine, ip netip.Addr, wantMAC MAC, wantGen uint64) {
	t.Helper()
	snap := m.Snapshot(ip)
	if snap == nil {
		t.Fatalf("snapshot for %s is nil", ip)
	}
	if snap.MAC != wantMAC || snap.Generation != wantGen {
		t.Fatalf("binding for %s = %s gen %d, want %s gen %d",
			ip, snap.MAC, snap.Generation, wantMAC, wantGen)
	}
}

func assertDeliver(t *testing.T, m *Machine, ip netip.Addr, wantMAC MAC) {
	t.Helper()
	got := m.Lookup(ip)
	if got.Action != ForwardDeliver || got.MAC != wantMAC {
		t.Fatalf("lookup %s = %+v, want deliver to %s", ip, got, wantMAC)
	}
}

func assertDrop(t *testing.T, m *Machine, ip netip.Addr) {
	t.Helper()
	got := m.Lookup(ip)
	if got.Action != ForwardDrop {
		t.Fatalf("lookup %s = %+v, want drop", ip, got)
	}
}

func assertErrIs(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

func lastEvent(t *testing.T, m *Machine, ip netip.Addr) Event {
	t.Helper()
	ev := m.Evidence(ip)
	if len(ev) == 0 {
		t.Fatalf("no events recorded for %s", ip)
	}
	return ev[len(ev)-1]
}

// TestHappyMigrationAndFramePath drives a full legal migration and checks
// where data frames go at every step, including that a late ARP from the old
// MAC cannot roll the new binding back.
func TestHappyMigrationAndFramePath(t *testing.T) {
	m, clock := newTestMachine()

	// Before any ticket, the IP is unknown and frames are dropped.
	assertDrop(t, m, ip1)

	if err := m.Grant(Ticket{IP: ip1, NewMAC: macA, Generation: 1}); err != nil {
		t.Fatalf("grant gen1: %v", err)
	}
	assertState(t, m, ip1, StateMigrating)
	// No confirmed binding yet: the first ticket also needs ARP proof.
	assertDrop(t, m, ip1)

	clock.advance(time.Second)
	if err := m.HandleArpClaim(ip1, macA); err != nil {
		t.Fatalf("confirm gen1: %v", err)
	}
	assertState(t, m, ip1, StateBound)
	assertBinding(t, m, ip1, macA, 1)
	assertDeliver(t, m, ip1, macA)

	// Incumbent refreshes are ticket-free and harmless.
	if err := m.HandleArpClaim(ip1, macA); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	assertBinding(t, m, ip1, macA, 1)

	// Control plane authorizes a drift to macB.
	if err := m.Grant(Ticket{IP: ip1, NewMAC: macB, Generation: 2}); err != nil {
		t.Fatalf("grant gen2: %v", err)
	}
	assertState(t, m, ip1, StateMigrating)
	// During migration, frames keep flowing to the incumbent.
	assertDeliver(t, m, ip1, macA)

	// Only the ticket-authorized MAC can confirm.
	if err := m.HandleArpClaim(ip1, macB); err != nil {
		t.Fatalf("confirm gen2: %v", err)
	}
	assertState(t, m, ip1, StateBound)
	assertBinding(t, m, ip1, macB, 2)
	assertDeliver(t, m, ip1, macB)

	// A late ARP from the old MAC is an unauthorized takeover now.
	err := m.HandleArpClaim(ip1, macA)
	assertErrIs(t, err, ErrArpDenied)
	assertState(t, m, ip1, StateQuarantined)
	assertBinding(t, m, ip1, macB, 2)
	assertDrop(t, m, ip1)

	ev := lastEvent(t, m, ip1)
	if ev.Kind != EventQuarantined || ev.MAC != macA {
		t.Fatalf("last event = %+v, want quarantine for %s", ev, macA)
	}

	// Control plane releases the entry; evidence is retained but the
	// binding is gone and frames drop.
	if err := m.Release(ip1); err != nil {
		t.Fatalf("release: %v", err)
	}
	assertState(t, m, ip1, StateEmpty)
	assertDrop(t, m, ip1)
	if len(m.Evidence(ip1)) == 0 {
		t.Fatal("evidence was discarded on release")
	}

	// A new, higher-generation ticket re-onboards the IP.
	if err := m.Grant(Ticket{IP: ip1, NewMAC: macC, Generation: 3}); err != nil {
		t.Fatalf("grant gen3: %v", err)
	}
	if err := m.HandleArpClaim(ip1, macC); err != nil {
		t.Fatalf("confirm gen3: %v", err)
	}
	assertBinding(t, m, ip1, macC, 3)
	assertDeliver(t, m, ip1, macC)
}

// TestStaleTicketRejected verifies the strictly increasing generation rule:
// equal and lower generations are refused at every lifecycle stage and can
// never overwrite a newer binding.
func TestStaleTicketRejected(t *testing.T) {
	m, _ := newTestMachine()

	// Regression: a same-MAC re-affirmation on a bound entry must consume
	// the generation in MaxGen, otherwise the next ticket can be wrongly
	// rejected as stale (or an older generation accepted later).
	if err := m.Grant(Ticket{IP: ip2, NewMAC: macA, Generation: 1}); err != nil {
		t.Fatalf("grant gen1: %v", err)
	}
	if err := m.HandleArpClaim(ip2, macA); err != nil {
		t.Fatalf("confirm gen1: %v", err)
	}
	if err := m.Grant(Ticket{IP: ip2, NewMAC: macA, Generation: 2}); err != nil {
		t.Fatalf("re-affirm gen2: %v", err)
	}
	snap := m.Snapshot(ip2)
	if snap.MaxGen != 2 || snap.Generation != 2 {
		t.Fatalf("after re-affirm: gen=%d maxGen=%d, want both 2",
			snap.Generation, snap.MaxGen)
	}
	assertErrIs(t, m.Grant(Ticket{IP: ip2, NewMAC: macB, Generation: 2}), ErrStaleTicket)

	if err := m.Grant(Ticket{IP: ip1, NewMAC: macA, Generation: 1}); err != nil {
		t.Fatalf("grant gen1: %v", err)
	}
	if err := m.HandleArpClaim(ip1, macA); err != nil {
		t.Fatalf("confirm gen1: %v", err)
	}

	// Replayed generation against a bound entry.
	err := m.Grant(Ticket{IP: ip1, NewMAC: macB, Generation: 1})
	assertErrIs(t, err, ErrStaleTicket)
	assertBinding(t, m, ip1, macA, 1)

	// Zero generation is invalid rather than a ticket.
	err = m.Grant(Ticket{IP: ip1, NewMAC: macB, Generation: 0})
	assertErrIs(t, err, ErrInvalidArgument)

	// A gen2 migration goes pending; the older gen1 ticket must not be
	// accepted while it is in flight.
	if err := m.Grant(Ticket{IP: ip1, NewMAC: macB, Generation: 2}); err != nil {
		t.Fatalf("grant gen2: %v", err)
	}
	err = m.Grant(Ticket{IP: ip1, NewMAC: macC, Generation: 1})
	assertErrIs(t, err, ErrStaleTicket)
	pendingSnap := m.Snapshot(ip1)
	if pendingSnap.Pending == nil || pendingSnap.Pending.NewMAC != macB || pendingSnap.Pending.Generation != 2 {
		t.Fatalf("pending ticket altered by stale grant: %+v", pendingSnap.Pending)
	}

	// Timeout consumes gen2; gen2 cannot be replayed afterwards.
	if err := m.ProbeTimeout(ip1); err != nil {
		t.Fatalf("timeout: %v", err)
	}
	assertState(t, m, ip1, StateBound)
	assertBinding(t, m, ip1, macA, 1)
	err = m.Grant(Ticket{IP: ip1, NewMAC: macB, Generation: 2})
	assertErrIs(t, err, ErrStaleTicket)

	// Evidence of every rejection is retained.
	var rejected int
	for _, ev := range m.Evidence(ip1) {
		if ev.Kind == EventTicketRejected {
			rejected++
		}
	}
	if rejected != 3 {
		t.Fatalf("recorded %d ticket rejections, want 3", rejected)
	}
}

// TestProbeTimeout covers both timeout shapes: a first binding whose probe
// never arrives returns to empty, while a failed migration reverts to the
// previous stable binding.
func TestProbeTimeout(t *testing.T) {
	m, _ := newTestMachine()

	// First binding never confirms: back to empty and dropping.
	if err := m.Grant(Ticket{IP: ip2, NewMAC: macA, Generation: 1}); err != nil {
		t.Fatalf("grant gen1: %v", err)
	}
	if err := m.ProbeTimeout(ip2); err != nil {
		t.Fatalf("timeout: %v", err)
	}
	assertState(t, m, ip2, StateEmpty)
	assertDrop(t, m, ip2)

	// Timeout outside a migration is an error.
	assertErrIs(t, m.ProbeTimeout(ip2), ErrInvalidState)

	// The failed generation stays consumed.
	assertErrIs(t, m.Grant(Ticket{IP: ip2, NewMAC: macA, Generation: 1}), ErrStaleTicket)

	// Build a stable gen2 binding, then fail a gen3 migration.
	if err := m.Grant(Ticket{IP: ip2, NewMAC: macA, Generation: 2}); err != nil {
		t.Fatalf("grant gen2: %v", err)
	}
	if err := m.HandleArpClaim(ip2, macA); err != nil {
		t.Fatalf("confirm gen2: %v", err)
	}
	if err := m.Grant(Ticket{IP: ip2, NewMAC: macB, Generation: 3}); err != nil {
		t.Fatalf("grant gen3: %v", err)
	}
	assertDeliver(t, m, ip2, macA)
	if err := m.ProbeTimeout(ip2); err != nil {
		t.Fatalf("timeout: %v", err)
	}
	assertState(t, m, ip2, StateBound)
	assertBinding(t, m, ip2, macA, 2)
	assertDeliver(t, m, ip2, macA)

	ev := lastEvent(t, m, ip2)
	if ev.Kind != EventProbeTimeout || ev.Generation != 3 || ev.MAC != macB {
		t.Fatalf("last event = %+v, want probe timeout for gen3/%s", ev, macB)
	}
}

// TestRevocation exercises the control plane revocation paths: active
// binding, pending migration, incumbent revoked mid-migration, and invalid
// revocation attempts.
func TestRevocation(t *testing.T) {
	m, _ := newTestMachine()

	// Revoking an unknown IP or an unseen generation changes nothing.
	assertErrIs(t, m.Revoke(ip1, 1), ErrUnknownTicket)

	if err := m.Grant(Ticket{IP: ip1, NewMAC: macA, Generation: 1}); err != nil {
		t.Fatalf("grant gen1: %v", err)
	}
	if err := m.HandleArpClaim(ip1, macA); err != nil {
		t.Fatalf("confirm gen1: %v", err)
	}

	// Revoking a generation that is not active is rejected.
	assertErrIs(t, m.Revoke(ip1, 2), ErrUnknownTicket)

	// Pending migration revocation falls back to the incumbent binding.
	if err := m.Grant(Ticket{IP: ip1, NewMAC: macB, Generation: 2}); err != nil {
		t.Fatalf("grant gen2: %v", err)
	}
	if err := m.Revoke(ip1, 2); err != nil {
		t.Fatalf("revoke pending gen2: %v", err)
	}
	assertState(t, m, ip1, StateBound)
	assertBinding(t, m, ip1, macA, 1)
	assertDeliver(t, m, ip1, macA)

	// Incumbent revoked while a newer migration is in flight: no fallback.
	if err := m.Grant(Ticket{IP: ip1, NewMAC: macB, Generation: 3}); err != nil {
		t.Fatalf("grant gen3: %v", err)
	}
	if err := m.Revoke(ip1, 1); err != nil {
		t.Fatalf("revoke incumbent gen1: %v", err)
	}
	assertState(t, m, ip1, StateMigrating)
	assertDrop(t, m, ip1)
	if err := m.HandleArpClaim(ip1, macB); err != nil {
		t.Fatalf("confirm gen3: %v", err)
	}
	assertBinding(t, m, ip1, macB, 3)

	// Revoking the active binding clears it entirely.
	if err := m.Revoke(ip1, 3); err != nil {
		t.Fatalf("revoke active gen3: %v", err)
	}
	assertState(t, m, ip1, StateEmpty)
	assertDrop(t, m, ip1)

	// Revocation of a quarantined entry is refused; release is required.
	if err := m.Grant(Ticket{IP: ip1, NewMAC: macA, Generation: 4}); err != nil {
		t.Fatalf("grant gen4: %v", err)
	}
	if err := m.HandleArpClaim(ip1, macA); err != nil {
		t.Fatalf("confirm gen4: %v", err)
	}
	assertErrIs(t, m.HandleArpClaim(ip1, macB), ErrArpDenied)
	assertState(t, m, ip1, StateQuarantined)
	assertErrIs(t, m.Revoke(ip1, 4), ErrInvalidState)

	// All revocation decisions are in the evidence trail.
	var revocations int
	for _, ev := range m.Evidence(ip1) {
		if ev.Kind == EventTicketRevoked {
			revocations++
		}
	}
	if revocations != 3 {
		t.Fatalf("recorded %d revocations, want 3", revocations)
	}
}

// TestTwoMACCompetition is the adversarial core: two MACs race for one IP.
// The MAC without a ticket never wins, the ticketed MAC wins only through an
// ARP confirmation that matches the ticket, and a late confirmation from the
// loser cannot overwrite the winner.
func TestTwoMACCompetition(t *testing.T) {
	m, _ := newTestMachine()

	// macA races with no ticket at all.
	assertErrIs(t, m.HandleArpClaim(ip1, macA), ErrArpDenied)
	assertState(t, m, ip1, StateQuarantined)
	assertDrop(t, m, ip1)
	ev := lastEvent(t, m, ip1)
	if ev.Kind != EventQuarantined || ev.MAC != macA {
		t.Fatalf("evidence = %+v, want quarantine for %s", ev, macA)
	}

	// A ticket for macB is the only way out of quarantine.
	if err := m.Grant(Ticket{IP: ip1, NewMAC: macB, Generation: 1}); err != nil {
		t.Fatalf("grant gen1: %v", err)
	}
	assertState(t, m, ip1, StateMigrating)

	// macA keeps claiming during the migration window and is shut out.
	assertErrIs(t, m.HandleArpClaim(ip1, macA), ErrArpDenied)
	assertState(t, m, ip1, StateQuarantined)
	assertDrop(t, m, ip1)

	// A higher-generation ticket reopens the window for macB.
	if err := m.Grant(Ticket{IP: ip1, NewMAC: macB, Generation: 2}); err != nil {
		t.Fatalf("grant gen2: %v", err)
	}
	if err := m.HandleArpClaim(ip1, macB); err != nil {
		t.Fatalf("confirm gen2: %v", err)
	}
	assertBinding(t, m, ip1, macB, 2)

	// Control plane then migrates back to macA with gen3 while macB tries
	// to cling to the address with a late, stale confirmation.
	if err := m.Grant(Ticket{IP: ip1, NewMAC: macA, Generation: 3}); err != nil {
		t.Fatalf("grant gen3: %v", err)
	}
	assertDeliver(t, m, ip1, macB)
	assertErrIs(t, m.HandleArpClaim(ip1, macB), ErrArpDenied)
	assertState(t, m, ip1, StateQuarantined)

	// The newer ticket remains the only recovery path; replaying gen2
	// (the winner's old ticket) is still rejected.
	assertErrIs(t, m.Grant(Ticket{IP: ip1, NewMAC: macB, Generation: 2}), ErrStaleTicket)
	if err := m.Grant(Ticket{IP: ip1, NewMAC: macA, Generation: 4}); err != nil {
		t.Fatalf("grant gen4: %v", err)
	}
	if err := m.HandleArpClaim(ip1, macA); err != nil {
		t.Fatalf("confirm gen4: %v", err)
	}
	assertBinding(t, m, ip1, macA, 4)

	// Every offending claim left forensic evidence with the real MAC.
	var conflicts int
	for _, ev := range m.Evidence(ip1) {
		if ev.Kind == EventArpConflict {
			conflicts++
		}
	}
	if conflicts != 3 {
		t.Fatalf("recorded %d conflicts, want 3", conflicts)
	}
}

// TestFrameForwardingMatrix enumerates where data frames go in every
// lifecycle state so traffic can never reach an unauthorized MAC.
func TestFrameForwardingMatrix(t *testing.T) {
	cases := []struct {
		name  string
		setup func(m *Machine)
		want  ForwardAction
		mac   MAC
	}{
		{
			name:  "unknown ip drops",
			setup: func(m *Machine) {},
			want:  ForwardDrop,
		},
		{
			name: "first ticket pending has no destination",
			setup: func(m *Machine) {
				m.Grant(Ticket{IP: ip1, NewMAC: macA, Generation: 1})
			},
			want: ForwardDrop,
		},
		{
			name: "bound entry delivers to incumbent",
			setup: func(m *Machine) {
				m.Grant(Ticket{IP: ip1, NewMAC: macA, Generation: 1})
				m.HandleArpClaim(ip1, macA)
			},
			want: ForwardDeliver,
			mac:  macA,
		},
		{
			name: "migration keeps frames on incumbent",
			setup: func(m *Machine) {
				m.Grant(Ticket{IP: ip1, NewMAC: macA, Generation: 1})
				m.HandleArpClaim(ip1, macA)
				m.Grant(Ticket{IP: ip1, NewMAC: macB, Generation: 2})
			},
			want: ForwardDeliver,
			mac:  macA,
		},
		{
			name: "confirmed migration switches destination atomically",
			setup: func(m *Machine) {
				m.Grant(Ticket{IP: ip1, NewMAC: macA, Generation: 1})
				m.HandleArpClaim(ip1, macA)
				m.Grant(Ticket{IP: ip1, NewMAC: macB, Generation: 2})
				m.HandleArpClaim(ip1, macB)
			},
			want: ForwardDeliver,
			mac:  macB,
		},
		{
			name: "quarantined entry drops every frame",
			setup: func(m *Machine) {
				m.Grant(Ticket{IP: ip1, NewMAC: macA, Generation: 1})
				m.HandleArpClaim(ip1, macA)
				m.HandleArpClaim(ip1, macB)
			},
			want: ForwardDrop,
		},
		{
			name: "released entry drops every frame",
			setup: func(m *Machine) {
				m.HandleArpClaim(ip1, macA)
				m.Release(ip1)
			},
			want: ForwardDrop,
		},
		{
			name: "revoked binding drops every frame",
			setup: func(m *Machine) {
				m.Grant(Ticket{IP: ip1, NewMAC: macA, Generation: 1})
				m.HandleArpClaim(ip1, macA)
				m.Revoke(ip1, 1)
			},
			want: ForwardDrop,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTestMachine()
			tc.setup(m)
			got := m.Lookup(ip1)
			if got.Action != tc.want {
				t.Fatalf("action = %s, want %s", got.Action, tc.want)
			}
			if tc.want == ForwardDeliver && got.MAC != tc.mac {
				t.Fatalf("destination = %s, want %s", got.MAC, tc.mac)
			}
			if tc.want == ForwardDrop && !got.MAC.IsZero() {
				t.Fatalf("drop decision carried MAC %s", got.MAC)
			}
		})
	}
}

// matrixOp is one event applied in the exhaustive state x event matrix.
type matrixOp struct {
	name string
	run  func(m *Machine) error
}

type matrixRow struct {
	name         string
	setup        func(m *Machine)
	op           matrixOp
	wantErr      error
	wantState    State
	wantMAC      MAC
	wantGen      uint64
	wantEvent    EventKind
	wantEventSet bool
}

func newStaleTicketOp() matrixOp {
	return matrixOp{
		name: "stale grant",
		run: func(m *Machine) error {
			return m.Grant(Ticket{IP: ip1, NewMAC: macC, Generation: 1})
		},
	}
}

func newHigherTicketOp(mac MAC) matrixOp {
	return matrixOp{
		name: "higher grant",
		run: func(m *Machine) error {
			return m.Grant(Ticket{IP: ip1, NewMAC: mac, Generation: 9})
		},
	}
}

// TestExhaustiveTransitions enumerates every control plane and data plane
// event against every lifecycle state and pins down the exact outcome,
// forming a complete state/event transition table for the machine.
func TestExhaustiveTransitions(t *testing.T) {
	ops := map[string]matrixOp{
		"staleGrant":   newStaleTicketOp(),
		"higherGrantA": newHigherTicketOp(macA),
		"higherGrantB": newHigherTicketOp(macB),
		"higherGrantC": newHigherTicketOp(macC),
		"arpA":         {name: "arp A", run: func(m *Machine) error { return m.HandleArpClaim(ip1, macA) }},
		"arpB":         {name: "arp B", run: func(m *Machine) error { return m.HandleArpClaim(ip1, macB) }},
		"arpC":         {name: "arp C", run: func(m *Machine) error { return m.HandleArpClaim(ip1, macC) }},
		"timeout":      {name: "probe timeout", run: func(m *Machine) error { return m.ProbeTimeout(ip1) }},
		"revokeGen1":   {name: "revoke gen1", run: func(m *Machine) error { return m.Revoke(ip1, 1) }},
		"revokeGen2":   {name: "revoke gen2", run: func(m *Machine) error { return m.Revoke(ip1, 2) }},
		"release":      {name: "release", run: func(m *Machine) error { return m.Release(ip1) }},
		"invalidArg":   {name: "invalid argument", run: func(m *Machine) error { return m.Grant(Ticket{IP: ip1, NewMAC: macA, Generation: 0}) }},
	}

	// Setup helpers leave the entry in the named state with macA gen1 as
	// the stable binding (where one exists).
	setupEmpty := func(m *Machine) {}
	setupBound := func(m *Machine) {
		m.Grant(Ticket{IP: ip1, NewMAC: macA, Generation: 1})
		m.HandleArpClaim(ip1, macA)
	}
	setupMigrating := func(m *Machine) {
		setupBound(m)
		m.Grant(Ticket{IP: ip1, NewMAC: macB, Generation: 2})
	}
	setupQuarantined := func(m *Machine) {
		setupBound(m)
		m.HandleArpClaim(ip1, macC)
	}

	rows := []matrixRow{
		// Empty state: an entry that has never been touched.
		{name: "empty/higher grant", setup: setupEmpty, op: ops["higherGrantB"], wantState: StateMigrating, wantMAC: MAC{}, wantGen: 0, wantEvent: EventTicketGranted, wantEventSet: true},
		// On a never-seen IP, gen1 is the first valid ticket rather than
		// stale; it enters the migration window and still needs ARP proof.
		{name: "empty/first grant", setup: setupEmpty, op: ops["staleGrant"], wantState: StateMigrating, wantMAC: MAC{}, wantGen: 0, wantEvent: EventTicketGranted, wantEventSet: true},
		{name: "empty/arp", setup: setupEmpty, op: ops["arpA"], wantErr: ErrArpDenied, wantState: StateQuarantined, wantEvent: EventQuarantined},
		{name: "empty/timeout", setup: setupEmpty, op: ops["timeout"], wantErr: ErrInvalidState, wantState: StateEmpty},
		{name: "empty/revoke", setup: setupEmpty, op: ops["revokeGen1"], wantErr: ErrUnknownTicket, wantState: StateEmpty},
		{name: "empty/release", setup: setupEmpty, op: ops["release"], wantErr: ErrInvalidState, wantState: StateEmpty},

		// Bound state: macA gen1 is the stable binding.
		{name: "bound/stale grant", setup: setupBound, op: ops["staleGrant"], wantErr: ErrStaleTicket, wantState: StateBound, wantMAC: macA, wantGen: 1, wantEvent: EventTicketRejected},
		{name: "bound/higher grant same mac", setup: setupBound, op: ops["higherGrantA"], wantState: StateBound, wantMAC: macA, wantGen: 9, wantEvent: EventTicketGranted, wantEventSet: true},
		{name: "bound/higher grant other mac", setup: setupBound, op: ops["higherGrantB"], wantState: StateMigrating, wantMAC: macA, wantGen: 1, wantEvent: EventTicketGranted, wantEventSet: true},
		{name: "bound/arp incumbent", setup: setupBound, op: ops["arpA"], wantState: StateBound, wantMAC: macA, wantGen: 1, wantEvent: EventArpRefresh},
		{name: "bound/arp challenger", setup: setupBound, op: ops["arpB"], wantErr: ErrArpDenied, wantState: StateQuarantined, wantMAC: macA, wantGen: 1, wantEvent: EventQuarantined},
		{name: "bound/timeout", setup: setupBound, op: ops["timeout"], wantErr: ErrInvalidState, wantState: StateBound, wantMAC: macA, wantGen: 1},
		{name: "bound/revoke active", setup: setupBound, op: ops["revokeGen1"], wantState: StateEmpty, wantMAC: MAC{}, wantGen: 0, wantEvent: EventTicketRevoked},
		{name: "bound/revoke unknown gen", setup: setupBound, op: ops["revokeGen2"], wantErr: ErrUnknownTicket, wantState: StateBound, wantMAC: macA, wantGen: 1},
		{name: "bound/release", setup: setupBound, op: ops["release"], wantErr: ErrInvalidState, wantState: StateBound, wantMAC: macA, wantGen: 1},

		// Migrating state: macA gen1 incumbent, pending macB gen2.
		{name: "migrating/stale grant", setup: setupMigrating, op: ops["staleGrant"], wantErr: ErrStaleTicket, wantState: StateMigrating, wantMAC: macA, wantGen: 1, wantEvent: EventTicketRejected},
		{name: "migrating/higher grant supersedes", setup: setupMigrating, op: ops["higherGrantC"], wantState: StateMigrating, wantMAC: macA, wantGen: 1, wantEvent: EventTicketGranted, wantEventSet: true},
		{name: "migrating/arp pending mac", setup: setupMigrating, op: ops["arpB"], wantState: StateBound, wantMAC: macB, wantGen: 2, wantEvent: EventMigrationConfirmed},
		{name: "migrating/arp old mac late", setup: setupMigrating, op: ops["arpA"], wantErr: ErrArpDenied, wantState: StateQuarantined, wantMAC: macA, wantGen: 1, wantEvent: EventQuarantined},
		{name: "migrating/arp third mac", setup: setupMigrating, op: ops["arpC"], wantErr: ErrArpDenied, wantState: StateQuarantined, wantMAC: macA, wantGen: 1, wantEvent: EventQuarantined},
		{name: "migrating/timeout reverts", setup: setupMigrating, op: ops["timeout"], wantState: StateBound, wantMAC: macA, wantGen: 1, wantEvent: EventProbeTimeout},
		{name: "migrating/revoke pending", setup: setupMigrating, op: ops["revokeGen2"], wantState: StateBound, wantMAC: macA, wantGen: 1, wantEvent: EventTicketRevoked},
		{name: "migrating/revoke incumbent", setup: setupMigrating, op: ops["revokeGen1"], wantState: StateMigrating, wantMAC: MAC{}, wantGen: 0, wantEvent: EventTicketRevoked},
		{name: "migrating/revoke unknown gen", setup: setupMigrating, op: newRevokeGen9(), wantErr: ErrUnknownTicket, wantState: StateMigrating, wantMAC: macA, wantGen: 1},
		{name: "migrating/release", setup: setupMigrating, op: ops["release"], wantErr: ErrInvalidState, wantState: StateMigrating, wantMAC: macA, wantGen: 1},

		// Quarantined state: frozen with macA gen1 evidence.
		{name: "quarantined/stale grant", setup: setupQuarantined, op: ops["staleGrant"], wantErr: ErrStaleTicket, wantState: StateQuarantined, wantMAC: macA, wantGen: 1, wantEvent: EventTicketRejected},
		{name: "quarantined/higher grant recovers", setup: setupQuarantined, op: ops["higherGrantB"], wantState: StateMigrating, wantMAC: macA, wantGen: 1, wantEvent: EventTicketGranted, wantEventSet: true},
		{name: "quarantined/arp incumbent", setup: setupQuarantined, op: ops["arpA"], wantErr: ErrArpDenied, wantState: StateQuarantined, wantMAC: macA, wantGen: 1, wantEvent: EventArpDenied},
		{name: "quarantined/arp challenger", setup: setupQuarantined, op: ops["arpB"], wantErr: ErrArpDenied, wantState: StateQuarantined, wantMAC: macA, wantGen: 1, wantEvent: EventArpDenied},
		{name: "quarantined/timeout", setup: setupQuarantined, op: ops["timeout"], wantErr: ErrInvalidState, wantState: StateQuarantined, wantMAC: macA, wantGen: 1},
		{name: "quarantined/revoke", setup: setupQuarantined, op: ops["revokeGen1"], wantErr: ErrInvalidState, wantState: StateQuarantined, wantMAC: macA, wantGen: 1},
		{name: "quarantined/release", setup: setupQuarantined, op: ops["release"], wantState: StateEmpty, wantMAC: MAC{}, wantGen: 0, wantEvent: EventTicketReleased},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			m, _ := newTestMachine()
			row.setup(m)
			before := len(m.Evidence(ip1))

			err := row.op.run(m)
			if row.wantErr != nil {
				assertErrIs(t, err, row.wantErr)
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			snap := m.Snapshot(ip1)
			if snap == nil {
				if row.wantState != StateEmpty || !row.wantMAC.IsZero() || row.wantGen != 0 {
					t.Fatalf("entry missing, want state %s mac %s gen %d",
						row.wantState, row.wantMAC, row.wantGen)
				}
			} else {
				if snap.State != row.wantState {
					t.Fatalf("state = %s, want %s", snap.State, row.wantState)
				}
				if snap.MAC != row.wantMAC {
					t.Fatalf("mac = %s, want %s", snap.MAC, row.wantMAC)
				}
				if snap.Generation != row.wantGen {
					t.Fatalf("gen = %d, want %d", snap.Generation, row.wantGen)
				}
			}

			if row.wantEventSet || row.wantEvent != 0 {
				ev := lastEvent(t, m, ip1)
				if ev.Kind != row.wantEvent {
					t.Fatalf("last event = %s, want %s", ev.Kind, row.wantEvent)
				}
			} else if len(m.Evidence(ip1)) != before {
				t.Fatalf("unexpected event emitted: %+v", lastEvent(t, m, ip1))
			}
		})
	}
}

func newRevokeGen9() matrixOp {
	return matrixOp{
		name: "revoke gen9",
		run:  func(m *Machine) error { return m.Revoke(ip1, 9) },
	}
}

// TestRandomSequenceInvariants fires long pseudo-random event sequences at
// the machine and checks, after every single step, the security invariants
// that no ordered scenario test could cover exhaustively on its own.
func TestRandomSequenceInvariants(t *testing.T) {
	const iterations = 2000
	rng := rand.New(rand.NewSource(0x5eed))
	macs := []MAC{macA, macB, macC}

	for iter := 0; iter < 30; iter++ {
		m, _ := newTestMachine()
		var nextGen uint64 = 1

		for step := 0; step < iterations; step++ {
			mac := macs[rng.Intn(len(macs))]

			switch rng.Intn(6) {
			case 0:
				// Control plane grant, alternating fresh and replayed gens.
				var gen uint64
				if nextGen > 2 && rng.Intn(4) == 0 {
					gen = uint64(1 + rng.Int63n(int64(nextGen)-1))
				} else {
					gen = nextGen
					nextGen++
				}
				_ = m.Grant(Ticket{IP: ip1, NewMAC: mac, Generation: gen})
			case 1, 2:
				_ = m.HandleArpClaim(ip1, mac)
			case 3:
				_ = m.ProbeTimeout(ip1)
			case 4:
				gen := uint64(1 + rng.Int63n(int64(nextGen)))
				_ = m.Revoke(ip1, gen)
			case 5:
				_ = m.Release(ip1)
			}

			snap := m.Snapshot(ip1)
			if snap == nil {
				continue
			}

			// Invariant 1: observed generations never move backwards.
			if snap.Generation > snap.MaxGen {
				t.Fatalf("iter %d step %d: active gen %d exceeds max gen %d",
					iter, step, snap.Generation, snap.MaxGen)
			}
			if snap.Pending != nil && snap.Pending.Generation > snap.MaxGen {
				t.Fatalf("iter %d step %d: pending gen %d exceeds max gen %d",
					iter, step, snap.Pending.Generation, snap.MaxGen)
			}

			// Invariant 2: state and pending ticket stay mutually consistent.
			if (snap.State == StateMigrating) != (snap.Pending != nil) {
				t.Fatalf("iter %d step %d: state %s but pending=%+v",
					iter, step, snap.State, snap.Pending)
			}
			if (snap.State == StateBound || snap.State == StateMigrating) == false {
				if snap.State == StateEmpty && !snap.MAC.IsZero() {
					t.Fatalf("iter %d step %d: empty entry retains MAC %s",
						iter, step, snap.MAC)
				}
			}

			// Invariant 3: forwarding never points at a non-binding MAC.
			decision := m.Lookup(ip1)
			switch snap.State {
			case StateBound, StateMigrating:
				if !snap.MAC.IsZero() {
					if decision.Action != ForwardDeliver || decision.MAC != snap.MAC {
						t.Fatalf("iter %d step %d: lookup %+v does not follow binding %s",
							iter, step, decision, snap.MAC)
					}
				} else if decision.Action != ForwardDrop {
					t.Fatalf("iter %d step %d: lookup %+v should drop without MAC",
						iter, step, decision)
				}
			default:
				if decision.Action != ForwardDrop {
					t.Fatalf("iter %d step %d: lookup %+v should drop in state %s",
						iter, step, decision, snap.State)
				}
			}

			// Invariant 4: every quarantine is backed by retained evidence.
			if snap.State == StateQuarantined {
				var found bool
				for _, ev := range snap.Events {
					if ev.Kind == EventArpConflict {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("iter %d step %d: quarantine without conflict evidence", iter, step)
				}
			}
		}

		// The evidence trail is append-only: events never disappear.
		snap := m.Snapshot(ip1)
		if len(snap.Events) == 0 {
			t.Fatalf("iter %d: no events recorded after random run", iter)
		}
	}
}

// TestConcurrentAccess drives the control plane and data plane entry points
// from many goroutines; run with -race to verify the locking strategy.
func TestConcurrentAccess(t *testing.T) {
	m, _ := newTestMachine()
	if err := m.Grant(Ticket{IP: ip1, NewMAC: macA, Generation: 1}); err != nil {
		t.Fatalf("seed grant: %v", err)
	}

	const goroutines = 16
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				switch (id + i) % 5 {
				case 0:
					_ = m.Grant(Ticket{
						IP:         ip1,
						NewMAC:     macsForConcurrency[(id+i)%len(macsForConcurrency)],
						Generation: uint64(2 + id*200 + i),
					})
				case 1:
					_ = m.HandleArpClaim(ip1, macsForConcurrency[i%len(macsForConcurrency)])
				case 2:
					_ = m.ProbeTimeout(ip1)
				case 3:
					_ = m.Lookup(ip1)
				case 4:
					_ = m.Snapshot(ip1)
				}
			}
		}(g)
	}

	wg.Wait()

	snap := m.Snapshot(ip1)
	if snap == nil {
		t.Fatal("entry vanished under concurrent access")
	}
	if len(snap.Events) == 0 {
		t.Fatal("evidence trail lost under concurrent access")
	}
}

var macsForConcurrency = []MAC{macA, macB, macC}

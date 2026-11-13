package bmc

import (
	"bytes"
	"testing"
	"time"
)

// fakeClock is a controllable time source for deterministic timeout tests.
type fakeClock struct {
	t time.Time
}

func (f *fakeClock) now() time.Time { return f.t }

func (f *fakeClock) advance(d time.Duration) time.Time {
	f.t = f.t.Add(d)
	return f.t
}

var (
	gmA = ClockIdentity{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77}
	gmB = ClockIdentity{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x78}
	gmC = ClockIdentity{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x99}
)

func port(identity ClockIdentity, number uint16) PortIdentity {
	return PortIdentity{ClockIdentity: identity, PortNumber: number}
}

func announce(identity ClockIdentity, steps uint16, source PortIdentity) Announce {
	return Announce{
		GrandmasterPriority1:               128,
		GrandmasterClockClass:              248,
		GrandmasterClockAccuracy:           0x21,
		GrandmasterOffsetScaledLogVariance: 0x4e5d,
		GrandmasterPriority2:               128,
		GrandmasterIdentity:                identity,
		StepsRemoved:                       steps,
		SourcePort:                         source,
	}
}

func newTestElector(t *testing.T, authenticate Authenticator, clock *fakeClock) *Elector {
	t.Helper()
	return NewElector(
		[]ClockIdentity{gmA, gmB},
		authenticate,
		WithClock(clock.now),
	)
}

func allowAll(PortIdentity) bool { return true }

func denyAll(PortIdentity) bool { return false }

func mustAdmit(t *testing.T, elector *Elector, msg Announce) {
	t.Helper()
	if admitted, reason := elector.HandleAnnounce(msg); !admitted || reason != RejectNone {
		t.Fatalf("message rejected: admitted=%v reason=%v", admitted, reason)
	}
}

func equalIdentity(a, b ClockIdentity) bool { return bytes.Equal(a, b) }

func TestRejectUnauthorizedPriority1Zero(t *testing.T) {
	clock := &fakeClock{t: time.Unix(100, 0)}
	elector := newTestElector(t, allowAll, clock)

	// The attacker uses the most attractive priority1 but an identity that is
	// not in the authorized set.
	attack := announce(gmC, 1, port(gmC, 1))
	attack.GrandmasterPriority1 = 0

	admitted, reason := elector.HandleAnnounce(attack)
	if admitted || reason != RejectUnauthorizedGrandmaster {
		t.Fatalf("unexpected admission: admitted=%v reason=%v", admitted, reason)
	}
	if got := elector.Elect(); got != nil {
		t.Fatalf("rejected message must not enter the election, elected %#v", got)
	}

	// A legitimate, higher-priority1 grandmaster still wins.
	legit := announce(gmA, 1, port(gmA, 1))
	mustAdmit(t, elector, legit)
	got := elector.Elect()
	if got == nil || !equalIdentity(got.Announce.GrandmasterIdentity, gmA) {
		t.Fatalf("expected authorized grandmaster A, got %#v", got)
	}
}

func TestRejectAuthenticationFailure(t *testing.T) {
	clock := &fakeClock{t: time.Unix(100, 0)}
	elector := newTestElector(t, denyAll, clock)

	msg := announce(gmA, 1, port(gmA, 1))
	msg.GrandmasterPriority1 = 0
	admitted, reason := elector.HandleAnnounce(msg)
	if admitted || reason != RejectAuthenticationFailed {
		t.Fatalf("expected authentication rejection, got admitted=%v reason=%v", admitted, reason)
	}
	if elector.Elect() != nil {
		t.Fatal("unauthenticated message must not participate in the election")
	}
}

func TestRejectStepsRemovedOutOfRange(t *testing.T) {
	clock := &fakeClock{t: time.Unix(100, 0)}
	elector := newTestElector(t, allowAll, clock)

	// stepsRemoved == 255 is the first rejected value.
	boundary := announce(gmA, MaxStepsRemoved, port(gmA, 1))
	if admitted, reason := elector.HandleAnnounce(boundary); admitted || reason != RejectStepsRemovedOutOfRange {
		t.Fatalf("stepsRemoved=255: admitted=%v reason=%v", admitted, reason)
	}

	// Values above the boundary are rejected as well.
	over := announce(gmA, 60000, port(gmA, 2))
	if admitted, reason := elector.HandleAnnounce(over); admitted || reason != RejectStepsRemovedOutOfRange {
		t.Fatalf("stepsRemoved=60000: admitted=%v reason=%v", admitted, reason)
	}

	// The largest accepted value is admitted.
	ok := announce(gmA, MaxStepsRemoved-1, port(gmA, 3))
	if admitted, reason := elector.HandleAnnounce(ok); !admitted || reason != RejectNone {
		t.Fatalf("stepsRemoved=254: admitted=%v reason=%v", admitted, reason)
	}

	got := elector.Elect()
	if got == nil || got.Announce.StepsRemoved != MaxStepsRemoved-1 {
		t.Fatalf("expected only the stepsRemoved=254 entry, got %#v", got)
	}
}

func TestAdmissionOrderAuthenticationFirst(t *testing.T) {
	clock := &fakeClock{t: time.Unix(100, 0)}
	elector := NewElector([]ClockIdentity{gmA}, func(PortIdentity) bool {
		return false
	}, WithClock(clock.now))

	// An unauthorized identity behind a failed authentication check reports
	// the authentication failure first.
	admitted, reason := elector.HandleAnnounce(announce(gmC, MaxStepsRemoved, port(gmC, 1)))
	if admitted || reason != RejectAuthenticationFailed {
		t.Fatalf("expected authentication failure, got admitted=%v reason=%v", admitted, reason)
	}
}

func TestPriority1SmallerWins(t *testing.T) {
	clock := &fakeClock{t: time.Unix(100, 0)}
	elector := newTestElector(t, allowAll, clock)

	low := announce(gmA, 1, port(gmA, 1))
	low.GrandmasterPriority1 = 10
	high := announce(gmB, 1, port(gmB, 1))
	high.GrandmasterPriority1 = 20

	mustAdmit(t, elector, high)
	mustAdmit(t, elector, low)

	got := elector.Elect()
	if got == nil || !equalIdentity(got.Announce.GrandmasterIdentity, gmA) {
		t.Fatalf("expected lower priority1 grandmaster A, got %#v", got)
	}
}

func TestVectorFieldsSmallerWinInOrder(t *testing.T) {
	clock := &fakeClock{t: time.Unix(100, 0)}
	elector := newTestElector(t, allowAll, clock)

	// Equal priority1; A has a worse clockClass, so B wins even though A's
	// identity bytes are smaller.
	a := announce(gmA, 1, port(gmA, 1))
	b := announce(gmB, 1, port(gmB, 1))
	a.GrandmasterClockClass = 100
	b.GrandmasterClockClass = 90
	mustAdmit(t, elector, a)
	mustAdmit(t, elector, b)
	if got := elector.Elect(); got == nil || !equalIdentity(got.Announce.GrandmasterIdentity, gmB) {
		t.Fatalf("expected clockClass tie-break to elect B, got %#v", got)
	}

	// Equal through clockClass; smaller clockAccuracy wins.
	a = announce(gmA, 1, port(gmA, 2))
	b = announce(gmB, 1, port(gmB, 2))
	a.GrandmasterClockClass = 90
	b.GrandmasterClockClass = 90
	a.GrandmasterClockAccuracy = 0x30
	b.GrandmasterClockAccuracy = 0x20
	mustAdmit(t, elector, a)
	mustAdmit(t, elector, b)
	if got := elector.Elect(); got == nil || !equalIdentity(got.Announce.GrandmasterIdentity, gmB) {
		t.Fatalf("expected clockAccuracy tie-break to elect B, got %#v", got)
	}

	// Equal through clockAccuracy; smaller offsetScaledLogVariance wins.
	a = announce(gmA, 1, port(gmA, 3))
	b = announce(gmB, 1, port(gmB, 3))
	a.GrandmasterClockClass = 90
	b.GrandmasterClockClass = 90
	a.GrandmasterClockAccuracy = 0x20
	b.GrandmasterClockAccuracy = 0x20
	a.GrandmasterOffsetScaledLogVariance = 1000
	b.GrandmasterOffsetScaledLogVariance = 900
	mustAdmit(t, elector, a)
	mustAdmit(t, elector, b)
	if got := elector.Elect(); got == nil || !equalIdentity(got.Announce.GrandmasterIdentity, gmB) {
		t.Fatalf("expected variance tie-break to elect B, got %#v", got)
	}

	// Equal through variance; smaller priority2 wins.
	a = announce(gmA, 1, port(gmA, 4))
	b = announce(gmB, 1, port(gmB, 4))
	a.GrandmasterClockClass = 90
	b.GrandmasterClockClass = 90
	a.GrandmasterClockAccuracy = 0x20
	b.GrandmasterClockAccuracy = 0x20
	a.GrandmasterOffsetScaledLogVariance = 900
	b.GrandmasterOffsetScaledLogVariance = 900
	a.GrandmasterPriority2 = 130
	b.GrandmasterPriority2 = 120
	mustAdmit(t, elector, a)
	mustAdmit(t, elector, b)
	if got := elector.Elect(); got == nil || !equalIdentity(got.Announce.GrandmasterIdentity, gmB) {
		t.Fatalf("expected priority2 tie-break to elect B, got %#v", got)
	}
}

func TestIdentityByteOrderTieBreak(t *testing.T) {
	clock := &fakeClock{t: time.Unix(100, 0)}
	elector := newTestElector(t, allowAll, clock)

	// Identical grandmaster vector: the smaller identity bytes (gmA) win.
	mustAdmit(t, elector, announce(gmB, 1, port(gmB, 1)))
	mustAdmit(t, elector, announce(gmA, 1, port(gmA, 1)))

	got := elector.Elect()
	if got == nil || !equalIdentity(got.Announce.GrandmasterIdentity, gmA) {
		t.Fatalf("expected byte-order tie-break to elect A, got %#v", got)
	}
}

func TestSameGrandmasterStepsRemovedThenPortTieBreak(t *testing.T) {
	clock := &fakeClock{t: time.Unix(100, 0)}
	elector := newTestElector(t, allowAll, clock)

	// Same grandmaster identity and vector; the shorter path wins.
	far := announce(gmA, 5, port(gmB, 1))
	near := announce(gmA, 2, port(gmC, 1))
	mustAdmit(t, elector, far)
	mustAdmit(t, elector, near)

	got := elector.Elect()
	if got == nil || got.Announce.StepsRemoved != 2 {
		t.Fatalf("expected shorter stepsRemoved path, got %#v", got)
	}

	// Equal stepsRemoved: the smaller source port identity wins.
	mustAdmit(t, elector, announce(gmA, 2, port(gmB, 7)))
	mustAdmit(t, elector, announce(gmA, 2, port(gmC, 7)))
	got = elector.Elect()
	if got == nil || !equalIdentity(got.Announce.SourcePort.ClockIdentity, gmB) {
		t.Fatalf("expected source port B tie-break, got %#v", got)
	}

	// Equal clock identity and stepsRemoved: the smaller port number wins.
	mustAdmit(t, elector, announce(gmA, 2, port(gmB, 2)))
	mustAdmit(t, elector, announce(gmA, 2, port(gmB, 1)))
	got = elector.Elect()
	if got == nil || got.Announce.SourcePort.PortNumber != 1 {
		t.Fatalf("expected port number 1 tie-break, got %#v", got)
	}

	// Candidates() collapses every path of gmA into a single best entry.
	candidates := elector.Candidates()
	if len(candidates) != 1 || !equalIdentity(candidates[0].Announce.GrandmasterIdentity, gmA) {
		t.Fatalf("expected one collapsed candidate for gmA, got %#v", candidates)
	}
}

func TestTimeoutEviction(t *testing.T) {
	clock := &fakeClock{t: time.Unix(100, 0)}
	elector := newTestElector(t, allowAll, clock)

	mustAdmit(t, elector, announce(gmA, 1, port(gmA, 1)))
	mustAdmit(t, elector, announce(gmB, 1, port(gmB, 1)))

	// Just under four seconds both grandmasters remain candidates.
	clock.advance(AnnounceTimeout - time.Millisecond)
	if got := elector.Elect(); got == nil {
		t.Fatal("candidates must survive until the four second timeout")
	}
	if len(elector.Candidates()) != 2 {
		t.Fatalf("expected two candidates before timeout, got %d", len(elector.Candidates()))
	}

	// Refresh A while B goes stale.
	clock.advance(time.Millisecond)
	mustAdmit(t, elector, announce(gmA, 1, port(gmA, 1)))
	clock.advance(AnnounceTimeout - time.Millisecond)

	got := elector.Elect()
	if got == nil || !equalIdentity(got.Announce.GrandmasterIdentity, gmA) {
		t.Fatalf("expected only refreshed grandmaster A, got %#v", got)
	}
	if len(elector.Candidates()) != 1 {
		t.Fatalf("expected stale grandmaster B removed, got %d candidates", len(elector.Candidates()))
	}

	// After another second without Announce, A expires too and no master
	// remains.
	clock.advance(time.Second)
	if got := elector.Elect(); got != nil {
		t.Fatalf("expected no candidate after the timeout, got %#v", got)
	}
}

func TestRejectedMessageDoesNotRefreshStaleEntry(t *testing.T) {
	clock := &fakeClock{t: time.Unix(100, 0)}
	elector := newTestElector(t, allowAll, clock)

	mustAdmit(t, elector, announce(gmA, 1, port(gmA, 1)))
	clock.advance(AnnounceTimeout)

	// A later rejected message must neither revive nor alter the stale entry.
	if admitted, reason := elector.HandleAnnounce(announce(gmC, 1, port(gmC, 1))); admitted {
		t.Fatalf("unauthorized message admitted: reason=%v", reason)
	}
	if removed := elector.Sweep(); removed != 1 {
		t.Fatalf("expected one stale entry removed, got %d", removed)
	}
}

func TestNilAuthenticatorFailsClosed(t *testing.T) {
	elector := NewElector([]ClockIdentity{gmA}, nil)
	admitted, reason := elector.HandleAnnounce(announce(gmA, 1, port(gmA, 1)))
	if admitted || reason != RejectAuthenticationFailed {
		t.Fatalf("nil authenticator must fail closed, got admitted=%v reason=%v", admitted, reason)
	}
	if elector.Elect() != nil {
		t.Fatal("expected no elected candidate")
	}
}

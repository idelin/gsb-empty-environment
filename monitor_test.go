package alertseg

import (
	"reflect"
	"testing"
	"time"
)

var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func at(min int) time.Time { return base.Add(time.Duration(min) * time.Minute) }

func ev(src string, st State, min int) TelemetryEvent {
	return TelemetryEvent{Time: at(min), Source: src, State: st}
}

func kinds(ms *Monitor) []NotifyKind {
	var out []NotifyKind
	for _, n := range ms.Notifications() {
		out = append(out, n.Kind)
	}
	return out
}

func equalKinds(got []NotifyKind, want ...NotifyKind) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestMergesRepeatedFiringIntoOneSegment(t *testing.T) {
	m := NewMonitor(10 * time.Minute)
	for i := 0; i < 5; i++ {
		m.Apply(ev("db-1", StateFiring, i))
	}
	if got := kinds(m); !equalKinds(got, NotifyFiring) {
		t.Fatalf("notifications = %v, want one FIRING", got)
	}
	segs := m.Segments()
	if len(segs) != 1 || !segs[0].Open || segs[0].Events != 5 || segs[0].State != StateFiring {
		t.Fatalf("segments = %+v, want one open FIRING segment with 5 events", segs)
	}
}

func TestRecoveryEmitsResolvedAndClosesSegment(t *testing.T) {
	m := NewMonitor(10 * time.Minute)
	m.Apply(ev("db-1", StateFiring, 0))
	m.Apply(ev("db-1", StateFiring, 1))
	m.Apply(ev("db-1", StateOK, 5))

	if got := kinds(m); !equalKinds(got, NotifyFiring, NotifyResolved) {
		t.Fatalf("notifications = %v, want FIRING, RESOLVED", got)
	}
	segs := m.Segments()
	if len(segs) != 2 {
		t.Fatalf("segments = %+v, want 2", segs)
	}
	if segs[0].Open || segs[0].Events != 2 || !segs[0].End.Equal(at(5)) {
		t.Fatalf("closed firing segment = %+v", segs[0])
	}
	if !segs[1].Open || segs[1].State != StateOK {
		t.Fatalf("open segment = %+v, want open OK", segs[1])
	}
}

func TestRepeatNotificationsRespectInterval(t *testing.T) {
	m := NewMonitor(10 * time.Minute)
	m.Apply(ev("db-1", StateFiring, 0))

	m.Tick(at(5))
	if got := kinds(m); !equalKinds(got, NotifyFiring) {
		t.Fatalf("before interval: notifications = %v", got)
	}
	m.Tick(at(10))
	m.Tick(at(19))
	m.Tick(at(20))
	if got := kinds(m); !equalKinds(got, NotifyFiring, NotifyRepeat, NotifyRepeat) {
		t.Fatalf("notifications = %v, want FIRING + 2 REPEAT", got)
	}
	ns := m.Notifications()
	if !ns[1].Time.Equal(at(10)) || !ns[2].Time.Equal(at(20)) {
		t.Fatalf("repeat times = %v, %v", ns[1].Time, ns[2].Time)
	}
}

func TestSuppressedPersistentFaultNotifiedOnLift(t *testing.T) {
	m := NewMonitor(10 * time.Minute)
	m.SetSuppressed(true, at(0))
	m.Apply(ev("api-1", StateFiring, 1))
	m.Apply(ev("api-1", StateFiring, 2))
	m.Tick(at(10)) // suppressed: no repeat

	if n := len(m.Notifications()); n != 0 {
		t.Fatalf("during suppression: %d notifications, want 0", n)
	}
	// Real state is preserved while suppressed.
	segs := m.Segments()
	if len(segs) != 1 || segs[0].State != StateFiring || segs[0].Events != 2 {
		t.Fatalf("segments during suppression = %+v", segs)
	}

	m.SetSuppressed(false, at(15))
	ns := m.Notifications()
	if len(ns) != 1 || ns[0].Kind != NotifyFiring || ns[0].Reason != "suppression-lifted" {
		t.Fatalf("after lift: notifications = %+v, want one backfilled FIRING", ns)
	}
	if !ns[0].Time.Equal(at(15)) {
		t.Fatalf("backfill time = %v, want lift time %v", ns[0].Time, at(15))
	}

	m.Tick(at(26)) // 11m after backfill -> repeat resumes
	if got := kinds(m); !equalKinds(got, NotifyFiring, NotifyRepeat) {
		t.Fatalf("notifications = %v, want FIRING + REPEAT", got)
	}
}

func TestRecoveryDuringSuppressionIsNotBackfilled(t *testing.T) {
	m := NewMonitor(10 * time.Minute)
	m.SetSuppressed(true, at(0))
	m.Apply(ev("cache-1", StateFiring, 1))
	m.Apply(ev("cache-1", StateOK, 3)) // recovers while suppressed
	m.SetSuppressed(false, at(5))

	if n := len(m.Notifications()); n != 0 {
		t.Fatalf("notifications = %d, want 0 (no backfill of recovered fault)", n)
	}

	// Monitor still works normally afterwards.
	m.Apply(ev("cache-1", StateFiring, 6))
	m.Apply(ev("cache-1", StateOK, 7))
	if got := kinds(m); !equalKinds(got, NotifyFiring, NotifyResolved) {
		t.Fatalf("post-lift notifications = %v, want FIRING, RESOLVED", got)
	}
}

func TestKnownFaultRecoveringDuringSuppressionStaysSilent(t *testing.T) {
	m := NewMonitor(10 * time.Minute)
	m.Apply(ev("db-1", StateFiring, 0)) // notified before suppression
	m.SetSuppressed(true, at(1))
	m.Apply(ev("db-1", StateOK, 2)) // recovery held
	m.SetSuppressed(false, at(5))

	if got := kinds(m); !equalKinds(got, NotifyFiring) {
		t.Fatalf("notifications = %v, want only the initial FIRING", got)
	}
}

func TestKnownFaultStillFiringAfterLiftRepeatsByInterval(t *testing.T) {
	m := NewMonitor(10 * time.Minute)
	m.Apply(ev("db-1", StateFiring, 0)) // notified
	m.SetSuppressed(true, at(1))
	m.Tick(at(20)) // suppressed: no repeat even though interval passed
	m.SetSuppressed(false, at(30))

	if got := kinds(m); !equalKinds(got, NotifyFiring) {
		t.Fatalf("notifications = %v, want only initial FIRING (no duplicate on lift)", got)
	}
	m.Tick(at(30)) // 30m since last notify >= interval
	if got := kinds(m); !equalKinds(got, NotifyFiring, NotifyRepeat) {
		t.Fatalf("notifications = %v, want FIRING + REPEAT", got)
	}
}

func TestMultipleSourcesAreIndependent(t *testing.T) {
	m := NewMonitor(10 * time.Minute)
	m.Apply(ev("a", StateFiring, 0))
	m.Apply(ev("b", StateFiring, 1))
	m.Apply(ev("a", StateOK, 2))

	if got := kinds(m); !equalKinds(got, NotifyFiring, NotifyFiring, NotifyResolved) {
		t.Fatalf("notifications = %v", got)
	}
	segs := m.Segments()
	if len(segs) != 3 {
		t.Fatalf("segments = %+v, want 3", segs)
	}
}

func TestSynthesizeIsDeterministic(t *testing.T) {
	f1 := Synthesize(42, base, []string{"db-1", "api-1"}, 6)
	f2 := Synthesize(42, base, []string{"db-1", "api-1"}, 6)
	if len(f1.Steps) != len(f2.Steps) {
		t.Fatalf("step counts differ")
	}
	for i := range f1.Steps {
		if !reflect.DeepEqual(f1.Steps[i], f2.Steps[i]) {
			t.Fatalf("step %d differs: %+v vs %+v", i, f1.Steps[i], f2.Steps[i])
		}
	}
	m, err := Replay(f1)
	if err != nil {
		t.Fatalf("replay synthesized fixture: %v", err)
	}
	if len(m.Notifications()) == 0 {
		t.Fatalf("synthesized fixture produced no notifications")
	}
}

package alertseg

import (
	"testing"
)

type wantNote struct {
	kind   NotifyKind
	reason string
	min    int
}

func checkFixture(t *testing.T, path string, want []wantNote) {
	t.Helper()
	f, err := LoadFixture(path)
	if err != nil {
		t.Fatalf("load %s: %v", path, err)
	}
	m, err := Replay(f)
	if err != nil {
		t.Fatalf("replay %s: %v", path, err)
	}
	ns := m.Notifications()
	if len(ns) != len(want) {
		t.Fatalf("%s: %d notifications %v, want %d", path, len(ns), kinds(m), len(want))
	}
	for i, w := range want {
		if ns[i].Kind != w.kind || ns[i].Reason != w.reason || !ns[i].Time.Equal(at(w.min)) {
			t.Errorf("%s: notification %d = {%s %s %v}, want {%s %s min=%d}",
				path, i, ns[i].Kind, ns[i].Reason, ns[i].Time, w.kind, w.reason, w.min)
		}
	}
}

func TestFixtureFlapping(t *testing.T) {
	checkFixture(t, "fixtures/flapping.json", []wantNote{
		{NotifyFiring, "state-change", 0},
		{NotifyRepeat, "repeat-interval", 10},
		{NotifyRepeat, "repeat-interval", 20},
		{NotifyResolved, "state-change", 25},
		{NotifyFiring, "state-change", 30},
		{NotifyResolved, "state-change", 31},
	})
}

func TestFixtureSuppressedPersistent(t *testing.T) {
	checkFixture(t, "fixtures/suppressed-persistent.json", []wantNote{
		{NotifyFiring, "suppression-lifted", 15},
		{NotifyRepeat, "repeat-interval", 26},
		{NotifyResolved, "state-change", 30},
	})
}

func TestFixtureRecoveryInSuppression(t *testing.T) {
	checkFixture(t, "fixtures/recovery-in-suppression.json", []wantNote{
		{NotifyFiring, "state-change", 6},
		{NotifyResolved, "state-change", 7},
	})
}

package alertseg

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func at(min int) time.Time { return base.Add(time.Duration(min) * time.Minute) }

func kinds(ns []Notification) []Kind {
	var ks []Kind
	for _, n := range ns {
		ks = append(ks, n.Kind)
	}
	return ks
}

func ats(ns []Notification) []time.Time {
	var ts []time.Time
	for _, n := range ns {
		ts = append(ts, n.At)
	}
	return ts
}

func TestFiringRepeatResolve(t *testing.T) {
	e := NewEngine(30*time.Minute, nil)
	e.Apply(Event{At: at(0), State: Firing})
	e.AdvanceTo(at(65)) // repeats at 30 and 60
	e.Apply(Event{At: at(70), State: OK})

	ns := e.Notifications()
	wantKinds := []Kind{KindFiring, KindRepeat, KindRepeat, KindResolved}
	if !reflect.DeepEqual(kinds(ns), wantKinds) {
		t.Fatalf("kinds = %v, want %v", kinds(ns), wantKinds)
	}
	wantAts := []time.Time{at(0), at(30), at(60), at(70)}
	if !reflect.DeepEqual(ats(ns), wantAts) {
		t.Fatalf("ats = %v, want %v", ats(ns), wantAts)
	}
	for _, n := range ns {
		if !n.Since.Equal(at(0)) {
			t.Fatalf("Since = %v, want %v", n.Since, at(0))
		}
	}
}

func TestSuppressedPersistentFaultNotifiedAfterLift(t *testing.T) {
	e := NewEngine(30*time.Minute, []Window{{Start: at(10), End: at(60)}})
	e.Apply(Event{At: at(15), State: Firing}) // inside suppression: silent
	e.AdvanceTo(at(59))
	if got := e.Notifications(); len(got) != 0 {
		t.Fatalf("before lift: got %d notifications, want 0", len(got))
	}
	e.AdvanceTo(at(60)) // lift
	ns := e.Notifications()
	if len(ns) != 1 || ns[0].Kind != KindFiring || !ns[0].At.Equal(at(60)) {
		t.Fatalf("after lift: got %v, want one FIRING at 01:00", ns)
	}
	if !ns[0].Since.Equal(at(15)) {
		t.Fatalf("Since = %v, want real fault start %v", ns[0].Since, at(15))
	}
	e.AdvanceTo(at(95)) // repeat schedule resumes from the lift
	ns = e.Notifications()
	if len(ns) != 2 || ns[1].Kind != KindRepeat || !ns[1].At.Equal(at(90)) {
		t.Fatalf("got %v, want FIRING@60 then REPEAT@90", ns)
	}
}

func TestRecoveryDuringSuppressionNoBackfill(t *testing.T) {
	e := NewEngine(30*time.Minute, []Window{{Start: at(10), End: at(60)}})
	e.Apply(Event{At: at(15), State: Firing})
	e.Apply(Event{At: at(45), State: OK}) // recovers while suppressed
	e.AdvanceTo(at(180))
	if got := e.Notifications(); len(got) != 0 {
		t.Fatalf("got %v, want no notifications at all", got)
	}
}

func TestRepeatsPausedDuringSuppression(t *testing.T) {
	e := NewEngine(30*time.Minute, []Window{{Start: at(10), End: at(60)}})
	e.Apply(Event{At: at(0), State: Firing}) // notified before suppression
	e.AdvanceTo(at(200))

	ns := e.Notifications()
	wantKinds := []Kind{KindFiring, KindRepeat, KindRepeat, KindRepeat, KindRepeat, KindRepeat}
	wantAts := []time.Time{at(0), at(60), at(90), at(120), at(150), at(180)}
	// repeat at 30 falls inside suppression and is dropped; the lift at 60
	// re-notifies the still-active fault as a REPEAT.
	if !reflect.DeepEqual(kinds(ns), wantKinds) || !reflect.DeepEqual(ats(ns), wantAts) {
		t.Fatalf("got %v %v, want %v %v", kinds(ns), ats(ns), wantKinds, wantAts)
	}
}

func TestSuppressionBoundaries(t *testing.T) {
	e := NewEngine(0, []Window{{Start: at(10), End: at(60)}})
	e.Apply(Event{At: at(10), State: Firing}) // at window start: suppressed
	e.Apply(Event{At: at(20), State: OK})
	e.Apply(Event{At: at(60), State: Firing}) // at window end: no longer suppressed
	e.AdvanceTo(at(70))

	ns := e.Notifications()
	wantKinds := []Kind{KindFiring}
	wantAts := []time.Time{at(60)}
	if !reflect.DeepEqual(kinds(ns), wantKinds) || !reflect.DeepEqual(ats(ns), wantAts) {
		t.Fatalf("got %v %v, want %v %v", kinds(ns), ats(ns), wantKinds, wantAts)
	}
}

func TestFlappingMergedIntoSegments(t *testing.T) {
	e := NewEngine(time.Hour, nil)
	e.Apply(Event{At: at(0), State: Firing})
	e.Apply(Event{At: at(1), State: Firing}) // duplicate: merged
	e.Apply(Event{At: at(2), State: Firing}) // duplicate: merged
	e.Apply(Event{At: at(3), State: OK})
	e.Apply(Event{At: at(4), State: OK}) // duplicate: merged
	e.Apply(Event{At: at(5), State: Firing})
	e.AdvanceTo(at(10))

	want := []Segment{
		{State: Firing, Start: at(0), End: at(3)},
		{State: OK, Start: at(3), End: at(5)},
		{State: Firing, Start: at(5)},
	}
	if got := e.Segments(); !reflect.DeepEqual(got, want) {
		t.Fatalf("segments = %v, want %v", got, want)
	}
	// One FIRING per fault segment, one RESOLVED for the first.
	wantKinds := []Kind{KindFiring, KindResolved, KindFiring}
	if got := kinds(e.Notifications()); !reflect.DeepEqual(got, wantKinds) {
		t.Fatalf("kinds = %v, want %v", got, wantKinds)
	}
}

func TestFixtures(t *testing.T) {
	cases := []struct {
		file      string
		wantKinds []Kind
		wantAts   []time.Time
	}{
		{
			file:      "flapping.json",
			wantKinds: []Kind{KindFiring, KindResolved, KindFiring, KindRepeat, KindRepeat, KindRepeat, KindResolved},
			wantAts:   []time.Time{at(0), at(1), at(2), at(32), at(62), at(92), at(120)},
		},
		{
			file:      "suppressed_persistent.json",
			wantKinds: []Kind{KindFiring, KindRepeat, KindResolved},
			wantAts:   []time.Time{at(60), at(90), at(120)},
		},
		{
			file:      "suppressed_recovery.json",
			wantKinds: nil,
			wantAts:   nil,
		},
	}
	for _, c := range cases {
		f, err := LoadFixture(filepath.Join("..", "fixtures", c.file))
		if err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		e, err := f.Replay()
		if err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		ns := e.Notifications()
		if !reflect.DeepEqual(kinds(ns), c.wantKinds) || !reflect.DeepEqual(ats(ns), c.wantAts) {
			t.Errorf("%s: got %v %v, want %v %v", c.file, kinds(ns), ats(ns), c.wantKinds, c.wantAts)
		}
	}
}

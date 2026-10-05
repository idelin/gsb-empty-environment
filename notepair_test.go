package notepair

import (
	"reflect"
	"testing"
)

func on(tick int64, ch, pitch, vel int) Event {
	return Event{Tick: tick, Kind: NoteOn, Channel: ch, Pitch: pitch, Velocity: vel}
}

func off(tick int64, ch, pitch int) Event {
	return Event{Tick: tick, Kind: NoteOff, Channel: ch, Pitch: pitch}
}

func pedalDown(tick int64, ch int) Event { return Event{Tick: tick, Kind: PedalDown, Channel: ch} }
func pedalUp(tick int64, ch int) Event   { return Event{Tick: tick, Kind: PedalUp, Channel: ch} }

func mustProcess(t *testing.T, events []Event) Result {
	t.Helper()
	res, err := Process(events)
	if err != nil {
		t.Fatalf("Process returned error: %v", err)
	}
	return res
}

func TestBasicPairing(t *testing.T) {
	res := mustProcess(t, []Event{
		on(0, 0, 60, 100),
		off(480, 0, 60),
	})
	want := []Note{{Channel: 0, Pitch: 60, Velocity: 100, OnTick: 0, OffTick: 480, EndTick: 480}}
	if !reflect.DeepEqual(res.Notes, want) {
		t.Errorf("Notes = %+v, want %+v", res.Notes, want)
	}
	if len(res.OrphanOffs) != 0 || len(res.Unclosed) != 0 {
		t.Errorf("unexpected orphans %v or unclosed %v", res.OrphanOffs, res.Unclosed)
	}
}

func TestOverlappingSamePitchFIFO(t *testing.T) {
	// Two overlapping instances of the same pitch; the first note-off
	// must close the earliest instance.
	res := mustProcess(t, []Event{
		on(0, 0, 60, 90),
		on(100, 0, 60, 80),
		off(150, 0, 60),
		off(200, 0, 60),
	})
	want := []Note{
		{Channel: 0, Pitch: 60, Velocity: 90, OnTick: 0, OffTick: 150, EndTick: 150},
		{Channel: 0, Pitch: 60, Velocity: 80, OnTick: 100, OffTick: 200, EndTick: 200},
	}
	if !reflect.DeepEqual(res.Notes, want) {
		t.Errorf("Notes = %+v, want %+v", res.Notes, want)
	}
}

func TestOrphanOff(t *testing.T) {
	res := mustProcess(t, []Event{
		off(10, 0, 64),
		on(20, 0, 64, 100),
		off(30, 0, 64),
		off(40, 0, 64), // second instance never started: orphan
	})
	if len(res.OrphanOffs) != 2 {
		t.Fatalf("OrphanOffs = %v, want 2 entries", res.OrphanOffs)
	}
	if res.OrphanOffs[0].Tick != 10 || res.OrphanOffs[1].Tick != 40 {
		t.Errorf("orphan ticks = %d, %d; want 10, 40",
			res.OrphanOffs[0].Tick, res.OrphanOffs[1].Tick)
	}
	if len(res.Notes) != 1 || !res.Notes[0].Closed() {
		t.Errorf("Notes = %+v, want one closed note", res.Notes)
	}
}

func TestUnclosedNote(t *testing.T) {
	res := mustProcess(t, []Event{
		on(0, 0, 60, 100),
		on(10, 0, 62, 100),
		off(20, 0, 60),
	})
	if len(res.Unclosed) != 1 {
		t.Fatalf("Unclosed = %+v, want 1 entry", res.Unclosed)
	}
	u := res.Unclosed[0]
	if u.Pitch != 62 || u.OffTick != -1 || u.EndTick != -1 {
		t.Errorf("unclosed note = %+v, want pitch 62 with OffTick/EndTick -1", u)
	}
}

func TestPedalExtendsSoundingButNotKeyOff(t *testing.T) {
	res := mustProcess(t, []Event{
		on(0, 0, 60, 100),
		pedalDown(50, 0),
		off(100, 0, 60), // key released under pedal
		pedalUp(200, 0), // sounding stops here
	})
	if len(res.Notes) != 1 {
		t.Fatalf("Notes = %+v, want 1 note", res.Notes)
	}
	n := res.Notes[0]
	if n.OffTick != 100 {
		t.Errorf("OffTick = %d, want 100 (pedal must not change key-off time)", n.OffTick)
	}
	if n.EndTick != 200 {
		t.Errorf("EndTick = %d, want 200 (pedal release)", n.EndTick)
	}
	if !n.Sustained {
		t.Errorf("Sustained = false, want true")
	}
	if len(res.Unclosed) != 0 {
		t.Errorf("Unclosed = %+v, want empty", res.Unclosed)
	}
}

func TestPedalDownAfterOffHasNoEffect(t *testing.T) {
	res := mustProcess(t, []Event{
		on(0, 0, 60, 100),
		off(100, 0, 60),
		pedalDown(150, 0),
		pedalUp(300, 0),
	})
	n := res.Notes[0]
	if n.EndTick != 100 || n.Sustained {
		t.Errorf("note = %+v, want EndTick 100, Sustained false", n)
	}
}

func TestPedalHoldsMultipleAndReNote(t *testing.T) {
	// Key off under pedal, same pitch re-struck, then pedal released:
	// only the first (held) instance ends at pedal-up.
	res := mustProcess(t, []Event{
		pedalDown(0, 0),
		on(10, 0, 60, 100),
		off(20, 0, 60),
		on(30, 0, 60, 90),
		pedalUp(40, 0),
		off(50, 0, 60),
	})
	want := []Note{
		{Channel: 0, Pitch: 60, Velocity: 100, OnTick: 10, OffTick: 20, EndTick: 40, Sustained: true},
		{Channel: 0, Pitch: 60, Velocity: 90, OnTick: 30, OffTick: 50, EndTick: 50},
	}
	if !reflect.DeepEqual(res.Notes, want) {
		t.Errorf("Notes = %+v, want %+v", res.Notes, want)
	}
}

func TestPedalNeverReleasedLeavesNoteUnclosed(t *testing.T) {
	res := mustProcess(t, []Event{
		pedalDown(0, 0),
		on(10, 0, 60, 100),
		off(20, 0, 60),
	})
	if len(res.Unclosed) != 1 {
		t.Fatalf("Unclosed = %+v, want 1 entry", res.Unclosed)
	}
	u := res.Unclosed[0]
	if u.OffTick != 20 || u.EndTick != -1 {
		t.Errorf("unclosed = %+v, want OffTick 20, EndTick -1", u)
	}
}

func TestSameTickOrderingMatters(t *testing.T) {
	// At tick 10, off-then-on: the off closes the first instance and
	// the on starts a new one, which stays open.
	res := mustProcess(t, []Event{
		on(0, 0, 60, 100),
		off(10, 0, 60),
		on(10, 0, 60, 80),
	})
	if res.Notes[0].OffTick != 10 || res.Notes[0].EndTick != 10 {
		t.Errorf("first note = %+v, want closed at 10", res.Notes[0])
	}
	if len(res.Unclosed) != 1 || res.Unclosed[0].OnTick != 10 {
		t.Errorf("Unclosed = %+v, want the note started at 10", res.Unclosed)
	}

	// Same tick, on-then-off: the off pairs with the note that was
	// already sounding, and the new note-on is a zero-duration note.
	res = mustProcess(t, []Event{
		on(0, 0, 60, 100),
		on(10, 0, 60, 80),
		off(10, 0, 60),
	})
	if res.Notes[0].OffTick != 10 {
		t.Errorf("first note OffTick = %d, want 10", res.Notes[0].OffTick)
	}
	if res.Notes[1].OffTick != -1 || res.Notes[1].EndTick != -1 {
		t.Errorf("second note = %+v, want unclosed", res.Notes[1])
	}
}

func TestZeroDurationNote(t *testing.T) {
	res := mustProcess(t, []Event{
		on(5, 0, 60, 100),
		off(5, 0, 60),
	})
	n := res.Notes[0]
	if n.OnTick != 5 || n.OffTick != 5 || n.EndTick != 5 {
		t.Errorf("note = %+v, want zero-duration note at tick 5", n)
	}
}

func TestSameTickPedalUpBeforeOff(t *testing.T) {
	// Pedal released at the same tick as the key, but first: the note
	// is not sustained.
	res := mustProcess(t, []Event{
		pedalDown(0, 0),
		on(10, 0, 60, 100),
		pedalUp(20, 0),
		off(20, 0, 60),
	})
	n := res.Notes[0]
	if n.EndTick != 20 || n.Sustained {
		t.Errorf("note = %+v, want EndTick 20, Sustained false", n)
	}

	// Off first, then pedal-up at the same tick: sustained until the
	// pedal-up, which is the same tick.
	res = mustProcess(t, []Event{
		pedalDown(0, 0),
		on(10, 0, 60, 100),
		off(20, 0, 60),
		pedalUp(20, 0),
	})
	n = res.Notes[0]
	if n.EndTick != 20 || !n.Sustained {
		t.Errorf("note = %+v, want EndTick 20, Sustained true", n)
	}
}

func TestCrossChannelIsolation(t *testing.T) {
	res := mustProcess(t, []Event{
		on(0, 0, 60, 100),
		on(0, 1, 60, 90),
		pedalDown(10, 0), // pedal only on channel 0
		off(20, 0, 60),   // closes ch0 note, held by pedal
		off(20, 1, 60),   // closes ch1 note immediately
		pedalUp(30, 1),   // pedal-up on ch1: nothing held there
		pedalUp(40, 0),   // releases the ch0 note
	})
	want := []Note{
		{Channel: 0, Pitch: 60, Velocity: 100, OnTick: 0, OffTick: 20, EndTick: 40, Sustained: true},
		{Channel: 1, Pitch: 60, Velocity: 90, OnTick: 0, OffTick: 20, EndTick: 20},
	}
	if !reflect.DeepEqual(res.Notes, want) {
		t.Errorf("Notes = %+v, want %+v", res.Notes, want)
	}
	if len(res.OrphanOffs) != 0 || len(res.Unclosed) != 0 {
		t.Errorf("orphans = %v, unclosed = %v; want both empty", res.OrphanOffs, res.Unclosed)
	}
}

func TestCrossChannelOrphan(t *testing.T) {
	// A note-off on channel 1 must not pair with a note on channel 0.
	res := mustProcess(t, []Event{
		on(0, 0, 60, 100),
		off(10, 1, 60),
	})
	if len(res.OrphanOffs) != 1 {
		t.Errorf("OrphanOffs = %v, want 1 entry", res.OrphanOffs)
	}
	if len(res.Unclosed) != 1 || res.Unclosed[0].Channel != 0 {
		t.Errorf("Unclosed = %+v, want the channel-0 note", res.Unclosed)
	}
}

func TestOutOfOrderRejected(t *testing.T) {
	_, err := Process([]Event{
		on(10, 0, 60, 100),
		off(5, 0, 60),
	})
	if err == nil {
		t.Fatal("expected error for decreasing ticks")
	}
	oor, ok := err.(*ErrOutOfOrder)
	if !ok {
		t.Fatalf("error type = %T, want *ErrOutOfOrder", err)
	}
	if oor.Index != 1 || oor.PrevTick != 10 || oor.Tick != 5 {
		t.Errorf("ErrOutOfOrder = %+v", oor)
	}
}

func TestUnknownKindRejected(t *testing.T) {
	_, err := Process([]Event{{Tick: 0, Kind: Kind(99)}})
	if err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

func TestEmptyStream(t *testing.T) {
	res := mustProcess(t, nil)
	if len(res.Notes) != 0 || len(res.OrphanOffs) != 0 || len(res.Unclosed) != 0 {
		t.Errorf("Result = %+v, want all empty", res)
	}
}

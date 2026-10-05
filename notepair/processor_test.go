package notepair

import (
	"reflect"
	"testing"
)

func on(tick int64, ch, pitch, vel uint8) Event {
	return Event{Tick: tick, Channel: ch, Kind: KindNoteOn, Pitch: pitch, Velocity: vel}
}

func off(tick int64, ch, pitch uint8) Event {
	return Event{Tick: tick, Channel: ch, Kind: KindNoteOff, Pitch: pitch}
}

func pedalDown(tick int64, ch uint8) Event {
	return Event{Tick: tick, Channel: ch, Kind: KindPedalDown}
}
func pedalUp(tick int64, ch uint8) Event { return Event{Tick: tick, Channel: ch, Kind: KindPedalUp} }

func TestBasicPairing(t *testing.T) {
	res := ProcessEvents([]Event{on(0, 0, 60, 100), off(10, 0, 60)})
	want := []Note{{Channel: 0, Pitch: 60, Velocity: 100, Start: 0, KeyOff: 10, SoundEnd: 10}}
	if !reflect.DeepEqual(res.Notes, want) {
		t.Fatalf("notes = %+v, want %+v", res.Notes, want)
	}
	if len(res.OrphanOffs) != 0 || len(res.Unclosed) != 0 || len(res.Violations) != 0 {
		t.Fatalf("unexpected reports: %+v", res)
	}
}

func TestOverlappingSamePitchFIFO(t *testing.T) {
	// Two overlapping instances of pitch 60; each off must pair with the
	// earliest still-open instance.
	res := ProcessEvents([]Event{
		on(0, 0, 60, 90),
		on(5, 0, 60, 80),
		off(8, 0, 60),
		off(12, 0, 60),
	})
	want := []Note{
		{Channel: 0, Pitch: 60, Velocity: 90, Start: 0, KeyOff: 8, SoundEnd: 8},
		{Channel: 0, Pitch: 60, Velocity: 80, Start: 5, KeyOff: 12, SoundEnd: 12},
	}
	if !reflect.DeepEqual(res.Notes, want) {
		t.Fatalf("notes = %+v, want %+v", res.Notes, want)
	}
}

func TestPedalExtendsSoundButNotKeyOff(t *testing.T) {
	res := ProcessEvents([]Event{
		on(0, 0, 60, 100),
		pedalDown(3, 0),
		off(5, 0, 60), // key released at 5 while pedal is down
		pedalUp(9, 0), // sound stops at 9
	})
	want := []Note{{Channel: 0, Pitch: 60, Velocity: 100, Start: 0, KeyOff: 5, SoundEnd: 9, Sustained: true}}
	if !reflect.DeepEqual(res.Notes, want) {
		t.Fatalf("notes = %+v, want %+v", res.Notes, want)
	}
}

func TestPedalDoesNotAffectNoteReleasedBeforePress(t *testing.T) {
	res := ProcessEvents([]Event{
		on(0, 0, 60, 100),
		off(5, 0, 60),
		pedalDown(6, 0),
		pedalUp(9, 0),
	})
	want := []Note{{Channel: 0, Pitch: 60, Velocity: 100, Start: 0, KeyOff: 5, SoundEnd: 5}}
	if !reflect.DeepEqual(res.Notes, want) {
		t.Fatalf("notes = %+v, want %+v", res.Notes, want)
	}
}

func TestPedalHoldsMultipleNotesAndRepress(t *testing.T) {
	res := ProcessEvents([]Event{
		pedalDown(0, 0),
		on(1, 0, 60, 100),
		on(2, 0, 64, 100),
		off(3, 0, 60),
		off(4, 0, 64),
		pedalUp(5, 0),
		pedalDown(6, 0),
		on(7, 0, 67, 100),
		off(8, 0, 67),
		pedalUp(10, 0),
	})
	want := []Note{
		{Channel: 0, Pitch: 60, Velocity: 100, Start: 1, KeyOff: 3, SoundEnd: 5, Sustained: true},
		{Channel: 0, Pitch: 64, Velocity: 100, Start: 2, KeyOff: 4, SoundEnd: 5, Sustained: true},
		{Channel: 0, Pitch: 67, Velocity: 100, Start: 7, KeyOff: 8, SoundEnd: 10, Sustained: true},
	}
	if !reflect.DeepEqual(res.Notes, want) {
		t.Fatalf("notes = %+v, want %+v", res.Notes, want)
	}
}

func TestPedalHeldAtStreamEnd(t *testing.T) {
	// Pedal never released: sounding end falls back to the stream end.
	res := ProcessEvents([]Event{
		pedalDown(0, 0),
		on(1, 0, 60, 100),
		off(3, 0, 60),
	})
	want := []Note{{Channel: 0, Pitch: 60, Velocity: 100, Start: 1, KeyOff: 3, SoundEnd: 3, Sustained: false}}
	if !reflect.DeepEqual(res.Notes, want) {
		t.Fatalf("notes = %+v, want %+v", res.Notes, want)
	}
}

func TestOrphanNoteOff(t *testing.T) {
	res := ProcessEvents([]Event{off(5, 0, 60)})
	if len(res.OrphanOffs) != 1 || res.OrphanOffs[0] != off(5, 0, 60) {
		t.Fatalf("orphans = %+v", res.OrphanOffs)
	}
	if len(res.Notes) != 0 {
		t.Fatalf("notes = %+v", res.Notes)
	}
}

func TestUnclosedNote(t *testing.T) {
	res := ProcessEvents([]Event{on(2, 0, 60, 100), on(4, 1, 62, 90)})
	want := []Note{
		{Channel: 0, Pitch: 60, Velocity: 100, Start: 2, KeyOff: -1, SoundEnd: -1},
		{Channel: 1, Pitch: 62, Velocity: 90, Start: 4, KeyOff: -1, SoundEnd: -1},
	}
	if !reflect.DeepEqual(res.Unclosed, want) {
		t.Fatalf("unclosed = %+v, want %+v", res.Unclosed, want)
	}
}

func TestVelocityZeroNoteOnIsNoteOff(t *testing.T) {
	res := ProcessEvents([]Event{on(0, 0, 60, 100), on(10, 0, 60, 0)})
	want := []Note{{Channel: 0, Pitch: 60, Velocity: 100, Start: 0, KeyOff: 10, SoundEnd: 10}}
	if !reflect.DeepEqual(res.Notes, want) {
		t.Fatalf("notes = %+v, want %+v", res.Notes, want)
	}
}

func TestSameTickRetrigger(t *testing.T) {
	// Canonical order: off before on at the same tick retriggers the key.
	res := ProcessEvents([]Event{
		on(0, 0, 60, 100),
		off(10, 0, 60),
		on(10, 0, 60, 90),
		off(20, 0, 60),
	})
	want := []Note{
		{Channel: 0, Pitch: 60, Velocity: 100, Start: 0, KeyOff: 10, SoundEnd: 10},
		{Channel: 0, Pitch: 60, Velocity: 90, Start: 10, KeyOff: 20, SoundEnd: 20},
	}
	if !reflect.DeepEqual(res.Notes, want) {
		t.Fatalf("notes = %+v, want %+v", res.Notes, want)
	}
	if len(res.Violations) != 0 {
		t.Fatalf("violations = %+v", res.Violations)
	}
}

func TestSameTickOrderViolation(t *testing.T) {
	// Note-on before note-off at the same tick breaks canonical order.
	res := ProcessEvents([]Event{
		on(0, 0, 60, 100),
		on(10, 0, 60, 90),
		off(10, 0, 60),
	})
	if len(res.Violations) != 1 || res.Violations[0].Index != 2 {
		t.Fatalf("violations = %+v", res.Violations)
	}
	// The off still pairs FIFO with the earliest open instance.
	if len(res.Notes) != 1 || res.Notes[0].KeyOff != 10 || res.Notes[0].Velocity != 100 {
		t.Fatalf("notes = %+v", res.Notes)
	}
	if len(res.Unclosed) != 1 {
		t.Fatalf("unclosed = %+v", res.Unclosed)
	}
}

func TestBackwardsTickViolation(t *testing.T) {
	res := ProcessEvents([]Event{on(10, 0, 60, 100), off(5, 0, 60)})
	if len(res.Violations) != 1 {
		t.Fatalf("violations = %+v", res.Violations)
	}
}

func TestSortEventsCanonicalizes(t *testing.T) {
	messy := []Event{
		on(10, 0, 60, 90),
		off(10, 0, 60),
		on(0, 0, 60, 100),
		pedalDown(10, 0),
		pedalUp(10, 0),
		off(20, 0, 60),
	}
	sorted := SortEvents(messy)
	wantOrder := []Event{
		on(0, 0, 60, 100),
		off(10, 0, 60),
		pedalUp(10, 0),
		pedalDown(10, 0),
		on(10, 0, 60, 90),
		off(20, 0, 60),
	}
	if !reflect.DeepEqual(sorted, wantOrder) {
		t.Fatalf("sorted = %+v, want %+v", sorted, wantOrder)
	}
	res := ProcessEvents(sorted)
	if len(res.Violations) != 0 {
		t.Fatalf("violations after sort = %+v", res.Violations)
	}
	if len(res.Notes) != 2 {
		t.Fatalf("notes = %+v", res.Notes)
	}
}

func TestCrossChannelIsolation(t *testing.T) {
	res := ProcessEvents([]Event{
		on(0, 0, 60, 100),
		pedalDown(1, 0),
		off(2, 1, 60), // different channel: orphan, must not close ch-0 note
		off(3, 0, 60), // closes ch-0 note; held by ch-0 pedal
		pedalUp(4, 1), // ch-1 pedal: no effect
		pedalUp(5, 0), // releases ch-0 note
	})
	want := []Note{{Channel: 0, Pitch: 60, Velocity: 100, Start: 0, KeyOff: 3, SoundEnd: 5, Sustained: true}}
	if !reflect.DeepEqual(res.Notes, want) {
		t.Fatalf("notes = %+v, want %+v", res.Notes, want)
	}
	if len(res.OrphanOffs) != 1 || res.OrphanOffs[0].Channel != 1 {
		t.Fatalf("orphans = %+v", res.OrphanOffs)
	}
}

func TestSamePitchDifferentChannelsDoNotMix(t *testing.T) {
	res := ProcessEvents([]Event{
		on(0, 0, 60, 100),
		on(0, 1, 60, 90),
		off(5, 1, 60),
		off(7, 0, 60),
	})
	want := []Note{
		{Channel: 1, Pitch: 60, Velocity: 90, Start: 0, KeyOff: 5, SoundEnd: 5},
		{Channel: 0, Pitch: 60, Velocity: 100, Start: 0, KeyOff: 7, SoundEnd: 7},
	}
	if !reflect.DeepEqual(res.Notes, want) {
		t.Fatalf("notes = %+v, want %+v", res.Notes, want)
	}
}

// Package notepair reconstructs note intervals from a stream of
// note-on, note-off and sustain-pedal events.
//
// Pairing rules:
//   - A note-off pairs with the earliest still-open note-on of the same
//     pitch on the same channel (FIFO), so overlapping instances of the
//     same pitch are supported.
//   - The sustain pedal extends the sounding end of notes whose key was
//     released while the pedal was down, but never changes the key
//     release time itself.
//   - Channels are fully isolated: events on one channel never pair with
//     or affect another channel.
package notepair

// EventKind identifies the type of a stream event.
type EventKind int

const (
	KindNoteOn EventKind = iota
	KindNoteOff
	KindPedalDown
	KindPedalUp
)

func (k EventKind) String() string {
	switch k {
	case KindNoteOn:
		return "NoteOn"
	case KindNoteOff:
		return "NoteOff"
	case KindPedalDown:
		return "PedalDown"
	case KindPedalUp:
		return "PedalUp"
	}
	return "Unknown"
}

// rank defines the canonical processing order for events sharing a tick:
// releases happen before presses so a key can be retriggered on the same
// tick, and the pedal lifts before new notes start.
func (k EventKind) rank() int {
	switch k {
	case KindNoteOff:
		return 0
	case KindPedalUp:
		return 1
	case KindPedalDown:
		return 2
	case KindNoteOn:
		return 3
	}
	return 4
}

// Event is a single input event. Pitch and Velocity are only meaningful
// for note events. A note-on with velocity 0 is treated as a note-off,
// following MIDI convention.
type Event struct {
	Tick     int64
	Channel  uint8
	Kind     EventKind
	Pitch    uint8
	Velocity uint8
}

// effectiveKind normalizes velocity-0 note-ons into note-offs.
func (e Event) effectiveKind() EventKind {
	if e.Kind == KindNoteOn && e.Velocity == 0 {
		return KindNoteOff
	}
	return e.Kind
}

// Note is a reconstructed note interval.
//
// Start is the note-on tick, KeyOff the key-release tick, and SoundEnd
// the tick at which the note actually stops sounding. SoundEnd >= KeyOff;
// the pedal may push it later but never moves KeyOff. Sustained reports
// whether the pedal extended the sounding end beyond KeyOff.
type Note struct {
	Channel   uint8
	Pitch     uint8
	Velocity  uint8
	Start     int64
	KeyOff    int64
	SoundEnd  int64
	Sustained bool
}

// OrderViolation describes an event that breaks the required stream
// ordering (non-decreasing ticks, canonical kind order within a tick).
type OrderViolation struct {
	Index  int // index of the offending event in the input slice
	Event  Event
	Reason string
}

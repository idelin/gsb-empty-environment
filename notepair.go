// Package notepair reconstructs note intervals from a stream of
// note-on, note-off and sustain-pedal events.
//
// Semantics:
//   - The same pitch may overlap: a note-on while the same pitch is
//     already sounding starts a new, independent instance.
//   - A note-off is paired with the earliest still-unended instance
//     of the same pitch on the same channel (FIFO).
//   - The sustain pedal prolongs the sounding interval of a note whose
//     key is released while the pedal is down, but it never changes the
//     recorded key-off time.
//   - Channels are fully isolated: notes and pedals on one channel
//     never affect another channel.
//   - Events must be presented with non-decreasing ticks. Events at the
//     same tick are applied in slice order, so the caller controls the
//     ordering of simultaneous events.
package notepair

import "fmt"

// Kind identifies the type of an Event.
type Kind int

const (
	NoteOn Kind = iota
	NoteOff
	PedalDown
	PedalUp
)

func (k Kind) String() string {
	switch k {
	case NoteOn:
		return "NoteOn"
	case NoteOff:
		return "NoteOff"
	case PedalDown:
		return "PedalDown"
	case PedalUp:
		return "PedalUp"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Event is a single input event. Pitch is meaningful for NoteOn and
// NoteOff; Velocity only for NoteOn. Channel and Pitch are ignored by
// pedal events except that the pedal applies to its own channel.
type Event struct {
	Tick     int64
	Kind     Kind
	Channel  int
	Pitch    int
	Velocity int
}

// Note is a reconstructed note interval.
//
// OnTick is when the key was pressed. OffTick is when the key was
// released, or -1 if the key was never released. EndTick is when the
// note stopped sounding, or -1 if it was still sounding at the end of
// the stream. EndTick >= OffTick always; the pedal may push EndTick
// past OffTick but never changes OffTick itself.
type Note struct {
	Channel   int
	Pitch     int
	Velocity  int
	OnTick    int64
	OffTick   int64
	EndTick   int64
	Sustained bool // true if the sounding end was deferred by the pedal
}

// Closed reports whether the note's sounding interval is fully known.
func (n Note) Closed() bool { return n.EndTick >= 0 }

// Result is the outcome of processing an event stream.
type Result struct {
	// Notes contains every note-on instance, in the order the
	// note-ons appeared. Unclosed notes have OffTick and/or
	// EndTick == -1.
	Notes []Note
	// OrphanOffs contains note-off events that matched no
	// still-unended instance on their channel and pitch.
	OrphanOffs []Event
	// Unclosed contains the notes still sounding at the end of the
	// stream (EndTick == -1). This includes notes whose key was
	// released under a pedal that was never released.
	Unclosed []Note
}

// ErrOutOfOrder is returned when event ticks are not non-decreasing.
type ErrOutOfOrder struct {
	Index          int
	PrevTick, Tick int64
}

func (e *ErrOutOfOrder) Error() string {
	return fmt.Sprintf("notepair: event %d at tick %d follows tick %d: events must be non-decreasing in time",
		e.Index, e.Tick, e.PrevTick)
}

// Validate checks that event ticks are non-decreasing and that kinds
// are known. Same-tick events are allowed and keep their slice order.
func Validate(events []Event) error {
	prev := int64(0)
	for i, ev := range events {
		if i > 0 && ev.Tick < prev {
			return &ErrOutOfOrder{Index: i, PrevTick: prev, Tick: ev.Tick}
		}
		prev = ev.Tick
		switch ev.Kind {
		case NoteOn, NoteOff, PedalDown, PedalUp:
		default:
			return fmt.Errorf("notepair: event %d has unknown kind %d", i, int(ev.Kind))
		}
	}
	return nil
}

// noteRef points at the Note slot inside Result.Notes for one
// in-flight instance.
type noteRef struct {
	index int // index into Result.Notes
	held  bool
}

// channelState holds everything belonging to one channel.
type channelState struct {
	active    map[int][]noteRef // pitch -> unended instances, FIFO
	sustained []int             // indexes of key-off notes held by pedal
	pedalDown bool
}

func newChannelState() *channelState {
	return &channelState{active: make(map[int][]noteRef)}
}

// Process reconstructs note intervals from events. It validates the
// stream with Validate and returns an error if it is malformed.
func Process(events []Event) (Result, error) {
	if err := Validate(events); err != nil {
		return Result{}, err
	}

	var res Result
	channels := make(map[int]*channelState)
	channel := func(c int) *channelState {
		cs, ok := channels[c]
		if !ok {
			cs = newChannelState()
			channels[c] = cs
		}
		return cs
	}

	for _, ev := range events {
		cs := channel(ev.Channel)
		switch ev.Kind {
		case NoteOn:
			res.Notes = append(res.Notes, Note{
				Channel:  ev.Channel,
				Pitch:    ev.Pitch,
				Velocity: ev.Velocity,
				OnTick:   ev.Tick,
				OffTick:  -1,
				EndTick:  -1,
			})
			ref := noteRef{index: len(res.Notes) - 1}
			cs.active[ev.Pitch] = append(cs.active[ev.Pitch], ref)

		case NoteOff:
			queue := cs.active[ev.Pitch]
			if len(queue) == 0 {
				res.OrphanOffs = append(res.OrphanOffs, ev)
				continue
			}
			// Pair with the earliest unended instance (FIFO).
			ref := queue[0]
			cs.active[ev.Pitch] = queue[1:]
			n := &res.Notes[ref.index]
			n.OffTick = ev.Tick
			if cs.pedalDown {
				cs.sustained = append(cs.sustained, ref.index)
			} else {
				n.EndTick = ev.Tick
			}

		case PedalDown:
			cs.pedalDown = true

		case PedalUp:
			cs.pedalDown = false
			for _, idx := range cs.sustained {
				n := &res.Notes[idx]
				n.EndTick = ev.Tick
				n.Sustained = true
			}
			cs.sustained = cs.sustained[:0]
		}
	}

	for i := range res.Notes {
		if !res.Notes[i].Closed() {
			res.Unclosed = append(res.Unclosed, res.Notes[i])
		}
	}
	return res, nil
}

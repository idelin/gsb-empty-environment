package notepair

import (
	"fmt"
	"sort"
)

const numChannels = 16

// openNote is a note-on that has not yet been paired with a note-off.
type openNote struct {
	channel  uint8
	pitch    uint8
	velocity uint8
	start    int64
}

// channelState holds the per-channel pairing state. Channels never share
// state, which guarantees cross-channel isolation.
type channelState struct {
	open      map[uint8][]openNote // FIFO queue of unclosed notes per pitch
	pedalDown bool
	sustained []Note // key released while pedal down, awaiting pedal-up
}

func newChannelState() *channelState {
	return &channelState{open: make(map[uint8][]openNote)}
}

// Result is the outcome of processing an event stream.
type Result struct {
	// Notes contains every completed note interval, in completion order.
	Notes []Note
	// OrphanOffs contains note-off events that matched no open note.
	OrphanOffs []Event
	// Unclosed contains notes whose key was never released by the end of
	// the stream. Their KeyOff and SoundEnd are -1.
	Unclosed []Note
	// Violations lists stream-ordering problems (see OrderViolation).
	Violations []OrderViolation
}

// Processor reconstructs note intervals from an event stream. It is
// stateful; create a new one per stream with NewProcessor.
type Processor struct {
	channels   [numChannels]*channelState
	result     Result
	lastTick   int64
	lastRank   int
	started    bool
	eventIndex int
}

func NewProcessor() *Processor {
	p := &Processor{}
	for i := range p.channels {
		p.channels[i] = newChannelState()
	}
	return p
}

// Process feeds one event into the processor.
func (p *Processor) Process(e Event) {
	p.validateOrder(e)
	ch := p.channels[e.Channel%numChannels]

	switch e.effectiveKind() {
	case KindNoteOn:
		ch.open[e.Pitch] = append(ch.open[e.Pitch], openNote{
			channel:  e.Channel,
			pitch:    e.Pitch,
			velocity: e.Velocity,
			start:    e.Tick,
		})
	case KindNoteOff:
		p.noteOff(ch, e)
	case KindPedalDown:
		ch.pedalDown = true
	case KindPedalUp:
		p.pedalUp(ch, e.Tick)
	}
	p.eventIndex++
}

// noteOff pairs a note-off with the earliest unclosed note of the pitch.
func (p *Processor) noteOff(ch *channelState, e Event) {
	queue := ch.open[e.Pitch]
	if len(queue) == 0 {
		p.result.OrphanOffs = append(p.result.OrphanOffs, e)
		return
	}
	head := queue[0]
	ch.open[e.Pitch] = queue[1:]

	note := Note{
		Channel:  head.channel,
		Pitch:    head.pitch,
		Velocity: head.velocity,
		Start:    head.start,
		KeyOff:   e.Tick,
		SoundEnd: e.Tick,
	}
	if ch.pedalDown {
		// The key is released now, but the pedal keeps the note
		// sounding until pedal-up. KeyOff stays at this tick.
		ch.sustained = append(ch.sustained, note)
		return
	}
	p.result.Notes = append(p.result.Notes, note)
}

// pedalUp ends the sounding of every note held by the pedal.
func (p *Processor) pedalUp(ch *channelState, tick int64) {
	ch.pedalDown = false
	for _, note := range ch.sustained {
		note.SoundEnd = tick
		note.Sustained = tick > note.KeyOff
		p.result.Notes = append(p.result.Notes, note)
	}
	ch.sustained = ch.sustained[:0]
}

// validateOrder records ordering violations without stopping processing.
func (p *Processor) validateOrder(e Event) {
	rank := e.effectiveKind().rank()
	switch {
	case p.started && e.Tick < p.lastTick:
		p.result.Violations = append(p.result.Violations, OrderViolation{
			Index:  p.eventIndex,
			Event:  e,
			Reason: fmt.Sprintf("tick %d goes backwards (previous tick %d)", e.Tick, p.lastTick),
		})
	case p.started && e.Tick == p.lastTick && rank < p.lastRank:
		p.result.Violations = append(p.result.Violations, OrderViolation{
			Index:  p.eventIndex,
			Event:  e,
			Reason: fmt.Sprintf("%s at tick %d breaks canonical same-tick order (NoteOff < PedalUp < PedalDown < NoteOn)", e.Kind, e.Tick),
		})
	}
	p.started = true
	p.lastTick = e.Tick
	p.lastRank = rank
}

// Finish closes the stream at endTick. Notes still held by the pedal are
// ended at endTick; notes whose key was never released are reported as
// unclosed. The accumulated Result is returned.
func (p *Processor) Finish(endTick int64) Result {
	for _, ch := range p.channels {
		p.pedalUp(ch, endTick)
		for pitch, queue := range ch.open {
			for _, o := range queue {
				p.result.Unclosed = append(p.result.Unclosed, Note{
					Channel:  o.channel,
					Pitch:    pitch,
					Velocity: o.velocity,
					Start:    o.start,
					KeyOff:   -1,
					SoundEnd: -1,
				})
			}
		}
		ch.open = make(map[uint8][]openNote)
	}
	sort.SliceStable(p.result.Unclosed, func(i, j int) bool {
		a, b := p.result.Unclosed[i], p.result.Unclosed[j]
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		return a.Pitch < b.Pitch
	})
	return p.result
}

// ProcessEvents runs a whole slice of events through a fresh processor
// and finishes the stream at the last event's tick.
func ProcessEvents(events []Event) Result {
	p := NewProcessor()
	var end int64
	for _, e := range events {
		p.Process(e)
		if e.Tick > end {
			end = e.Tick
		}
	}
	return p.Finish(end)
}

// SortEvents returns a copy of events sorted into canonical stream order:
// non-decreasing tick, and NoteOff < PedalUp < PedalDown < NoteOn within
// a tick. The sort is stable, so relative order of equal-rank events is
// preserved.
func SortEvents(events []Event) []Event {
	sorted := make([]Event, len(events))
	copy(sorted, events)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.Tick != b.Tick {
			return a.Tick < b.Tick
		}
		return a.effectiveKind().rank() < b.effectiveKind().rank()
	})
	return sorted
}

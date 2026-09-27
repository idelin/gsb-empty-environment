package desim

import (
	"bytes"
	"container/heap"
	"encoding/gob"
)

// NoData is substituted for a nil event payload during checkpointing,
// because gob cannot encode a nil interface value.
type NoData struct{}

func init() { gob.Register(NoData{}) }

type eventState struct {
	Tick uint64
	Cat  Category
	Seq  uint64
	Type string
	Data interface{}
	Slot int
	Gen  uint32
}

type schedState struct {
	Now    uint64
	Seq    uint64
	Events []eventState
	Slots  []slotState
	Free   []int
}

func (s *Scheduler) snapshot() schedState {
	st := schedState{
		Now:    s.now,
		Seq:    s.seq,
		Slots:  s.slots,
		Free:   s.free,
		Events: make([]eventState, 0, len(s.events)),
	}
	for _, ev := range s.events {
		data := ev.Data
		if data == nil {
			data = NoData{}
		}
		st.Events = append(st.Events, eventState{
			Tick: ev.Tick, Cat: ev.Cat, Seq: ev.Seq,
			Type: ev.Type, Data: data,
			Slot: ev.slot, Gen: ev.gen,
		})
	}
	return st
}

func (s *Scheduler) restore(st schedState) {
	s.now = st.Now
	s.seq = st.Seq
	s.slots = st.Slots
	s.free = st.Free
	s.events = s.events[:0]
	for _, es := range st.Events {
		data := es.Data
		if _, ok := data.(NoData); ok {
			data = nil
		}
		s.events = append(s.events, &Event{
			Tick: es.Tick, Cat: es.Cat, Seq: es.Seq,
			Type: es.Type, Data: data,
			slot: es.Slot, gen: es.Gen,
		})
	}
	// The ordering key (Tick, Cat, Seq) is a total order with unique
	// keys, so the pop sequence after Init is identical to the one of
	// the original heap.
	heap.Init(&s.events)
}

// Marshal serializes the scheduler state (clock, sequence counter,
// pending events including their cancellation slots) to bytes. Event
// payloads must be gob-encodable and registered with gob.Register, or
// nil. Handlers are functions and are not serialized; they must be
// re-registered after Unmarshal.
func (s *Scheduler) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(s.snapshot()); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Unmarshal restores scheduler state produced by Marshal. The
// registered handlers are left untouched.
func (s *Scheduler) Unmarshal(b []byte) error {
	var st schedState
	if err := gob.NewDecoder(bytes.NewReader(b)).Decode(&st); err != nil {
		return err
	}
	s.restore(st)
	return nil
}

package eventlog

import (
	"encoding/binary"
	"hash/fnv"
)

// OpKind identifies the type of a state-machine operation.
type OpKind int

const (
	// OpAdd adds Arg to the state. Commutative and associative.
	OpAdd OpKind = iota
	// OpMul multiplies the state by Arg. Commutative and associative.
	OpMul
	// OpSet overwrites the state with Arg. Not commutative: it erases
	// everything before it and is erased by everything after it.
	OpSet
	// OpObserve records a public observation point. It never merges and
	// always acts as a segment boundary.
	OpObserve
)

func (k OpKind) String() string {
	switch k {
	case OpAdd:
		return "add"
	case OpMul:
		return "mul"
	case OpSet:
		return "set"
	case OpObserve:
		return "observe"
	}
	return "unknown"
}

// Event is a single log entry. Count is used only by compressed events and
// records how many original events the entry represents; it never changes
// the state transition itself, only the applied-event bookkeeping.
type Event struct {
	Kind  OpKind
	Arg   int
	Tag   string // only meaningful for OpObserve
	Count int
}

func (e Event) count() int {
	if e.Count <= 0 {
		return 1
	}
	return e.Count
}

// Observation is a public observation point captured from the log.
type Observation struct {
	Seq   int    // index of the observe event in the original log
	Tag   string // caller-supplied label of the observe event
	Value int    // state value observed at that point
}

// Summary is a compact digest of a machine state.
type Summary struct {
	Value    int
	Applied  int // number of original non-observe events applied
	Observed int // number of observation points seen
	Hash     uint64
}

func computeHash(value, applied, observed int) uint64 {
	h := fnv.New64a()
	var buf [8]byte
	for _, v := range []int{value, applied, observed} {
		binary.LittleEndian.PutUint64(buf[:], uint64(int64(v)))
		h.Write(buf[:])
	}
	return h.Sum64()
}

// NewSummary builds a Summary with a consistent hash.
func NewSummary(value, applied, observed int) Summary {
	return Summary{
		Value:    value,
		Applied:  applied,
		Observed: observed,
		Hash:     computeHash(value, applied, observed),
	}
}

// Valid reports whether the summary's hash matches its fields.
func (s Summary) Valid() bool {
	return s.Hash == computeHash(s.Value, s.Applied, s.Observed)
}

// Machine is the deterministic state machine the log drives.
type Machine struct {
	value        int
	applied      int
	total        int
	observations []Observation
}

// NewMachine returns a machine holding the given initial value.
func NewMachine(initial int) *Machine {
	return &Machine{value: initial}
}

// Apply advances the machine by one (possibly compressed) event.
func (m *Machine) Apply(ev Event) {
	n := ev.count()
	switch ev.Kind {
	case OpAdd:
		// For a compressed add, Arg is the total of the merged run.
		m.value += ev.Arg
	case OpMul:
		// For a compressed mul, Arg is the product of the merged run.
		m.value *= ev.Arg
	case OpSet:
		m.value = ev.Arg
	case OpObserve:
		m.observations = append(m.observations, Observation{
			Seq:   m.total,
			Tag:   ev.Tag,
			Value: m.value,
		})
		m.total++
		return
	}
	m.applied += n
	m.total += n
}

// Summary digests the current machine state.
func (m *Machine) Summary() Summary {
	return NewSummary(m.value, m.applied, len(m.observations))
}

// Observations returns a copy of the observation points recorded so far.
func (m *Machine) Observations() []Observation {
	out := make([]Observation, len(m.observations))
	copy(out, m.observations)
	return out
}

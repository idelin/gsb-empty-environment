package netsec

import "fmt"

const (
	// EpochCount is the number of global epochs. Epochs wrap modulo EpochCount.
	EpochCount = 4

	// MaxThreads is the largest number of instrumented threads the model
	// accepts.
	MaxThreads = 3

	// MaxEventsPerThread is the largest accepted script length for one
	// thread (or for an updater event stream).
	MaxEventsPerThread = 8
)

// Epoch is a global epoch in the range [0, EpochCount).
type Epoch uint8

// Next returns the next epoch, wrapping after EpochCount-1.
func (e Epoch) Next() Epoch {
	return (e + 1) % EpochCount
}

// Valid reports whether e is in the modelled range.
func (e Epoch) Valid() bool {
	return e < EpochCount
}

func (e Epoch) String() string {
	return fmt.Sprintf("E%d", uint8(e))
}

// Generation distinguishes successive uses of the same object address.
type Generation uint32

func (g Generation) String() string {
	return fmt.Sprintf("g%d", uint32(g))
}

// ObjectID identifies an address (for example, a table slot or root cell).
// Reusing an ObjectID after reclamation creates a new Generation.
type ObjectID uint32

// ObjectRef is an address plus generation pair. Reclamation and dereference
// checks are always performed on the exact pair, never on ObjectID alone.
type ObjectRef struct {
	ID  ObjectID
	Gen Generation
}

func (r ObjectRef) String() string {
	return fmt.Sprintf("%d@%s", uint32(r.ID), r.Gen)
}

// ThreadID identifies a modelled thread. UpdaterThread is used when applying a
// global updater event through State.Apply.
type ThreadID int

const (
	UpdaterThread ThreadID = -1
	Thread0       ThreadID = 0
	Thread1       ThreadID = 1
	Thread2       ThreadID = 2
)

// ObjectStatus is the lifecycle state of one ObjectRef.
type ObjectStatus uint8

const (
	// ObjectLive means the generation is allocated and has not been retired.
	ObjectLive ObjectStatus = iota + 1
	// ObjectRetired means Retire was called, but the reclamation grace
	// period has not completed.
	ObjectRetired
	// ObjectReclaimed means the generation is no longer safe to dereference.
	ObjectReclaimed
)

func (s ObjectStatus) String() string {
	switch s {
	case ObjectLive:
		return "live"
	case ObjectRetired:
		return "retired"
	case ObjectReclaimed:
		return "reclaimed"
	default:
		return fmt.Sprintf("object-status(%d)", uint8(s))
	}
}

package streamagg

// OpKind identifies the kind of a recorded operation.
type OpKind string

const (
	// OpEvent ingests an event into a partition.
	OpEvent OpKind = "event"
	// OpIdle marks a partition idle (pause).
	OpIdle OpKind = "idle"
	// OpActive resumes a partition.
	OpActive OpKind = "active"
)

// Op is one recorded operation against an aggregator, used to replay an
// event history from scratch or from a checkpoint.
type Op struct {
	Kind      OpKind
	Partition string
	Timestamp int64
	Value     float64
}

// Apply applies a single recorded operation to the aggregator.
func (a *Aggregator) Apply(op Op) {
	switch op.Kind {
	case OpEvent:
		a.OnEvent(Event{Partition: op.Partition, Timestamp: op.Timestamp, Value: op.Value})
	case OpIdle:
		a.MarkIdle(op.Partition)
	case OpActive:
		a.MarkActive(op.Partition)
	}
}

// Replay applies the full operation log to a fresh aggregator.
func Replay(ops []Op) *Aggregator {
	return ReplayFrom(Checkpoint{}, ops)
}

// ReplayFrom restores an aggregator from a checkpoint and replays the
// remaining operations on top of it.
func ReplayFrom(cp Checkpoint, ops []Op) *Aggregator {
	a := Restore(cp)
	for _, op := range ops {
		a.Apply(op)
	}
	return a
}

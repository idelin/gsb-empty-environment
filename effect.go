package replay

import "strconv"

// EffectSpec is the reducer's declaration of an external side effect.
// It carries no identity; the executor assigns the effect ID.
type EffectSpec struct {
	Kind   string
	Detail string
}

// Effect is a dispatchable description of an external side effect.
// It is only a description: replay regenerates it, but confirmed
// effects are never executed again.
type Effect struct {
	ID      string
	EventID string
	Seq     int
	Kind    string
	Detail  string
}

// EffectID derives a deterministic effect ID from the event ID and
// the effect's sequence number within that event. Because the ID is
// stable across retries and replays, a lost receipt can be recovered
// by resending the same ID.
func EffectID(eventID string, seq int) string {
	return eventID + "#" + strconv.Itoa(seq)
}

func newEffect(eventID string, seq int, spec EffectSpec) Effect {
	return Effect{
		ID:      EffectID(eventID, seq),
		EventID: eventID,
		Seq:     seq,
		Kind:    spec.Kind,
		Detail:  spec.Detail,
	}
}

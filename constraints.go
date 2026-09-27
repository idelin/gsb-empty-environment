package clocksync

// Constraint is a difference constraint on clock offsets:
//
//	theta[To] - theta[From] <= Bound
//
// derived from one message: recv - send = offset delta + network delay,
// with delay >= 0. Bound may be negative; it is never clamped to zero,
// because a negative bound is exactly what exposes inconsistent telemetry.
type Constraint struct {
	From, To string
	Bound    float64
	Msg      string
}

// DeriveConstraints converts each message into one difference constraint.
func DeriveConstraints(msgs []Message) []Constraint {
	cons := make([]Constraint, 0, len(msgs))
	for _, m := range msgs {
		cons = append(cons, Constraint{
			From:  m.From,
			To:    m.To,
			Bound: m.RecvTS - m.SendTS,
			Msg:   m.ID,
		})
	}
	return cons
}

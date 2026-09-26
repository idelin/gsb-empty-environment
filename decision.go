package tailsampler

// Decision is the outcome of a sampling evaluation for one trace.
type Decision struct {
	Keep   bool
	Reason string
}

// DecisionPending is returned when a span is buffered and its trace is not
// decided yet.
var DecisionPending = Decision{Keep: false, Reason: "pending"}

func keep(reason string) Decision { return Decision{Keep: true, Reason: reason} }

func drop(reason string) Decision { return Decision{Keep: false, Reason: reason} }

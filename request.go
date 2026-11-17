package netsec

import (
	"fmt"
	"sync"
	"sync/atomic"
)

var requestIDs atomic.Uint64

// Request is one immutable set of secret source values. An authorization is
// bound to the identity of the Request for which it was issued. Request also
// serializes executions on the same request, so a request-level transaction
// cannot interleave with another execution of the same request.
type Request struct {
	id     uint64
	inputs map[Source]int

	execMu sync.Mutex
}

// NewRequest copies inputs into a fresh request. Source identifiers must be
// in [0, MaxSources); duplicate keys are impossible in a Go map.
func NewRequest(inputs map[Source]int) (*Request, error) {
	copied := make(map[Source]int, len(inputs))
	for source, value := range inputs {
		if !source.Valid() {
			return nil, fmt.Errorf("%w: %d is outside [0,%d)", ErrInvalidSource, source, MaxSources)
		}
		copied[source] = value
	}
	return &Request{
		id:     requestIDs.Add(1),
		inputs: copied,
	}, nil
}

// ID returns a process-local identifier used in audit events.
func (r *Request) ID() uint64 {
	if r == nil {
		return 0
	}
	return r.id
}

func (r *Request) input(source Source) (int, bool) {
	value, ok := r.inputs[source]
	return value, ok
}

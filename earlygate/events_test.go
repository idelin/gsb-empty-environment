package earlygate_test

import (
	"errors"
	"sync"
	"testing"

	"example.org/netsec/q0165/earlygate"
)

type testOp struct {
	name string
	kind string
}

var testTable = map[string]earlygate.Classification{
	"read":    {ReadOnly: true, Replayable: true, Identity: earlygate.IdentityNone},
	"write":   {ReadOnly: false, Replayable: false, Identity: earlygate.IdentityNone},
	"authget": {ReadOnly: true, Replayable: true, Identity: earlygate.IdentityHandshake},
	"idempot": {ReadOnly: false, Replayable: true, Identity: earlygate.IdentityNone},
}

func staticClassifier() earlygate.Classifier[testOp] {
	return earlygate.ClassifierFunc[testOp](func(op testOp) (earlygate.Classification, error) {
		c, ok := testTable[op.kind]
		if !ok {
			return earlygate.Classification{}, errors.New("unknown operation kind: " + op.kind)
		}
		return c, nil
	})
}

func classifierFor(kind string, c earlygate.Classification) earlygate.Classifier[testOp] {
	base := staticClassifier()
	return earlygate.ClassifierFunc[testOp](func(op testOp) (earlygate.Classification, error) {
		if op.kind == kind {
			return c, nil
		}
		return base.Classify(op)
	})
}

type recorder struct {
	mu        sync.Mutex
	decisions []earlygate.Decision[testOp]
}

func (r *recorder) handle(d []earlygate.Decision[testOp]) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.decisions = append(r.decisions, d...)
}

func (r *recorder) snapshot() []earlygate.Decision[testOp] {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]earlygate.Decision[testOp](nil), r.decisions...)
}

func refs(decisions []earlygate.Decision[testOp]) map[uint64]earlygate.Decision[testOp] {
	out := make(map[uint64]earlygate.Decision[testOp], len(decisions))
	for _, d := range decisions {
		switch v := d.(type) {
		case earlygate.ExecuteEarly[testOp]:
			out[v.RequestID] = v
		case earlygate.HoldEarly[testOp]:
			out[v.RequestID] = v
		case earlygate.DispatchNormal[testOp]:
			out[v.RequestID] = v
		case earlygate.RejectRequest[testOp]:
			out[v.RequestID] = v
		case earlygate.CancelAck[testOp]:
			out[v.RequestID] = v
		}
	}
	return out
}

func earlyIDs(t *testing.T, decisions []earlygate.Decision[testOp]) map[uint64]bool {
	t.Helper()
	out := map[uint64]bool{}
	for _, d := range decisions {
		if v, ok := d.(earlygate.ExecuteEarly[testOp]); ok {
			out[v.RequestID] = true
		}
	}
	return out
}

func normalIDs(t *testing.T, decisions []earlygate.Decision[testOp]) map[uint64]bool {
	t.Helper()
	out := map[uint64]bool{}
	for _, d := range decisions {
		if v, ok := d.(earlygate.DispatchNormal[testOp]); ok {
			out[v.RequestID] = true
		}
	}
	return out
}

func rejectedIDs(t *testing.T, decisions []earlygate.Decision[testOp]) map[uint64]bool {
	t.Helper()
	out := map[uint64]bool{}
	for _, d := range decisions {
		if v, ok := d.(earlygate.RejectRequest[testOp]); ok {
			out[v.RequestID] = true
		}
	}
	return out
}

func newTestGate(t *testing.T) (*earlygate.Gate[testOp], *recorder) {
	t.Helper()
	rec := &recorder{}
	gate, err := earlygate.New[testOp](staticClassifier(), earlygate.WithDecisionHandler(rec.handle))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return gate, rec
}

package netsec_test

import (
	"errors"
	"testing"

	netsec "example.org/netsec/q0077"
)

func TestImportableAPI(t *testing.T) {
	m := netsec.NewStateMachine(netsec.WithEarlyDataAccepted())
	if err := m.Feed(netsec.Finished); !errors.Is(err, netsec.ErrMissingEndOfEarlyData) {
		t.Fatalf("missing boundary = %v", err)
	}

	m = netsec.NewStateMachine(netsec.WithEarlyDataAccepted())
	if err := m.FeedAll(netsec.EndOfEarlyData, netsec.Finished); err != nil {
		t.Fatalf("complete early-data sequence = %v", err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate = %v", err)
	}
}

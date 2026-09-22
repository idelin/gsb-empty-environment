package sim

import (
	"reflect"
	"testing"

	"mvreg/mvreg"
)

func readValues(s *Simulator[string], replica string) []string {
	items := s.Read(replica)
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = item.Value
	}
	return out
}

func TestControlledCommunicationKeepsConcurrentValues(t *testing.T) {
	s := New[string]([]string{"A", "B"}, 2)
	log := s.Run([]Event[string]{
		{Time: 1, Kind: Write, Target: "A", Value: "red"},
		{Time: 2, Kind: Write, Target: "B", Value: "blue"},
		{Time: 3, Kind: Send, Target: "A", To: "B"},
	})

	if got := readValues(s, "B"); !reflect.DeepEqual(got, []string{"red", "blue"}) {
		t.Fatalf("B values = %v, want both concurrent values", got)
	}
	if len(log) != 4 || log[3].Time != 5 {
		t.Fatalf("unexpected delayed delivery log: %+v", log)
	}
}

func TestFailureDefersDeliveryAndRecoveryAllowsDelivery(t *testing.T) {
	s := New[string]([]string{"A", "B"}, 1)
	s.Run([]Event[string]{
		{Time: 1, Kind: Write, Target: "A", Value: "red"},
		{Time: 2, Kind: Fail, Target: "B"},
		{Time: 3, Kind: Send, Target: "A", To: "B"},
		{Time: 4, Kind: Write, Target: "B", Value: "blue"},
		{Time: 7, Kind: Recover, Target: "B"},
	})

	if got := readValues(s, "B"); !reflect.DeepEqual(got, []string{"red"}) {
		t.Fatalf("B values = %v, want deferred message delivered after recovery only", got)
	}
}

func TestWallClockDoesNotDominateConcurrentValue(t *testing.T) {
	s := New[string]([]string{"A", "B"}, 10)
	s.Run([]Event[string]{
		{Time: 1, Kind: Write, Target: "A", Value: "old"},
		{Time: 1000, Kind: Write, Target: "B", Value: "new wall time, no causality"},
		{Time: 1001, Kind: Send, Target: "B", To: "A"},
	})

	if got := readValues(s, "A"); !reflect.DeepEqual(got, []string{"old", "new wall time, no causality"}) {
		t.Fatalf("A values = %v, wall clock must not overwrite a concurrent value", got)
	}
}

func TestSimulatedExplicitResolvePropagates(t *testing.T) {
	s := New[string]([]string{"A", "B", "C"}, 1)
	s.Run([]Event[string]{
		{Time: 1, Kind: Write, Target: "A", Value: "red"},
		{Time: 2, Kind: Write, Target: "B", Value: "blue"},
		{Time: 3, Kind: Send, Target: "A", To: "B"},
		{Time: 5, Kind: Send, Target: "B", To: "A"},
		{Time: 7, Kind: Resolve, Target: "A", Value: "purple"},
		{Time: 8, Kind: Send, Target: "A", To: "C"},
		{Time: 9, Kind: Send, Target: "B", To: "C"},
	})

	if got := readValues(s, "C"); !reflect.DeepEqual(got, []string{"purple"}) {
		t.Fatalf("C values = %v, want propagated resolve", got)
	}
	item := s.Read("C")[0]
	if item.Origin != "A" || item.Vector["A"] != 2 || item.Vector["B"] != 1 {
		t.Fatalf("resolved item = %+v, want merge version A:2,B:1", item)
	}
}

func TestSnapshotAtSendTimeDelaysFutureWrites(t *testing.T) {
	s := New[string]([]string{"A", "B"}, 10)
	s.Run([]Event[string]{
		{Time: 1, Kind: Write, Target: "A", Value: "first"},
		{Time: 2, Kind: Send, Target: "A", To: "B"},
		{Time: 3, Kind: Write, Target: "A", Value: "later, not in snapshot"},
	})

	if got := readValues(s, "B"); !reflect.DeepEqual(got, []string{"first"}) {
		t.Fatalf("B values = %v, want only send-time snapshot", got)
	}
	if got := readValues(s, "A"); !reflect.DeepEqual(got, []string{"later, not in snapshot"}) {
		t.Fatalf("A values = %v, want local successor", got)
	}
}

func TestEventLog(t *testing.T) {
	s := New[string]([]string{"A"}, 1)
	log := s.Run([]Event[string]{
		{Time: 2, Kind: Fail, Target: "A"},
		{Time: 3, Kind: Write, Target: "A", Value: "x"},
		{Time: 4, Kind: Recover, Target: "A"},
		{Time: 5, Kind: Write, Target: "A", Value: "y"},
	})
	want := []LogEntry{
		{Time: 2, Replica: "A", Message: "failed"},
		{Time: 3, Replica: "A", Message: "write rejected: replica down"},
		{Time: 4, Replica: "A", Message: "recovered"},
		{Time: 5, Replica: "A", Message: "write y at {A:1}"},
	}
	if !reflect.DeepEqual(log, want) {
		t.Fatalf("log = %#v, want %#v", log, want)
	}
}

func TestUnknownReplicaPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for unknown replica")
		}
	}()
	New[string]([]string{"A"}, 1).Run([]Event[string]{{Kind: Write, Target: "Z"}})
}

var _ = mvreg.VersionVector{}

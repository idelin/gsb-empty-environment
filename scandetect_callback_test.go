package netsec

import (
	"testing"
	"time"
)

func TestChangeCallbackAndHistory(t *testing.T) {
	var fired []Change
	d := NewDetector(WithChangeCallback(func(c Change) {
		fired = append(fired, c)
	}))
	id := mustRegister(t, d, Query{Window: time.Minute, PortThreshold: 1, HostThreshold: 100, At: t0.Add(time.Minute)})

	f1 := failAt(1, "10.0.0.1", 9000, "192.0.2.1", 22)
	d.Ingest(f1)

	if len(fired) != 1 || fired[0].Kind != ChangeRaised || fired[0].QueryID != id {
		t.Fatalf("callback = %+v, want one raise", fired)
	}
	if fired[0].Alert.Kind != AlertHostPortSweep || fired[0].Alert.Count != 1 {
		t.Fatalf("callback alert = %+v", fired[0].Alert)
	}

	d.Ingest(successOf(f1))
	if len(fired) != 2 || fired[1].Kind != ChangeWithdrawn {
		t.Fatalf("callback = %+v, want withdrawal second", fired)
	}
	if fired[1].Seq <= fired[0].Seq {
		t.Fatal("change sequence must be monotonic")
	}

	history := d.Changes()
	if len(history) != 2 || history[0].Kind != ChangeRaised || history[1].Kind != ChangeWithdrawn {
		t.Fatalf("history = %+v, want raise then withdrawal", history)
	}

	// Changes() returns an independent copy.
	history[0].Seq = 999
	if d.Changes()[0].Seq == 999 {
		t.Fatal("Changes() must return a defensive copy")
	}
}

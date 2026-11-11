package evidence

import "testing"

func TestOversizedWindowEvictsAndReportsLoss(t *testing.T) {
	// A single candidate whose window dwarfs the budget cannot be retained.
	// The policy evicts it and reports the lost range explicitly rather than
	// truncating the window without telling the host.
	b := NewBuffer(5)
	if err := b.Notify(Candidate{EventID: 1, Match: Range{Start: 0, End: 20}}); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, b, 0, mkBytes(0, 20))

	records := b.Evictions()
	if len(records) != 1 || records[0].EventID != 1 {
		t.Fatalf("evictions = %+v", records)
	}
	if len(records[0].Missing) != 1 ||
		records[0].Missing[0].Range != (Range{Start: 0, End: 20}) ||
		records[0].Missing[0].Reason != ReasonEvicted {
		t.Fatalf("missing record = %+v", records[0].Missing)
	}

	ev, err := b.Collect(1)
	if err != nil {
		t.Fatal(err)
	}
	if !ev.Evicted {
		t.Fatal("expected evicted evidence")
	}
	assertPartition(t, ev)

	// Budget is restored purely through eviction plus cache reclaim, so no
	// overflow debt remains under normal operation.
	if got := b.Stats().OverflowBytes; got != 0 {
		t.Fatalf("overflow = %d, want 0", got)
	}
}

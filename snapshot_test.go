package snapshot

import (
	"testing"
	"time"
)

// runSnapshotOnce starts a 3-node cluster performing concurrent transfers,
// lets node A initiate a snapshot mid-flight, and returns the results.
func runSnapshotOnce(t *testing.T, snapID int) (map[string]SnapshotResult, int) {
	t.Helper()

	balances := map[string]int{"A": 100, "B": 200, "C": 300}
	total := 0
	for _, b := range balances {
		total += b
	}

	collector := make(chan SnapshotResult, len(balances))
	procs := NewCluster(balances, collector)
	for _, p := range procs {
		p.Start()
	}
	defer func() {
		for _, p := range procs {
			p.Stop()
		}
	}()

	// Let concurrent transfers flow before and during the snapshot.
	time.Sleep(150 * time.Millisecond)
	procs[0].InitiateSnapshot(snapID) // node A initiates

	results := make(map[string]SnapshotResult, len(procs))
	timeout := time.After(5 * time.Second)
	for len(results) < len(procs) {
		select {
		case r := <-collector:
			results[r.ProcessID] = r
		case <-timeout:
			t.Fatalf("timed out waiting for snapshot results, got %d/%d", len(results), len(procs))
		}
	}
	return results, total
}

// TestSnapshotConservesTotal verifies that local states plus in-flight
// channel messages sum exactly to the cluster's total money, i.e. the
// snapshot is a consistent cut taken without pausing the workload.
func TestSnapshotConservesTotal(t *testing.T) {
	for round := 0; round < 10; round++ {
		results, total := runSnapshotOnce(t, round+1)

		sum := 0
		for _, r := range results {
			sum += r.Balance + r.InFlightTotal()
		}
		if sum != total {
			t.Fatalf("round %d: money not conserved: snapshot total %d, want %d", round, sum, total)
		}
		t.Logf("round %d: total=%d conserved (A=%d+%d, B=%d+%d, C=%d+%d in-flight)",
			round, total,
			results["A"].Balance, results["A"].InFlightTotal(),
			results["B"].Balance, results["B"].InFlightTotal(),
			results["C"].Balance, results["C"].InFlightTotal())
	}
}

// TestSnapshotRecordsInFlightMessages ensures the snapshot actually observes
// in-flight channel state while transfers are ongoing (non-trivial cut).
func TestSnapshotRecordsInFlightMessages(t *testing.T) {
	results, _ := runSnapshotOnce(t, 42)

	recorded := 0
	for _, r := range results {
		for _, msgs := range r.InFlight {
			recorded += len(msgs)
		}
	}
	if recorded == 0 {
		t.Fatal("expected some in-flight transfers to be recorded, got none")
	}
	t.Logf("recorded %d in-flight transfers across channels", recorded)
}

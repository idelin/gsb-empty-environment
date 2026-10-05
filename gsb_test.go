package gsb

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

func ids(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("n%d", i)
	}
	return out
}

func seedRecords(sim *Sim, perNode int) {
	for _, id := range sim.IDs() {
		e := sim.Engines[id]
		for i := 0; i < perNode; i++ {
			e.Table.Apply(Record{
				NodeID:      fmt.Sprintf("m-%d", sim.Rnd.Intn(12)),
				Incarnation: uint64(sim.Rnd.Intn(4)),
				State:       State(sim.Rnd.Intn(3)),
			})
		}
	}
}

func runUntilConverged(t *testing.T, sim *Sim, maxSweeps int) int {
	t.Helper()
	for sweep := 1; sweep <= maxSweeps; sweep++ {
		if err := sim.Sweep(context.Background(), nil); err != nil {
			t.Fatalf("sweep %d: %v", sweep, err)
		}
		if _, ok := sim.Converged(); ok {
			return sweep
		}
	}
	msg, _ := sim.Converged()
	t.Fatalf("not converged after %d sweeps: %s", maxSweeps, msg)
	return -1
}

func totalRestarts(sim *Sim) int {
	sum := 0
	for _, id := range sim.IDs() {
		_, _, _, r, _ := sim.Engines[id].Stats.Totals()
		sum += r
	}
	return sum
}

func totalRejected(sim *Sim) int {
	sum := 0
	for _, id := range sim.IDs() {
		_, _, _, _, r := sim.Engines[id].Stats.Totals()
		sum += r
	}
	return sum
}

// --- merge lattice ---

func TestMergeLattice(t *testing.T) {
	a := Record{"n", 1, Alive}
	s := Record{"n", 1, Suspect}
	d := Record{"n", 1, Dead}
	if !(a.Compare(s) < 0 && s.Compare(d) < 0) {
		t.Fatal("want alive < suspect < dead at equal incarnation")
	}
	if hi := (Record{"n", 2, Alive}); hi.Compare(d) <= 0 {
		t.Fatal("higher incarnation must dominate any state")
	}
	if Merge(Merge(a, s), d) != Merge(a, Merge(s, d)) {
		t.Fatal("merge not associative")
	}
	if Merge(s, a) != Merge(a, s) {
		t.Fatal("merge not commutative")
	}
	if Merge(a, a) != a {
		t.Fatal("merge not idempotent")
	}
}

// --- digest binding ---

func TestDigestBindsBoundaryCountContent(t *testing.T) {
	recs := []Record{{"a", 1, Alive}, {"b", 2, Suspect}}
	h := HashRecords(0, 4, recs)
	if h == HashRecords(1, 4, recs) {
		t.Error("shard index not bound")
	}
	if h == HashRecords(0, 8, recs) {
		t.Error("shard boundary (numShards) not bound")
	}
	if h == HashRecords(0, 4, recs[:1]) {
		t.Error("record count not bound")
	}
	if h == HashRecords(0, 4, []Record{{"a", 1, Alive}, {"b", 3, Suspect}}) {
		t.Error("incarnation not bound")
	}
	if h == HashRecords(0, 4, []Record{{"a", 1, Alive}, {"b", 2, Dead}}) {
		t.Error("state not bound")
	}
}

// --- stale data safety ---

func TestStaleRecordCannotOverwrite(t *testing.T) {
	tab := NewTable(4)
	tab.Apply(Record{"n", 5, Alive})
	if changed := tab.Apply(Record{"n", 2, Dead}); changed != 0 {
		t.Fatal("stale incarnation overwrote a newer record")
	}
	if changed := tab.Apply(Record{"n", 5, Alive}); changed != 0 {
		t.Fatal("same-version lower state overwrote")
	}
	if changed := tab.Apply(Record{"n", 5, Suspect}); changed != 1 {
		t.Fatal("same-version higher state should advance")
	}
	r, _ := tab.Get("n")
	if r.Incarnation != 5 || r.State != Suspect {
		t.Fatalf("got %+v", r)
	}
}

// --- snapshot / cursor protocol ---

func TestStaleSnapshotRestartsNotSkips(t *testing.T) {
	eng := NewEngine("s", NewTable(1), nil)
	eng.Table.Apply(Record{"a", 1, Alive}, Record{"b", 1, Alive})
	d := eng.Table.Digest(0)

	req := RangeRequest{Shard: 0, After: "", Snapshot: d.Version, MaxBytes: 4096}
	if resp := eng.ServeRange(req); resp.Restart {
		t.Fatal("fresh cursor must not restart")
	}
	// Local update during the pull invalidates the snapshot.
	eng.Table.Apply(Record{"c", 1, Alive})
	resp := eng.ServeRange(req)
	if !resp.Restart {
		t.Fatal("stale snapshot must restart, not skip records")
	}
	// After re-binding to the new snapshot, all records are served.
	req.Snapshot = resp.Snapshot
	resp = eng.ServeRange(req)
	if resp.Restart || !resp.Done || len(resp.Records) != 3 {
		t.Fatalf("want full 3-record page, got %+v", resp)
	}
}

func TestByteBudgetPaginatesAndProgresses(t *testing.T) {
	eng := NewEngine("s", NewTable(1), nil)
	for i := 0; i < 30; i++ {
		eng.Table.Apply(Record{fmt.Sprintf("m-%02d", i), 1, Alive})
	}
	const budget = 200
	version := eng.Table.Digest(0).Version
	after := ""
	var got []Record
	var last RangeResponse
	for pages := 0; ; pages++ {
		if pages > 100 {
			t.Fatal("no progress: too many pages")
		}
		resp := eng.ServeRange(RangeRequest{Shard: 0, After: after, Snapshot: version, MaxBytes: budget})
		if resp.Restart {
			t.Fatal("unexpected restart on a stable snapshot")
		}
		if len(resp.Records) == 0 && !resp.Done {
			t.Fatal("empty non-final page: cursor would not progress")
		}
		if n := sizeOf(resp); n > budget && len(resp.Records) > 1 {
			t.Fatalf("page of %d records exceeds budget: %d > %d", len(resp.Records), n, budget)
		}
		got = append(got, resp.Records...)
		if len(resp.Records) > 0 {
			after = resp.Records[len(resp.Records)-1].NodeID
		}
		last = resp
		if resp.Done {
			break
		}
	}
	if len(got) != 30 {
		t.Fatalf("got %d records, want 30", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].NodeID >= got[i].NodeID {
			t.Fatal("pages not in ascending order")
		}
	}
	if !verifyShard(0, 1, last.Final, got) {
		t.Fatal("reassembled content fails digest verification")
	}
}

// --- convergence scenarios ---

func TestCleanConvergence(t *testing.T) {
	sim := NewSim(ids(5), 8, 42)
	seedRecords(sim, 4)
	runUntilConverged(t, sim, 10)

	// Post-convergence sweep: digests only, zero records pulled.
	pulled := 0
	if err := sim.Sweep(context.Background(), func(from string, rs RoundStat) {
		pulled += rs.RecordsPulled
	}); err != nil {
		t.Fatal(err)
	}
	if pulled != 0 {
		t.Fatalf("converged cluster pulled %d records", pulled)
	}
}

func TestIncompleteForgedDuplicateAndDrops(t *testing.T) {
	// One shard, so every fault deterministically hits a non-empty page.
	sim := NewSim(ids(5), 1, 7)
	seedRecords(sim, 4)
	a, b := "n0", "n1"
	sim.Links[a][b].Fault = Chain(
		ForgeDigests(2),
		TruncateRanges(3),
		ReplayRanges(2),
		DropMessages(2),
	)
	sim.Links[b][a].Fault = Chain(TruncateRanges(2), DropMessages(1))
	// Run a fixed number of sweeps: convergence via other links must not
	// let the faulted link off the hook before its faults fire.
	for sweep := 0; sweep < 8; sweep++ {
		if err := sim.Sweep(context.Background(), nil); err != nil {
			t.Fatalf("sweep %d: %v", sweep, err)
		}
	}
	if msg, ok := sim.Converged(); !ok {
		t.Fatalf("not converged: %s", msg)
	}
	if totalRejected(sim) == 0 {
		t.Fatal("faults were not detected: want rejected messages")
	}
	if totalRestarts(sim) == 0 {
		t.Fatal("incomplete transfers should trigger restarts")
	}
}

func TestReshardDuringSync(t *testing.T) {
	sim := NewSim(ids(4), 4, 9)
	seedRecords(sim, 4)
	if err := sim.Sweep(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	// Two nodes change sharding configuration mid-synchronization.
	sim.Engines["n0"].Table.SetNumShards(16)
	sim.Engines["n1"].Table.SetNumShards(16)
	runUntilConverged(t, sim, 40)
}

func TestTransportCancelAndResume(t *testing.T) {
	sim := NewSim(ids(5), 8, 11)
	seedRecords(sim, 5)

	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	err := sim.Sweep(ctx, func(from string, rs RoundStat) { once.Do(cancel) })
	if err == nil {
		t.Fatal("want the sweep to be interrupted by cancellation")
	}

	// Reconnect and finish. Data is stable, so resumed cursors must not
	// force any snapshot restart.
	runUntilConverged(t, sim, 40)
	if r := totalRestarts(sim); r != 0 {
		t.Fatalf("stable data: resume should not restart snapshots, got %d", r)
	}
}

func TestTrustCannotBlockRecovery(t *testing.T) {
	sim := NewSim(ids(4), 4, 3)
	seedRecords(sim, 3)
	a := sim.Engines["n0"]
	for i := 0; i < 50; i++ {
		a.Trust.Penalize("n1")
	}
	if got := a.Trust.Score("n1"); got < TrustFloor {
		t.Fatalf("score %v below floor %v", got, TrustFloor)
	}
	// The distrusted node announces a legitimate new incarnation.
	sim.Engines["n1"].Table.Apply(Record{"fresh", 99, Alive})
	runUntilConverged(t, sim, 40)
	r, ok := a.Table.Get("fresh")
	if !ok || r.Incarnation != 99 {
		t.Fatalf("distrusted peer's incarnation never recovered: %+v ok=%v", r, ok)
	}
}

func TestTrustSchedulingStaysFair(t *testing.T) {
	tr := NewTrust()
	for i := 0; i < 50; i++ {
		tr.Penalize("low")
	}
	rnd := rand.New(rand.NewSource(1))
	first := 0
	for i := 0; i < 2000; i++ {
		ord := tr.Order([]string{"low", "a", "b", "c"}, rnd)
		if len(ord) != 4 {
			t.Fatal("peer dropped from schedule")
		}
		if ord[0] == "low" {
			first++
		}
	}
	if first == 0 {
		t.Fatal("floored peer never scheduled first: starvation")
	}
}

// TestTerminationUnderFairPolling is the executable termination argument:
// with finite stable data, bounded faults and fair polling, the cluster
// converges within finitely many sweeps and then goes quiet (digests only).
func TestTerminationUnderFairPolling(t *testing.T) {
	sim := NewSim(ids(6), 8, 123)
	seedRecords(sim, 5)
	// Bounded faults: a handful of drops on one link, then the link heals.
	sim.Links["n0"]["n3"].Fault = DropMessages(6)

	sweeps := runUntilConverged(t, sim, 40)
	t.Logf("converged after %d sweeps", sweeps)

	// Quiescence: a further sweep transfers no records, only digests.
	var bytes, pulled int
	if err := sim.Sweep(context.Background(), func(from string, rs RoundStat) {
		bytes += rs.Bytes()
		pulled += rs.RecordsPulled
	}); err != nil {
		t.Fatal(err)
	}
	if pulled != 0 {
		t.Fatalf("post-convergence traffic: %d records", pulled)
	}
	if bytes == 0 {
		t.Fatal("digest exchange should still cost bytes")
	}
	for _, id := range sim.IDs() {
		rounds, b, p, r, rej := sim.Engines[id].Stats.Totals()
		t.Logf("%s: rounds=%d bytes=%d pulled=%d restarts=%d rejected=%d", id, rounds, b, p, r, rej)
	}
}

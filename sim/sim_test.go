package sim_test

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"antientropy/member"
	"antientropy/sim"
	"antientropy/syncx"
)

func rec(id string, inc uint64, st member.State) member.Record {
	return member.Record{NodeID: id, Incarnation: inc, State: st}
}

func ids(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("node-%d", i)
	}
	return out
}

func runUntilConverged(t *testing.T, c *sim.Cluster, maxRounds int) []sim.RoundReport {
	t.Helper()
	var reports []sim.RoundReport
	for i := 0; i < maxRounds; i++ {
		rep := c.RunRound(context.Background())
		reports = append(reports, rep)
		if c.Converged() {
			return reports
		}
	}
	t.Fatalf("not converged after %d rounds", maxRounds)
	return nil
}

func seedRecords(c *sim.Cluster, n int, rng *rand.Rand) {
	for i := 0; i < n; i++ {
		node := c.Order[rng.Intn(len(c.Order))]
		c.Introduce(node, rec(fmt.Sprintf("svc-%02d", rng.Intn(n/2+1)),
			uint64(rng.Intn(5)+1), member.State(rng.Intn(3))))
	}
}

// Baseline: gossip converges and matches the independent full-merge model;
// after convergence a round moves zero records (digest-only steady state).
func TestBasicConvergence(t *testing.T) {
	c := sim.NewCluster(ids(5), 8, 128, 0)
	seedRecords(c, 40, rand.New(rand.NewSource(42)))
	runUntilConverged(t, c, 100)
	if err := c.CheckModel(); err != nil {
		t.Fatal(err)
	}
	rep := c.RunRound(context.Background())
	if rep.RecordsPulled != 0 {
		t.Fatalf("post-convergence round pulled %d records", rep.RecordsPulled)
	}
	if c.LimitViolations != 0 {
		t.Fatalf("%d byte-limit violations", c.LimitViolations)
	}
}

// Tiny response budgets force multi-page pulls; cursors must advance
// monotonically per shard and byte limits must hold.
func TestPaginationAndByteLimits(t *testing.T) {
	c := sim.NewCluster(ids(3), 4, 56, 0)
	for i := 0; i < 12; i++ {
		c.Introduce(c.Order[0], rec(fmt.Sprintf("svc-%02d", i), 1, member.Alive))
	}
	reports := runUntilConverged(t, c, 300)
	pages := 0
	for _, r := range reports {
		for _, st := range r.Stats {
			pages += st.Pages
			prev := map[int]string{}
			for _, m := range st.CursorProgress {
				if p, ok := prev[m.Shard]; ok && m.From < p {
					t.Fatalf("cursor moved backwards on shard %d: %+v", m.Shard, m)
				}
				if !m.Done && m.To <= m.From {
					t.Fatalf("cursor not advancing: %+v", m)
				}
				prev[m.Shard] = m.To
			}
		}
	}
	if pages <= 12 {
		t.Fatalf("expected multi-page pulls, got %d pages", pages)
	}
	if c.LimitViolations != 0 {
		t.Fatalf("%d byte-limit violations", c.LimitViolations)
	}
	if err := c.CheckModel(); err != nil {
		t.Fatal(err)
	}
}

// Incomplete/corrupted responses are detected via the page checksum; the
// round fails cleanly and later rounds recover.
func TestCorruptResponsesDetected(t *testing.T) {
	c := sim.NewCluster(ids(4), 8, 128, 0)
	for i := 0; i < 20; i++ {
		c.Introduce(c.Order[0], rec(fmt.Sprintf("svc-%02d", i), 1, member.Alive))
	}
	c.Faults.CorruptPage = func(round int, from, to string) bool {
		return round <= 5 && (round+len(from)+len(to))%2 != 0
	}
	reports := runUntilConverged(t, c, 100)
	sawErr := false
	for _, r := range reports {
		for _, e := range r.Errors {
			if strings.Contains(e, "corrupt") {
				sawErr = true
			}
		}
	}
	if !sawErr {
		t.Fatal("corruption was never detected")
	}
	if err := c.CheckModel(); err != nil {
		t.Fatal(err)
	}
}

// Forged digests that hide divergence (advertised digest rewritten to the
// requester's local digest) are detected by post-pull verification, the
// peer's trust is demoted, and forced full sync still converges.
func TestForgedDigests(t *testing.T) {
	c := sim.NewCluster(ids(4), 8, 256, 2) // forced full sync every 2nd round
	for i := 0; i < 16; i++ {
		c.Introduce(c.Order[1], rec(fmt.Sprintf("svc-%02d", i), 1, member.Alive))
	}
	forged := 0
	c.Faults.ForgeDigest = func(round int, from, to string, d *syncx.Digest) {
		// "hide divergence": make the advertised digest equal to the
		// requester's local digest so the shard looks in-sync.
		local := c.Nodes[from].Srv.DigestsFor(d.NumShards)
		if d.Shard < len(local) && local[d.Shard] != *d {
			*d = local[d.Shard]
			forged++
		}
	}
	reports := runUntilConverged(t, c, 100)
	if forged == 0 {
		t.Fatal("fault never fired")
	}
	detected := 0
	for _, r := range reports {
		detected += r.Forged
	}
	if detected == 0 {
		t.Fatal("forgery was never detected by post-pull verification")
	}
	if err := c.CheckModel(); err != nil {
		t.Fatal(err)
	}
}

// Duplicated/replayed pages are idempotently absorbed and cannot move the
// cursor backwards.
func TestDuplicatePages(t *testing.T) {
	c := sim.NewCluster(ids(3), 4, 96, 0)
	for i := 0; i < 15; i++ {
		c.Introduce(c.Order[2], rec(fmt.Sprintf("svc-%02d", i), 1, member.Alive))
	}
	fired := map[string]bool{}
	c.Faults.DuplicatePage = func(round int, from, to string) bool {
		// replay at most one stale page per pair per round, so the
		// cursor guard absorbs it and the pull still completes
		key := fmt.Sprintf("%d|%s|%s", round, from, to)
		if round%2 == 0 && !fired[key] {
			fired[key] = true
			return true
		}
		return false
	}
	runUntilConverged(t, c, 200)
	if len(fired) == 0 {
		t.Fatal("fault never fired")
	}
	if err := c.CheckModel(); err != nil {
		t.Fatal(err)
	}
}

// A rolling shard-config change invalidates snapshots mid-round: pulls get
// StatusRestart (never skipped records) and the cluster reconverges under
// the new sharding.
func TestShardConfigChange(t *testing.T) {
	c := sim.NewCluster(ids(4), 8, 128, 0)
	for i := 0; i < 20; i++ {
		c.Introduce(c.Order[0], rec(fmt.Sprintf("svc-%02d", i), 1, member.Alive))
	}
	runUntilConverged(t, c, 100)

	// give node-1 a fresh record so peers pull from it this round
	c.Introduce(c.Order[1], rec("svc-new", 1, member.Alive))
	// Invalidate one server's snapshots in the middle of a client's round.
	swapped := false
	c.Faults.BeforePull = func(round int, from, to string) {
		if !swapped && to == c.Order[1] {
			swapped = true
			c.Nodes[to].Srv.SetNumShards(16)
		}
	}
	rep := c.RunRound(context.Background())
	if rep.Restarts == 0 {
		t.Fatal("expected at least one snapshot restart during config change")
	}
	c.Faults.BeforePull = nil

	// Rolling change on the remaining nodes, one per round.
	for _, id := range c.Order {
		c.Nodes[id].Srv.SetNumShards(16)
		c.RunRound(context.Background())
	}
	// New records after the change must still propagate.
	for i := 20; i < 28; i++ {
		c.Introduce(c.Order[1], rec(fmt.Sprintf("svc-%02d", i), 1, member.Alive))
	}
	runUntilConverged(t, c, 200)
	if err := c.CheckModel(); err != nil {
		t.Fatal(err)
	}
}

// Transport cancellation aborts a round without corrupting state; partial
// merges remain valid and later rounds complete the sync.
func TestCancellation(t *testing.T) {
	c := sim.NewCluster(ids(3), 4, 96, 0)
	for i := 0; i < 15; i++ {
		c.Introduce(c.Order[0], rec(fmt.Sprintf("svc-%02d", i), 1, member.Alive))
	}
	c.Faults.CancelPull = func(round int, from, to string) bool {
		return round <= 3 && from == c.Order[1]
	}
	reports := runUntilConverged(t, c, 200)
	sawCancel := false
	for _, r := range reports {
		for _, e := range r.Errors {
			if strings.Contains(e, "canceled") {
				sawCancel = true
			}
		}
	}
	if !sawCancel {
		t.Fatal("expected cancellation errors")
	}
	if err := c.CheckModel(); err != nil {
		t.Fatal(err)
	}
}

// A local update racing an in-flight stale page must win: the old response
// never overwrites the newer local incarnation.
func TestStalePageDoesNotOverwriteNewer(t *testing.T) {
	c := sim.NewCluster(ids(2), 2, 512, 0)
	c.Introduce(c.Order[0], rec("svc-x", 5, member.Alive))
	c.Introduce(c.Order[1], rec("svc-y", 1, member.Alive))
	raced := false
	c.Faults.BeforePullApply = func(round int, from, to string) {
		if from == c.Order[1] && to == c.Order[0] && !raced {
			raced = true
			// local update on the requester while the stale page is in flight
			c.Introduce(from, rec("svc-x", 9, member.Suspect))
		}
	}
	c.RunRound(context.Background())
	if !raced {
		t.Fatal("race hook never fired")
	}
	got, _ := c.Nodes[c.Order[1]].Srv.Table.Get("svc-x")
	if got.Incarnation != 9 || got.State != member.Suspect {
		t.Fatalf("stale page overwrote newer local record: %+v", got)
	}
}

// Trust scores only reorder probes. A zero-trust node publishing a legal
// new incarnation still recovers within a bounded number of cycles.
func TestTrustScoreCannotStarveRecovery(t *testing.T) {
	c := sim.NewCluster(ids(5), 8, 128, 0)
	for i := 0; i < 10; i++ {
		c.Introduce(c.Order[0], rec(fmt.Sprintf("svc-%02d", i), 1, member.Alive))
	}
	runUntilConverged(t, c, 100)
	// everyone distrusts node 4
	for _, id := range c.Order {
		if id != c.Order[4] {
			c.Nodes[id].Sched.SetTrust(c.Order[4], 0)
		}
	}
	// node 4 publishes a legal new incarnation
	c.Introduce(c.Order[4], rec("svc-00", 99, member.Dead))
	n := len(c.Order)
	reports := runUntilConverged(t, c, 4*n) // must recover within a few cycles
	rounds := len(reports)
	t.Logf("zero-trust node recovered after %d rounds (cycle=%d)", rounds, n-1)
	if err := c.CheckModel(); err != nil {
		t.Fatal(err)
	}
}

// Termination: with finite stable data (updates quiesce) and fair polling,
// the cluster converges within a bounded number of rounds, after which
// rounds cost digest-only bytes and move zero records.
func TestTerminationUnderFairPolling(t *testing.T) {
	n := 6
	c := sim.NewCluster(ids(n), 8, 128, 0)
	seedRecords(c, 30, rand.New(rand.NewSource(7)))
	// all updates are introduced up front: data is finite and stable
	bound := 3 * n * (n - 1)
	convergedRound := -1
	totalBytes := 0
	for r := 0; r < bound; r++ {
		rep := c.RunRound(context.Background())
		totalBytes += rep.TotalBytes()
		if c.Converged() {
			convergedRound = rep.Round
			break
		}
	}
	if convergedRound < 0 {
		t.Fatalf("did not terminate within bound %d rounds", bound)
	}
	// steady state: two more rounds move no records
	for i := 0; i < 2; i++ {
		rep := c.RunRound(context.Background())
		if rep.RecordsPulled != 0 {
			t.Fatalf("round %d after convergence still pulled %d records", rep.Round, rep.RecordsPulled)
		}
		totalBytes += rep.TotalBytes()
	}
	t.Logf("converged at round %d (bound %d), total bytes %d", convergedRound, bound, totalBytes)
	if err := c.CheckModel(); err != nil {
		t.Fatal(err)
	}
}

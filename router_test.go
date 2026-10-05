package sessionrouter

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
)

func newTestRouter(t *testing.T, nodes ...string) (*Router, *TestClock) {
	t.Helper()
	clk := NewTestClock()
	r, err := NewRouter(NewMemStore(), clk)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		r.NodeUp(NodeID(n))
	}
	return r, clk
}

func eventTypes(r *Router) []string {
	var out []string
	for _, e := range r.Events() {
		out = append(out, e.Type)
	}
	return out
}

func hasEvent(r *Router, typ string) bool {
	for _, e := range r.Events() {
		if e.Type == typ {
			return true
		}
	}
	return false
}

func TestCreateDeterministicAcrossRouters(t *testing.T) {
	r1, _ := newTestRouter(t, "n1", "n2", "n3")
	r2, _ := newTestRouter(t, "n3", "n1", "n2") // different registration order
	d1 := r1.Create("r1", "s1", 100)
	d2 := r2.Create("r1", "s1", 100)
	if !d1.Allowed || d1.Generation != 1 {
		t.Fatalf("unexpected create decision: %+v", d1)
	}
	if d1.Owner != d2.Owner || d1.Token != d2.Token {
		t.Fatalf("placement not deterministic: %+v vs %+v", d1, d2)
	}
	if d1.ActiveSessions != 1 {
		t.Fatalf("active count = %d, want 1", d1.ActiveSessions)
	}
	// Unhealthy nodes are never chosen.
	r1.NodeDown(d1.Owner)
	d3 := r1.Create("r2", "s2", 100)
	if !d3.Allowed || d3.Owner == d1.Owner {
		t.Fatalf("create landed on down node: %+v", d3)
	}
}

func TestRenewRequiresCurrentGeneration(t *testing.T) {
	r, clk := newTestRouter(t, "n1")
	r.Create("r1", "s1", 100)
	clk.Advance(10)
	d := r.Renew("r2", "s1", 2, 50) // wrong generation
	if d.Allowed || d.Code != CodeStaleGeneration {
		t.Fatalf("stale renew accepted: %+v", d)
	}
	d = r.Renew("r3", "s1", 1, 50)
	if !d.Allowed || d.Code != CodeOK {
		t.Fatalf("valid renew rejected: %+v", d)
	}
	s, _ := r.Lookup("s1")
	if s.ExpiresAt != clk.Now()+50 {
		t.Fatalf("expiry = %d, want %d", s.ExpiresAt, clk.Now()+50)
	}
}

func TestFailoverRecoveryAndLateNotifications(t *testing.T) {
	r, _ := newTestRouter(t, "n1", "n2")
	d := r.Create("r1", "s1", 1000)
	failed := d.Owner

	r.NodeDown(failed)
	s, _ := r.Lookup("s1")
	if s.Generation != 2 {
		t.Fatalf("generation after failover = %d, want 2", s.Generation)
	}
	if s.Owner == failed {
		t.Fatalf("session still on failed node %s", s.Owner)
	}
	if s.Token == d.Token {
		t.Fatalf("routing token not re-issued after failover")
	}

	// The old node recovers and still reports the old session: every
	// old-generation notification must be rejected.
	r.NodeUp(failed)
	if got := r.Renew("r2", "s1", 1, 50); got.Allowed || got.Code != CodeStaleGeneration {
		t.Fatalf("late renew accepted: %+v", got)
	}
	if got := r.Complete("r3", "s1", 1); got.Allowed || got.Code != CodeStaleGeneration {
		t.Fatalf("late complete accepted: %+v", got)
	}
	if got := r.Writeback("r4", "s1", 1, "payload"); got.Allowed || got.Code != CodeStaleGeneration {
		t.Fatalf("late writeback accepted: %+v", got)
	}

	// Session stays on the failover node; current generation works.
	if got := r.Renew("r5", "s1", 2, 50); !got.Allowed {
		t.Fatalf("current-generation renew rejected: %+v", got)
	}
	s, _ = r.Lookup("s1")
	if s.Owner == failed {
		t.Fatalf("session moved back to recovered node")
	}
	for _, want := range []string{"created", "node-down", "migrated", "node-up", "renewed"} {
		if !hasEvent(r, want) {
			t.Fatalf("missing event %q in %v", want, eventTypes(r))
		}
	}
}

func TestDuplicateMigrationIdempotentAndConflict(t *testing.T) {
	r, _ := newTestRouter(t, "n1", "n2", "n3")
	r.Create("r1", "s1", 1000)
	r.Create("r2", "s2", 1000)

	d1 := r.Migrate("m1", "s1")
	if !d1.Allowed || d1.Generation != 2 {
		t.Fatalf("first migrate: %+v", d1)
	}
	d2 := r.Migrate("m1", "s1") // same request ID, same parameters
	if !d2.Replayed || d2.Generation != d1.Generation || d2.Owner != d1.Owner || d2.Token != d1.Token {
		t.Fatalf("duplicate migrate not replayed identically: %+v vs %+v", d1, d2)
	}
	s, _ := r.Lookup("s1")
	if s.Generation != 2 {
		t.Fatalf("duplicate migrate bumped generation to %d", s.Generation)
	}

	d3 := r.Migrate("m1", "s2") // same request ID, different session
	if d3.Allowed || d3.Code != CodeConflict {
		t.Fatalf("request-ID reuse not flagged as conflict: %+v", d3)
	}
	s2, _ := r.Lookup("s2")
	if s2.Generation != 1 {
		t.Fatalf("conflicting request mutated s2: generation %d", s2.Generation)
	}
}

func TestTTLBoundaryExpiryBeforeRequest(t *testing.T) {
	r, clk := newTestRouter(t, "n1")
	r.Create("r1", "s1", 5) // expires at tick 5

	clk.AdvanceTo(4)
	if d := r.Renew("r2", "s1", 1, 5); !d.Allowed {
		t.Fatalf("renew before expiry rejected: %+v", d)
	} // now expires at tick 9

	clk.AdvanceTo(9) // same tick as expiry: expiry is processed first
	d := r.Renew("r3", "s1", 1, 5)
	if d.Allowed || d.Code != CodeSessionClosed {
		t.Fatalf("renew at expiry tick accepted: %+v", d)
	}
	s, _ := r.Lookup("s1")
	if !s.Closed || s.CloseReason != "expired" {
		t.Fatalf("session not expired at boundary: %+v", s)
	}
	if r.ActiveCount() != 0 {
		t.Fatalf("active count = %d, want 0", r.ActiveCount())
	}
	if !hasEvent(r, "expired") {
		t.Fatalf("missing expired event in %v", eventTypes(r))
	}
}

func TestCloseThenRecreateWithHigherGeneration(t *testing.T) {
	r, _ := newTestRouter(t, "n1")
	r.Create("r1", "s1", 100)
	if d := r.Complete("r2", "s1", 1); !d.Allowed {
		t.Fatalf("complete rejected: %+v", d)
	}
	d := r.Create("r3", "s1", 100)
	if !d.Allowed || d.Generation != 2 {
		t.Fatalf("recreate did not use higher generation: %+v", d)
	}
	// Notifications for the old incarnation are stale.
	if got := r.Writeback("r4", "s1", 1, "x"); got.Allowed || got.Code != CodeStaleGeneration {
		t.Fatalf("writeback for old incarnation accepted: %+v", got)
	}
	// Recreate on an active session is rejected.
	if got := r.Create("r5", "s1", 100); got.Allowed || got.Code != CodeSessionExists {
		t.Fatalf("duplicate create accepted: %+v", got)
	}
}

func TestAllNodesUnavailable(t *testing.T) {
	r, _ := newTestRouter(t)
	if d := r.Create("r1", "s1", 10); d.Allowed || d.Code != CodeNoHealthyNodes {
		t.Fatalf("create with no nodes: %+v", d)
	}

	r.NodeUp("n1")
	if d := r.Create("r2", "s1", 10); !d.Allowed {
		t.Fatalf("create after node up: %+v", d)
	}
	r.NodeDown("n1") // last node goes away
	s, _ := r.Lookup("s1")
	if !s.Closed {
		t.Fatalf("session not force-closed when all nodes unavailable: %+v", s)
	}
	if r.ActiveCount() != 0 {
		t.Fatalf("active count = %d, want 0", r.ActiveCount())
	}
	if !hasEvent(r, "force-closed") {
		t.Fatalf("missing force-closed event in %v", eventTypes(r))
	}
	// Migration with no healthy target is rejected.
	r.NodeUp("n2")
	r.Create("r3", "s2", 10)
	r.NodeDown("n2")
	if d := r.Migrate("m1", "s2"); d.Allowed || d.Code != CodeNoHealthyNodes && d.Code != CodeSessionClosed {
		t.Fatalf("migrate with no healthy nodes: %+v", d)
	}
}

func TestIdempotentReplayAcrossOps(t *testing.T) {
	r, _ := newTestRouter(t, "n1")
	d1 := r.Create("r1", "s1", 100)
	d2 := r.Create("r1", "s1", 100)
	if !d2.Replayed || d2.Token != d1.Token || r.ActiveCount() != 1 {
		t.Fatalf("create replay mismatch: %+v vs %+v", d1, d2)
	}
	if d := r.Create("r1", "s2", 100); d.Allowed || d.Code != CodeConflict {
		t.Fatalf("create request-ID conflict not detected: %+v", d)
	}
	r1 := r.Renew("r3", "s1", 1, 200)
	r2 := r.Renew("r3", "s1", 1, 200)
	if !r2.Replayed || r1.Token != r2.Token {
		t.Fatalf("renew replay mismatch: %+v vs %+v", r1, r2)
	}
	s, _ := r.Lookup("s1")
	if s.ExpiresAt != Tick(200) {
		t.Fatalf("replayed renew applied twice, expiry=%d", s.ExpiresAt)
	}
}

func TestCrashRecoveryNeverRollsBackGeneration(t *testing.T) {
	dir, err := ioutil.TempDir("", "sessionrouter")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	store := NewFileStore(dir)

	clk := NewTestClock()
	r1, err := NewRouter(store, clk)
	if err != nil {
		t.Fatal(err)
	}
	r1.NodeUp("n1")
	r1.NodeUp("n2")
	r1.Create("r1", "s1", 1000)
	r1.Migrate("m1", "s1") // generation 2
	r1.Migrate("m2", "s1") // generation 3
	if d := r1.Renew("r2", "s1", 3, 500); !d.Allowed {
		t.Fatalf("renew at gen 3: %+v", d)
	}
	tick := r1.Now()

	// Crash and recover from the persisted state.
	clk2 := NewTestClock()
	clk2.AdvanceTo(tick)
	r2, err := NewRouter(store, clk2)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := r2.Lookup("s1")
	if !ok {
		t.Fatal("session lost after recovery")
	}
	if s.Generation != 3 {
		t.Fatalf("generation = %d after recovery, want 3", s.Generation)
	}
	// Idempotent decisions survive the crash.
	if d := r2.Migrate("m2", "s1"); !d.Replayed || d.Generation != 3 {
		t.Fatalf("idempotent migrate not replayed after recovery: %+v", d)
	}
	// Stale-generation requests are still rejected after recovery.
	if d := r2.Renew("r9", "s1", 2, 10); d.Allowed || d.Code != CodeStaleGeneration {
		t.Fatalf("stale renew accepted after recovery: %+v", d)
	}
	// Recreate after close continues above the high-water mark.
	r2.Complete("r10", "s1", 3)
	if d := r2.Create("r11", "s1", 100); !d.Allowed || d.Generation != 4 {
		t.Fatalf("recreate after recovery: %+v", d)
	}
}

func TestStaleBackupCannotRollBackGeneration(t *testing.T) {
	dir, err := ioutil.TempDir("", "sessionrouter")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	store := NewFileStore(dir)

	clk := NewTestClock()
	r1, err := NewRouter(store, clk)
	if err != nil {
		t.Fatal(err)
	}
	r1.NodeUp("n1")
	r1.Create("r1", "s1", 1000)

	// Snapshot the state file at generation 1 (simulates a stale backup).
	stale, err := ioutil.ReadFile(filepath.Join(dir, stateFile))
	if err != nil {
		t.Fatal(err)
	}

	r1.Migrate("m1", "s1") // generation 2
	r1.Migrate("m2", "s1") // generation 3
	tick := r1.Now()

	// Crash; an operator restores the stale gen-1 state file, but
	// highwater.json still records generation 3.
	if err := ioutil.WriteFile(filepath.Join(dir, stateFile), stale, 0644); err != nil {
		t.Fatal(err)
	}
	clk2 := NewTestClock()
	clk2.AdvanceTo(tick)
	r2, err := NewRouter(store, clk2)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := r2.Lookup("s1")
	if !ok {
		t.Fatal("session lost after recovery")
	}
	if s.Generation != 3 {
		t.Fatalf("generation rolled back to %d after stale restore, want 3", s.Generation)
	}
	// Requests carrying the rolled-back generation are rejected.
	if d := r2.Renew("r2", "s1", 1, 10); d.Allowed || d.Code != CodeStaleGeneration {
		t.Fatalf("rolled-back generation accepted: %+v", d)
	}
	// New generations continue above the high-water mark, never reusing 2.
	r2.Migrate("m3", "s1")
	s, _ = r2.Lookup("s1")
	if s.Generation != 4 {
		t.Fatalf("generation after stale restore = %d, want 4", s.Generation)
	}
}

func TestEventLogTimeline(t *testing.T) {
	r, clk := newTestRouter(t, "n1", "n2")
	r.Create("r1", "s1", 5)
	clk.AdvanceTo(3)
	r.Renew("r2", "s1", 1, 5) // expires at 8
	clk.AdvanceTo(8)
	r.Create("r3", "s2", 10) // triggers expiry of s1 first

	want := []string{"node-up", "node-up", "created", "renewed", "expired", "created"}
	got := eventTypes(r)
	if len(got) != len(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("events = %v, want %v", got, want)
		}
	}
	// Events carry ticks and are non-decreasing in time.
	var last Tick = -1
	for _, e := range r.Events() {
		if e.Tick < last {
			t.Fatalf("event ticks not monotonic: %+v", r.Events())
		}
		last = e.Tick
	}
}

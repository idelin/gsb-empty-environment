package router

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func newTestRouter(nodes ...string) (*Router, *ManualClock) {
	clk := NewManualClock()
	r := NewRouter(clk, nil)
	for _, n := range nodes {
		r.AddNode(n)
	}
	return r, clk
}

func hasEvent(events []Event, kind string) bool {
	for _, e := range events {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

func TestCreateDeterministicAndRenew(t *testing.T) {
	r, _ := newTestRouter("n1", "n2", "n3")

	d1 := r.Create("req-1", "sess-a", 10)
	if !d1.Accepted || d1.Generation != 1 || d1.Owner == "" || d1.Token == "" {
		t.Fatalf("create rejected or incomplete: %+v", d1)
	}
	if d1.ActiveSessions != 1 {
		t.Fatalf("active count = %d, want 1", d1.ActiveSessions)
	}

	// Deterministic placement: a fresh router must pick the same owner.
	r2, _ := newTestRouter("n1", "n2", "n3")
	d2 := r2.Create("req-1", "sess-a", 10)
	if d2.Owner != d1.Owner || d2.Token != d1.Token {
		t.Fatalf("non-deterministic placement: %+v vs %+v", d1, d2)
	}

	// Renew with wrong generation rejected, with current generation accepted.
	if d := r.Renew("req-2", "sess-a", 99, 10); d.Accepted {
		t.Fatalf("stale-generation renew accepted: %+v", d)
	}
	d3 := r.Renew("req-3", "sess-a", 1, 20)
	if !d3.Accepted {
		t.Fatalf("renew rejected: %+v", d3)
	}
	s, _ := r.Session("sess-a")
	if s.ExpiresAt != 20 {
		t.Fatalf("expiresAt = %d, want 20", s.ExpiresAt)
	}
}

func TestFailoverMigratesAndRejectsStaleGeneration(t *testing.T) {
	r, _ := newTestRouter("n1", "n2", "n3")
	d := r.Create("req-1", "sess-a", 100)
	owner := d.Owner
	oldToken := d.Token

	r.NodeFailed(owner)

	s, _ := r.Session("sess-a")
	if s.Generation != 2 {
		t.Fatalf("generation = %d, want 2", s.Generation)
	}
	if s.Owner == owner {
		t.Fatalf("session still on failed node %s", owner)
	}
	if s.Token == oldToken {
		t.Fatalf("route token not re-issued after migration")
	}
	if !hasEvent(r.Events(), "migrated") {
		t.Fatalf("missing migrated event: %+v", r.Events())
	}

	// Stale-generation renew, complete and writeback from the old owner all rejected.
	if d := r.Renew("req-2", "sess-a", 1, 100); d.Accepted {
		t.Fatalf("stale renew accepted: %+v", d)
	}
	if d := r.Complete("req-3", "sess-a", 1); d.Accepted {
		t.Fatalf("stale complete accepted: %+v", d)
	}
	if d := r.Writeback("req-4", "sess-a", 1, "data"); d.Accepted {
		t.Fatalf("stale writeback accepted: %+v", d)
	}
	// New generation works.
	if d := r.Renew("req-5", "sess-a", 2, 100); !d.Accepted {
		t.Fatalf("post-migration renew rejected: %+v", d)
	}
}

func TestRecoveredNodeLateNotificationsRejected(t *testing.T) {
	r, clk := newTestRouter("n1", "n2")
	d := r.Create("req-1", "sess-a", 100)
	owner := d.Owner

	r.NodeFailed(owner)
	r.NodeRecovered(owner) // old node comes back, still holding gen-1 state

	// Late completion and writeback from the recovered node (old generation).
	if d := r.Complete("req-2", "sess-a", 1); d.Accepted {
		t.Fatalf("late complete from recovered node accepted: %+v", d)
	}
	if d := r.Writeback("req-3", "sess-a", 1, "stale"); d.Accepted {
		t.Fatalf("late writeback from recovered node accepted: %+v", d)
	}
	// Session is still active under the new owner.
	s, _ := r.Session("sess-a")
	if s.State != StateActive || s.Owner == owner {
		t.Fatalf("unexpected session state after recovery: %+v", s)
	}
	clk.Advance(1)
	if got := r.ActiveCount(); got != 1 {
		t.Fatalf("active count = %d, want 1", got)
	}
}

func TestDuplicateMigrationIsIdempotent(t *testing.T) {
	r, _ := newTestRouter("n1", "n2")
	d := r.Create("req-1", "sess-a", 100)
	owner := d.Owner

	r.NodeFailed(owner)
	s1, _ := r.Session("sess-a")
	r.NodeFailed(owner) // duplicate failure notification
	r.NodeFailed(owner)
	s2, _ := r.Session("sess-a")

	if !reflect.DeepEqual(s1, s2) {
		t.Fatalf("duplicate migration changed state: %+v vs %+v", s1, s2)
	}
	if s2.Generation != 2 {
		t.Fatalf("generation = %d after duplicate failure, want 2", s2.Generation)
	}
	if !hasEvent(r.Events(), "node_failure_ignored") {
		t.Fatalf("missing node_failure_ignored event")
	}
}

func TestTTLBoundaryExpiryBeatsRenew(t *testing.T) {
	r, clk := newTestRouter("n1")
	r.Create("req-1", "sess-a", 10) // expires at tick 10

	clk.Advance(9)
	if d := r.Renew("req-2", "sess-a", 1, 10); !d.Accepted {
		t.Fatalf("renew one tick before expiry rejected: %+v", d)
	}

	// Session now expires at tick 19; land exactly on the boundary.
	clk.Advance(10) // now = 19 == ExpiresAt
	d := r.Renew("req-3", "sess-a", 1, 10)
	if d.Accepted {
		t.Fatalf("renew at exact expiry tick accepted; expiry must win: %+v", d)
	}
	s, _ := r.Session("sess-a")
	if s.State != StateExpired {
		t.Fatalf("state = %s, want expired", s.State)
	}
	if !hasEvent(r.Events(), "expired") {
		t.Fatalf("missing expired event")
	}
	if got := r.ActiveCount(); got != 0 {
		t.Fatalf("active count = %d, want 0", got)
	}
}

func TestIdempotentReplayAndConflict(t *testing.T) {
	r, _ := newTestRouter("n1", "n2")
	d1 := r.Create("req-1", "sess-a", 100)

	// Replay after a migration: the stored decision must be returned verbatim.
	r.NodeFailed(d1.Owner)
	d2 := r.Create("req-1", "sess-a", 100)
	if d1 != d2 {
		t.Fatalf("replayed decision differs:\n%+v\n%+v", d1, d2)
	}

	// Same request ID, different session: conflict.
	d3 := r.Create("req-1", "sess-b", 100)
	if d3.Accepted || !d3.Conflict {
		t.Fatalf("cross-session request ID reuse not flagged: %+v", d3)
	}

	// Idempotent renew replay.
	r1 := r.Renew("req-9", "sess-a", 2, 50)
	r2 := r.Renew("req-9", "sess-a", 2, 50)
	if !r1.Accepted || r1 != r2 {
		t.Fatalf("renew replay mismatch: %+v vs %+v", r1, r2)
	}
	s, _ := r.Session("sess-a")
	if s.ExpiresAt != 50 {
		t.Fatalf("double-renew extended twice: expiresAt=%d want 50", s.ExpiresAt)
	}
}

func TestAllNodesUnavailable(t *testing.T) {
	r, _ := newTestRouter("n1")
	r.Create("req-1", "sess-a", 100)
	r.NodeFailed("n1")

	// No healthy nodes: migration deferred, creation rejected, renew rejected.
	if !hasEvent(r.Events(), "migration_deferred") {
		t.Fatalf("missing migration_deferred event")
	}
	if d := r.Create("req-2", "sess-b", 10); d.Accepted {
		t.Fatalf("create accepted with no healthy nodes: %+v", d)
	}
	if d := r.Renew("req-3", "sess-a", 1, 100); d.Accepted {
		t.Fatalf("renew accepted while owner unhealthy: %+v", d)
	}
	s, _ := r.Session("sess-a")
	if s.Generation != 1 || s.Owner != "n1" {
		t.Fatalf("session moved without healthy nodes: %+v", s)
	}

	// Node returns: the unmigrated session is serviceable again.
	r.NodeRecovered("n1")
	if d := r.Renew("req-4", "sess-a", 1, 100); !d.Accepted {
		t.Fatalf("renew after recovery rejected: %+v", d)
	}
}

func TestCloseAndRebuildWithHigherGeneration(t *testing.T) {
	r, _ := newTestRouter("n1", "n2")
	r.Create("req-1", "sess-a", 100)
	r.NodeFailed(r.sessions["sess-a"].Owner) // generation now 2
	if d := r.Complete("req-2", "sess-a", 2); !d.Accepted {
		t.Fatalf("complete rejected: %+v", d)
	}

	d := r.Create("req-3", "sess-a", 100)
	if !d.Accepted || d.Generation != 3 {
		t.Fatalf("rebuild after close: %+v, want generation 3", d)
	}
	// Operations against the closed generation are rejected.
	if d := r.Renew("req-4", "sess-a", 2, 100); d.Accepted {
		t.Fatalf("renew of closed generation accepted: %+v", d)
	}
}

func TestCrashRecoveryNeverRollsBackGeneration(t *testing.T) {
	dir, err := ioutil.TempDir("", "router-wal")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "wal.jsonl")

	clk := NewManualClock()
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRouter(clk, store)
	r.AddNode("n1")
	r.AddNode("n2")
	r.AddNode("n3")
	created := r.Create("req-1", "sess-a", 100)
	owner := r.sessions["sess-a"].Owner
	r.NodeFailed(owner) // gen -> 2
	r.NodeFailed(owner) // duplicate, no-op
	r.Renew("req-2", "sess-a", 2, 200)
	before := r.ActiveCount()
	// Crash: no clean close of router state, just reopen the log.
	store.Close()

	clk2 := NewManualClock()
	store2, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	r2 := RecoverRouter(clk2, store2)

	s, ok := r2.Session("sess-a")
	if !ok || s.Generation != 2 {
		t.Fatalf("recovered generation = %d, want 2 (no rollback)", s.Generation)
	}
	if s.ExpiresAt != 200 {
		t.Fatalf("recovered expiresAt = %d, want 200", s.ExpiresAt)
	}
	if got := r2.ActiveCount(); got != before {
		t.Fatalf("recovered active count = %d, want %d", got, before)
	}
	// Idempotent results survive the crash: the original decision is
	// replayed verbatim, even though the session has since migrated.
	d := r2.Create("req-1", "sess-a", 100)
	if d != created {
		t.Fatalf("replayed create after crash differs:\n%+v\n%+v", d, created)
	}
	// Stale generation still rejected after recovery.
	if d := r2.Renew("req-3", "sess-a", 1, 100); d.Accepted {
		t.Fatalf("stale renew accepted after recovery: %+v", d)
	}
	// And the recovered router keeps moving forward.
	r2.NodeFailed(s.Owner)
	s2, _ := r2.Session("sess-a")
	if s2.Generation != 3 {
		t.Fatalf("post-recovery generation = %d, want 3", s2.Generation)
	}
}

func TestEventLogCoversLifecycle(t *testing.T) {
	r, clk := newTestRouter("n1", "n2")
	r.Create("req-1", "sess-a", 5)
	r.Renew("req-2", "sess-a", 1, 5)
	r.Writeback("req-3", "sess-a", 1, "payload")
	owner := r.sessions["sess-a"].Owner
	r.NodeFailed(owner)
	r.NodeRecovered(owner)
	clk.Advance(100)
	r.Tick()

	want := []string{"node_added", "created", "renewed", "writeback", "node_failed", "migrated", "node_recovered", "expired"}
	events := r.Events()
	for _, k := range want {
		if !hasEvent(events, k) {
			t.Fatalf("event log missing %q: %+v", k, events)
		}
	}
	for _, e := range events {
		if e.Tick > clk.Now() {
			t.Fatalf("event from the future: %+v", e)
		}
	}
}

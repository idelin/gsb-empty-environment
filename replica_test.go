package snaprepl

import (
	"crypto/sha256"
	"testing"
)

// chunkMsgs splits data into snapshot chunk messages, as the leader would.
func chunkMsgs(snapIndex, snapTerm int, data []byte, digest [32]byte, size int) []Msg {
	var msgs []Msg
	for off := 0; off < len(data); off += size {
		end := off + size
		if end > len(data) {
			end = len(data)
		}
		msgs = append(msgs, Msg{
			Kind: MsgSnapChunk, SnapIndex: snapIndex, SnapTerm: snapTerm,
			Offset: off, Total: len(data), Data: data[off:end], Digest: digest,
		})
	}
	return msgs
}

// A follower partitioned while the leader compacts must install the
// chunked snapshot, then continue with log entries after the snapshot
// index, and converge with the leader.
func TestLaggingFollowerInstallsSnapshotThenLogs(t *testing.T) {
	c := NewCluster(1, 3)
	c.StartHeartbeat()
	c.Net.Blocked[2] = true
	c.ClientWrites(25, 5)
	c.Sim.After(200, func() { delete(c.Net.Blocked, 2) })

	end, ok := c.RunUntilConverged(5000, 200)
	if !ok {
		t.Fatalf("liveness: follower did not converge by t=%d", end)
	}
	if err := c.Mon.CheckSafety(); err != nil {
		t.Fatalf("safety: %v", err)
	}
	f := c.Nodes[2]
	if f.baseIndex == 0 {
		t.Fatal("follower 2 never installed a snapshot")
	}
	if !f.SM.Equal(c.Nodes[0].SM) {
		t.Fatal("follower state differs from leader after catch-up")
	}
	t.Logf("converged at t=%d; follower 2 installed snapshot at index %d, applies=%v",
		end, f.baseIndex, c.Mon.Applies())
}

// With heavy loss, duplication and reordering, interleaved log entries
// and snapshot chunks must never be applied twice, and the cluster must
// still converge once the network becomes fair.
func TestInterleavedFaultsNoDuplicateApply(t *testing.T) {
	c := NewCluster(42, 3)
	c.StartHeartbeat()
	c.Net.DropRate = 0.2
	c.Net.DupRate = 0.6
	c.ClientWrites(30, 4)
	c.Sim.After(300, func() { c.Net.DropRate, c.Net.DupRate = 0, 0 })

	end, ok := c.RunUntilConverged(5000, 300)
	if !ok {
		t.Fatalf("liveness: no convergence under faults by t=%d", end)
	}
	if err := c.Mon.CheckSafety(); err != nil {
		t.Fatalf("safety under interleaving: %v", err)
	}
	t.Logf("converged at t=%d under drop/dup/reorder; applies=%v", end, c.Mon.Applies())
}

// Liveness: once faults stop, convergence happens within a bounded
// number of virtual time units.
func TestLivenessBoundedConvergence(t *testing.T) {
	c := NewCluster(7, 3)
	c.StartHeartbeat()
	c.Net.DropRate = 0.3
	c.Net.DupRate = 0.3
	c.ClientWrites(30, 4)
	const faultsStop = 200
	c.Sim.After(faultsStop, func() { c.Net.DropRate, c.Net.DupRate = 0, 0 })

	end, ok := c.RunUntilConverged(5000, faultsStop)
	if !ok {
		t.Fatal("liveness: cluster never converged")
	}
	const bound = 300
	if end > faultsStop+bound {
		t.Fatalf("liveness: converged at t=%d, beyond bound t=%d", end, faultsStop+bound)
	}
	if err := c.Mon.CheckSafety(); err != nil {
		t.Fatalf("safety: %v", err)
	}
	t.Logf("liveness evidence: converged at t=%d, %d ticks after faults stopped (bound %d)",
		end, end-faultsStop, bound)
}

// A snapshot with a missing chunk must never be published; once the
// final chunk arrives the publish happens atomically.
func TestMissingChunkBlocksPublish(t *testing.T) {
	c := NewCluster(2, 3)
	f := c.Nodes[2]
	f.SM.Apply("set old 1")

	data := []byte(`{"new":"2"}`)
	chunks := chunkMsgs(1, 1, data, sha256.Sum256(data), 4)
	if len(chunks) < 2 {
		t.Fatal("test needs multiple chunks")
	}
	for _, m := range chunks[:len(chunks)-1] {
		f.onSnapChunk(m)
	}
	if f.baseIndex != 0 {
		t.Fatal("published a snapshot with a missing chunk")
	}
	if v, _ := f.Read("old"); v != "1" {
		t.Fatal("old state not readable while staging")
	}
	f.onSnapChunk(chunks[len(chunks)-1])
	if f.baseIndex != 1 {
		t.Fatal("snapshot not published after the final chunk")
	}
	if v, _ := f.Read("new"); v != "2" {
		t.Fatal("new state not visible after publish")
	}
}

// While chunks arrive, reads must observe only the old state; after the
// atomic publish, only the new state. Never a mix.
func TestNoMixedReadsDuringInstall(t *testing.T) {
	c := NewCluster(3, 3)
	f := c.Nodes[2]
	f.SM.Apply("set k old")

	data := []byte(`{"k":"new","x":"1"}`)
	chunks := chunkMsgs(4, 1, data, sha256.Sum256(data), 3)
	for i, m := range chunks[:len(chunks)-1] {
		f.onSnapChunk(m)
		v, ok := f.Read("k")
		if !ok || v != "old" {
			t.Fatalf("chunk %d: read during install got %q, want old state", i, v)
		}
		if _, leaked := f.Read("x"); leaked {
			t.Fatalf("chunk %d: new key leaked into old state during install", i)
		}
	}
	f.onSnapChunk(chunks[len(chunks)-1])
	if v, _ := f.Read("k"); v != "new" {
		t.Fatalf("after publish read got %q, want new state", v)
	}
	if v, _ := f.Read("x"); v != "1" {
		t.Fatalf("after publish new key missing, got %q", v)
	}
}

// A corrupted chunk makes the assembled digest mismatch: the staged
// snapshot is rolled back, the old state survives, and a clean
// retransmission still completes the install.
func TestDigestMismatchRollback(t *testing.T) {
	c := NewCluster(4, 3)
	f := c.Nodes[2]
	f.SM.Apply("set old 1")

	good := []byte(`{"new":"2"}`)
	digest := sha256.Sum256(good)
	bad := []byte(`{"new":"9"}`) // same length, wrong content
	for _, m := range chunkMsgs(1, 1, bad, digest, 4) {
		f.onSnapChunk(m)
	}
	if f.baseIndex != 0 {
		t.Fatal("published a snapshot whose digest did not match")
	}
	if f.staging != nil {
		t.Fatal("staged bytes not discarded on digest mismatch")
	}
	if len(c.Mon.Rollbacks) != 1 {
		t.Fatalf("expected 1 rollback, got %d", len(c.Mon.Rollbacks))
	}
	if v, _ := f.Read("old"); v != "1" {
		t.Fatal("old state damaged by rolled-back snapshot")
	}
	// Clean retransmission recovers.
	for _, m := range chunkMsgs(1, 1, good, digest, 4) {
		f.onSnapChunk(m)
	}
	if f.baseIndex != 1 {
		t.Fatal("retransmitted snapshot not installed")
	}
	if v, _ := f.Read("new"); v != "2" {
		t.Fatal("new state not visible after clean retransmission")
	}
}

// Chunks for an already-installed (or superseded) snapshot are
// acknowledged and ignored: no staging, no state change, no re-apply.
func TestStaleSnapshotChunkIgnored(t *testing.T) {
	c := NewCluster(5, 3)
	f := c.Nodes[2]
	data := []byte(`{"a":"1"}`)
	for _, m := range chunkMsgs(5, 1, data, sha256.Sum256(data), 4) {
		f.onSnapChunk(m)
	}
	if f.baseIndex != 5 {
		t.Fatal("setup: snapshot 5 not installed")
	}
	stale := chunkMsgs(3, 1, []byte(`{"a":"0"}`), [32]byte{}, 4)
	for _, m := range stale {
		f.onSnapChunk(m)
	}
	if f.staging != nil {
		t.Fatal("stale chunk created a staging area")
	}
	if f.baseIndex != 5 {
		t.Fatal("stale chunk moved the snapshot base")
	}
	if v, _ := f.Read("a"); v != "1" {
		t.Fatal("stale chunk altered the state machine")
	}
}

// After a snapshot is installed, AppendEntries carrying entries at or
// below the snapshot index are skipped; only newer entries are appended
// and applied, exactly once.
func TestSkipsLogsCoveredBySnapshot(t *testing.T) {
	c := NewCluster(6, 3)
	f := c.Nodes[2]
	data := []byte(`{"base":"1"}`)
	for _, m := range chunkMsgs(5, 1, data, sha256.Sum256(data), 4) {
		f.onSnapChunk(m)
	}
	f.onAppend(Msg{
		Kind: MsgAppend, From: 0, Term: 1, PrevIndex: 3, PrevTerm: 1,
		Entries: []Entry{
			{Index: 4, Term: 1, Cmd: "set ghost 1"}, // covered by snapshot
			{Index: 5, Term: 1, Cmd: "set ghost 2"}, // covered by snapshot
			{Index: 6, Term: 1, Cmd: "set k6 v6"},
			{Index: 7, Term: 1, Cmd: "set k7 v7"},
		},
		LeaderCommit: 7,
	})
	if _, ok := f.Read("ghost"); ok {
		t.Fatal("log entry covered by snapshot was applied")
	}
	if v, _ := f.Read("k6"); v != "v6" {
		t.Fatal("post-snapshot entry 6 not applied")
	}
	if v, _ := f.Read("k7"); v != "v7" {
		t.Fatal("post-snapshot entry 7 not applied")
	}
	// Duplicate redelivery of the same batch must be a no-op.
	f.onAppend(Msg{
		Kind: MsgAppend, From: 0, Term: 1, PrevIndex: 3, PrevTerm: 1,
		Entries: []Entry{
			{Index: 4, Term: 1, Cmd: "set ghost 1"},
			{Index: 5, Term: 1, Cmd: "set ghost 2"},
			{Index: 6, Term: 1, Cmd: "set k6 v6"},
			{Index: 7, Term: 1, Cmd: "set k7 v7"},
		},
		LeaderCommit: 7,
	})
	if err := c.Mon.CheckSafety(); err != nil {
		t.Fatalf("safety: %v", err)
	}
	if got := c.Mon.Applies()[2]; got != 2 {
		t.Fatalf("follower applied %d entries, want exactly 2", got)
	}
}

// End-to-end digest corruption on the wire: the follower rolls back the
// corrupted stream and still converges after the corruption stops.
func TestCorruptionRollbackThenConverge(t *testing.T) {
	c := NewCluster(11, 3)
	c.StartHeartbeat()
	c.Net.Blocked[2] = true
	c.ClientWrites(15, 5)
	c.Sim.After(160, func() { delete(c.Net.Blocked, 2); c.Net.CorruptRate = 0.6 })
	c.Sim.After(400, func() { c.Net.CorruptRate = 0 })

	end, ok := c.RunUntilConverged(5000, 400)
	if !ok {
		t.Fatal("liveness: no convergence after corruption stopped")
	}
	if len(c.Mon.Rollbacks) == 0 {
		t.Fatal("expected at least one digest-mismatch rollback")
	}
	if err := c.Mon.CheckSafety(); err != nil {
		t.Fatalf("safety: %v", err)
	}
	t.Logf("converged at t=%d after %d rollback(s)", end, len(c.Mon.Rollbacks))
}

// Seed sweep: random loss/duplication/reorder/corruption/partition
// schedules. Safety must hold on every seed and every run must converge
// once the network becomes fair.
func TestSeedSweepSafetyAndLiveness(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		c := NewCluster(seed, 3)
		c.StartHeartbeat()
		c.Net.DropRate = 0.15
		c.Net.DupRate = 0.4
		c.Net.CorruptRate = 0.1
		if seed%3 == 0 {
			c.Net.Blocked[2] = true
			c.Sim.After(250, func() { delete(c.Net.Blocked, 2) })
		}
		c.ClientWrites(35, 3)
		c.Sim.After(400, func() { c.Net.DropRate, c.Net.DupRate, c.Net.CorruptRate = 0, 0, 0 })

		end, ok := c.RunUntilConverged(20000, 400)
		if !ok {
			t.Fatalf("seed %d: liveness: no convergence by t=%d", seed, end)
		}
		if err := c.Mon.CheckSafety(); err != nil {
			t.Fatalf("seed %d: safety: %v", seed, err)
		}
	}
	t.Log("20/20 seeds: safety invariants held, all clusters converged")
}

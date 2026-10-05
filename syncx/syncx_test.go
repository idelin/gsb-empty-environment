package syncx_test

import (
	"testing"

	"antientropy/member"
	"antientropy/syncx"
)

func rec(id string, inc uint64, st member.State) member.Record {
	return member.Record{NodeID: id, Incarnation: inc, State: st}
}

// The merge is a lattice join: higher incarnation wins; within one
// incarnation alive < suspect < dead.
func TestMergeLattice(t *testing.T) {
	a := rec("n", 1, member.Alive)
	b := rec("n", 1, member.Dead)
	c := rec("n", 2, member.Alive)
	if member.Merge(a, b) != b || member.Merge(b, a) != b {
		t.Fatal("same incarnation: dead must win over alive")
	}
	if member.Merge(b, c) != c || member.Merge(c, b) != c {
		t.Fatal("higher incarnation must win")
	}
	if member.Merge(a, a) != a {
		t.Fatal("merge must be idempotent")
	}
	s := rec("n", 1, member.Suspect)
	if member.Merge(a, s) != s || member.Merge(s, b) != b {
		t.Fatal("alive < suspect < dead ordering broken")
	}
}

// A stale apply after a newer local update must be a no-op.
func TestStaleApplyDoesNotRegress(t *testing.T) {
	tbl := member.NewTable()
	tbl.Apply(rec("n", 9, member.Suspect))
	if changed := tbl.Apply(rec("n", 5, member.Dead)); changed != 0 {
		t.Fatal("stale response changed the table")
	}
	got, _ := tbl.Get("n")
	if got.Incarnation != 9 || got.State != member.Suspect {
		t.Fatalf("table regressed: %+v", got)
	}
}

// Digests bind shard boundary, record count and content.
func TestDigestBindsBoundaryCountContent(t *testing.T) {
	recs := []member.Record{rec("a", 1, member.Alive)}
	base := syncx.ComputeDigest(recs, 0, 4)
	if syncx.ComputeDigest(recs, 1, 4) == base {
		t.Fatal("digest ignores shard id (boundary)")
	}
	if syncx.ComputeDigest(recs, 0, 8) == base {
		t.Fatal("digest ignores numShards (boundary)")
	}
	two := append(append([]member.Record{}, recs...), rec("b", 1, member.Alive))
	if syncx.ComputeDigest(two, 0, 4) == base {
		t.Fatal("digest ignores record count/content")
	}
	if syncx.ComputeDigest([]member.Record{rec("a", 2, member.Alive)}, 0, 4) == base {
		t.Fatal("digest ignores incarnation (content)")
	}
	if syncx.ComputeDigest([]member.Record{rec("a", 1, member.Dead)}, 0, 4) == base {
		t.Fatal("digest ignores state (content)")
	}
	// order-independent
	rev := []member.Record{rec("b", 1, member.Alive), rec("a", 1, member.Alive)}
	if syncx.ComputeDigest(rev, 0, 4) != syncx.ComputeDigest(two, 0, 4) {
		t.Fatal("digest must be order-independent")
	}
}

// Shard assignment is a fixed pure hash.
func TestShardOfFixedHash(t *testing.T) {
	if syncx.ShardOf("node-x", 16) != syncx.ShardOf("node-x", 16) {
		t.Fatal("shard assignment not deterministic")
	}
	seen := map[int]bool{}
	for i := 0; i < 100; i++ {
		sh := syncx.ShardOf(string(rune('a'+i%26))+string(rune('0'+i/26)), 4)
		if sh < 0 || sh >= 4 {
			t.Fatalf("shard %d out of range", sh)
		}
		seen[sh] = true
	}
	if len(seen) < 2 {
		t.Fatal("hash does not spread across shards")
	}
}

// Pull pagination: byte-limited pages chain via cursors and cover the shard.
func TestPullPagination(t *testing.T) {
	srv := syncx.NewServer(1)
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		srv.Table.Apply(rec(id, 1, member.Alive))
	}
	d := srv.HandleDigest(syncx.DigestRequest{StartShard: 0, MaxBytes: 4096})
	if len(d.Digests) != 1 || d.Digests[0].Count != 5 {
		t.Fatalf("bad digest response: %+v", d)
	}
	// one record per page
	var got []member.Record
	cursor := ""
	pages := 0
	for {
		resp := srv.HandlePull(syncx.PullRequest{
			Shard: 0, SnapshotVersion: d.SnapshotVersion, Cursor: cursor, MaxBytes: syncx.PullResponseHeader + 17,
		})
		if resp.Status != syncx.StatusOK {
			t.Fatalf("unexpected status %d", resp.Status)
		}
		if syncx.ChecksumRecords(resp.Records) != resp.Checksum {
			t.Fatal("bad page checksum")
		}
		got = append(got, resp.Records...)
		pages++
		if resp.Done {
			break
		}
		if resp.NextCursor <= cursor {
			t.Fatal("cursor not advancing")
		}
		cursor = resp.NextCursor
		if pages > 10 {
			t.Fatal("pagination did not terminate")
		}
	}
	if len(got) != 5 || pages < 5 {
		t.Fatalf("got %d records in %d pages, want 5 records in >=5 pages", len(got), pages)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].NodeID >= got[i].NodeID {
			t.Fatal("records not in order")
		}
	}
}

// Snapshot invalidation (table updates evicting old snapshots, or a shard
// config change) must yield StatusRestart, never skipped records.
func TestSnapshotInvalidationRestarts(t *testing.T) {
	srv := syncx.NewServer(4)
	srv.Table.Apply(rec("a", 1, member.Alive))
	d1 := srv.HandleDigest(syncx.DigestRequest{StartShard: 0, MaxBytes: 4096})
	// two revisions evict the first snapshot (keep=2)
	srv.Table.Apply(rec("b", 1, member.Alive))
	srv.HandleDigest(syncx.DigestRequest{StartShard: 0, MaxBytes: 4096})
	srv.Table.Apply(rec("c", 1, member.Alive))
	srv.HandleDigest(syncx.DigestRequest{StartShard: 0, MaxBytes: 4096})
	resp := srv.HandlePull(syncx.PullRequest{Shard: 0, SnapshotVersion: d1.SnapshotVersion, MaxBytes: 4096})
	if resp.Status != syncx.StatusRestart {
		t.Fatal("evicted snapshot must yield StatusRestart")
	}
	// config change invalidates everything
	d2 := srv.HandleDigest(syncx.DigestRequest{StartShard: 0, MaxBytes: 4096})
	srv.SetNumShards(8)
	resp = srv.HandlePull(syncx.PullRequest{Shard: 0, SnapshotVersion: d2.SnapshotVersion, MaxBytes: 4096})
	if resp.Status != syncx.StatusRestart {
		t.Fatal("config change must invalidate snapshots")
	}
}

// Digest exchange paginates under a byte limit and always progresses.
func TestDigestPagination(t *testing.T) {
	srv := syncx.NewServer(16)
	srv.Table.Apply(rec("a", 1, member.Alive))
	limit := syncx.DigestResponseHeader + 3*syncx.DigestSize
	var all []syncx.Digest
	start := 0
	for i := 0; i < 20; i++ {
		resp := srv.HandleDigest(syncx.DigestRequest{StartShard: start, MaxBytes: limit})
		if resp.Bytes > limit {
			t.Fatalf("digest response %d bytes exceeds limit %d", resp.Bytes, limit)
		}
		all = append(all, resp.Digests...)
		if resp.NextShard < 0 {
			break
		}
		if resp.NextShard <= start {
			t.Fatal("digest cursor not advancing")
		}
		start = resp.NextShard
	}
	if len(all) != 16 {
		t.Fatalf("got %d digests, want 16", len(all))
	}
}

// The scheduler reorders by trust but never starves: every peer is probed
// exactly once per cycle, even with trust 0.
func TestSchedulerFairCycles(t *testing.T) {
	s := syncx.NewScheduler([]string{"a", "b", "c", "d"})
	s.SetTrust("a", 1)
	s.SetTrust("b", 0)
	s.SetTrust("c", 0.5)
	counts := map[string]int{}
	first := map[string]int{}
	for i := 0; i < 12; i++ {
		p := s.Next()
		if _, ok := first[p]; !ok {
			first[p] = i
		}
		counts[p]++
	}
	for _, p := range []string{"a", "b", "c", "d"} {
		if counts[p] != 3 {
			t.Fatalf("peer %s probed %d times in 3 cycles, want exactly 3", p, counts[p])
		}
	}
	if !(first["a"] < first["c"] && first["c"] < first["b"]) {
		t.Fatalf("trust did not reorder probes: %v", first)
	}
}

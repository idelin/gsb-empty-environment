package paxos

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func setOps(from, to uint64) []Op {
	ops := make([]Op, 0, to-from)
	for i := from + 1; i <= to; i++ {
		ops = append(ops, Op{Kind: OpSet, Key: fmt.Sprintf("k%03d", i), Value: fmt.Sprintf("v%03d", i)})
	}
	return ops
}

func TestCheckpointWriteRestore(t *testing.T) {
	dir := t.TempDir()
	s, err := NewSnapshotter(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	state := map[string]string{"a": "1", "b": "2"}
	if err := s.WriteCheckpoint(10, state); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if snap.Index != 10 || snap.State["a"] != "1" || snap.State["b"] != "2" {
		t.Fatalf("restored %+v, want index 10 with a=1,b=2", snap)
	}
	// Mutating the input after write must not affect the archive.
	state["a"] = "mutated"
	snap, _ = s.Latest()
	if snap.State["a"] == "mutated" {
		t.Fatal("checkpoint aliased the caller's map")
	}
}

func TestIncrementalChainOnCheckpoint(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewSnapshotter(dir, 0)
	if err := s.WriteCheckpoint(5, map[string]string{"base": "0"}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteIncremental(5, 8, setOps(5, 8)); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteIncremental(8, 10, setOps(8, 10)); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if snap.Index != 10 {
		t.Fatalf("index=%d, want 10", snap.Index)
	}
	if snap.State["base"] != "0" || snap.State["k006"] != "v006" || snap.State["k010"] != "v010" {
		t.Fatalf("state missing chained ops: %+v", snap.State)
	}
}

func TestIncrementalChainWithoutCheckpoint(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewSnapshotter(dir, 0)
	if err := s.WriteIncremental(0, 3, setOps(0, 3)); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteIncremental(3, 4, []Op{{Kind: OpDel, Key: "k001"}}); err != nil {
		t.Fatal(err)
	}
	snap, err := s.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if snap.Index != 4 {
		t.Fatalf("index=%d, want 4", snap.Index)
	}
	if _, ok := snap.State["k001"]; ok {
		t.Fatal("delete op not replayed")
	}
	if snap.State["k002"] != "v002" {
		t.Fatalf("state=%+v", snap.State)
	}
}

func TestEmptyArchive(t *testing.T) {
	s, _ := NewSnapshotter(t.TempDir(), 0)
	if _, err := s.Latest(); err != ErrNoSnapshot {
		t.Fatalf("err=%v, want ErrNoSnapshot", err)
	}
}

func TestIncrementalRangeValidation(t *testing.T) {
	s, _ := NewSnapshotter(t.TempDir(), 0)
	if err := s.WriteIncremental(5, 5, nil); err == nil {
		t.Fatal("empty range accepted")
	}
	if err := s.WriteIncremental(6, 5, nil); err == nil {
		t.Fatal("inverted range accepted")
	}
}

func TestPruneRetention(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewSnapshotter(dir, 2) // keep 2 checkpoints
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.WriteCheckpoint(5, map[string]string{"g": "5"}))
	must(s.WriteIncremental(5, 7, setOps(5, 7)))
	must(s.WriteCheckpoint(10, map[string]string{"g": "10"}))
	must(s.WriteIncremental(10, 12, setOps(10, 12)))
	must(s.WriteCheckpoint(15, map[string]string{"g": "15"}))
	must(s.WriteIncremental(15, 16, setOps(15, 16)))

	files, err := s.Files()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(files, " ")
	for _, gone := range []string{checkpointName(5), incrementalName(5, 7)} {
		if strings.Contains(joined, gone) {
			t.Fatalf("pruned file still present: %s in %v", gone, files)
		}
	}
	// inc(10,12] is kept: it is still needed to chain from the retained
	// checkpoint-10. inc(5,7] is pruned: fully covered by checkpoint-10.
	for _, kept := range []string{checkpointName(10), checkpointName(15), incrementalName(10, 12), incrementalName(15, 16)} {
		if !strings.Contains(joined, kept) {
			t.Fatalf("retained file missing: %s in %v", kept, files)
		}
	}

	// Restore still works from the newest checkpoint.
	snap, err := s.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if snap.Index != 16 || snap.State["g"] != "15" || snap.State["k016"] != "v016" {
		t.Fatalf("restored %+v after prune", snap)
	}
}

func TestNoTempFilesRemain(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewSnapshotter(dir, 1)
	if err := s.WriteCheckpoint(1, map[string]string{"a": "b"}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteIncremental(1, 2, setOps(1, 2)); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") || strings.HasPrefix(e.Name(), ".") {
			t.Fatalf("leftover temp file %s", e.Name())
		}
	}
}

func TestCorruptCheckpoint(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewSnapshotter(dir, 0)
	if err := s.WriteCheckpoint(3, map[string]string{"a": "1"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, checkpointName(3)), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Latest(); err == nil {
		t.Fatal("corrupt checkpoint restored without error")
	}
}

func TestCorruptIncremental(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewSnapshotter(dir, 0)
	if err := s.WriteIncremental(0, 2, setOps(0, 2)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, incrementalName(0, 2)), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Latest(); err == nil {
		t.Fatal("corrupt incremental restored without error")
	}
}

// TestConcurrentSnapshotAccess hammers the archiver with concurrent
// checkpoint/incremental writes and restores; run with -race.
func TestConcurrentSnapshotAccess(t *testing.T) {
	dir := t.TempDir()
	s, _ := NewSnapshotter(dir, 3)
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			base := uint64(w * 1000)
			for i := uint64(1); i <= 20; i++ {
				_ = s.WriteIncremental(base+i-1, base+i, setOps(base+i-1, base+i))
			}
		}(w)
	}
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := uint64(1); i <= 10; i++ {
				_ = s.WriteCheckpoint(uint64(w*100)+i, map[string]string{"w": fmt.Sprint(w)})
			}
		}(w)
	}
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				_, _ = s.Latest()
				_, _ = s.Files()
			}
		}()
	}
	wg.Wait()
	if _, err := s.Latest(); err != nil {
		t.Fatalf("final restore after concurrent writes: %v", err)
	}
}

// TestNodeSnapshotLifecycle covers the node-level integration: incremental
// snapshots + checkpoints triggered by apply, log compaction, retention,
// and full recovery onto a fresh node.
func TestNodeSnapshotLifecycle(t *testing.T) {
	dir := t.TempDir()
	cfg := func(int) Config {
		return Config{Lease: defaultLease, IncrEvery: 2, ChkEvery: 6, SnapshotDir: dir, Retain: 2}
	}
	c := NewCluster(3, cfg)
	leader := c.Node(1)
	mustCampaign(t, leader)
	for i := 1; i <= 12; i++ {
		mustPropose(t, leader, Op{Kind: OpSet, Key: fmt.Sprintf("k%02d", i), Value: fmt.Sprintf("v%02d", i)})
	}

	// Checkpoints at 6 and 12; incrementals at 2,4 (pruned), 8,10 (kept).
	files, err := leader.snap.Files()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(files, " ")
	for _, want := range []string{checkpointName(6), checkpointName(12), incrementalName(6, 8), incrementalName(8, 10)} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected archive file %s, have %v", want, files)
		}
	}
	for _, gone := range []string{incrementalName(0, 2), incrementalName(2, 4)} {
		if strings.Contains(joined, gone) {
			t.Fatalf("file %s should have been pruned: %v", gone, files)
		}
	}

	// Log compaction: slots <= 12 must be gone from the leader's log.
	if got := leader.LogLen(); got != 0 {
		t.Fatalf("leader retains %d compacted log slots", got)
	}

	// Recover a brand-new node purely from the archive.
	fresh, err := NewNode(99, nil, NewHub(), Config{SnapshotDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := fresh.Recover(); err != nil {
		t.Fatal(err)
	}
	if got := fresh.CommitIndex(); got != 12 {
		t.Fatalf("recovered commitIndex=%d, want 12", got)
	}
	for i := 1; i <= 12; i++ {
		want := fmt.Sprintf("v%02d", i)
		if v, ok := fresh.KV().Get(fmt.Sprintf("k%02d", i)); !ok || v != want {
			t.Fatalf("recovered state missing k%02d=%s", i, want)
		}
	}
	if !fresh.KV().Equal(leader.KV()) {
		t.Fatal("recovered state diverges from leader")
	}
}

// TestRecoverEmptyArchive: Recover on a fresh archive is a no-op.
func TestRecoverEmptyArchive(t *testing.T) {
	n, err := NewNode(1, nil, NewHub(), Config{SnapshotDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Recover(); err != nil {
		t.Fatal(err)
	}
	if n.CommitIndex() != 0 || n.KV().Applied() != 0 {
		t.Fatal("recover on empty archive changed state")
	}
}

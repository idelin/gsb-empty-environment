package bitmapckpt

import (
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func tmpArchive(t *testing.T) string {
	return filepath.Join(os.TempDir(), "bitmapckpt-test-"+randName()+".bin")
}

var nameMu sync.Mutex
var nameCounter int

func randName() string {
	nameMu.Lock()
	defer nameMu.Unlock()
	nameCounter++
	return string(rune('a'+nameCounter%26)) + string(rune('a'+(nameCounter/26)%26)) + string(rune('a'+(nameCounter/676)%26)) + time.Now().Format("150405.000000")
}

func applyPhase(bm *Bitmap, r *rand.Rand, seed int) {
	for i := 0; i < 200; i++ {
		bm.Add(uint64(r.Intn(1 << 22)))
	}
	base := uint64(r.Intn(1 << 20))
	bm.AddRange(base, base+uint64(1000+seed))
}

// TestSnapshotChainRestore verifies that a checkpoint plus a chain of
// incremental snapshots restores every intermediate state exactly.
func TestSnapshotChainRestore(t *testing.T) {
	r := rand.New(rand.NewSource(21))
	bm := New()

	applyPhase(bm, r, 1)
	cp := bm.Checkpoint()
	state1 := bm.Clone()

	applyPhase(bm, r, 2)
	s1 := bm.Snapshot()
	state2 := bm.Clone()

	applyPhase(bm, r, 3)
	s2 := bm.Snapshot()
	state3 := bm.Clone()

	applyPhase(bm, r, 4)
	s3 := bm.Snapshot()
	state4 := bm.Clone()

	cases := []struct {
		name  string
		snaps []*Snapshot
		want  *Bitmap
	}{
		{"checkpoint only", nil, state1},
		{"checkpoint+s1", []*Snapshot{s1}, state2},
		{"checkpoint+s1+s2", []*Snapshot{s1, s2}, state3},
		{"checkpoint+s1+s2+s3", []*Snapshot{s1, s2, s3}, state4},
	}
	for _, c := range cases {
		got, err := Restore(cp, c.snaps...)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !got.Equals(c.want) {
			t.Fatalf("%s: restored state mismatch (got %d, want %d)",
				c.name, got.Cardinality(), c.want.Cardinality())
		}
	}
}

// TestSnapshotIsolationNoAliasing mutates the source bitmap heavily after
// taking snapshots (forcing container conversions) and verifies that the
// snapshots are not affected: no aliasing leaks through copy-on-write.
func TestSnapshotIsolationNoAliasing(t *testing.T) {
	bm := New()
	r := rand.New(rand.NewSource(31))
	applyPhase(bm, r, 1)
	cp := bm.Checkpoint()
	frozen := bm.Clone()

	// Hammer the same key ranges: array->bitmap conversions, run merges,
	// COW clones. None of it may leak into the checkpoint.
	for i := 0; i < 50; i++ {
		base := uint64(r.Intn(1 << 20))
		bm.AddRange(base, base+uint64(r.Intn(70000)))
		for j := 0; j < 100; j++ {
			bm.Add(uint64(r.Intn(1 << 22)))
		}
	}
	got, err := Restore(cp)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equals(frozen) {
		t.Fatalf("checkpoint state leaked mutations: got %d, want %d",
			got.Cardinality(), frozen.Cardinality())
	}
}

// TestSnapshotFromEmptyChain verifies snapshots work without any checkpoint.
func TestSnapshotFromEmptyChain(t *testing.T) {
	bm := New()
	bm.Add(1)
	bm.Add(2)
	s1 := bm.Snapshot()
	bm.Add(3)
	s2 := bm.Snapshot()
	got, err := Restore(nil, s1, s2)
	if err != nil {
		t.Fatal(err)
	}
	if got.Cardinality() != 3 {
		t.Fatalf("cardinality = %d, want 3", got.Cardinality())
	}
	got1, err := Restore(nil, s1)
	if err != nil {
		t.Fatal(err)
	}
	if got1.Cardinality() != 2 {
		t.Fatalf("cardinality = %d, want 2", got1.Cardinality())
	}
}

// TestBrokenChainRejected verifies chain validation.
func TestBrokenChainRejected(t *testing.T) {
	bm := New()
	bm.Add(1)
	cp := bm.Checkpoint()
	bm.Add(2)
	bm.Snapshot() // s1, discarded
	bm.Add(3)
	s2 := bm.Snapshot() // s2.Base == s1.Gen, not cp.Gen
	if _, err := Restore(cp, s2); err == nil {
		t.Fatal("expected broken chain error")
	}
	if _, err := Restore(nil, s2); err == nil {
		t.Fatal("expected broken chain error for missing base")
	}
}

// TestCheckpointResetsDirty verifies a snapshot right after a checkpoint is
// empty, and only captures subsequent changes.
func TestCheckpointResetsDirty(t *testing.T) {
	bm := New()
	bm.AddRange(0, 100)
	bm.Add(1 << 20)
	bm.Checkpoint()
	s := bm.Snapshot()
	if len(s.Cs) != 0 {
		t.Fatalf("snapshot after checkpoint must be empty, got %d containers", len(s.Cs))
	}
	bm.Add(2 << 20)
	bm.AddRange(3<<16, 3<<16+10)
	s2 := bm.Snapshot()
	if len(s2.Cs) != 2 {
		t.Fatalf("snapshot must contain exactly the 2 dirty containers, got %d", len(s2.Cs))
	}
}

// TestArchiveRoundTrip persists checkpoints and snapshots, then restores.
func TestArchiveRoundTrip(t *testing.T) {
	path := tmpArchive(t)
	defer os.Remove(path)

	bm := New()
	r := rand.New(rand.NewSource(41))
	arc, err := OpenArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	applyPhase(bm, r, 1)
	if err := arc.AppendCheckpoint(bm.Checkpoint()); err != nil {
		t.Fatal(err)
	}
	var snaps []*Snapshot
	for i := 0; i < 5; i++ {
		applyPhase(bm, r, i+2)
		s := bm.Snapshot()
		snaps = append(snaps, s)
		if err := arc.AppendSnapshot(s); err != nil {
			t.Fatal(err)
		}
	}
	if err := arc.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := arc.Close(); err != nil {
		t.Fatal(err)
	}

	cp, gotSnaps, err := ReadArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	if cp == nil {
		t.Fatal("expected a checkpoint")
	}
	if len(gotSnaps) != len(snaps) {
		t.Fatalf("read %d snapshots, want %d", len(gotSnaps), len(snaps))
	}
	for i, s := range gotSnaps {
		if s.Gen != snaps[i].Gen || s.Base != snaps[i].Base {
			t.Fatalf("snapshot %d: gen/base = %d/%d, want %d/%d",
				i, s.Gen, s.Base, snaps[i].Gen, snaps[i].Base)
		}
	}
	restored, err := RestoreFromArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	if !restored.Equals(bm) {
		t.Fatalf("restored bitmap mismatch: got %d, want %d",
			restored.Cardinality(), bm.Cardinality())
	}
}

// TestArchiveKeepsOnlyLatestCheckpoint verifies that records before the
// latest checkpoint are folded away on read.
func TestArchiveKeepsOnlyLatestCheckpoint(t *testing.T) {
	path := tmpArchive(t)
	defer os.Remove(path)

	bm := New()
	arc, err := OpenArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	bm.Add(1)
	arc.AppendCheckpoint(bm.Checkpoint())
	bm.Add(2)
	arc.AppendSnapshot(bm.Snapshot())
	bm.Add(3)
	arc.AppendCheckpoint(bm.Checkpoint()) // folds 1,2,3
	bm.Add(4)
	arc.AppendSnapshot(bm.Snapshot())
	arc.Close()

	cp, snaps, err := ReadArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot after latest checkpoint, got %d", len(snaps))
	}
	restored, err := Restore(cp, snaps...)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Cardinality() != 4 {
		t.Fatalf("cardinality = %d, want 4", restored.Cardinality())
	}
}

// TestArchiveCorruptionDetected flips a payload byte and expects a CRC error.
func TestArchiveCorruptionDetected(t *testing.T) {
	path := tmpArchive(t)
	defer os.Remove(path)

	bm := New()
	bm.AddRange(0, 100000)
	arc, err := OpenArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	arc.AppendCheckpoint(bm.Checkpoint())
	arc.Close()

	data, err := readFileBytes(path)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)/2] ^= 0xff
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := writeFileBytes(path, data); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadArchive(path); err == nil {
		t.Fatal("expected corruption to be detected")
	}
}

// TestArchiveTruncationDetected truncates the file and expects an error.
func TestArchiveTruncationDetected(t *testing.T) {
	path := tmpArchive(t)
	defer os.Remove(path)

	bm := New()
	bm.AddRange(0, 100000)
	arc, err := OpenArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	arc.AppendCheckpoint(bm.Checkpoint())
	arc.Close()

	data, err := readFileBytes(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := writeFileBytes(path, data[:len(data)-3]); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadArchive(path); err == nil {
		t.Fatal("expected truncation to be detected")
	}
}

// TestArchiveBadMagic writes garbage and expects an error.
func TestArchiveBadMagic(t *testing.T) {
	path := tmpArchive(t)
	defer os.Remove(path)
	if err := writeFileBytes(path, []byte("not an archive at all")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadArchive(path); err == nil {
		t.Fatal("expected bad magic error")
	}
}

// TestArchiveClosedErrors verifies use-after-close fails cleanly (no leaks,
// no panics) and Close is idempotent in effect.
func TestArchiveClosedErrors(t *testing.T) {
	path := tmpArchive(t)
	defer os.Remove(path)

	bm := New()
	bm.Add(1)
	arc, err := OpenArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := arc.Close(); err != nil {
		t.Fatal(err)
	}
	if err := arc.AppendCheckpoint(bm.Checkpoint()); err != ErrClosed {
		t.Fatalf("expected ErrClosed, got %v", err)
	}
	if err := arc.AppendSnapshot(bm.Snapshot()); err != ErrClosed {
		t.Fatalf("expected ErrClosed, got %v", err)
	}
	if err := arc.Sync(); err != ErrClosed {
		t.Fatalf("expected ErrClosed, got %v", err)
	}
	if err := arc.Close(); err != ErrClosed {
		t.Fatalf("expected ErrClosed on double close, got %v", err)
	}
}

func readFileBytes(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []byte
	buf := make([]byte, 4096)
	for {
		n, err := f.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			break
		}
	}
	return out, nil
}

func writeFileBytes(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// TestConcurrentSnapshotAndArchive runs writers, a snapshotter and an
// archiver concurrently; the final restored state must equal the source.
func TestConcurrentSnapshotAndArchive(t *testing.T) {
	path := tmpArchive(t)
	defer os.Remove(path)

	bm := New()
	arc, err := OpenArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(100 + id)))
			base := uint64(id) << 24
			for i := 0; i < 3000; i++ {
				bm.Add(base + uint64(r.Intn(1<<22)))
				if i%97 == 0 {
					bm.AddRange(base+uint64(r.Intn(1<<20)), base+uint64(r.Intn(1<<20))+500)
				}
			}
		}(w)
	}
	var swg sync.WaitGroup
	stop := make(chan struct{})
	swg.Add(1)
	go func() {
		defer swg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				s := bm.Snapshot()
				if err := arc.AppendSnapshot(s); err != nil {
					t.Errorf("append snapshot: %v", err)
					return
				}
				time.Sleep(time.Millisecond)
			}
		}
	}()
	wg.Wait()
	close(stop)
	swg.Wait()
	if err := arc.AppendCheckpoint(bm.Checkpoint()); err != nil {
		t.Fatal(err)
	}
	if err := arc.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := RestoreFromArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	if !restored.Equals(bm) {
		t.Fatalf("restored mismatch: got %d, want %d",
			restored.Cardinality(), bm.Cardinality())
	}
}

// TestNoGoroutineOrHandleLeak asserts that snapshots/archives leave no
// goroutines behind and that the archive file is really closed.
func TestNoGoroutineOrHandleLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	path := tmpArchive(t)
	defer os.Remove(path)

	bm := New()
	bm.AddRange(0, 10000)
	arc, err := OpenArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	arc.AppendCheckpoint(bm.Checkpoint())
	arc.AppendSnapshot(bm.Snapshot())
	if err := arc.Close(); err != nil {
		t.Fatal(err)
	}
	// File must be closed: appending must fail with ErrClosed.
	if err := arc.AppendSnapshot(bm.Snapshot()); err != ErrClosed {
		t.Fatalf("expected ErrClosed after Close, got %v", err)
	}
	if _, err := RestoreFromArchive(path); err != nil {
		t.Fatal(err)
	}
	// Let any stray goroutine surface.
	time.Sleep(50 * time.Millisecond)
	after := runtime.NumGoroutine()
	if after > before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}

// TestRestoredBitmapIsIndependent mutates a restored bitmap and verifies the
// source bitmap and the checkpoint are unaffected.
func TestRestoredBitmapIsIndependent(t *testing.T) {
	bm := New()
	bm.AddRange(0, 50000)
	cp := bm.Checkpoint()
	snapCard := cp.Cs[0].cardinality()

	restored, err := Restore(cp)
	if err != nil {
		t.Fatal(err)
	}
	restored.AddRange(100000, 200000)
	restored.Add(1 << 40)

	if bm.Cardinality() != 50000 {
		t.Fatalf("source bitmap polluted: %d", bm.Cardinality())
	}
	if cp.Cs[0].cardinality() != snapCard {
		t.Fatal("checkpoint container polluted by restored bitmap")
	}
}

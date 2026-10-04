package snapshot

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func applyN(t *testing.T, m *Machine, from, to uint64, prefix string) {
	t.Helper()
	for i := from; i <= to; i++ {
		if err := m.Apply(i, 1, Put(fmt.Sprintf("%s-%d", prefix, i), fmt.Sprintf("v%d", i))); err != nil {
			t.Fatalf("apply %d: %v", i, err)
		}
	}
}

func mustCapture(t *testing.T, s *Snapshotter) SegmentMeta {
	t.Helper()
	meta, err := s.CaptureIncremental()
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	return meta
}

func assertMachinesEqual(t *testing.T, want, got *Machine) {
	t.Helper()
	w, g := want.Keys(), got.Keys()
	if len(w) != len(g) {
		t.Fatalf("key count %d, want %d", len(g), len(w))
	}
	for k, v := range w {
		if g[k] != v {
			t.Fatalf("key %q = %q, want %q", k, g[k], v)
		}
	}
	if want.LastIndex() != got.LastIndex() {
		t.Fatalf("lastIndex %d, want %d", got.LastIndex(), want.LastIndex())
	}
}

// archiveFactories runs a test against both archive implementations.
func archiveFactories(t *testing.T) map[string]func() Archive {
	dir := t.TempDir()
	return map[string]func() Archive{
		"mem": func() Archive { return NewMemArchive() },
		"file": func() Archive {
			a, err := NewFileArchive(filepath.Join(dir, "a"))
			if err != nil {
				t.Fatal(err)
			}
			return a
		},
	}
}

func TestIncrementalCaptureAndRestore(t *testing.T) {
	for name, newArch := range archiveFactories(t) {
		t.Run(name, func(t *testing.T) {
			arch := newArch()
			m := NewMachine()
			s := NewSnapshotter(m, arch)

			applyN(t, m, 1, 10, "k")
			meta := mustCapture(t, s)
			if meta.Base != 0 || meta.Last != 10 || meta.Empty {
				t.Fatalf("meta = %+v, want {Base:0 Last:10}", meta)
			}
			applyN(t, m, 11, 25, "k")
			if err := m.Apply(26, 1, Del("k-1")); err != nil {
				t.Fatal(err)
			}
			meta = mustCapture(t, s)
			if meta.Base != 10 || meta.Last != 26 {
				t.Fatalf("meta = %+v, want {Base:10 Last:26}", meta)
			}

			fresh := NewMachine()
			if err := Restore(arch, fresh); err != nil {
				t.Fatalf("restore: %v", err)
			}
			assertMachinesEqual(t, m, fresh)
			if _, ok := fresh.Get("k-1"); ok {
				t.Fatal("deleted key resurrected after restore")
			}
		})
	}
}

func TestEmptyCaptureWritesNothing(t *testing.T) {
	for name, newArch := range archiveFactories(t) {
		t.Run(name, func(t *testing.T) {
			arch := newArch()
			m := NewMachine()
			s := NewSnapshotter(m, arch)
			applyN(t, m, 1, 5, "k")
			mustCapture(t, s)
			meta := mustCapture(t, s) // no changes since
			if !meta.Empty {
				t.Fatalf("meta = %+v, want Empty", meta)
			}
			segs, err := arch.Segments()
			if err != nil {
				t.Fatal(err)
			}
			if len(segs) != 1 {
				t.Fatalf("segments = %d, want 1 (empty capture skipped)", len(segs))
			}
		})
	}
}

func TestCheckpointGCsCoveredSegments(t *testing.T) {
	for name, newArch := range archiveFactories(t) {
		t.Run(name, func(t *testing.T) {
			arch := newArch()
			m := NewMachine()
			s := NewSnapshotter(m, arch)

			applyN(t, m, 1, 10, "a")
			mustCapture(t, s)
			applyN(t, m, 11, 20, "b")
			mustCapture(t, s)

			cp, err := s.Checkpoint()
			if err != nil {
				t.Fatalf("checkpoint: %v", err)
			}
			if cp.LastIndex != 20 {
				t.Fatalf("checkpoint index %d, want 20", cp.LastIndex)
			}
			segs, err := arch.Segments()
			if err != nil {
				t.Fatal(err)
			}
			if len(segs) != 0 {
				t.Fatalf("segments after GC = %v, want none", segs)
			}

			// New increments chain on top of the checkpoint.
			applyN(t, m, 21, 30, "c")
			meta := mustCapture(t, s)
			if meta.Base != 20 {
				t.Fatalf("segment base %d, want 20 (chained on checkpoint)", meta.Base)
			}

			fresh := NewMachine()
			if err := Restore(arch, fresh); err != nil {
				t.Fatalf("restore: %v", err)
			}
			assertMachinesEqual(t, m, fresh)
		})
	}
}

func TestRestoreEmptyArchive(t *testing.T) {
	for name, newArch := range archiveFactories(t) {
		t.Run(name, func(t *testing.T) {
			fresh := NewMachine()
			if err := Restore(newArch(), fresh); err != nil {
				t.Fatalf("restore empty archive: %v", err)
			}
			if fresh.LastIndex() != 0 || fresh.Len() != 0 {
				t.Fatalf("machine not empty after empty restore")
			}
		})
	}
}

func TestRestoreDetectsGap(t *testing.T) {
	arch := NewMemArchive()
	m := NewMachine()
	s := NewSnapshotter(m, arch)
	applyN(t, m, 1, 10, "k")
	mustCapture(t, s) // segment 0..10
	applyN(t, m, 11, 20, "k")
	mustCapture(t, s) // segment 10..20

	// Remove the middle link: keep only 10..20 with no checkpoint at 10.
	arch.DeleteSegmentsBefore(10)
	fresh := NewMachine()
	if err := Restore(arch, fresh); !errors.Is(err, ErrGap) {
		t.Fatalf("restore with gap = %v, want ErrGap", err)
	}
}

func TestRestoreSegmentSpanningCheckpoint(t *testing.T) {
	// A segment whose Base predates the checkpoint but whose Last exceeds it
	// must still restore correctly (its changes are idempotent on top).
	arch := NewMemArchive()
	m := NewMachine()
	s := NewSnapshotter(m, arch)
	applyN(t, m, 1, 12, "k")
	mustCapture(t, s) // segment 0..12

	// Hand-craft a checkpoint at index 10 (as if taken mid-chain).
	cpData := map[string]string{}
	for i := 1; i <= 10; i++ {
		cpData[fmt.Sprintf("k-%d", i)] = fmt.Sprintf("v%d", i)
	}
	if err := arch.PutCheckpoint(Checkpoint{LastIndex: 10, LastTerm: 1, Data: cpData}); err != nil {
		t.Fatal(err)
	}

	fresh := NewMachine()
	if err := Restore(arch, fresh); err != nil {
		t.Fatalf("restore: %v", err)
	}
	assertMachinesEqual(t, m, fresh)
}

func TestImmutableSegmentsAndCheckpoints(t *testing.T) {
	for name, newArch := range archiveFactories(t) {
		t.Run(name, func(t *testing.T) {
			arch := newArch()
			seg := Segment{Base: 0, Last: 5, Changes: map[string]Change{"a": {Value: "1"}}}
			if err := arch.PutSegment(seg); err != nil {
				t.Fatal(err)
			}
			if err := arch.PutSegment(seg); !errors.Is(err, ErrExists) {
				t.Fatalf("rewrite segment = %v, want ErrExists", err)
			}
			cp := Checkpoint{LastIndex: 5, Data: map[string]string{"a": "1"}}
			if err := arch.PutCheckpoint(cp); err != nil {
				t.Fatal(err)
			}
			if err := arch.PutCheckpoint(cp); !errors.Is(err, ErrExists) {
				t.Fatalf("rewrite checkpoint = %v, want ErrExists", err)
			}
			if _, err := arch.GetSegment(999); !errors.Is(err, ErrNotFound) {
				t.Fatalf("missing segment = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestFileArchiveDetectsCorruption(t *testing.T) {
	dir := t.TempDir()
	arch, err := NewFileArchive(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := NewMachine()
	s := NewSnapshotter(m, arch)
	applyN(t, m, 1, 5, "k")
	mustCapture(t, s)

	// Corrupt the stored checksum digit so integrity verification fails.
	segs, _ := arch.Segments()
	path := filepath.Join(dir, segmentName(segs[0].Base, segs[0].Last))
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	i := bytes.Index(b, []byte(`"sum":`)) + len(`"sum":`)
	for b[i+1] >= '0' && b[i+1] <= '9' { // last digit of the sum
		i++
	}
	if b[i] == '9' {
		b[i] = '8'
	} else {
		b[i]++
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}

	fresh := NewMachine()
	if err := Restore(arch, fresh); !errors.Is(err, ErrChecksum) {
		t.Fatalf("restore corrupted = %v, want ErrChecksum", err)
	}
}

func TestCaptureRollbackOnArchiveFailure(t *testing.T) {
	arch := NewMemArchive()
	m := NewMachine()
	good := NewSnapshotter(m, arch)
	applyN(t, m, 1, 5, "k")
	mustCapture(t, good) // segment 0..5 archived

	// Poison the archive so the next PutSegment fails (base 0..5 exists is
	// not it; instead pre-occupy base 5).
	if err := arch.PutSegment(Segment{Base: 5, Last: 5, Changes: map[string]Change{"x": {Value: "y"}}}); err != nil {
		t.Fatal(err)
	}
	applyN(t, m, 6, 8, "k")
	if _, err := good.CaptureIncremental(); !errors.Is(err, ErrExists) {
		t.Fatalf("capture = %v, want ErrExists", err)
	}
	// Dirty set rolled back: a retry against a fresh archive captures 5..8.
	arch2 := NewMemArchive()
	if err := arch2.PutSegment(Segment{Base: 0, Last: 5, Changes: map[string]Change{"k-1": {Value: "v1"}, "k-2": {Value: "v2"}, "k-3": {Value: "v3"}, "k-4": {Value: "v4"}, "k-5": {Value: "v5"}}}); err != nil {
		t.Fatal(err)
	}
	retry := NewSnapshotter(m, arch2)
	meta, err := retry.CaptureIncremental()
	if err != nil {
		t.Fatalf("retry capture: %v", err)
	}
	if meta.Base != 5 || meta.Last != 8 {
		t.Fatalf("retry meta = %+v, want {Base:5 Last:8}", meta)
	}
	fresh := NewMachine()
	if err := Restore(arch2, fresh); err != nil {
		t.Fatal(err)
	}
	assertMachinesEqual(t, m, fresh)
}

func TestMachineApplyOrderEnforced(t *testing.T) {
	m := NewMachine()
	if err := m.Apply(2, 1, Put("a", "1")); !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("apply(2) on empty = %v, want ErrOutOfOrder", err)
	}
	if err := m.Apply(1, 1, Put("a", "1")); err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(1, 1, Put("b", "2")); !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("re-apply(1) = %v, want ErrOutOfOrder", err)
	}
}

func TestRestoreIntoNonEmptyMachineRejected(t *testing.T) {
	arch := NewMemArchive()
	m := NewMachine()
	s := NewSnapshotter(m, arch)
	applyN(t, m, 1, 3, "k")
	mustCapture(t, s)
	if err := Restore(arch, m); err == nil {
		t.Fatal("restore into non-empty machine must fail")
	}
}

// Heavy concurrency: many writers apply while a capturer runs. The
// restored state must exactly match the source machine.
func TestConcurrentApplyCaptureCheckpoint(t *testing.T) {
	arch := NewMemArchive()
	m := NewMachine()
	s := NewSnapshotter(m, arch)

	const writers = 4
	const perWriter = 250
	// Applies are serialized (as a real commit engine would drive them) but
	// race with concurrent captures.
	var index uint64
	var applyMu sync.Mutex
	applyNext := func(op Op) error {
		applyMu.Lock()
		defer applyMu.Unlock()
		index++
		return m.Apply(index, 1, op)
	}

	stop := make(chan struct{})
	var capWg sync.WaitGroup
	capWg.Add(1)
	go func() { // capturer
		defer capWg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if _, err := s.CaptureIncremental(); err != nil {
					t.Errorf("capture: %v", err)
					return
				}
			}
		}
	}()

	var writersWg sync.WaitGroup
	for w := 0; w < writers; w++ {
		writersWg.Add(1)
		go func(w int) {
			defer writersWg.Done()
			for i := 0; i < perWriter; i++ {
				op := Put(fmt.Sprintf("w%d-k%d", w, i), "v")
				if i%7 == 3 { // exercise deletes too
					op = Del(fmt.Sprintf("w%d-k%d", w, i/2))
				}
				if err := applyNext(op); err != nil {
					t.Errorf("apply: %v", err)
					return
				}
			}
		}(w)
	}
	writersWg.Wait()
	close(stop)
	capWg.Wait()

	// Final capture + checkpoint must leave a restorable archive.
	if _, err := s.CaptureIncremental(); err != nil {
		t.Fatalf("final capture: %v", err)
	}
	if _, err := s.Checkpoint(); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}
	applyN(t, m, m.LastIndex()+1, m.LastIndex()+10, "tail")
	if _, err := s.CaptureIncremental(); err != nil {
		t.Fatalf("tail capture: %v", err)
	}

	fresh := NewMachine()
	if err := Restore(arch, fresh); err != nil {
		t.Fatalf("restore: %v", err)
	}
	assertMachinesEqual(t, m, fresh)
}

// No goroutine or file leaks: the package spawns no background goroutines
// and atomic writes leave no temp files behind.
func TestNoLeaks(t *testing.T) {
	before := runtime.NumGoroutine()
	dir := t.TempDir()
	arch, err := NewFileArchive(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := NewMachine()
	s := NewSnapshotter(m, arch)
	applyN(t, m, 1, 50, "k")
	for i := 0; i < 5; i++ {
		if _, err := s.CaptureIncremental(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	fresh := NewMachine()
	if err := Restore(arch, fresh); err != nil {
		t.Fatal(err)
	}
	assertMachinesEqual(t, m, fresh)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}
	if len(entries) != 1 { // only the checkpoint survives GC
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("archive dir = %v, want exactly the checkpoint", names)
	}
	if after := runtime.NumGoroutine(); after != before {
		t.Fatalf("goroutine leak: before=%d after=%d", before, after)
	}
}

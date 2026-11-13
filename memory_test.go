package netsec

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"
)

func testSource(vm VMID, instance InstanceID, opts ...MemoryOption) *Memory {
	opts = append([]MemoryOption{WithSourceInstance(instance)}, opts...)
	return NewMemory(vm, opts...)
}

func testTarget(vm VMID, instance InstanceID, opts ...TargetOption) *Target {
	return NewTarget(vm, instance, opts...)
}

func testSigner(t *testing.T) *Signer {
	t.Helper()
	s, err := NewSigner(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mustWrite(t *testing.T, m *Memory, page PageID, data []byte) Generation {
	t.Helper()
	gen, err := m.WritePage(page, data)
	if err != nil {
		t.Fatalf("write page %d: %v", page, err)
	}
	return gen
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func TestMemoryLimitAndAtomicWriteMetadata(t *testing.T) {
	m := testSource("vm-a", "src-a")
	if _, err := m.WritePage(MaxPages, []byte("x")); !errors.Is(err, ErrPageOutOfRange) {
		t.Fatalf("page 64 error = %v", err)
	}
	if _, err := m.WritePage(63, []byte("last")); err != nil {
		t.Fatalf("page 63: %v", err)
	}
	gen, err := m.WritePage(7, []byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	if gen != 1 {
		t.Fatalf("first generation = %d, want 1", gen)
	}
	info, err := m.PageInfo(7)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Valid || !info.Dirty || info.VM != "vm-a" {
		t.Fatalf("unexpected page info: %+v", info)
	}
	if want := DigestBytes([]byte("first")); info.Digest != want {
		t.Fatalf("digest = %s, want %s", info.Digest, want)
	}
	gen, err = m.WritePage(7, []byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	if gen != 2 {
		t.Fatalf("second generation = %d, want 2", gen)
	}
	got, err := m.ReadPage(7)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("second")) {
		t.Fatalf("read = %q", got)
	}
}

func TestDirtyClearRequiresExactGeneration(t *testing.T) {
	m := testSource("vm-a", "src-a")
	gen := mustWrite(t, m, 3, []byte("v1"))
	snap, err := m.SnapshotDirty()
	if err != nil {
		t.Fatal(err)
	}
	if snap.Epoch != 1 || len(snap.Pages) != 1 || snap.Pages[0].Generation != gen {
		t.Fatalf("bad snapshot: %+v", snap)
	}

	// A write lands between snapshoting and acknowledging the copy.
	mustWrite(t, m, 3, []byte("v2"))
	cleared, err := m.AcknowledgeCopied(3, gen)
	if err != nil {
		t.Fatal(err)
	}
	if cleared {
		t.Fatal("dirty was cleared even though the source generation advanced")
	}
	info, _ := m.PageInfo(3)
	if !info.Dirty || info.Generation != 2 {
		t.Fatalf("race lost dirty bit or generation: %+v", info)
	}
	if pages := m.DirtyPages(); len(pages) != 1 || pages[0] != 3 {
		t.Fatalf("dirty pages = %v", pages)
	}

	// The correct generation retires the bit.
	cleared, err = m.AcknowledgeCopied(3, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !cleared {
		t.Fatal("matching generation did not clear dirty")
	}
	info, _ = m.PageInfo(3)
	if info.Dirty {
		t.Fatal("dirty remained after matching acknowledgement")
	}
}

func TestRepeatedSnapshotsDuplicateRounds(t *testing.T) {
	m := testSource("vm-a", "src-a")
	mustWrite(t, m, 1, []byte("one"))
	s1, err := m.SnapshotDirty()
	if err != nil {
		t.Fatal(err)
	}
	cleared, err := m.AcknowledgeCopied(1, s1.Pages[0].Generation)
	if err != nil || !cleared {
		t.Fatalf("ack = %v, %v", cleared, err)
	}
	s2, err := m.SnapshotDirty()
	if err != nil {
		t.Fatal(err)
	}
	if s2.Epoch <= s1.Epoch {
		t.Fatalf("epoch did not advance: %d -> %d", s1.Epoch, s2.Epoch)
	}
	if len(s2.Pages) != 0 {
		t.Fatalf("duplicate round had dirty pages: %+v", s2.Pages)
	}
	s3, err := m.SnapshotDirty()
	if err != nil {
		t.Fatal(err)
	}
	if s3.Epoch <= s2.Epoch || len(s3.Pages) != 0 {
		t.Fatalf("third snapshot = %+v", s3)
	}
}

func TestFreezeWaitsForAdmittedWriteAndBlocksNewWrites(t *testing.T) {
	entered := make(chan WriteAdmission, 1)
	release := make(chan struct{})
	var once sync.Once
	m := testSource("vm-a", "src-a", WithWriteBarrier(func(a WriteAdmission) {
		once.Do(func() {
			entered <- a
			<-release
		})
	}))

	type writeResult struct {
		gen Generation
		err error
	}
	writeDone := make(chan writeResult, 1)
	go func() {
		gen, err := m.WritePage(5, []byte("in-flight"))
		writeDone <- writeResult{gen: gen, err: err}
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("write was not admitted")
	}

	frozen := make(chan FreezePoint, 1)
	freezeErr := make(chan error, 1)
	go func() {
		fp, err := m.Freeze()
		frozen <- fp
		freezeErr <- err
	}()
	waitFor(t, "freeze gate", m.Frozen)
	if _, err := m.WritePage(6, []byte("blocked")); !errors.Is(err, ErrFrozen) {
		t.Fatalf("post-freeze write error = %v", err)
	}

	close(release)
	res := <-writeDone
	if res.err != nil || res.gen != 1 {
		t.Fatalf("in-flight write = %+v", res)
	}
	fp := <-frozen
	if err := <-freezeErr; err != nil {
		t.Fatal(err)
	}
	if fp.All[5].Generation != 1 || !fp.All[5].Dirty {
		t.Fatalf("freeze omitted admitted write: %+v", fp.All[5])
	}
	if len(fp.Dirty) != 1 || fp.Dirty[0].Page != 5 {
		t.Fatalf("freeze dirty set = %+v", fp.Dirty)
	}
	if err := m.Thaw(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.WritePage(5, []byte("after-thaw")); err != nil {
		t.Fatalf("thawed source write: %v", err)
	}
}

func TestConcurrentWritesHaveUniqueGenerations(t *testing.T) {
	m := testSource("vm-a", "src-a")
	const n = 64
	reqs := make([]WriteRequest, n)
	for i := range reqs {
		reqs[i] = WriteRequest{Page: 9, Data: []byte{byte(i)}}
	}
	out := ConcurrentWrites(m, reqs...)
	seen := make(map[Generation]bool)
	var max Generation
	for i, o := range out {
		if o.Err != nil {
			t.Fatalf("write %d: %v", i, o.Err)
		}
		if seen[o.Generation] {
			t.Fatalf("duplicate generation %d", o.Generation)
		}
		seen[o.Generation] = true
		if o.Generation > max {
			max = o.Generation
		}
	}
	if max != n {
		t.Fatalf("max generation = %d, want %d", max, n)
	}
	info, _ := m.PageInfo(9)
	if info.Generation != n || !info.Dirty {
		t.Fatalf("final page state = %+v", info)
	}
}

func TestConcurrentWritesVersusFreeze(t *testing.T) {
	m := testSource("vm-a", "src-a")
	const n = 32
	reqs := make([]WriteRequest, n)
	for i := range reqs {
		reqs[i] = WriteRequest{Page: 2, Data: []byte{byte(i), byte(i >> 8)}}
	}
	outCh := make(chan []WriteOutcome, 1)
	go func() { outCh <- ConcurrentWrites(m, reqs...) }()

	fpCh := make(chan FreezePoint, 1)
	errCh := make(chan error, 1)
	go func() {
		fp, err := m.Freeze()
		fpCh <- fp
		errCh <- err
	}()

	out := <-outCh
	fp := <-fpCh
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	var max Generation
	for i, o := range out {
		switch {
		case o.Err == nil:
			if o.Generation > max {
				max = o.Generation
			}
		case errors.Is(o.Err, ErrFrozen):
			// Blocked at the freeze gate; never applied.
		default:
			t.Fatalf("write %d unexpected error: %v", i, o.Err)
		}
	}
	if fp.All[2].Generation != max {
		t.Fatalf("freeze generation %d != max admitted write %d", fp.All[2].Generation, max)
	}
	if max > 0 {
		data, _ := m.ReadPage(2)
		if DigestBytes(data) != fp.All[2].Digest {
			t.Fatalf("freeze digest does not match final bytes")
		}
	}
}

func TestSourceCannotReleaseBeforeConfirmation(t *testing.T) {
	m := testSource("vm-a", "src-a")
	mustWrite(t, m, 0, []byte("x"))
	if _, err := m.Freeze(); err != nil {
		t.Fatal(err)
	}
	if err := m.Release(); !errors.Is(err, ErrNotTerminated) {
		t.Fatalf("release before confirmation = %v", err)
	}
	if err := m.Thaw(); err != nil {
		t.Fatal(err)
	}
	if err := m.Release(); !errors.Is(err, ErrNotTerminated) {
		t.Fatalf("release after thaw = %v", err)
	}
}

func TestConcurrentFreezeProducesOneEpoch(t *testing.T) {
	m := testSource("vm-a", "src-a")
	mustWrite(t, m, 0, []byte("x"))
	const n = 8
	start := make(chan struct{})
	results := make(chan FreezePoint, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			<-start
			fp, err := m.Freeze()
			results <- fp
			errs <- err
		}()
	}
	close(start)
	var epoch Epoch
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
		fp := <-results
		if epoch == 0 {
			epoch = fp.Epoch
		}
		if fp.Epoch != epoch {
			t.Fatalf("freeze returned different epochs: %d vs %d", fp.Epoch, epoch)
		}
	}
	if epoch != 1 {
		t.Fatalf("freeze advanced epoch %d times", epoch)
	}
}

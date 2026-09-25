package slotstore

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempDir(t *testing.T) string {
	dir, err := ioutil.TempDir("", "slotstore-test-")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func openTest(t *testing.T, dir string, slots, cap uint32, inj FaultInjector) (*Store, *RecoveryReport) {
	s, rep, err := Open(dir, Config{SlotCount: slots, PayloadCap: cap, Injector: inj})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return s, rep
}

// --- generational handles -------------------------------------------------

func TestPutGetRoundtrip(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	s, _ := openTest(t, dir, 8, 64, nil)
	h, err := s.Put([]byte("hello world"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(h)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello world" {
		t.Fatalf("got %q", got)
	}
	s.Close()

	// Survives a clean restart.
	s2, rep := openTest(t, dir, 8, 64, nil)
	defer s2.Close()
	if len(rep.ScrubbedSlots) != 0 || len(rep.Repairs) != 0 {
		t.Fatalf("clean reopen should need no repairs, got %+v", rep)
	}
	got, err = s2.Get(h)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello world" {
		t.Fatalf("after reopen got %q", got)
	}
}

func TestDeleteReuseSlotBumpsGeneration(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	s, _ := openTest(t, dir, 4, 64, nil)
	defer s.Close()

	h0, err := s.Put([]byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(h0); err != nil {
		t.Fatal(err)
	}
	// Old handle is dead immediately after delete.
	if _, err := s.Get(h0); err != ErrStaleHandle {
		t.Fatalf("expected ErrStaleHandle after delete, got %v", err)
	}
	// LIFO free chain: next Put reuses the same slot with gen+1.
	h1, err := s.Put([]byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	if h1.Slot != h0.Slot {
		t.Fatalf("expected slot reuse: h0=%+v h1=%+v", h0, h1)
	}
	if h1.Gen != h0.Gen+1 {
		t.Fatalf("expected generation bump: h0=%+v h1=%+v", h0, h1)
	}
	if _, err := s.Get(h0); err != ErrStaleHandle {
		t.Fatalf("old handle must not see new object, got %v", err)
	}
	got, err := s.Get(h1)
	if err != nil || string(got) != "second" {
		t.Fatalf("new handle broken: %q %v", got, err)
	}
	// Delete via stale handle must fail too.
	if err := s.Delete(h0); err != ErrStaleHandle {
		t.Fatalf("delete with stale handle: got %v", err)
	}
}

func TestStoreFull(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	s, _ := openTest(t, dir, 2, 16, nil)
	defer s.Close()
	if _, err := s.Put([]byte("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put([]byte("b")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put([]byte("c")); err != ErrStoreFull {
		t.Fatalf("expected ErrStoreFull, got %v", err)
	}
}

// --- free chain validation ------------------------------------------------

// rewriteSlot rewrites slot i on disk with the given contents (test helper).
func rewriteSlot(t *testing.T, dir string, i uint32, payloadCap uint32, sl slot) {
	f, err := os.OpenFile(filepath.Join(dir, dataFileName), os.O_RDWR, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	stride := int64(slotOverhead + payloadCap)
	buf := make([]byte, stride)
	encodeSlot(buf, sl, payloadCap)
	if _, err := f.WriteAt(buf, int64(i)*stride); err != nil {
		t.Fatal(err)
	}
	f.Sync()
}

func TestFreeChainCycleIsError(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	s, _ := openTest(t, dir, 4, 64, nil)
	s.Close()

	// Make slot 0 (chain head) point at itself: 0 -> 0 -> ...
	rewriteSlot(t, dir, 0, 64, slot{state: stateFree, gen: 0, next: 0, prev: -1})

	_, _, err := Open(dir, Config{})
	if err == nil {
		t.Fatal("expected corruption error for cyclic free chain")
	}
	ce, ok := err.(*CorruptError)
	if !ok {
		t.Fatalf("expected *CorruptError, got %T: %v", err, err)
	}
	if !strings.Contains(ce.Reason, "cycle") {
		t.Fatalf("error should mention cycle: %v", ce)
	}
}

func TestBacklinkMismatchRepaired(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	s, _ := openTest(t, dir, 4, 64, nil)
	s.Close()

	// Slot 1's backlink claims prev=3 instead of 0.
	rewriteSlot(t, dir, 1, 64, slot{state: stateFree, gen: 0, next: 2, prev: 3})

	s2, rep := openTest(t, dir, 4, 64, nil)
	defer s2.Close()
	if len(rep.Repairs) == 0 {
		t.Fatal("expected backlink repair to be reported")
	}
	found := false
	for _, r := range rep.Repairs {
		if strings.Contains(r, "backlink") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected backlink repair, got %v", rep.Repairs)
	}
	// Store still fully usable: all 4 slots available.
	for i := 0; i < 4; i++ {
		if _, err := s2.Put([]byte("x")); err != nil {
			t.Fatalf("put %d after repair: %v", i, err)
		}
	}
}

// --- torn / half-written slots ---------------------------------------------

func TestBadTrailerChecksumNotAValidRecord(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	s, _ := openTest(t, dir, 4, 64, nil)
	h, err := s.Put([]byte("important record"))
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	// Corrupt the CRC trailer of the occupied slot.
	f, err := os.OpenFile(filepath.Join(dir, dataFileName), os.O_RDWR, 0644)
	if err != nil {
		t.Fatal(err)
	}
	stride := int64(slotOverhead + 64)
	off := int64(h.Slot)*stride + stride - 1
	b := []byte{0}
	if _, err := f.ReadAt(b, off); err != nil {
		t.Fatal(err)
	}
	b[0] ^= 0xFF
	if _, err := f.WriteAt(b, off); err != nil {
		t.Fatal(err)
	}
	f.Sync()
	f.Close()

	s2, rep := openTest(t, dir, 4, 64, nil)
	defer s2.Close()
	// The slot must not come back as a valid record.
	if _, err := s2.Get(h); err != ErrStaleHandle {
		t.Fatalf("corrupt slot must not serve records, got %v", err)
	}
	if len(rep.ScrubbedSlots) != 1 || rep.ScrubbedSlots[0] != h.Slot {
		t.Fatalf("expected slot %d scrubbed, got %v", h.Slot, rep.ScrubbedSlots)
	}
	// And it returns to the free pool.
	if _, err := s2.Put([]byte("reused")); err != nil {
		t.Fatal(err)
	}
}

func TestHalfWrittenSlotScrubbed(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	s, _ := openTest(t, dir, 4, 64, nil)
	s.Close()

	// Simulate a slot left in state=writing (valid CRC, half-committed).
	rewriteSlot(t, dir, 2, 64, slot{state: stateWriting, gen: 7, payload: []byte("partial")})

	s2, rep := openTest(t, dir, 4, 64, nil)
	defer s2.Close()
	if len(rep.ScrubbedSlots) != 1 || rep.ScrubbedSlots[0] != 2 {
		t.Fatalf("expected slot 2 scrubbed, got %v", rep.ScrubbedSlots)
	}
	// Slot 2 is free again; all 4 slots usable.
	for i := 0; i < 4; i++ {
		if _, err := s2.Put([]byte("x")); err != nil {
			t.Fatalf("put %d: %v", i, err)
		}
	}
}

// --- crash recovery via fault injection -------------------------------------

type crashAt struct {
	point FaultPoint
	fired bool
}

type crashError struct{}

func (crashError) Error() string { return "injected crash" }

func (c *crashAt) Inject(p FaultPoint) error {
	if p == c.point && !c.fired {
		c.fired = true
		return crashError{}
	}
	return nil
}

func TestCrashAfterSlotSyncBeforeSuperblock(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	inj := &crashAt{point: FaultAfterSlotSync}
	s, _ := openTest(t, dir, 4, 64, inj)

	_, err := s.Put([]byte("committed-bytes"))
	if _, ok := err.(crashError); !ok {
		t.Fatalf("expected injected crash, got %v", err)
	}
	// Store is now "dead"; any further op reports the crash.
	if _, err := s.Put([]byte("x")); err != ErrCrashed {
		t.Fatalf("expected ErrCrashed, got %v", err)
	}

	// Recover: slot says occupied, superblock still lists it free.
	s2, rep := openTest(t, dir, 4, 64, nil)
	defer s2.Close()
	if len(rep.Repairs) == 0 {
		t.Fatal("expected a repair for occupied-but-on-free-chain slot")
	}
	// The record is fully intact and readable (slot is ground truth).
	got, err := s2.Get(Handle{Slot: 0, Gen: 0})
	if err != nil || string(got) != "committed-bytes" {
		t.Fatalf("recovered record: %q %v", got, err)
	}
	// Free chain consistent: exactly 3 slots remain.
	for i := 0; i < 3; i++ {
		if _, err := s2.Put([]byte("y")); err != nil {
			t.Fatalf("put %d: %v", i, err)
		}
	}
	if _, err := s2.Put([]byte("z")); err != ErrStoreFull {
		t.Fatalf("expected full after 3 puts, got %v", err)
	}
}

func TestCrashBeforeSlotWriteLeavesNoTrace(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	inj := &crashAt{point: FaultBeforeSlotWrite}
	s, _ := openTest(t, dir, 4, 64, inj)
	s.Put([]byte("never-written"))

	s2, rep := openTest(t, dir, 4, 64, nil)
	defer s2.Close()
	if len(rep.ScrubbedSlots) != 0 || len(rep.Repairs) != 0 {
		t.Fatalf("crash before write should be clean, got %+v", rep)
	}
	count := 0
	s2.ForEach(func(h Handle, p []byte) { count++ })
	if count != 0 {
		t.Fatalf("no record should exist, found %d", count)
	}
}

func TestDeleteCrashBetweenSlotAndSuperblock(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	s, _ := openTest(t, dir, 4, 64, nil)
	h, _ := s.Put([]byte("doomed"))
	s.Close()

	// Reopen with an injector that kills Delete after the slot is written
	// free but before the superblock free-head is updated.
	inj := &crashAt{point: FaultAfterSlotSync}
	s2, _ := openTest(t, dir, 4, 64, inj)
	if err := s2.Delete(h); err == nil {
		t.Fatal("expected injected crash")
	}

	s3, rep := openTest(t, dir, 4, 64, nil)
	defer s3.Close()
	if len(rep.Repairs) == 0 {
		t.Fatal("expected repair for free-but-unchained slot")
	}
	// Slot was written free with bumped generation: old handle stays dead.
	if _, err := s3.Get(h); err != ErrStaleHandle {
		t.Fatalf("stale handle after crashed delete: %v", err)
	}
	// Slot is back in the free pool and reusable with gen+1.
	h2, err := s3.Put([]byte("reborn"))
	if err != nil {
		t.Fatal(err)
	}
	if h2.Slot != h.Slot || h2.Gen != h.Gen+1 {
		t.Fatalf("expected slot %d gen %d, got %+v", h.Slot, h.Gen+1, h2)
	}
}

func TestFaultPointsFireInOrder(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	var seen []FaultPoint
	inj := injectorFunc(func(p FaultPoint) error {
		seen = append(seen, p)
		return nil
	})
	s, _ := openTest(t, dir, 4, 64, inj)
	defer s.Close()
	if _, err := s.Put([]byte("x")); err != nil {
		t.Fatal(err)
	}
	want := []FaultPoint{FaultBeforeSlotWrite, FaultAfterSlotWrite, FaultAfterSlotSync, FaultAfterSuperSync}
	if len(seen) != len(want) {
		t.Fatalf("points = %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("points = %v, want %v", seen, want)
		}
	}
}

type injectorFunc func(FaultPoint) error

func (f injectorFunc) Inject(p FaultPoint) error { return f(p) }

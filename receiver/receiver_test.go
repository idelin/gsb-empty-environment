package receiver

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
)

// fob simulates a rolling-code transmitter.
type fob struct {
	key []byte
	id  uint64
	ctr uint32
}

func newFob(id uint64, key []byte) *fob { return &fob{id: id, key: key} }

// press emits the next counter.
func (f *fob) press() Frame {
	f.ctr++
	return Sign(f.key, f.id, f.ctr)
}

// pressAt emits a specific counter (used to jump across the window).
func (f *fob) pressAt(c uint32) Frame {
	f.ctr = c
	return Sign(f.key, f.id, c)
}

// mockDoor is an idempotent door controller: it deduplicates on OpID and
// counts physical actuations.
type mockDoor struct {
	mu         sync.Mutex
	seen       map[string]int
	actuations int
}

func newMockDoor() *mockDoor { return &mockDoor{seen: make(map[string]int)} }

func (d *mockDoor) Unlock(_ context.Context, op UnlockOp) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seen[op.OpID]++
	if d.seen[op.OpID] == 1 {
		d.actuations++
	}
	return nil
}

func (d *mockDoor) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.actuations
}

func (d *mockDoor) callsFor(opID string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.seen[opID]
}

func walPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "receiver.wal")
}

func mustSubmit(t *testing.T, r *Receiver, f Frame) Result {
	t.Helper()
	res, err := r.Submit(context.Background(), f)
	if err != nil {
		t.Fatalf("Submit(counter=%d): %v", f.Counter, err)
	}
	return res
}

// A consumed code must never unlock again, including after power loss.
func TestReplayRejectedAcrossRestart(t *testing.T) {
	path := walPath(t)
	f := newFob(7, []byte("secret-key-1"))
	keys := MapKeyStore{7: f.key}
	door := newMockDoor()
	ctx := context.Background()

	r, err := Open(ctx, path, keys, door.Unlock, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if res := mustSubmit(t, r, f.press()); res != ResultUnlocked {
			t.Fatalf("press %d: got %v", i+1, res)
		}
	}
	if got := door.count(); got != 3 {
		t.Fatalf("actuations = %d, want 3", got)
	}
	// Replaying an already-opened code in the same power cycle.
	if res := mustSubmit(t, r, f.pressAt(2)); res != ResultReplay {
		t.Fatalf("replay in-session: got %v", res)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	// Power cycle: reopen on the same WAL.
	r2, err := Open(ctx, path, keys, door.Unlock, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	for _, c := range []uint32{1, 2, 3} {
		if res := mustSubmit(t, r2, f.pressAt(c)); res != ResultReplay {
			t.Fatalf("replay after restart counter=%d: got %v", c, res)
		}
	}
	if got := door.count(); got != 3 {
		t.Fatalf("actuations after restart = %d, want 3", got)
	}
	// A fresh code still works.
	if res := mustSubmit(t, r2, f.press()); res != ResultUnlocked {
		t.Fatalf("fresh code after restart: got %v", res)
	}
	if got := door.count(); got != 4 {
		t.Fatalf("actuations = %d, want 4", got)
	}
}

// Power loss after the accept record is fsynced but before the unlock
// callback: recovery must fire the unlock exactly once.
func TestCrashAfterAcceptBeforeUnlock(t *testing.T) {
	path := walPath(t)
	f := newFob(9, []byte("secret-key-2"))
	keys := MapKeyStore{9: f.key}
	door := newMockDoor()
	ctx := context.Background()

	crashed := false
	opts := &Options{CrashHook: func(p CrashPoint) bool {
		return p == CrashAfterAccept && !crashed
	}}
	r, err := Open(ctx, path, keys, door.Unlock, opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Submit(ctx, f.press()); err != ErrCrashed {
		t.Fatalf("want ErrCrashed, got %v", err)
	}
	crashed = true
	if got := door.count(); got != 0 {
		t.Fatalf("door fired before callback: %d", got)
	}
	r.Close()

	// Recovery re-issues the pending unlock with the same OpID.
	r2, err := Open(ctx, path, keys, door.Unlock, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	if got := door.count(); got != 1 {
		t.Fatalf("actuations after recovery = %d, want 1", got)
	}
	// The consumed code is rejected; the next code works.
	if res := mustSubmit(t, r2, f.pressAt(1)); res != ResultReplay {
		t.Fatalf("replay of crashed code: got %v", res)
	}
	if res := mustSubmit(t, r2, f.press()); res != ResultUnlocked {
		t.Fatalf("next code: got %v", res)
	}
	if got := door.count(); got != 2 {
		t.Fatalf("actuations = %d, want 2", got)
	}
}

// Power loss after the door already fired but before the done record:
// recovery retries with the same OpID and the door deduplicates.
func TestCrashBeforeDoneUsesIdempotentOpID(t *testing.T) {
	path := walPath(t)
	f := newFob(11, []byte("secret-key-3"))
	keys := MapKeyStore{11: f.key}
	door := newMockDoor()
	ctx := context.Background()

	crashed := false
	opts := &Options{CrashHook: func(p CrashPoint) bool {
		return p == CrashBeforeDone && !crashed
	}}
	r, err := Open(ctx, path, keys, door.Unlock, opts)
	if err != nil {
		t.Fatal(err)
	}
	frame := f.press()
	if _, err := r.Submit(ctx, frame); err != ErrCrashed {
		t.Fatalf("want ErrCrashed, got %v", err)
	}
	crashed = true
	if got := door.count(); got != 1 {
		t.Fatalf("door should have fired once before crash, got %d", got)
	}
	r.Close()

	r2, err := Open(ctx, path, keys, door.Unlock, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	opID := "000000000000000b:00000001"
	if got := door.callsFor(opID); got != 2 {
		t.Fatalf("recovery should retry op once, calls = %d", got)
	}
	if got := door.count(); got != 1 {
		t.Fatalf("physical actuations = %d, want 1 (idempotent dedup)", got)
	}
	// A second recovery cycle must not fire again: done is now durable.
	r2.Close()
	r3, err := Open(ctx, path, keys, door.Unlock, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r3.Close()
	if got := door.count(); got != 1 {
		t.Fatalf("actuations after second recovery = %d, want 1", got)
	}
}

// Codes beyond the sliding window need two consecutive counters to
// resync; the resync state survives power loss between the two codes.
func TestCrossWindowDualCodeResync(t *testing.T) {
	path := walPath(t)
	f := newFob(13, []byte("secret-key-4"))
	keys := MapKeyStore{13: f.key}
	door := newMockDoor()
	ctx := context.Background()

	r, err := Open(ctx, path, keys, door.Unlock, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		mustSubmit(t, r, f.press()) // counters 1..3
	}
	before := door.count()

	// Fob jumped far ahead (pressed many times out of range).
	if res := mustSubmit(t, r, f.pressAt(100)); res != ResultResyncArmed {
		t.Fatalf("first resync code: got %v", res)
	}
	// A non-consecutive second code re-arms instead of unlocking.
	if res := mustSubmit(t, r, f.pressAt(105)); res != ResultResyncArmed {
		t.Fatalf("non-consecutive second code: got %v", res)
	}
	if got := door.count(); got != before {
		t.Fatalf("door fired during resync: %d", got)
	}
	// Power loss between the two resync codes.
	r.Close()
	r2, err := Open(ctx, path, keys, door.Unlock, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	// The consecutive code completes the resync and unlocks exactly once.
	if res := mustSubmit(t, r2, f.pressAt(106)); res != ResultUnlocked {
		t.Fatalf("consecutive second code: got %v", res)
	}
	if got := door.count(); got != before+1 {
		t.Fatalf("actuations = %d, want %d", got, before+1)
	}
	// Codes at or below the resynced counter are replays now.
	for _, c := range []uint32{3, 100, 105, 106} {
		if res := mustSubmit(t, r2, f.pressAt(c)); res != ResultReplay {
			t.Fatalf("counter %d after resync: got %v", c, res)
		}
	}
	// Beyond the resync window: outright rejection.
	if res := mustSubmit(t, r2, f.pressAt(1<<20)); res != ResultOutOfWindow {
		t.Fatalf("far-future code: got %v", res)
	}
	if got := door.count(); got != before+1 {
		t.Fatalf("actuations = %d, want %d", got, before+1)
	}
}

// Two reader heads racing with the same code must unlock at most once.
func TestTwoReaderRaceSameCode(t *testing.T) {
	path := walPath(t)
	f := newFob(17, []byte("secret-key-5"))
	keys := MapKeyStore{17: f.key}
	door := newMockDoor()
	ctx := context.Background()

	r, err := Open(ctx, path, keys, door.Unlock, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	// For each code, two readers deliver the same frame concurrently.
	for i := 0; i < 20; i++ {
		frame := f.press()
		var wg sync.WaitGroup
		results := make([]Result, 2)
		for reader := 0; reader < 2; reader++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				res, err := r.Submit(ctx, frame)
				if err != nil {
					t.Errorf("Submit: %v", err)
					return
				}
				results[idx] = res
			}(reader)
		}
		wg.Wait()
		unlocked := 0
		for _, res := range results {
			if res == ResultUnlocked {
				unlocked++
			} else if res != ResultReplay {
				t.Fatalf("counter %d: unexpected %v", frame.Counter, res)
			}
		}
		if unlocked != 1 {
			t.Fatalf("counter %d: %d unlocks, want exactly 1", frame.Counter, unlocked)
		}
	}
	if got := door.count(); got != 20 {
		t.Fatalf("actuations = %d, want 20", got)
	}
}

// Many goroutines hammering one frame: still exactly one unlock.
func TestConcurrentFloodSingleCode(t *testing.T) {
	path := walPath(t)
	f := newFob(19, []byte("secret-key-6"))
	keys := MapKeyStore{19: f.key}
	door := newMockDoor()
	ctx := context.Background()

	r, err := Open(ctx, path, keys, door.Unlock, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	frame := f.press()
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Submit(ctx, frame)
		}()
	}
	wg.Wait()
	if got := door.count(); got != 1 {
		t.Fatalf("actuations = %d, want 1", got)
	}
}

// Bad tags and unknown devices never unlock.
func TestBadAuthRejected(t *testing.T) {
	path := walPath(t)
	f := newFob(23, []byte("secret-key-7"))
	keys := MapKeyStore{23: f.key}
	door := newMockDoor()

	r, err := Open(context.Background(), path, keys, door.Unlock, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	forged := Sign([]byte("wrong-key"), 23, 1)
	if res := mustSubmit(t, r, forged); res != ResultBadAuth {
		t.Fatalf("forged tag: got %v", res)
	}
	unknown := Sign([]byte("secret-key-7"), 999, 1)
	if res := mustSubmit(t, r, unknown); res != ResultBadAuth {
		t.Fatalf("unknown device: got %v", res)
	}
	if got := door.count(); got != 0 {
		t.Fatalf("actuations = %d, want 0", got)
	}
}

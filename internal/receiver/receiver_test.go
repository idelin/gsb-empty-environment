package receiver

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"example.org/offlinecli/internal/tlog"
)

// TestAcceptFirstHeadAndReadOffline verifies the very first signed head is
// accepted and can later be read with no network access.
func TestAcceptFirstHeadAndReadOffline(t *testing.T) {
	log, pub := newTestLog(t)
	log.append([]byte("r0"), []byte("r1"), []byte("r2"))
	r := openReceiver(t, pub)

	note := log.checkpointAt(t, 3)
	accepted, err := r.Observe(note, nil)
	if err != nil || !accepted {
		t.Fatalf("first Observe accepted=%v err=%v", accepted, err)
	}

	cp, ok, err := r.Current()
	if err != nil || !ok {
		t.Fatalf("Current ok=%v err=%v", ok, err)
	}
	if cp.Size != 3 || cp.Hash != log.root(3) {
		t.Fatalf("stored head mismatch: %+v", cp)
	}

	// Reopen from disk with no mirror at all: the seen root must survive.
	r2 := reopen(t, r, pub)
	defer r2.Close()
	cp2, ok, err := r2.Current()
	if err != nil || !ok || cp2.Hash != log.root(3) {
		t.Fatalf("offline reopen failed: ok=%v err=%v cp=%+v", ok, err, cp2)
	}
}

// TestAdvanceAcrossNonPowerOfTwoSizes drives the receiver through a sequence of
// tree sizes that repeatedly crosses non-power-of-two boundaries, each advance
// gated by a freshly generated consistency proof.
func TestAdvanceAcrossNonPowerOfTwoSizes(t *testing.T) {
	log, pub := newTestLog(t)
	r := openReceiver(t, pub)

	// Build a log of 20 records; accept heads at irregular sizes chosen to
	// include odd numbers and non-power-of-two boundaries.
	for i := 0; i < 20; i++ {
		log.append([]byte(fmt.Sprintf("record-%d", i)))
	}

	sizes := []int64{1, 2, 3, 5, 6, 7, 9, 13, 17, 20}
	var last int64
	for _, n := range sizes {
		note := log.checkpointAt(t, n)
		var proof []tlog.Hash
		if last > 0 {
			p, err := log.proof(last, n)
			if err != nil {
				t.Fatalf("proof %d->%d: %v", last, n, err)
			}
			proof = p
		}
		accepted, err := r.Observe(note, proof)
		if err != nil {
			t.Fatalf("Observe size %d: %v", n, err)
		}
		if !accepted {
			t.Fatalf("expected advance to size %d to be accepted", n)
		}
		last = n
	}

	cp, ok, _ := r.Current()
	if !ok || cp.Size != 20 || cp.Hash != log.root(20) {
		t.Fatalf("final head wrong: ok=%v cp=%+v", ok, cp)
	}
}

// TestStaleMirrorCannotRollBack accepts a head at size 10, then confirms a
// mirror stuck on an older size can never move the accepted root backwards.
func TestStaleMirrorCannotRollBack(t *testing.T) {
	log, pub := newTestLog(t)
	for i := 0; i < 12; i++ {
		log.append([]byte(fmt.Sprintf("r%d", i)))
	}
	r := openReceiver(t, pub)

	if _, err := r.Observe(log.checkpointAt(t, 10), nil); err != nil {
		t.Fatal(err)
	}

	stale := &staleMirror{log: log, staleSize: 4}
	for attempt := 0; attempt < 3; attempt++ {
		accepted, err := r.ObserveFrom(stale)
		if err == nil {
			t.Fatalf("stale mirror accepted on attempt %d", attempt)
		}
		if !errors.Is(err, ErrStale) {
			t.Fatalf("expected ErrStale, got %v", err)
		}
		if accepted {
			t.Fatal("stale mirror reported an accepted advance")
		}
	}

	cp, _, _ := r.Current()
	if cp.Size != 10 {
		t.Fatalf("head rolled back to size %d", cp.Size)
	}

	// A direct stale Observe must be rejected too.
	proof, _ := log.proof(4, 10)
	if _, err := r.Observe(log.checkpointAt(t, 4), proof); !errors.Is(err, ErrStale) {
		t.Fatalf("direct stale Observe: expected ErrStale, got %v", err)
	}
}

// TestBadConsistencyProofRejected ensures a larger checkpoint with an invalid
// proof cannot advance the head.
func TestBadConsistencyProofRejected(t *testing.T) {
	log, pub := newTestLog(t)
	for i := 0; i < 12; i++ {
		log.append([]byte(fmt.Sprintf("r%d", i)))
	}
	r := openReceiver(t, pub)

	if _, err := r.Observe(log.checkpointAt(t, 5), nil); err != nil {
		t.Fatal(err)
	}

	bad := &badProofMirror{log: log}
	if _, err := r.ObserveFrom(bad); err == nil {
		t.Fatal("checkpoint with garbage proof accepted")
	}

	// Correct proof from the honest mirror must succeed.
	good := &honestMirror{log: log}
	accepted, err := r.ObserveFrom(good)
	if err != nil {
		t.Fatalf("honest mirror rejected: %v", err)
	}
	if !accepted {
		t.Fatal("expected honest advance to be accepted")
	}
	cp, _, _ := r.Current()
	if cp.Size != 12 {
		t.Fatalf("head at size %d, want 12", cp.Size)
	}
}

// TestIdempotentSameCheckpoint confirms re-delivering the identical head is a
// no-op rather than an error.
func TestIdempotentSameCheckpoint(t *testing.T) {
	log, pub := newTestLog(t)
	log.append([]byte("a"), []byte("b"))
	r := openReceiver(t, pub)

	note := log.checkpointAt(t, 2)
	if _, err := r.Observe(note, nil); err != nil {
		t.Fatal(err)
	}
	accepted, err := r.Observe(note, nil)
	if err != nil || accepted {
		t.Fatalf("idempotent re-delivery accepted=%v err=%v", accepted, err)
	}
}

// TestEquivocationFreezesAndKeepsEvidence presents two different signed roots
// at the same size, then asserts the receiver freezes, writes conflict
// evidence, and refuses all later updates including valid ones.
func TestEquivocationFreezesAndKeepsEvidence(t *testing.T) {
	log, pub := newTestLog(t)
	for i := 0; i < 8; i++ {
		log.append([]byte(fmt.Sprintf("r%d", i)))
	}
	r := openReceiver(t, pub)

	// Accept the honest size-6 head.
	goodNote := log.checkpointAt(t, 6)
	if _, err := r.Observe(goodNote, nil); err != nil {
		t.Fatal(err)
	}

	// Forge a conflicting size-6 root by signing a different leaf set.
	forkLeaves := make([]tlog.Hash, 6)
	for i := range forkLeaves {
		forkLeaves[i] = tlog.RecordHash([]byte(fmt.Sprintf("fork-%d", i)))
	}
	forkRoot := tlog.TreeHash(forkLeaves)
	forkEnv, err := log.signer.Sign(6, forkRoot)
	if err != nil {
		t.Fatal(err)
	}

	_, err = r.Observe(forkEnv.Text, nil)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}

	frozen, err := r.Frozen()
	if err != nil || !frozen {
		t.Fatalf("Frozen=%v err=%v", frozen, err)
	}

	// Conflict evidence file must exist and contain both signed notes.
	evidencePath := filepath.Join(r.Dir(), fmt.Sprintf("%s%d%s", conflictPrefix, 6, conflictSuffix))
	data, err := os.ReadFile(evidencePath)
	if err != nil {
		t.Fatalf("conflict evidence missing: %v", err)
	}
	var evidence conflictRecord
	if err := json.Unmarshal(data, &evidence); err != nil {
		t.Fatalf("evidence is not valid JSON: %v", err)
	}
	if string(evidence.Accepted) != string(goodNote) {
		t.Fatal("evidence accepted note does not match original")
	}
	if string(evidence.Conflicting) != string(forkEnv.Text) {
		t.Fatal("evidence conflicting note does not match the fork")
	}
	if evidence.Size != 6 {
		t.Fatalf("evidence size = %d, want 6", evidence.Size)
	}

	// The accepted head must be unchanged.
	cp, _, _ := r.Current()
	if cp.Size != 6 || cp.Hash != log.root(6) {
		t.Fatalf("accepted head changed after freeze: %+v", cp)
	}

	// Even a valid, larger, properly proven checkpoint is now refused.
	log.append([]byte("r8"), []byte("r9"))
	honest := &honestMirror{log: log}
	if _, err := r.ObserveFrom(honest); !errors.Is(err, ErrFrozen) {
		t.Fatalf("expected ErrFrozen after conflict, got %v", err)
	}

	// Freeze must persist across a process restart.
	r2 := reopen(t, r, pub)
	defer r2.Close()
	if frozen, _ := r2.Frozen(); !frozen {
		t.Fatal("freeze state did not persist across reopen")
	}
	if _, err := r2.ObserveFrom(honest); !errors.Is(err, ErrFrozen) {
		t.Fatalf("reopened receiver not frozen: %v", err)
	}
}

// TestConcurrentDownloadThreadsNeverRollBack hammers one receiver from many
// goroutines using a mix of honest and stale mirrors. The accepted head must
// only ever move forward and end at the log's true size.
func TestConcurrentDownloadThreadsNeverRollBack(t *testing.T) {
	log, pub := newTestLog(t)
	for i := 0; i < 30; i++ {
		log.append([]byte(fmt.Sprintf("r%d", i)))
	}
	r := openReceiver(t, pub)

	// Seed an initial head so later stale offers have something to fight.
	if _, err := r.Observe(log.checkpointAt(t, 3), nil); err != nil {
		t.Fatal(err)
	}

	honest := &honestMirror{log: log}
	stale := &staleMirror{log: log, staleSize: 2}

	const threads = 16
	var wg sync.WaitGroup
	for i := 0; i < threads; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for round := 0; round < 25; round++ {
				if id%3 == 0 {
					_, _ = r.ObserveFrom(stale) // must never roll back
				} else {
					_, _ = r.ObserveFrom(honest)
				}
				// Monotonicity invariant under concurrent reads.
				cp, ok, err := r.Current()
				if err != nil || !ok || cp.Size < 3 {
					t.Errorf("concurrent invariant violated: ok=%v size=%d err=%v", ok, cp.Size, err)
					return
				}
			}
		}(i)
	}
	wg.Wait()

	cp, ok, _ := r.Current()
	if !ok || cp.Size != 30 || cp.Hash != log.root(30) {
		t.Fatalf("final head size=%d want 30 (ok=%v)", cp.Size, ok)
	}
	frozen, _ := r.Frozen()
	if frozen {
		t.Fatal("receiver unexpectedly frozen under honest traffic")
	}
}

package receiver

import (
	"os"
	"path/filepath"
	"testing"

	"example.org/offlinecli/internal/tlog"
)

// TestCrashBeforeRenameLeavesOldState installs a hook that aborts the write
// after the temp file is fsynced but before the rename. A subsequent Open must
// recover cleanly, drop the temp file, and retain the previously accepted head.
func TestCrashBeforeRenameLeavesOldState(t *testing.T) {
	log, pub := newTestLog(t)
	for i := 0; i < 10; i++ {
		log.append([]byte{byte(i)})
	}
	r := openReceiver(t, pub)

	// Establish a durable head at size 4.
	if _, err := r.Observe(log.checkpointAt(t, 4), nil); err != nil {
		t.Fatal(err)
	}

	// Arm a simulated power loss during the next commit.
	r.setCrashHook(func() {
		panic(simulatedPowerLoss)
	})

	proof, err := log.proof(4, 10)
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected simulated crash panic")
			}
		}()
		_, _ = r.Observe(log.checkpointAt(t, 10), proof)
	}()

	// A temp file must be present, and the state file must still describe size 4.
	tmpCount := countMatching(t, r.Dir(), tempStatePattern)
	if tmpCount != 1 {
		t.Fatalf("expected 1 leftover temp file, got %d", tmpCount)
	}
	// Reopen (the helper first releases the lock; a real power loss would have
	// the kernel drop it automatically). Cleanup runs and the old head is intact.
	r2 := reopen(t, r, pub)
	defer r2.Close()

	if got := countMatching(t, r.Dir(), tempStatePattern); got != 0 {
		t.Fatalf("temp file not cleaned up, got %d", got)
	}
	cp, ok, err := r2.Current()
	if err != nil || !ok || cp.Size != 4 {
		t.Fatalf("post-crash head wrong: ok=%v size=%d err=%v", ok, cp.Size, err)
	}

	// After recovery the receiver can still advance normally.
	r2.setCrashHook(nil)
	good := &honestMirror{log: log}
	accepted, err := r2.ObserveFrom(good)
	if err != nil || !accepted {
		t.Fatalf("post-recovery advance failed accepted=%v err=%v", accepted, err)
	}
	if cp2, _, _ := r2.Current(); cp2.Size != 10 {
		t.Fatalf("size after recovery advance = %d, want 10", cp2.Size)
	}
}

// TestCrashAfterRenameKeepsNewState simulates a crash hook that runs (temp
// already durable) but lets the rename proceed; the new head must be present.
func TestCrashAfterRenameKeepsNewState(t *testing.T) {
	log, pub := newTestLog(t)
	for i := 0; i < 6; i++ {
		log.append([]byte{byte(i + 10)})
	}
	r := openReceiver(t, pub)
	if _, err := r.Observe(log.checkpointAt(t, 2), nil); err != nil {
		t.Fatal(err)
	}

	var fired bool
	r.setCrashHook(func() { fired = true })
	proof, _ := log.proof(2, 6)
	if _, err := r.Observe(log.checkpointAt(t, 6), proof); err != nil {
		t.Fatal(err)
	}
	if !fired {
		t.Fatal("crash hook did not run")
	}

	r2 := reopen(t, r, pub)
	defer r2.Close()
	cp, ok, _ := r2.Current()
	if !ok || cp.Size != 6 || cp.Hash != log.root(6) {
		t.Fatalf("new state not durable: ok=%v cp=%+v", ok, cp)
	}
}

// TestCorruptStateFailsClosed verifies that a torn/garbled state file is
// detected and rejected rather than silently treated as "no state", which would
// otherwise open the door to a rollback/re-bootstrap attack.
func TestCorruptStateFailsClosed(t *testing.T) {
	log, pub := newTestLog(t)
	log.append([]byte("a"), []byte("b"), []byte("c"))
	r := openReceiver(t, pub)
	if _, err := r.Observe(log.checkpointAt(t, 3), nil); err != nil {
		t.Fatal(err)
	}

	garbled := []byte("{this is not valid json")
	if err := os.WriteFile(statePath(r.Dir()), garbled, 0o600); err != nil {
		t.Fatal(err)
	}

	r2 := reopen(t, r, pub)
	defer r2.Close()
	if _, _, err := r2.Current(); err == nil {
		t.Fatal("corrupt state was not rejected")
	}
	if _, err := r2.Observe(log.checkpointAt(t, 2), nil); err == nil {
		t.Fatal("Observe accepted despite corrupt state (must fail closed)")
	}
}

// TestTamperedSignedNoteRejected mutates the signed_note inside the state JSON;
// readState re-verifies the note and must refuse it.
func TestTamperedSignedNoteRejected(t *testing.T) {
	log, pub := newTestLog(t)
	log.append([]byte("a"), []byte("b"))
	r := openReceiver(t, pub)
	if _, err := r.Observe(log.checkpointAt(t, 2), nil); err != nil {
		t.Fatal(err)
	}

	path := statePath(r.Dir())
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Flip a byte inside the embedded base64 signature region (near file end).
	data[len(data)-5] ^= 0x01
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	r2 := reopen(t, r, pub)
	defer r2.Close()
	if _, _, err := r2.Current(); err == nil {
		t.Fatal("tampered signed note was not rejected")
	}
}

// TestOpenRejectsWrongOrigin ensures state written for one origin cannot be
// reused by a receiver pinned to a different origin.
func TestOpenRejectsWrongOrigin(t *testing.T) {
	log, pub := newTestLog(t)
	log.append([]byte("a"))
	r := openReceiver(t, pub)
	if _, err := r.Observe(log.checkpointAt(t, 1), nil); err != nil {
		t.Fatal(err)
	}

	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r2, err := Open(r.Dir(), "different.origin/log", pub)
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	if _, _, err := r2.Current(); err == nil {
		t.Fatal("cross-origin state reuse was permitted")
	}
}

// TestRejectUnsignedOrForeignSignature confirms Observe never accepts a note
// that fails signature verification.
func TestRejectUnsignedOrForeignSignature(t *testing.T) {
	log, pub := newTestLog(t)
	log.append([]byte("a"))
	r := openReceiver(t, pub)

	note := log.checkpointAt(t, 1)
	if _, err := r.Observe(note, nil); err != nil {
		t.Fatal(err)
	}

	// Truncate the signature line.
	bad := append([]byte{}, note[:len(note)-6]...)
	if _, err := r.Observe(bad, nil); err == nil {
		t.Fatal("truncated signature accepted")
	}

	// A note signed by a completely different key.
	otherLog, _ := newTestLog(t)
	otherLog.append([]byte("a"))
	foreign := otherLog.checkpointAt(t, 1)
	if _, err := r.Observe(foreign, nil); err == nil {
		t.Fatal("foreign-key signature accepted")
	}
}

const simulatedPowerLoss = "simulated power loss"

func countMatching(t *testing.T, dir, pattern string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		matched, err := filepath.Match(pattern, entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if matched {
			count++
		}
	}
	return count
}

// TestCrashDuringEvidenceWriteStaysFrozen simulates a power loss while the
// conflict evidence is being written. Because the state is marked frozen
// before the evidence file is committed, a restart must still be frozen.
func TestCrashDuringEvidenceWriteStaysFrozen(t *testing.T) {
	log, pub := newTestLog(t)
	for i := 0; i < 5; i++ {
		log.append([]byte{byte(i)})
	}
	r := openReceiver(t, pub)
	if _, err := r.Observe(log.checkpointAt(t, 4), nil); err != nil {
		t.Fatal(err)
	}

	// First write (state -> frozen) succeeds; crash during the second, which is
	// the evidence-file commit.
	var writes int
	r.setCrashHook(func() {
		writes++
		if writes == 2 {
			panic(simulatedPowerLoss)
		}
	})

	fork := make([]tlog.Hash, 4)
	for i := range fork {
		fork[i] = tlog.RecordHash([]byte{byte(i + 100)})
	}
	forkEnv, err := log.signer.Sign(4, tlog.TreeHash(fork))
	if err != nil {
		t.Fatal(err)
	}

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected simulated crash")
			}
		}()
		_, _ = r.Observe(forkEnv.Text, nil)
	}()

	r2 := reopen(t, r, pub)
	defer r2.Close()
	frozen, err := r2.Frozen()
	if err != nil || !frozen {
		t.Fatalf("receiver not frozen after evidence-write crash: frozen=%v err=%v", frozen, err)
	}

	honest := &honestMirror{log: log}
	if _, err := r2.ObserveFrom(honest); err == nil {
		t.Fatal("frozen receiver accepted a checkpoint after crash")
	}
}

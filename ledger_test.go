package netsec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// counter returns an ApplyFunc that records how many times the side effect
// physically executed.
func counter(execCount *int) ApplyFunc {
	return func() ([]byte, error) {
		*execCount++
		return []byte(fmt.Sprintf("exec-%d", *execCount)), nil
	}
}

func TestConfirmExecutesOnceAndReplaysIdentical(t *testing.T) {
	l, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gen := l.CurrentGen()
	var n int

	r1, err := l.Confirm(gen, "op-1", []byte("open-valve"), counter(&n))
	if err != nil {
		t.Fatal(err)
	}
	if r1.Replayed || string(r1.Result) != "exec-1" || n != 1 {
		t.Fatalf("first confirm: %+v n=%d", r1, n)
	}

	// Retransmission of identical content returns the original result.
	r2, err := l.Confirm(gen, "op-1", []byte("open-valve"), counter(&n))
	if err != nil {
		t.Fatal(err)
	}
	if !r2.Replayed || string(r2.Result) != "exec-1" || n != 1 {
		t.Fatalf("retransmission re-executed or changed result: %+v n=%d", r2, n)
	}
}

func TestConflictingContentRejected(t *testing.T) {
	l, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gen := l.CurrentGen()
	var n int
	if _, err := l.Confirm(gen, "op-1", []byte("open-valve"), counter(&n)); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Confirm(gen, "op-1", []byte("close-valve"), counter(&n)); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
	if n != 1 {
		t.Fatalf("conflicting retransmission executed side effect: n=%d", n)
	}
}

// Power loss after commit: restart from disk, retransmission must replay the
// stored result without re-executing.
func TestPowerLossAfterCommitReplaysStoredResult(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	gen := l.CurrentGen()
	var n int
	if _, err := l.Confirm(gen, "op-1", []byte("open-valve"), counter(&n)); err != nil {
		t.Fatal(err)
	}

	// Simulate power loss: abandon the instance, reopen from disk.
	l2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	r, err := l2.Confirm(gen, "op-1", []byte("open-valve"), counter(&n))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Replayed || string(r.Result) != "exec-1" || n != 1 {
		t.Fatalf("post-crash retransmission misbehaved: %+v n=%d", r, n)
	}
}

// Power loss between the durable intent and the commit: the capability is
// already spent (fail-closed); the confirmation must never re-execute.
func TestPowerLossAfterIntentConsumesCapability(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	gen := l.CurrentGen()
	var n int

	// Simulate power loss right after the intent is fsynced.
	l.crashAfterIntent = func() { panic("power loss") }
	func() {
		defer func() { recover() }()
		l.Confirm(gen, "op-1", []byte("open-valve"), counter(&n))
	}()
	if n != 0 {
		t.Fatalf("side effect ran before crash: n=%d", n)
	}

	// Reboot and retransmit the identical confirmation.
	l2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l2.Confirm(gen, "op-1", []byte("open-valve"), counter(&n)); !errors.Is(err, ErrInterrupted) {
		t.Fatalf("want ErrInterrupted, got %v", err)
	}
	if n != 0 {
		t.Fatalf("interrupted operation re-executed: n=%d", n)
	}
	// Conflicting content on the interrupted opID is still a conflict.
	if _, err := l2.Confirm(gen, "op-1", []byte("close-valve"), counter(&n)); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}

// Reconnect: a new session generation must not allow the same confirmation
// to trigger the operation again, even after a power cycle.
func TestReconnectNewSessionCannotRetrigger(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	gen1 := l.CurrentGen()
	var n int
	if _, err := l.Confirm(gen1, "op-1", []byte("open-valve"), counter(&n)); err != nil {
		t.Fatal(err)
	}

	// Power loss, then reconnect: restart and advance the session.
	l2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	gen2, err := l2.AdvanceGen()
	if err != nil {
		t.Fatal(err)
	}
	if gen2 <= gen1 {
		t.Fatalf("generation did not advance: %d -> %d", gen1, gen2)
	}

	// The same confirmation replayed into the new session is rejected.
	if _, err := l2.Confirm(gen2, "op-1", []byte("open-valve"), counter(&n)); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("want ErrSessionExpired, got %v", err)
	}
	if n != 1 {
		t.Fatalf("operation re-triggered in new session: n=%d", n)
	}

	// A confirmation still carrying the expired generation is rejected too.
	if _, err := l2.Confirm(gen1, "op-2", []byte("open-valve"), counter(&n)); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("want ErrSessionExpired for stale gen, got %v", err)
	}
	if n != 1 {
		t.Fatalf("stale-generation confirmation executed: n=%d", n)
	}

	// A fresh operation ID works fine in the new session.
	if _, err := l2.Confirm(gen2, "op-2", []byte("open-valve"), counter(&n)); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("new operation did not execute: n=%d", n)
	}
}

// The advanced generation must survive a restart, so an expired session
// stays expired after another power cycle.
func TestAdvancedGenSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	gen1 := l.CurrentGen()
	if _, err := l.AdvanceGen(); err != nil {
		t.Fatal(err)
	}

	l2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if l2.CurrentGen() != gen1+1 {
		t.Fatalf("generation not persisted: got %d want %d", l2.CurrentGen(), gen1+1)
	}
	var n int
	if _, err := l2.Confirm(gen1, "op-1", []byte("x"), counter(&n)); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expired gen accepted after restart: %v", err)
	}
}

// A crash during receipt write leaves an orphan temp file; Open must ignore
// it and the operation must remain unconsumed.
func TestOrphanTempFileIgnored(t *testing.T) {
	dir := t.TempDir()
	// First Open creates the directory layout.
	if _, err := Open(dir); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(dir, "receipts", "op-9.json.tmp")
	if err := os.WriteFile(orphan, []byte(`{"op_id":"op-9","state":"committed"`), 0o644); err != nil {
		t.Fatal(err)
	}
	l2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if _, err := l2.Confirm(l2.CurrentGen(), "op-9", []byte("x"), counter(&n)); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("orphan temp file consumed the capability: n=%d", n)
	}
}

// A failing side effect still consumes the capability (fail-closed); the
// failure is replayed to identical retransmissions.
func TestApplyErrorConsumedOnce(t *testing.T) {
	l, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gen := l.CurrentGen()
	var n int
	fail := func() ([]byte, error) { n++; return nil, errors.New("device busy") }

	r1, err := l.Confirm(gen, "op-1", []byte("reboot"), fail)
	if err != nil {
		t.Fatal(err)
	}
	if r1.ExecErr != "device busy" || n != 1 {
		t.Fatalf("first confirm: %+v n=%d", r1, n)
	}
	r2, err := l.Confirm(gen, "op-1", []byte("reboot"), fail)
	if err != nil {
		t.Fatal(err)
	}
	if !r2.Replayed || r2.ExecErr != "device busy" || n != 1 {
		t.Fatalf("failure not replayed deterministically: %+v n=%d", r2, n)
	}
}

func TestInvalidOpID(t *testing.T) {
	l, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", "../escape", "a/b", "a\x00b"} {
		if _, err := l.Confirm(l.CurrentGen(), id, []byte("x"), counter(new(int))); err == nil {
			t.Fatalf("op ID %q accepted", id)
		}
	}
}

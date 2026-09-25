package applog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAckMonotonicAndIdempotent(t *testing.T) {
	l := openTestLog(t, t.TempDir(), 100, Hooks{})
	for i := 0; i < 6; i++ {
		if _, err := l.Append([]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.RegisterConsumer("c", 0); err != nil {
		t.Fatal(err)
	}
	if err := l.Ack("c", 4); err != nil {
		t.Fatalf("ack 4: %v", err)
	}
	if pos, _ := l.Position("c"); pos != 4 {
		t.Fatalf("position = %d, want 4", pos)
	}
	if err := l.Ack("c", 2); err != ErrNonMonotonicAck {
		t.Fatalf("backwards ack: %v, want ErrNonMonotonicAck", err)
	}
	if pos, _ := l.Position("c"); pos != 4 {
		t.Fatalf("position after rejected ack = %d, want 4", pos)
	}
	// Re-acking the same position is an idempotent no-op (covers a retry
	// whose first response was lost after the commit).
	if err := l.Ack("c", 4); err != nil {
		t.Fatalf("idempotent ack: %v", err)
	}
	if pos, _ := l.Position("c"); pos != 4 {
		t.Fatalf("position after idempotent ack = %d, want 4", pos)
	}
	if err := l.Ack("unknown", 1); err != ErrUnknownConsumer {
		t.Fatalf("unknown consumer: %v, want ErrUnknownConsumer", err)
	}
}

func TestAckBeyondAppendedRange(t *testing.T) {
	l := openTestLog(t, t.TempDir(), 100, Hooks{})
	if _, err := l.Append([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := l.RegisterConsumer("c", 0); err != nil {
		t.Fatal(err)
	}
	if err := l.Ack("c", 5); err != ErrAckBeyondHighWater {
		t.Fatalf("ack beyond high watermark: %v, want ErrAckBeyondHighWater", err)
	}
	// Ack exactly at the high watermark (everything consumed) is valid.
	if err := l.Ack("c", 1); err != nil {
		t.Fatalf("ack at high watermark: %v", err)
	}
	// Registering past the readable range is rejected too.
	if err := l.RegisterConsumer("late", 2); err != ErrAckBeyondHighWater {
		t.Fatalf("register beyond: %v, want ErrAckBeyondHighWater", err)
	}
}

func TestAckRetryAfterLostResponse(t *testing.T) {
	dir := t.TempDir()
	l := openTestLog(t, dir, 100, Hooks{})
	for i := 0; i < 5; i++ {
		if _, err := l.Append([]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.RegisterConsumer("c", 0); err != nil {
		t.Fatal(err)
	}
	// The ack commits (manifest renamed), then the response is "lost" right
	// at the crash-simulation point after the rename.
	var committed bool
	l.hooks.AfterManifestRename = func() { committed = true }
	if err := l.Ack("c", 4); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if !committed {
		t.Fatal("commit hook did not fire")
	}
	l.Close()

	// Client retries with the same request after reconnect/reopen.
	l2 := reopenTestLog(t, dir, 100, Hooks{})
	if pos, _ := l2.Position("c"); pos != 4 {
		t.Fatalf("durable position = %d, want 4", pos)
	}
	if err := l2.Ack("c", 4); err != nil {
		t.Fatalf("retry after lost response: %v", err)
	}
	if pos, _ := l2.Position("c"); pos != 4 {
		t.Fatalf("position after retry = %d, want 4 (must not move)", pos)
	}
}

func TestAckBeforeRenameFailureIsNotDurable(t *testing.T) {
	dir := t.TempDir()
	var failNext bool
	l := openTestLog(t, dir, 100, Hooks{
		BeforeManifestRename: func() error {
			if failNext {
				return errInjected
			}
			return nil
		},
	})
	for i := 0; i < 8; i++ {
		if _, err := l.Append([]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.RegisterConsumer("c", 0); err != nil {
		t.Fatal(err)
	}
	failNext = true
	if err := l.Ack("c", 6); err != errInjected {
		t.Fatalf("ack = %v, want injected error", err)
	}
	// In-memory rollback.
	if pos, _ := l.Position("c"); pos != 0 {
		t.Fatalf("in-memory position = %d, want 0", pos)
	}
	l.Close()
	// Durable rollback: the temp file may remain but MANIFEST is unchanged.
	l2 := reopenTestLog(t, dir, 100, Hooks{})
	if pos, _ := l2.Position("c"); pos != 0 {
		t.Fatalf("durable position = %d, want 0", pos)
	}
	// A leftover MANIFEST.tmp is harmless: the committed MANIFEST is
	// authoritative and the temp file is overwritten on the next commit.
	_ = os.Remove(filepath.Join(dir, "MANIFEST.tmp"))
	// The failed attempt consumed nothing; a later ack to 6 works normally.
	if err := l2.Ack("c", 6); err != nil {
		t.Fatalf("retry ack: %v", err)
	}
}

func TestAckBatchAtomic(t *testing.T) {
	dir := t.TempDir()
	l := openTestLog(t, dir, 100, Hooks{})
	for i := 0; i < 6; i++ {
		if _, err := l.Append([]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.RegisterConsumer("a", 0); err != nil {
		t.Fatal(err)
	}
	if err := l.RegisterConsumer("b", 0); err != nil {
		t.Fatal(err)
	}
	if err := l.Ack("a", 2); err != nil {
		t.Fatal(err)
	}
	// b's entry is illegal (beyond high watermark): the whole batch fails.
	if err := l.AckBatch(map[string]uint64{"a": 5, "b": 99}); err != ErrAckBeyondHighWater {
		t.Fatalf("batch = %v, want ErrAckBeyondHighWater", err)
	}
	if pos, _ := l.Position("a"); pos != 2 {
		t.Fatalf("a = %d, want 2 (batch must be all-or-nothing)", pos)
	}
	if pos, _ := l.Position("b"); pos != 0 {
		t.Fatalf("b = %d, want 0", pos)
	}
	l.Close()
	l2 := reopenTestLog(t, dir, 100, Hooks{})
	if pos, _ := l2.Position("a"); pos != 2 {
		t.Fatalf("durable a = %d, want 2", pos)
	}
	if pos, _ := l2.Position("b"); pos != 0 {
		t.Fatalf("durable b = %d, want 0", pos)
	}
	// A valid batch commits with one manifest write.
	if err := l2.AckBatch(map[string]uint64{"a": 5, "b": 3}); err != nil {
		t.Fatalf("valid batch: %v", err)
	}
	if pos, _ := l2.Position("a"); pos != 5 {
		t.Fatalf("a = %d, want 5", pos)
	}
	if pos, _ := l2.Position("b"); pos != 3 {
		t.Fatalf("b = %d, want 3", pos)
	}
}

func TestBatchCommitFailureRollsBackAll(t *testing.T) {
	var failNext bool
	l := openTestLog(t, t.TempDir(), 100, Hooks{
		BeforeManifestSync: func() error {
			if failNext {
				return errInjected
			}
			return nil
		},
	})
	for i := 0; i < 6; i++ {
		if _, err := l.Append([]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.RegisterConsumer("a", 0); err != nil {
		t.Fatal(err)
	}
	if err := l.RegisterConsumer("b", 0); err != nil {
		t.Fatal(err)
	}
	failNext = true
	if err := l.AckBatch(map[string]uint64{"a": 4, "b": 4}); err != errInjected {
		t.Fatalf("batch = %v, want injected error", err)
	}
	if pos, _ := l.Position("a"); pos != 0 {
		t.Fatalf("a = %d, want 0", pos)
	}
	if pos, _ := l.Position("b"); pos != 0 {
		t.Fatalf("b = %d, want 0", pos)
	}
}

func TestRemoveUnknownAndDuplicate(t *testing.T) {
	l := openTestLog(t, t.TempDir(), 100, Hooks{})
	if err := l.RemoveConsumer("nope"); err != ErrUnknownConsumer {
		t.Fatalf("remove unknown: %v, want ErrUnknownConsumer", err)
	}
	if err := l.RegisterConsumer("c", 0); err != nil {
		t.Fatal(err)
	}
	if err := l.RegisterConsumer("c", 0); err != ErrConsumerExists {
		t.Fatalf("duplicate: %v, want ErrConsumerExists", err)
	}
}

func TestConsumerRegistrationSurvivesCrash(t *testing.T) {
	dir := t.TempDir()
	l := openTestLog(t, dir, 100, Hooks{})
	if err := l.RegisterConsumer("c", 0); err != nil {
		t.Fatal(err)
	}
	l.Close()
	l2 := reopenTestLog(t, dir, 100, Hooks{})
	if _, err := l2.Position("c"); err != nil {
		t.Fatalf("consumer lost across reopen: %v", err)
	}
}

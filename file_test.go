package netsec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newFileSink(t *testing.T, maxBytes int, faults Faults) (*FileSink, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audit.wal")
	sink, err := NewFileSink(path, maxBytes, faults)
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })
	return sink, path
}

func TestFileSinkDurableReopenRoundTrip(t *testing.T) {
	ctx := context.Background()
	sink, path := newFileSink(t, 1<<20, Faults{})

	op := testOp("op-persist")
	if err := sink.Append(ctx, Record{Seq: 1, OpID: op.OpID, Actor: op.Actor, Action: op.Action, Detail: op.Detail, Status: StatusPending}); err != nil {
		t.Fatalf("append pending: %v", err)
	}
	if err := sink.Append(ctx, Record{Seq: 2, OpID: op.OpID, Actor: op.Actor, Action: op.Action, Detail: op.Detail, Status: StatusSucceeded}); err != nil {
		t.Fatalf("append terminal: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopen models a process restart: only fsynced frames must be visible.
	reopened, err := NewFileSink(path, 1<<20, Faults{})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	records, err := reopened.Replay(ctx)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(records) != 2 || records[0].Status != StatusPending || records[1].Status != StatusSucceeded {
		t.Fatalf("unexpected replayed records: %+v", records)
	}
	if reopened.NextSeq() != 3 {
		t.Fatalf("want next seq 3, got %d", reopened.NextSeq())
	}
}

func TestFileSinkBackpressureAndCompaction(t *testing.T) {
	ctx := context.Background()
	op := testOp("op-bounded")
	tf := frameEncodedLen(Record{OpID: op.OpID, Actor: op.Actor, Action: op.Action, Detail: op.Detail, Status: StatusSucceeded})

	// Three distinct terminals fit (header plus three frames); a fourth does
	// not, even after compaction, because distinct OpIDs cannot be merged.
	sink, _ := newFileSink(t, headerSize+3*tf, Faults{})
	for i := 0; i < 3; i++ {
		rec := Record{Seq: uint64(i + 1), OpID: "op-" + string(rune('a'+i)), Actor: op.Actor, Action: op.Action, Detail: op.Detail, Status: StatusSucceeded}
		if err := sink.Append(ctx, rec); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	overflow := Record{Seq: 4, OpID: "op-overflow", Actor: op.Actor, Action: op.Action, Detail: op.Detail, Status: StatusSucceeded}
	if err := sink.Append(ctx, overflow); !errors.Is(err, ErrSinkFull) {
		t.Fatalf("want ErrSinkFull, got %v", err)
	}

	// Compaction is safe to call and never drops a distinct terminal.
	if err := sink.Compact(ctx); err != nil {
		t.Fatalf("compact: %v", err)
	}
	records, err := sink.Replay(ctx)
	if err != nil {
		t.Fatalf("replay after compact: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("compaction must retain three distinct terminals, got %d", len(records))
	}
}

func TestFileSinkTruncatesTornTailOnReopen(t *testing.T) {
	ctx := context.Background()
	sink, path := newFileSink(t, 1<<20, Faults{})
	op := testOp("op-torn")
	good := Record{Seq: 1, OpID: op.OpID, Actor: op.Actor, Action: op.Action, Detail: op.Detail, Status: StatusSucceeded}
	if err := sink.Append(ctx, good); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Simulate a crash mid-write by appending a partial, un-fsynced frame.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open for tear: %v", err)
	}
	if _, err := f.Write([]byte{0x00, 0x00, 0x00, 0x40, 'x', 'y'}); err != nil {
		t.Fatalf("write torn tail: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close torn write: %v", err)
	}

	reopened, err := NewFileSink(path, 1<<20, Faults{})
	if err != nil {
		t.Fatalf("reopen with torn tail: %v", err)
	}
	records, err := reopened.Replay(ctx)
	if err != nil {
		t.Fatalf("replay torn log: %v", err)
	}
	if len(records) != 1 || records[0].OpID != op.OpID {
		t.Fatalf("torn tail must be truncated and good record kept, got %+v", records)
	}

	// The log is sealed again and accepts new appends after recovery.
	next := Record{Seq: 2, OpID: "op-after", Actor: op.Actor, Action: op.Action, Detail: op.Detail, Status: StatusSucceeded}
	if err := reopened.Append(ctx, next); err != nil {
		t.Fatalf("append after recovery: %v", err)
	}
}

func TestFileSinkRejectsCorruptFrame(t *testing.T) {
	sink, path := newFileSink(t, 1<<20, Faults{})
	op := testOp("op-corrupt")
	if err := sink.Append(context.Background(), Record{Seq: 1, OpID: op.OpID, Actor: op.Actor, Action: op.Action, Detail: op.Detail, Status: StatusSucceeded}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Flip a payload byte so the CRC check fails on an otherwise full frame.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	data[headerSize+framePrefix] ^= 0xFF
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}

	if _, err := NewFileSink(path, 1<<20, Faults{}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
}

func TestFileSinkCommitterCrashRecoverNoReexecute(t *testing.T) {
	ctx := context.Background()
	op := testOp("op-wal-crash")
	sink, path := newFileSink(t, 1<<20, Faults{})
	c, err := New(ctx, sink, Config{MaxPending: 4})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Crash immediately after the reservation is fsynced: the side effect must
	// never run, but the reservation must survive a real process restart.
	var effectRan bool
	sink.faults.AfterAppend = func(rec Record) error {
		if rec.Status == StatusPending {
			_ = c.Close()
			return errors.New("simulated power loss after fsync")
		}
		return nil
	}

	_, commitErr := c.Commit(ctx, op, func(context.Context) error {
		effectRan = true
		return nil
	})
	if commitErr == nil {
		t.Fatal("want crash error, got nil")
	}
	if effectRan {
		t.Fatal("side effect ran after post-fsync crash")
	}

	// Reopen the physical WAL in a fresh committer, exactly like a new process.
	reopenedSink, err := NewFileSink(path, 1<<20, Faults{})
	if err != nil {
		t.Fatalf("reopen sink: %v", err)
	}
	recovered, err := New(ctx, reopenedSink, Config{MaxPending: 4})
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	t.Cleanup(func() { _ = recovered.Close() })

	pending := recovered.Pending()
	if len(pending) != 1 || pending[0] != op.OpID {
		t.Fatalf("want recovered pending %q, got %v", op.OpID, pending)
	}

	// A duplicate request is refused rather than re-executing the operation.
	_, err = recovered.Commit(ctx, op, func(context.Context) error {
		effectRan = true
		return nil
	})
	if !errors.Is(err, ErrPending) {
		t.Fatalf("want ErrPending, got %v", err)
	}
	if effectRan {
		t.Fatal("recovered pending operation was re-executed")
	}

	// Manual resolution writes the terminal outcome without a side effect.
	res, err := recovered.Resolve(ctx, op.OpID, nil)
	if err != nil || res.Status != StatusSucceeded {
		t.Fatalf("Resolve: %v res=%+v", err, res)
	}
	if len(recovered.Pending()) != 0 {
		t.Fatal("resolved op must leave the pending set")
	}
}

func TestFileSinkFaultBeforeAckWritesNothing(t *testing.T) {
	ctx := context.Background()
	sink, path := newFileSink(t, 1<<20, Faults{
		BeforeAppend: func(Record) error { return errors.New("power lost before write") },
	})

	err := sink.Append(ctx, Record{Seq: 1, OpID: "x", Status: StatusPending})
	if err == nil {
		t.Fatal("want injected error, got nil")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != headerSize {
		t.Fatalf("only the initial header may exist, got %d bytes", info.Size())
	}
}

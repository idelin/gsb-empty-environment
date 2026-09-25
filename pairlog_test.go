package pairlog

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

const testPayload = "01234567890123456789" // 20 bytes -> 36 byte frame

func testOpts(hook func(FaultEvent) error) Options {
	return Options{MaxSegmentBytes: 64, FaultHook: hook}
}

func openTest(t *testing.T, dir string, hook func(FaultEvent) error) *Log {
	t.Helper()
	l, err := Open(dir, testOpts(hook))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return l
}

func appendN(t *testing.T, l *Log, n int) []uint64 {
	t.Helper()
	offsets := make([]uint64, n)
	for i := 0; i < n; i++ {
		off, err := l.Append([]byte(fmt.Sprintf("%s-%d", testPayload, i)))
		if err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
		offsets[i] = off
	}
	return offsets
}

// payload is 22 bytes (testPayload + "-N"), frame = 38 bytes.
// MaxSegmentBytes=64 -> one record per segment.
const frameSize = 38

func readAll(t *testing.T, l *Log, consumer string, n int) []Record {
	t.Helper()
	recs := make([]Record, 0, n)
	for i := 0; i < n; i++ {
		rec, err := l.Read(consumer)
		if err != nil {
			t.Fatalf("Read %d: %v", i, err)
		}
		recs = append(recs, rec)
	}
	return recs
}

func TestAppendReadAckRules(t *testing.T) {
	dir := t.TempDir()
	l := openTest(t, dir, nil)
	defer l.Close()

	if err := l.RegisterConsumer("c1"); err != nil {
		t.Fatalf("RegisterConsumer: %v", err)
	}
	offsets := appendN(t, l, 3)
	if offsets[0] != 0 || offsets[1] != frameSize || offsets[2] != 2*frameSize {
		t.Fatalf("unexpected offsets: %v", offsets)
	}

	// Cannot acknowledge before reading.
	if err := l.Ack(map[string]uint64{"c1": frameSize}); !errors.Is(err, ErrAckBeyondRead) {
		t.Fatalf("ack before read: got %v, want ErrAckBeyondRead", err)
	}

	recs := readAll(t, l, "c1", 3)
	if recs[0].Offset != 0 || recs[0].Next != frameSize {
		t.Fatalf("unexpected record: %+v", recs[0])
	}
	if _, err := l.Read("c1"); err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}

	// Non-boundary offset is rejected.
	if err := l.Ack(map[string]uint64{"c1": 10}); !errors.Is(err, ErrInvalidOffset) {
		t.Fatalf("ack non-boundary: got %v, want ErrInvalidOffset", err)
	}
	// Unknown consumer is rejected.
	if err := l.Ack(map[string]uint64{"ghost": frameSize}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ack unknown consumer: got %v, want ErrNotFound", err)
	}

	// Acknowledge cumulatively to the end.
	if err := l.Ack(map[string]uint64{"c1": 3 * frameSize}); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	// Retry of the durable position is a successful no-op (lost response).
	if err := l.Ack(map[string]uint64{"c1": 3 * frameSize}); err != nil {
		t.Fatalf("retry Ack: %v", err)
	}
	// Regression is rejected and the cursor does not move.
	if err := l.Ack(map[string]uint64{"c1": frameSize}); !errors.Is(err, ErrAckRegression) {
		t.Fatalf("regression: got %v, want ErrAckRegression", err)
	}
	ack, readHigh, ok := l.Cursor("c1")
	if !ok || ack != 3*frameSize || readHigh != 3*frameSize {
		t.Fatalf("cursor = %d,%d,%v", ack, readHigh, ok)
	}
}

func TestBatchAckIsAtomic(t *testing.T) {
	dir := t.TempDir()
	l := openTest(t, dir, nil)
	defer l.Close()

	l.RegisterConsumer("c1")
	l.RegisterConsumer("c2")
	appendN(t, l, 1)
	readAll(t, l, "c1", 1)
	readAll(t, l, "c2", 1)

	// One bad entry aborts the whole batch.
	err := l.Ack(map[string]uint64{"c1": frameSize, "ghost": frameSize})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
	if ack, _, _ := l.Cursor("c1"); ack != 0 {
		t.Fatalf("c1 cursor moved despite failed batch: %d", ack)
	}

	err = l.Ack(map[string]uint64{"c1": frameSize, "c2": 2 * frameSize})
	if !errors.Is(err, ErrAckBeyondRead) {
		t.Fatalf("got %v, want ErrAckBeyondRead", err)
	}
	if ack, _, _ := l.Cursor("c1"); ack != 0 {
		t.Fatalf("c1 cursor moved despite failed batch: %d", ack)
	}

	if err := l.Ack(map[string]uint64{"c1": frameSize, "c2": frameSize}); err != nil {
		t.Fatalf("valid batch: %v", err)
	}
}

func TestSlowConsumerBlocksReclaim(t *testing.T) {
	dir := t.TempDir()
	l := openTest(t, dir, nil)
	defer l.Close()

	l.RegisterConsumer("fast")
	l.RegisterConsumer("slow")
	appendN(t, l, 4) // segments at 0, 41, 82, 123 (active)

	readAll(t, l, "fast", 4)
	readAll(t, l, "slow", 1)
	l.Ack(map[string]uint64{"fast": 4 * frameSize, "slow": frameSize})

	res, err := l.Reclaim()
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if res.Watermark != frameSize || len(res.RemovedBases) != 1 || res.RemovedBases[0] != 0 {
		t.Fatalf("unexpected reclaim: %+v", res)
	}
	if segs := l.Segments(); len(segs) != 3 {
		t.Fatalf("segments after reclaim: %+v", segs)
	}

	// The slow consumer catches up; more segments become collectable, but the
	// active segment is never removed.
	readAll(t, l, "slow", 3)
	l.Ack(map[string]uint64{"slow": 4 * frameSize})
	res, err = l.Reclaim()
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if res.Watermark != 3*frameSize || len(res.RemovedBases) != 2 {
		t.Fatalf("unexpected reclaim: %+v", res)
	}
	segs := l.Segments()
	if len(segs) != 1 || segs[0].Base != 3*frameSize {
		t.Fatalf("active segment must survive: %+v", segs)
	}
}

func TestUnregisterAdvancesWatermark(t *testing.T) {
	dir := t.TempDir()
	l := openTest(t, dir, nil)
	defer l.Close()

	l.RegisterConsumer("active-reader")
	l.RegisterConsumer("stuck")
	appendN(t, l, 4)
	readAll(t, l, "active-reader", 4)
	l.Ack(map[string]uint64{"active-reader": 4 * frameSize})

	res, _ := l.Reclaim()
	if res.Watermark != 0 || len(res.RemovedBases) != 0 {
		t.Fatalf("stuck consumer should hold watermark: %+v", res)
	}

	if err := l.UnregisterConsumer("stuck"); err != nil {
		t.Fatalf("UnregisterConsumer: %v", err)
	}
	res, err := l.Reclaim()
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if res.Watermark != 3*frameSize || len(res.RemovedBases) != 3 {
		t.Fatalf("watermark should advance after unregister: %+v", res)
	}
}

func TestCrashRecoveryPersistsCursors(t *testing.T) {
	dir := t.TempDir()
	l := openTest(t, dir, nil)
	l.RegisterConsumer("c1")
	appendN(t, l, 3)
	readAll(t, l, "c1", 2)
	l.Ack(map[string]uint64{"c1": 2 * frameSize})
	l.Close()

	l = openTest(t, dir, nil)
	defer l.Close()
	ack, readHigh, ok := l.Cursor("c1")
	if !ok || ack != 2*frameSize || readHigh != 2*frameSize {
		t.Fatalf("cursor after reopen = %d,%d,%v", ack, readHigh, ok)
	}
	// The read horizon restarts at the durable cursor: the third record is
	// delivered again after recovery.
	rec, err := l.Read("c1")
	if err != nil || rec.Offset != 2*frameSize {
		t.Fatalf("Read after reopen: %+v, %v", rec, err)
	}
	// Retrying the durable acknowledgement is still a no-op.
	if err := l.Ack(map[string]uint64{"c1": 2 * frameSize}); err != nil {
		t.Fatalf("retry ack after reopen: %v", err)
	}
	if err := l.Ack(map[string]uint64{"c1": 3 * frameSize}); err != nil {
		t.Fatalf("ack after re-read: %v", err)
	}
}

func TestAckResponseLostRetryKeepsPosition(t *testing.T) {
	dir := t.TempDir()
	l := openTest(t, dir, nil)
	l.RegisterConsumer("c1")
	appendN(t, l, 2)
	readAll(t, l, "c1", 2)
	l.Close()

	// Crash after the state rename but before the directory fsync: the
	// acknowledgement is durable, the response is lost.
	armed := false
	hook := func(ev FaultEvent) error {
		if armed && ev.Point == FaultStateBeforeDirSync {
			return errors.New("boom")
		}
		return nil
	}
	l = openTest(t, dir, hook)
	armed = true
	readAll(t, l, "c1", 2)
	err := l.Ack(map[string]uint64{"c1": 2 * frameSize})
	if !errors.Is(err, ErrInjectedFault) {
		t.Fatalf("got %v, want ErrInjectedFault", err)
	}
	if _, err := l.Append([]byte("x")); !errors.Is(err, ErrClosed) {
		t.Fatalf("log must be closed after simulated crash, got %v", err)
	}

	l = openTest(t, dir, nil)
	defer l.Close()
	ack, _, _ := l.Cursor("c1")
	if ack != 2*frameSize {
		t.Fatalf("ack was not durable: %d", ack)
	}
	// The client retries the same acknowledgement: success, no regression.
	if err := l.Ack(map[string]uint64{"c1": 2 * frameSize}); err != nil {
		t.Fatalf("retry after lost response: %v", err)
	}
	// A stale lower retry is rejected without moving the cursor.
	if err := l.Ack(map[string]uint64{"c1": frameSize}); !errors.Is(err, ErrAckRegression) {
		t.Fatalf("stale retry: got %v, want ErrAckRegression", err)
	}
	if ack, _, _ := l.Cursor("c1"); ack != 2*frameSize {
		t.Fatalf("cursor regressed: %d", ack)
	}
}

func TestCrashBeforeStateCommitKeepsOldCursor(t *testing.T) {
	dir := t.TempDir()
	l := openTest(t, dir, nil)
	l.RegisterConsumer("c1")
	appendN(t, l, 2)
	readAll(t, l, "c1", 2)
	l.Close()

	// Crash before the temporary state file is even written: nothing changed.
	armed := false
	hook := func(ev FaultEvent) error {
		if armed && ev.Point == FaultStateBeforeWrite {
			return errors.New("boom")
		}
		return nil
	}
	l = openTest(t, dir, hook)
	armed = true
	readAll(t, l, "c1", 2)
	if err := l.Ack(map[string]uint64{"c1": 2 * frameSize}); !errors.Is(err, ErrInjectedFault) {
		t.Fatalf("got %v, want ErrInjectedFault", err)
	}

	l = openTest(t, dir, nil)
	defer l.Close()
	ack, _, _ := l.Cursor("c1")
	if ack != 0 {
		t.Fatalf("uncommitted ack must not survive: %d", ack)
	}
	// Records are delivered again and can be acknowledged without regression.
	readAll(t, l, "c1", 2)
	if err := l.Ack(map[string]uint64{"c1": 2 * frameSize}); err != nil {
		t.Fatalf("re-ack after crash: %v", err)
	}
}

func TestReclaimCrashConsistency(t *testing.T) {
	dir := t.TempDir()
	l := openTest(t, dir, nil)
	l.RegisterConsumer("c1")
	appendN(t, l, 4) // segments 0, 41, 82, 123(active)
	readAll(t, l, "c1", 4)
	l.Ack(map[string]uint64{"c1": 4 * frameSize})
	l.Close()

	// Crash after unlinking reclaimed segments, before committing the
	// watermark.
	armed := false
	hook := func(ev FaultEvent) error {
		if armed && ev.Point == FaultReclaimBeforeState {
			return errors.New("boom")
		}
		return nil
	}
	l = openTest(t, dir, hook)
	armed = true
	if _, err := l.Reclaim(); !errors.Is(err, ErrInjectedFault) {
		t.Fatalf("got %v, want ErrInjectedFault", err)
	}

	// Files are gone but state.json still has the old watermark.
	if _, err := os.Stat(filepath.Join(dir, segmentName(0))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("segment 0 should be unlinked: %v", err)
	}

	l = openTest(t, dir, nil)
	defer l.Close()
	// Recovery raises the watermark to the oldest surviving segment.
	if wm := l.Watermark(); wm != 3*frameSize {
		t.Fatalf("watermark after recovery = %d, want %d", wm, 3*frameSize)
	}
	if ack, _, _ := l.Cursor("c1"); ack != 4*frameSize {
		t.Fatalf("consumer cursor lost: %d", ack)
	}
	// Reclaim is idempotent now.
	res, err := l.Reclaim()
	if err != nil || len(res.RemovedBases) != 0 || res.Watermark != 3*frameSize {
		t.Fatalf("reclaim after recovery: %+v, %v", res, err)
	}
	// The log still accepts appends at the right offset.
	off, err := l.Append([]byte(testPayload))
	if err != nil || off != 4*frameSize {
		t.Fatalf("append after recovery: %d, %v", off, err)
	}
}

func TestTornTailIsTruncatedOnRecovery(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir, Options{MaxSegmentBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	l.RegisterConsumer("c1")
	appendN(t, l, 2)
	l.Close()

	// Simulate a torn write: garbage appended to the active segment.
	segPath := filepath.Join(dir, segmentName(0))
	f, err := os.OpenFile(segPath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("partial-frame-garbage"))
	f.Close()

	l, err = Open(dir, Options{MaxSegmentBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if end := l.End(); end != 2*frameSize {
		t.Fatalf("torn tail not truncated: end=%d", end)
	}
	recs := readAll(t, l, "c1", 2)
	if recs[1].Offset != frameSize {
		t.Fatalf("unexpected record after recovery: %+v", recs[1])
	}
	// Appends continue at the truncated boundary.
	off, err := l.Append([]byte(testPayload))
	if err != nil || off != 2*frameSize {
		t.Fatalf("append after truncation: %d, %v", off, err)
	}
}

func TestRegisterStartsAtLogEnd(t *testing.T) {
	dir := t.TempDir()
	l := openTest(t, dir, nil)
	defer l.Close()

	appendN(t, l, 2)
	if err := l.RegisterConsumer("late"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Read("late"); err != io.EOF {
		t.Fatalf("late consumer should see EOF, got %v", err)
	}
	// Re-registering is a no-op.
	if err := l.RegisterConsumer("late"); err != nil {
		t.Fatal(err)
	}
	// Unregistering an unknown consumer is a no-op.
	if err := l.UnregisterConsumer("nobody"); err != nil {
		t.Fatal(err)
	}
}

func TestSecondOpenFailsWhileLocked(t *testing.T) {
	dir := t.TempDir()
	l := openTest(t, dir, nil)
	defer l.Close()
	if _, err := Open(dir, testOpts(nil)); err == nil {
		t.Fatal("second Open should fail while the log is locked")
	}
}

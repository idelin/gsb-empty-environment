package appendhandle

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestInterruptRecoveryResumesAtFirstUnwrittenByte(t *testing.T) {
	ff := NewFaultFile(nil)
	ff.MaxWrite = 5
	ff.FailEvery = 4
	ff.FailsLeft = 50
	v := newFaultVolume(t, ff)

	const size = 50
	h, err := v.Reserve(size)
	if err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(i + 1)
	}

	// First request dies after accepting 20 bytes across short writes/faults.
	pos := 0
	for attempts := 0; attempts < 4 && pos < 20; attempts++ {
		n, err := h.Write(context.Background(), payload[pos:])
		pos += n
		if errors.Is(err, ErrInjected) {
			continue
		}
		if err != nil {
			t.Fatalf("phase 1 write: %v", err)
		}
	}
	if h.Written() < 20 || h.Written() >= size {
		t.Fatalf("interrupted written count = %d, want partial write in [20,%d)", h.Written(), size)
	}

	// Recovery request picks up the same handle at byte 20.
	written := h.Written()
	writeFull(t, context.Background(), h, payload[written:])
	if h.Written() != size {
		t.Fatalf("recovered written count = %d, want %d", h.Written(), size)
	}
	if _, err := h.Commit(context.Background()); err != nil {
		t.Fatalf("commit after recovery: %v", err)
	}

	got := make([]byte, size)
	if _, err := v.ReadAt(got, 0); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("recovered payload mismatch: %x", got)
	}
}

func TestOnlyCommittedPrefixIsVisible(t *testing.T) {
	ff := NewFaultFile(nil)
	v := newFaultVolume(t, ff)

	h, err := v.Reserve(8)
	if err != nil {
		t.Fatal(err)
	}
	writeFull(t, context.Background(), h, []byte("12345678"))

	if got := v.Size(); got != 0 {
		t.Fatalf("uncommitted reservation visible, size = %d", got)
	}
	if _, err := v.ReadAt(make([]byte, 1), 0); !errors.Is(err, io.EOF) {
		t.Fatalf("uncommitted read err = %v, want io.EOF", err)
	}
	if _, err := v.ReadAt(make([]byte, 1), -1); !errors.Is(err, ErrNegativeOffset) {
		t.Fatalf("negative read err = %v, want ErrNegativeOffset", err)
	}

	if _, err := h.Commit(context.Background()); err != nil {
		t.Fatalf("commit: %v", err)
	}

	tail, err := v.Reserve(4)
	if err != nil {
		t.Fatal(err)
	}
	writeFull(t, context.Background(), tail, []byte("tail"))
	buf := make([]byte, 12)
	n, err := v.ReadAt(buf, 0)
	if !errors.Is(err, io.EOF) || n != 8 {
		t.Fatalf("clipped read = (%d, %v), want (8, io.EOF)", n, err)
	}
	if string(buf[:8]) != "12345678" {
		t.Fatalf("committed payload = %q", buf[:8])
	}
}

func TestCommitWaitsForEarlierReservation(t *testing.T) {
	ff := NewFaultFile(nil)
	v := newFaultVolume(t, ff)

	ha, err := v.Reserve(10)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := v.Reserve(10)
	if err != nil {
		t.Fatal(err)
	}
	writeFull(t, context.Background(), ha, bytes.Repeat([]byte{'a'}, 10))
	writeFull(t, context.Background(), hb, bytes.Repeat([]byte{'b'}, 10))

	type result struct {
		prefix int64
		err    error
	}
	done := make(chan result, 1)
	go func() {
		prefix, err := hb.Commit(context.Background())
		done <- result{prefix, err}
	}()

	select {
	case r := <-done:
		t.Fatalf("B committed before A: %+v", r)
	case <-time.After(50 * time.Millisecond):
	}
	if got := v.Size(); got != 0 {
		t.Fatalf("prefix advanced out of order: %d", got)
	}

	if _, err := ha.Commit(context.Background()); err != nil {
		t.Fatalf("commit A: %v", err)
	}
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("commit B: %v", r.err)
		}
		if r.prefix != 20 {
			t.Fatalf("B commit prefix = %d, want 20", r.prefix)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("B commit never unblocked")
	}
	if got := v.Size(); got != 20 {
		t.Fatalf("final prefix = %d, want 20", got)
	}
}

func TestSyncFailureLeavesHandleRetryable(t *testing.T) {
	ff := NewFaultFile(nil)
	ff.SyncFails = 2
	v := newFaultVolume(t, ff)

	h, err := v.Reserve(12)
	if err != nil {
		t.Fatal(err)
	}
	writeFull(t, context.Background(), h, bytes.Repeat([]byte{'S'}, 12))

	if _, err := h.Commit(context.Background()); !errors.Is(err, ErrInjected) {
		t.Fatalf("first commit err = %v, want injected sync fault", err)
	}
	if got := v.Size(); got != 0 {
		t.Fatalf("prefix advanced despite failed sync: %d", got)
	}
	if got := ff.SyncCalls(); got != 1 {
		t.Fatalf("sync calls = %d, want 1", got)
	}

	// Second attempt also fails, third succeeds. State is fully recoverable.
	if _, err := h.Commit(context.Background()); !errors.Is(err, ErrInjected) {
		t.Fatalf("second commit err = %v", err)
	}
	prefix, err := h.Commit(context.Background())
	if err != nil {
		t.Fatalf("third commit: %v", err)
	}
	if prefix != 12 || v.Size() != 12 {
		t.Fatalf("prefix after retry = %d (volume %d), want 12", prefix, v.Size())
	}

	// Re-committing a published handle is a no-op success.
	if _, err := h.Commit(context.Background()); err != nil {
		t.Fatalf("idempotent commit: %v", err)
	}
}

func TestPartialCommitAndFullReservationGuards(t *testing.T) {
	ff := NewFaultFile(nil)
	v := newFaultVolume(t, ff)

	if _, err := v.Reserve(0); !errors.Is(err, ErrInvalidSize) {
		t.Fatalf("zero reservation err = %v, want ErrInvalidSize", err)
	}

	h, err := v.Reserve(4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Write(context.Background(), []byte("ab")); err != nil {
		t.Fatalf("partial write: %v", err)
	}
	if _, err := h.Commit(context.Background()); !errors.Is(err, ErrPartialReservation) {
		t.Fatalf("partial commit err = %v, want ErrPartialReservation", err)
	}
	writeFull(t, context.Background(), h, []byte("cd"))
	if _, err := h.Write(context.Background(), []byte{'x'}); !errors.Is(err, ErrReservationFull) {
		t.Fatalf("overflow write err = %v, want ErrReservationFull", err)
	}
	if _, err := h.Commit(context.Background()); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err := h.Write(context.Background(), []byte{'y'}); !errors.Is(err, ErrHandleDone) {
		t.Fatalf("write after commit err = %v, want ErrHandleDone", err)
	}

	// Abort after commit must be rejected; published bytes cannot be revoked.
	if err := h.Abort(); !errors.Is(err, ErrHandleDone) {
		t.Fatalf("abort after commit err = %v, want ErrHandleDone", err)
	}
}

func TestHandleIsNotSeekable(t *testing.T) {
	var h *Handle
	if _, ok := any(h).(io.Seeker); ok {
		t.Fatal("Handle must not implement io.Seeker: callers cannot move the offset")
	}
	var v *Volume
	if _, ok := any(v).(io.Writer); ok {
		t.Fatal("Volume must not accept positional-free writes; all writes go through reserved handles")
	}
}

func TestCommitContextTimeoutIsRetryable(t *testing.T) {
	ff := NewFaultFile(nil)
	v := newFaultVolume(t, ff)

	ha, err := v.Reserve(8)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := v.Reserve(8)
	if err != nil {
		t.Fatal(err)
	}
	writeFull(t, context.Background(), ha, []byte("aaaaaaaa"))
	writeFull(t, context.Background(), hb, []byte("bbbbbbbb"))

	// B cannot pass A; its deadline expires without altering handle state.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := hb.Commit(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timed-out commit err = %v, want DeadlineExceeded", err)
	}
	if got := v.Size(); got != 0 {
		t.Fatalf("prefix moved after timeout: %d", got)
	}
	if hb.Done() != true {
		t.Fatal("B should remain marked committed while waiting for the prefix")
	}

	if _, err := ha.Commit(context.Background()); err != nil {
		t.Fatalf("commit A: %v", err)
	}
	prefix, err := hb.Commit(context.Background())
	if err != nil {
		t.Fatalf("retry B: %v", err)
	}
	if prefix != 16 || v.Size() != 16 {
		t.Fatalf("prefix after retry = %d (volume %d), want 16", prefix, v.Size())
	}
}

func TestRealFileHolePunchAndReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.log")

	v, of, err := OpenVolume(path, nil)
	if err != nil {
		t.Fatalf("open volume: %v", err)
	}

	first, err := v.Reserve(4096)
	if err != nil {
		t.Fatal(err)
	}
	writeFull(t, context.Background(), first, bytes.Repeat([]byte{'a'}, 4096))
	if _, err := first.Commit(context.Background()); err != nil {
		t.Fatalf("commit first: %v", err)
	}

	hole, err := v.Reserve(4096)
	if err != nil {
		t.Fatal(err)
	}
	writeFull(t, context.Background(), hole, bytes.Repeat([]byte{'b'}, 4096))

	// Capture the block count with all three ranges materialized so the hole
	// punch can be proven to deallocate storage rather than zero-fill it.
	var stBefore syscall.Stat_t
	if err := syscall.Stat(path, &stBefore); err != nil {
		t.Fatal(err)
	}
	if err := hole.Abort(); err != nil {
		t.Fatalf("abort hole: %v", err)
	}
	var stPunched syscall.Stat_t
	if err := syscall.Stat(path, &stPunched); err != nil {
		t.Fatal(err)
	}
	if stPunched.Blocks >= stBefore.Blocks {
		t.Fatalf("hole did not deallocate blocks: before=%d after=%d", stBefore.Blocks, stPunched.Blocks)
	}

	last, err := v.Reserve(4096)
	if err != nil {
		t.Fatal(err)
	}
	writeFull(t, context.Background(), last, bytes.Repeat([]byte{'c'}, 4096))
	if _, err := last.Commit(context.Background()); err != nil {
		t.Fatalf("commit last: %v", err)
	}
	if err := of.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() != 3*4096 {
		t.Fatalf("file size = %d, want %d (hole must not shrink size)", st.Size(), 3*4096)
	}

	// Reopen: stat size becomes the committed prefix, offsets keep their values.
	v2, of2, err := OpenVolume(path, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer of2.Close()
	if got := v2.Size(); got != 3*4096 {
		t.Fatalf("reopened prefix = %d, want %d", got, 3*4096)
	}
	zeros := make([]byte, 4096)
	if _, err := v2.ReadAt(zeros, 4096); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("read hole: %v", err)
	}
	if !bytes.Equal(zeros, make([]byte, 4096)) {
		t.Fatal("hole did not read back as zeros after reopen")
	}
	next, err := v2.Reserve(8)
	if err != nil {
		t.Fatal(err)
	}
	if next.Offset() != 3*4096 {
		t.Fatalf("offset after reopen = %d, want %d", next.Offset(), 3*4096)
	}
}

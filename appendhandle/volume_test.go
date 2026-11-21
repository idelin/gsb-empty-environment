package appendhandle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"testing"
)

// writeFull drives a handle to completion across short writes and transient
// injected faults, mirroring how a service retries a multi-segment append.
func writeFull(t *testing.T, ctx context.Context, h *Handle, payload []byte) {
	t.Helper()
	pos := 0
	for pos < len(payload) {
		n, err := h.Write(ctx, payload[pos:])
		pos += n
		if errors.Is(err, ErrInjected) {
			continue
		}
		if err != nil {
			t.Fatalf("write at %d failed: %v", pos, err)
		}
	}
}

func newFaultVolume(t *testing.T, ff *FaultFile) *Volume {
	t.Helper()
	v, err := Open(ff, nil)
	if err != nil {
		t.Fatalf("open volume: %v", err)
	}
	return v
}

func TestConcurrentAppendUnderFaults(t *testing.T) {
	ff := NewFaultFile(nil)
	ff.MaxWrite = 3
	ff.FailEvery = 7
	ff.FailsLeft = 500
	v := newFaultVolume(t, ff)

	const runners = 16
	var wg sync.WaitGroup
	errs := make(chan error, runners)
	type reservation struct {
		off     int64
		payload []byte
	}
	records := make([]reservation, runners)

	for r := 0; r < runners; r++ {
		wg.Add(1)
		go func(run int) {
			defer wg.Done()
			size := int64(64 + run*17)
			h, err := v.Reserve(size)
			if err != nil {
				errs <- fmt.Errorf("runner %d reserve: %w", run, err)
				return
			}
			payload := make([]byte, size)
			for i := range payload {
				payload[i] = byte((run*13 + i) % 251)
			}
			writeFull(t, context.Background(), h, payload)
			if h.Written() != size {
				errs <- fmt.Errorf("runner %d wrote %d of %d", run, h.Written(), size)
				return
			}
			records[run] = reservation{off: h.Offset(), payload: payload}
			if _, err := h.Commit(context.Background()); err != nil {
				errs <- fmt.Errorf("runner %d commit: %w", run, err)
			}
		}(r)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	var total int64
	for r := 0; r < runners; r++ {
		total += int64(64 + r*17)
	}
	if got := v.Size(); got != total {
		t.Fatalf("committed prefix = %d, want %d", got, total)
	}
	if got := v.Allocated(); got != total {
		t.Fatalf("allocated high-water mark = %d, want %d", got, total)
	}

	sorted := append([]reservation(nil), records...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].off < sorted[j].off })
	for i, rec := range sorted {
		buf := make([]byte, len(rec.payload))
		if _, err := v.ReadAt(buf, rec.off); err != nil {
			t.Fatalf("read reservation %d at %d: %v", i, rec.off, err)
		}
		if !bytes.Equal(buf, rec.payload) {
			t.Fatalf("reservation at %d payload mismatch", rec.off)
		}
	}
	// Allocations must be disjoint and exactly tile the reserved range.
	for i := 1; i < len(sorted); i++ {
		prev := sorted[i-1]
		if got := prev.off + int64(len(prev.payload)); got != sorted[i].off {
			t.Fatalf("reservation gap/reuse: %d != %d", got, sorted[i].off)
		}
	}
}

func TestAbortLeavesExplicitHoleAndNeverReusesOffsets(t *testing.T) {
	ff := NewFaultFile(nil)
	v := newFaultVolume(t, ff)

	ha, err := v.Reserve(64)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := v.Reserve(64)
	if err != nil {
		t.Fatal(err)
	}
	writeFull(t, context.Background(), ha, bytes.Repeat([]byte{'A'}, 64))
	writeFull(t, context.Background(), hb, bytes.Repeat([]byte{'B'}, 30))

	if _, err := ha.Commit(context.Background()); err != nil {
		t.Fatalf("commit A: %v", err)
	}
	if got := v.Size(); got != 64 {
		t.Fatalf("size after A = %d, want 64", got)
	}

	if err := hb.Abort(); err != nil {
		t.Fatalf("abort B: %v", err)
	}
	if got := v.Size(); got != 128 {
		t.Fatalf("prefix did not advance over hole: %d", got)
	}
	raw := ff.MemFile().Data()
	if !bytes.Equal(raw[64:128], make([]byte, 64)) {
		t.Fatalf("aborted range not zeroed: %x", raw[64:128])
	}
	holes := ff.MemFile().Holes()
	found := false
	for _, iv := range holes {
		if iv.lo == 64 && iv.hi == 128 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected explicit hole [64,128), got %+v", holes)
	}

	hc, err := v.Reserve(16)
	if err != nil {
		t.Fatal(err)
	}
	if hc.Offset() != 128 {
		t.Fatalf("new reservation offset = %d, want 128", hc.Offset())
	}
	writeFull(t, context.Background(), hc, bytes.Repeat([]byte{'C'}, 16))
	if _, err := hc.Commit(context.Background()); err != nil {
		t.Fatalf("commit C: %v", err)
	}

	full := make([]byte, 144)
	if _, err := v.ReadAt(full, 0); err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("read full prefix: %v", err)
	}
	if !bytes.Equal(full[:64], bytes.Repeat([]byte{'A'}, 64)) {
		t.Fatal("A payload lost")
	}
	if !bytes.Equal(full[64:128], make([]byte, 64)) {
		t.Fatal("hole filled with stale bytes")
	}
	if !bytes.Equal(full[128:], bytes.Repeat([]byte{'C'}, 16)) {
		t.Fatal("C payload lost")
	}
}

func TestContextCancelPunchesWholeReservation(t *testing.T) {
	ff := NewFaultFile(nil)
	ff.MaxWrite = 2
	v := newFaultVolume(t, ff)

	h, err := v.Reserve(20)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := h.Write(ctx, bytes.Repeat([]byte{'X'}, 20)); err != nil {
		t.Fatalf("initial write: %v", err)
	}
	cancel()

	if _, err := h.Write(ctx, []byte{'Y'}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled write err = %v, want context.Canceled", err)
	}
	if !h.Done() {
		t.Fatal("handle still open after canceled write")
	}
	if _, err := h.Write(context.Background(), []byte{'Z'}); !errors.Is(err, ErrHandleDone) {
		t.Fatalf("write after abort err = %v, want ErrHandleDone", err)
	}
	if _, err := h.Commit(context.Background()); !errors.Is(err, ErrHandleDone) {
		t.Fatalf("commit after abort err = %v, want ErrHandleDone", err)
	}

	if got := v.Size(); got != 20 {
		t.Fatalf("prefix over canceled reservation = %d, want 20", got)
	}
	if raw := ff.MemFile().Data(); !bytes.Equal(raw, make([]byte, 20)) {
		t.Fatalf("partially written bytes survived cancel: %x", raw)
	}
	if ff.MemFile().HoleCount != 1 {
		t.Fatalf("hole punch count = %d, want 1", ff.MemFile().HoleCount)
	}

	next, err := v.Reserve(4)
	if err != nil {
		t.Fatal(err)
	}
	if next.Offset() != 20 {
		t.Fatalf("offset after cancel = %d, want 20", next.Offset())
	}
}

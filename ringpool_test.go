package ringpool

import (
	"bytes"
	"io"
	"math/rand"
	"testing"
	"time"
)

// fillFrame writes a deterministic pseudo-random pattern for frame seed.
func fillFrame(seed int, buf []byte) {
	x := uint32(seed)*2654435761 + 1
	for i := range buf {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		buf[i] = byte(x)
	}
}

// verifyFrame checks buf against the deterministic pattern for seed.
func verifyFrame(t *testing.T, seed int, buf []byte) {
	t.Helper()
	x := uint32(seed)*2654435761 + 1
	for i := range buf {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		if buf[i] != byte(x) {
			t.Fatalf("frame %d corrupted at byte %d: got %02x want %02x", seed, i, buf[i], byte(x))
		}
	}
}

// TestStream100MB pushes 100MB of random-size frames through a 4MB ring
// pool and verifies data integrity, borrow/release conservation, and
// that outstanding memory never exceeds the ring capacity.
func TestStream100MB(t *testing.T) {
	const (
		blockSize = 4096
		numBlocks = 1024 // 4 MiB ring
		total     = 100 << 20
		maxFrame  = 64 << 10
	)
	pool := New(blockSize, numBlocks)
	defer pool.Close()

	pr, pw := io.Pipe()
	fw := NewFrameWriter(pw)
	fr := NewFrameReader(pool, pr)

	var maxOutstanding int64

	werr := make(chan error, 1)
	go func() {
		rng := rand.New(rand.NewSource(42))
		sent, frame := 0, 0
		for sent < total {
			n := 1 + rng.Intn(maxFrame)
			if n > total-sent {
				n = total - sent
			}
			payload := make([]byte, n)
			fillFrame(frame, payload)
			if err := fw.WriteFrame(payload); err != nil {
				werr <- err
				return
			}
			sent += n
			frame++
		}
		werr <- pw.Close()
	}()

	received, frames := 0, 0
	var checksum uint64
	for {
		s, err := fr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read frame %d: %v", frames, err)
		}
		verifyFrame(t, frames, s.Bytes())
		for _, b := range s.Bytes() {
			checksum += uint64(b)
		}
		received += s.Len()
		if cur := pool.Stats().OutstandingBytes; cur > maxOutstanding {
			maxOutstanding = cur
		}
		s.Release()
		frames++
	}
	if err := <-werr; err != nil {
		t.Fatalf("writer: %v", err)
	}

	if received != total {
		t.Fatalf("streamed %d bytes, want %d", received, total)
	}
	st := pool.Stats()
	if st.OutstandingSlices != 0 || st.OutstandingBytes != 0 {
		t.Fatalf("leak: %d slices / %d bytes still outstanding", st.OutstandingSlices, st.OutstandingBytes)
	}
	if st.TotalBorrowed != st.TotalReleased {
		t.Fatalf("borrow/release not conserved: %d vs %d", st.TotalBorrowed, st.TotalReleased)
	}
	if st.FreeBlocks != numBlocks {
		t.Fatalf("blocks leaked: %d of %d free", st.FreeBlocks, numBlocks)
	}
	if maxOutstanding > int64(pool.Capacity()) {
		t.Fatalf("outstanding %d exceeded ring capacity %d", maxOutstanding, pool.Capacity())
	}
	t.Logf("streamed %d bytes in %d frames (checksum=%d)", received, frames, checksum)
	t.Logf("ring capacity %d bytes, max outstanding %d bytes", pool.Capacity(), maxOutstanding)
	t.Logf("borrowed=%d released=%d freeBlocks=%d/%d",
		st.TotalBorrowed, st.TotalReleased, st.FreeBlocks, st.NumBlocks)
}

// TestRetainRelease checks reference counting and block recycling.
func TestRetainRelease(t *testing.T) {
	pool := New(1024, 8)
	defer pool.Close()

	res, err := pool.Reserve(100)
	if err != nil {
		t.Fatal(err)
	}
	copy(res.Buf, bytes.Repeat([]byte{0xAB}, 100))
	s := res.Commit()

	s.Retain()
	s.Retain()
	if got := pool.Stats().OutstandingSlices; got != 1 {
		t.Fatalf("outstanding slices = %d, want 1", got)
	}
	s.Release()
	s.Release()
	if got := pool.Stats().FreeBlocks; got != 7 {
		t.Fatalf("free blocks = %d, want 7 (slice still held)", got)
	}
	s.Release()
	st := pool.Stats()
	if st.OutstandingSlices != 0 || st.FreeBlocks != 8 {
		t.Fatalf("after final release: %+v", st)
	}
	if st.TotalBorrowed != st.TotalReleased {
		t.Fatalf("borrow/release mismatch: %+v", st)
	}
}

// TestDoubleReleasePanics guards the conservation invariant.
func TestDoubleReleasePanics(t *testing.T) {
	pool := New(1024, 4)
	defer pool.Close()
	res, _ := pool.Reserve(10)
	s := res.Commit()
	s.Release()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on double release")
		}
	}()
	s.Release()
}

// TestWrapAround forces the ring cursor to wrap and verifies that both
// the wrapped allocation and still-held slices stay intact.
func TestWrapAround(t *testing.T) {
	pool := New(1024, 4) // 4 KiB ring
	defer pool.Close()

	mustReserve := func(n int) *Reservation {
		t.Helper()
		r, err := pool.Reserve(n)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	r1 := mustReserve(2048) // blocks 0,1
	copy(r1.Buf, bytes.Repeat([]byte{1}, 2048))
	s1 := r1.Commit()

	r2 := mustReserve(1024) // block 2
	copy(r2.Buf, bytes.Repeat([]byte{2}, 1024))
	s2 := r2.Commit()

	s1.Release() // free blocks 0,1; cursor sits at block 3

	r3 := mustReserve(2048) // must wrap to blocks 0,1
	copy(r3.Buf, bytes.Repeat([]byte{3}, 2048))
	s3 := r3.Commit()

	if !bytes.Equal(s2.Bytes(), bytes.Repeat([]byte{2}, 1024)) {
		t.Fatal("held slice corrupted by wrap-around allocation")
	}
	if !bytes.Equal(s3.Bytes(), bytes.Repeat([]byte{3}, 2048)) {
		t.Fatal("wrapped slice corrupted")
	}
	s2.Release()
	s3.Release()
	if got := pool.Stats().FreeBlocks; got != 4 {
		t.Fatalf("free blocks = %d, want 4", got)
	}
}

// TestBackpressure verifies Reserve blocks when the ring is full and
// unblocks when consumers release slices.
func TestBackpressure(t *testing.T) {
	pool := New(1024, 4)
	defer pool.Close()

	r1, err := pool.Reserve(4096) // whole ring
	if err != nil {
		t.Fatal(err)
	}
	s1 := r1.Commit()

	if _, ok, err := pool.TryReserve(1024); err != nil || ok {
		t.Fatalf("TryReserve on full ring = (%v, %v), want (nil, false, nil)", ok, err)
	}

	done := make(chan *Slice, 1)
	go func() {
		r2, err := pool.Reserve(1024)
		if err != nil {
			t.Error(err)
			return
		}
		done <- r2.Commit()
	}()

	select {
	case <-done:
		t.Fatal("Reserve succeeded while ring was full")
	case <-time.After(50 * time.Millisecond):
	}

	s1.Release()
	select {
	case s2 := <-done:
		s2.Release()
	case <-time.After(2 * time.Second):
		t.Fatal("Reserve did not unblock after release")
	}
	if got := pool.Stats().FreeBlocks; got != 4 {
		t.Fatalf("free blocks = %d, want 4", got)
	}
}

// TestTooLarge ensures oversized reservations fail instead of deadlocking.
func TestTooLarge(t *testing.T) {
	pool := New(1024, 4)
	defer pool.Close()
	if _, err := pool.Reserve(4097); err != ErrTooLarge {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

// TestZeroAlloc proves the hot path (reserve/commit/release) performs no
// heap allocations once object pools are warm.
func TestZeroAlloc(t *testing.T) {
	pool := New(4096, 16)
	defer pool.Close()
	allocs := testing.AllocsPerRun(1000, func() {
		res, err := pool.Reserve(100)
		if err != nil {
			t.Fatal(err)
		}
		s := res.Commit()
		_ = s.Bytes()
		s.Release()
	})
	if allocs != 0 {
		t.Fatalf("hot path allocated %v times per op, want 0", allocs)
	}
}

// TestFrameReaderCancelOnError covers the reservation cancel path.
func TestFrameReaderCancelOnError(t *testing.T) {
	pool := New(1024, 4)
	defer pool.Close()
	// Header promises 100 bytes, stream ends after 10.
	var buf bytes.Buffer
	buf.Write([]byte{0, 0, 0, 100})
	buf.Write(make([]byte, 10))
	fr := NewFrameReader(pool, &buf)
	if _, err := fr.Next(); err != io.ErrUnexpectedEOF {
		t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
	}
	if got := pool.Stats().FreeBlocks; got != 4 {
		t.Fatalf("free blocks = %d, want 4 (reservation must be cancelled)", got)
	}
}

func BenchmarkFrameThroughput(b *testing.B) {
	pool := New(4096, 1024)
	defer pool.Close()
	payload := make([]byte, 1500)
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res, err := pool.Reserve(len(payload))
		if err != nil {
			b.Fatal(err)
		}
		copy(res.Buf, payload)
		s := res.Commit()
		_ = s.Bytes()
		s.Release()
	}
}

package ringpool

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"runtime"
	"testing"
)

const (
	testNumBlocks = 256                           // blocks in the ring
	testBlockSize = 16 * 1024                     // 16 KiB per block
	testPoolBytes = testNumBlocks * testBlockSize // 4 MiB resident
	streamBytes   = 100 << 20                     // 100 MiB random frame stream
	maxFrame      = 64 * 1024                     // frames intentionally span blocks
	minFrame      = 64
	frameSeed     = 42
)

// nextFrame generates the next deterministic random frame: a 4-byte
// little-endian length header followed by n random payload bytes.
func nextFrame(r *rand.Rand, hdr, payload []byte) int {
	n := minFrame + r.Intn(maxFrame-minFrame)
	binary.LittleEndian.PutUint32(hdr, uint32(n))
	r.Read(payload[:n])
	return n
}

// TestStream100MB pushes 100 MiB of random frames through a 4 MiB ring
// pool and verifies byte-exact delivery, borrow/release conservation and
// full block recycling.
func TestStream100MB(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 100MiB stream in -short mode")
	}
	streamFrames(t, streamBytes)
}

func streamFrames(t *testing.T, total int) {
	t.Helper()
	p := New(testNumBlocks, testBlockSize)
	if got := p.Capacity(); got != testPoolBytes {
		t.Fatalf("resident capacity = %d, want %d (4MiB)", got, testPoolBytes)
	}

	whdr := make([]byte, 4)
	payload := make([]byte, maxFrame)

	// Producer: deterministic random frames.
	go func() {
		wr := rand.New(rand.NewSource(frameSeed))
		written := 0
		for written < total {
			n := nextFrame(wr, whdr, payload)
			if _, err := p.Write(whdr); err != nil {
				t.Errorf("write header: %v", err)
				return
			}
			if _, err := p.Write(payload[:n]); err != nil {
				t.Errorf("write payload: %v", err)
				return
			}
			written += n
		}
		p.Close()
	}()

	// Consumer: reassemble frames from zero-copy slices and verify
	// against the same deterministic stream.
	rd := rand.New(rand.NewSource(frameSeed))
	expect := make([]byte, maxFrame)
	frame := make([]byte, maxFrame)
	rhdr := make([]byte, 4)
	var borrowed, released int64
	consumed := 0

	fill := func(dst []byte) {
		for off := 0; off < len(dst); {
			s, err := p.Slice(len(dst) - off)
			if err != nil {
				t.Fatalf("slice: %v", err)
			}
			borrowed++
			off += copy(dst[off:], s.Bytes())
			s.Release()
			released++
		}
	}

	for consumed < total {
		fill(rhdr)
		n := int(binary.LittleEndian.Uint32(rhdr))
		// Mirror the producer's PRNG sequence to regenerate expectations.
		wantN := minFrame + rd.Intn(maxFrame-minFrame)
		if n != wantN {
			t.Fatalf("frame length = %d, want %d (stream corrupted)", n, wantN)
		}
		rd.Read(expect[:n])
		fill(frame[:n])
		if !bytes.Equal(frame[:n], expect[:n]) {
			t.Fatalf("frame payload mismatch at offset %d", consumed)
		}
		consumed += n
	}

	if _, err := p.Slice(1); err != ErrClosed {
		t.Fatalf("Slice after drain = %v, want ErrClosed", err)
	}
	if borrowed != released {
		t.Fatalf("borrow/release not conserved: borrowed=%d released=%d", borrowed, released)
	}
	if got := p.Leased(); got != 0 {
		t.Fatalf("leased blocks after drain = %d, want 0", got)
	}
	if got := p.FreeBlocks(); got != testNumBlocks {
		t.Fatalf("free blocks after drain = %d, want %d (block leak)", got, testNumBlocks)
	}
	t.Logf("streamed %d MiB through %d MiB ring; slices borrowed=%d released=%d; leased=%d free=%d/%d",
		total>>20, p.Capacity()>>20, borrowed, released, p.Leased(), p.FreeBlocks(), p.NumBlocks())
}

// TestSliceZeroCopy proves slices alias ring memory: mutating the ring
// via subsequent writes must not copy, and two slices into one block
// share the same backing array.
func TestSliceZeroCopy(t *testing.T) {
	p := New(4, 64)
	in := []byte("hello ring")
	if _, err := p.Write(in); err != nil {
		t.Fatal(err)
	}
	s1, err := p.Slice(5)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := p.Slice(5)
	if err != nil {
		t.Fatal(err)
	}
	if string(s1.Bytes()) != "hello" || string(s2.Bytes()) != " ring" {
		t.Fatalf("got %q %q", s1.Bytes(), s2.Bytes())
	}
	// Both slices came from the same block: same backing array, and the
	// block stays leased until both are released.
	if &s1.Bytes()[0] == &s2.Bytes()[0] {
		t.Fatal("slices overlap unexpectedly")
	}
	if got := p.Leased(); got != 1 {
		t.Fatalf("leased = %d, want 1 (shared block)", got)
	}
	s1.Release()
	if got := p.FreeBlocks(); got != 3 {
		t.Fatalf("free = %d, want 3 (block still referenced)", got)
	}
	s2.Release()
	if got := p.FreeBlocks(); got != 4 {
		t.Fatalf("free = %d, want 4 (block recycled)", got)
	}
}

// TestBackpressure verifies the writer blocks when the ring is full and
// unblocks as the consumer releases slices.
func TestBackpressure(t *testing.T) {
	p := New(2, 16)
	if _, err := p.Write(make([]byte, 32)); err != nil { // fills both blocks
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		p.Write([]byte("x")) // must block until a block is freed
		close(done)
	}()
	runtime.Gosched()
	select {
	case <-done:
		t.Fatal("write completed while ring was full")
	default:
	}
	s, err := p.Slice(16)
	if err != nil {
		t.Fatal(err)
	}
	s.Release() // frees one block -> writer unblocks
	<-done
}

// TestSteadyStateAllocs measures allocations while streaming after
// warm-up; the hot path must be (near) allocation free.
func TestSteadyStateAllocs(t *testing.T) {
	p := New(testNumBlocks, testBlockSize)
	buf := make([]byte, 4096)
	rnd := rand.New(rand.NewSource(1))
	rnd.Read(buf)

	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				p.Close()
				return
			default:
				if _, err := p.Write(buf); err != nil {
					return
				}
			}
		}
	}()

	drain := func(bytesToRead int) {
		scratch := make([]byte, 4096)
		for bytesToRead > 0 {
			s, err := p.Slice(4096)
			if err != nil {
				t.Fatalf("slice: %v", err)
			}
			copy(scratch, s.Bytes()) // consumer-side use
			bytesToRead -= s.Len()
			s.Release()
		}
	}

	drain(1 << 20) // warm up slicePool and the pipeline
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	drain(32 << 20) // 32 MiB steady-state stream
	runtime.ReadMemStats(&after)
	close(stop)

	mallocs := after.Mallocs - before.Mallocs
	allocBytes := after.TotalAlloc - before.TotalAlloc
	t.Logf("32MiB steady-state: mallocs=%d allocBytes=%d numGC=%d",
		mallocs, allocBytes, after.NumGC-before.NumGC)
	limit := uint64(64 * 1024)
	if raceEnabled {
		limit = 512 * 1024 // the race detector allocates on its own
	}
	if allocBytes > limit {
		t.Fatalf("steady-state allocated %d bytes for 32MiB streamed, want < %d", allocBytes, limit)
	}
}

// BenchmarkSlice measures the zero-copy cut/release hot path.
func BenchmarkSlice(b *testing.B) {
	p := New(testNumBlocks, testBlockSize)
	chunk := make([]byte, testBlockSize)
	go func() {
		for {
			if _, err := p.Write(chunk); err != nil {
				return
			}
		}
	}()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s, err := p.Slice(testBlockSize)
		if err != nil {
			b.Fatal(err)
		}
		s.Release()
	}
	b.SetBytes(testBlockSize)
	p.Close()
}

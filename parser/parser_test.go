package parser_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"example.org/offlinecli/lease"
	"example.org/offlinecli/parser"
)

func frame(payload []byte) []byte {
	b := make([]byte, 2+len(payload))
	binary.BigEndian.PutUint16(b, uint16(len(payload)))
	copy(b[2:], payload)
	return b
}

func TestParserDecodesFramesAndEnforcesIsolation(t *testing.T) {
	const capacity = 64
	pool := lease.NewPool(1, capacity)
	ctx := context.Background()

	leaseA, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p := parser.New(leaseA, capacity)

	wire := append(frame([]byte("alpha")), frame([]byte("beta"))...)
	frames, err := p.Read(wire)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("frames = %d; want 2", len(frames))
	}
	if b, _ := frames[0].Payload(); !bytes.Equal(b, []byte("alpha")) {
		t.Fatalf("frame0 = %q; want alpha", b)
	}
	if b, _ := frames[1].Payload(); !bytes.Equal(b, []byte("beta")) {
		t.Fatalf("frame1 = %q; want beta", b)
	}
	if err := p.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	// Async consumer keeps frame 1 pinned until the test releases the gate.
	gate := make(chan struct{})
	consumerDone := make(chan struct{})
	go func() {
		<-gate
		frames[1].Release()
		close(consumerDone)
	}()

	frames[0].Release()
	leaseA.Release()

	// Reads after release are rejected even while memory stays pinned.
	if _, err := frames[0].Payload(); !errors.Is(err, lease.ErrLeaseClosed) {
		t.Fatalf("frame read after release err = %v; want ErrLeaseClosed", err)
	}

	// A waiting acquirer stays blocked for the full timeout while pinned.
	assertBlockedAcquire(t, pool)

	// Let the consumer finish; the last reference then drops.
	close(gate)
	select {
	case <-consumerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("consumer never finished")
	}

	// New tenant gets the same physical buffer but never sees old payload.
	leaseB, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if leaseB.BufferID() != leaseA.BufferID() {
		t.Fatalf("buffer not reused: A=%d B=%d",
			leaseA.BufferID(), leaseB.BufferID())
	}
	if pool.Clears() != 1 {
		t.Fatalf("clears = %d; want 1", pool.Clears())
	}

	seg, err := leaseB.Segment(0, capacity)
	if err != nil {
		t.Fatal(err)
	}
	view, _ := seg.Bytes()
	for i, c := range view {
		if c != 0 {
			t.Fatalf("tenant B observed stale byte at %d: %q", i, view)
		}
	}
	seg.Release()
	leaseB.Release()
}

// assertBlockedAcquire proves that, while a reference is outstanding, an
// acquire cannot return even after its deadline expires.
func assertBlockedAcquire(t *testing.T, pool *lease.Pool) {
	t.Helper()
	blockCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := pool.Acquire(blockCtx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("pinned acquire err = %v; want DeadlineExceeded", err)
	}
}

func TestParserReassemblesSplitFrames(t *testing.T) {
	pool := lease.NewPool(1, 32)
	l, _ := pool.Acquire(context.Background())
	p := parser.New(l, 32)

	wire := frame([]byte("split-payload"))
	var all []*parser.Frame
	for i := 0; i < len(wire); i += 3 {
		end := i + 3
		if end > len(wire) {
			end = len(wire)
		}
		frames, err := p.Read(wire[i:end])
		if err != nil {
			t.Fatalf("split read: %v", err)
		}
		all = append(all, frames...)
	}
	if err := p.Flush(); err != nil {
		t.Fatalf("flush after splits: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("frames = %d; want 1", len(all))
	}
	if b, _ := all[0].Payload(); !bytes.Equal(b, []byte("split-payload")) {
		t.Fatalf("payload = %q; want split-payload", b)
	}

	// In-flight frame pins the buffer across Read calls.
	l.Release()
	if _, err := all[0].Payload(); !errors.Is(err, lease.ErrLeaseClosed) {
		t.Fatalf("read after lease release err = %v; want ErrLeaseClosed", err)
	}
	all[0].Release()
}

func TestParserRejectsOversizedFrame(t *testing.T) {
	const capacity = 8
	pool := lease.NewPool(1, capacity)
	l, _ := pool.Acquire(context.Background())
	p := parser.New(l, capacity)

	_, err := p.Read(frame(make([]byte, capacity+1)))
	if !errors.Is(err, parser.ErrBufferFull) {
		t.Fatalf("oversized frame err = %v; want ErrBufferFull", err)
	}
	l.Release()
}

func TestParserFlushDetectsTruncatedStream(t *testing.T) {
	pool := lease.NewPool(1, 32)
	l, _ := pool.Acquire(context.Background())
	p := parser.New(l, 32)

	if _, err := p.Read(frame([]byte("ab"))[:3]); err != nil {
		t.Fatalf("partial header read: %v", err)
	}
	if err := p.Flush(); !errors.Is(err, parser.ErrUnexpectedEOF) {
		t.Fatalf("flush err = %v; want ErrUnexpectedEOF", err)
	}
	l.Release()
}

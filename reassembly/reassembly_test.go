package reassembly

import (
	"bytes"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func recv(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("subscription channel closed unexpectedly")
		}
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
		return Event{}
	}
}

func expectData(t *testing.T, ch <-chan Event, seq uint32, want []byte) {
	t.Helper()
	ev := recv(t, ch)
	if ev.Kind != EventData {
		t.Fatalf("got %v (seq %d), want EventData seq %d %q", ev.Kind, ev.Seq, seq, want)
	}
	if ev.Seq != seq || !bytes.Equal(ev.Data, want) {
		t.Fatalf("got Data{seq %d, %q}, want Data{seq %d, %q}", ev.Seq, ev.Data, seq, want)
	}
}

func expectGap(t *testing.T, ch <-chan Event, start, end uint32) {
	t.Helper()
	ev := recv(t, ch)
	if ev.Kind != EventGap || ev.Seq != start || ev.End != end {
		t.Fatalf("got %+v, want Gap{seq %d, end %d}", ev, start, end)
	}
}

func expectSilence(t *testing.T, ch <-chan Event) {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if ok {
			t.Fatalf("unexpected event %+v", ev)
		}
		t.Fatal("subscription channel closed unexpectedly")
	case <-time.After(50 * time.Millisecond):
	}
}

// Retransmissions and out-of-order delivery across the 32-bit sequence
// wrap boundary must reassemble exactly once, in order.
func TestWrapAroundRetransmission(t *testing.T) {
	p := NewPool(1 << 20)
	c := p.NewConn(0xFFFFFFFE)
	ch, _ := c.Subscribe(16)

	// Out-of-order across the wrap: the tail arrives first and must be
	// buffered until the head completes the prefix.
	c.Write(0x00000000, []byte("CD"))
	expectSilence(t, ch)
	c.Write(0xFFFFFFFE, []byte("AB")) // straddles the wrap: FFFE, FFFF
	expectData(t, ch, 0xFFFFFFFE, []byte("ABCD"))

	// Full retransmission across the wrap, with conflicting bytes: the
	// first arrival already won, nothing may be emitted.
	c.Write(0xFFFFFFFE, []byte("WXYZ"))
	// Retransmission of bytes already delivered on both sides of the wrap
	// point.
	c.Write(0xFFFFFFFF, []byte("YZ"))
	expectSilence(t, ch)

	// The stream keeps advancing past the wrap.
	c.Write(0x00000002, []byte("EF"))
	expectData(t, ch, 0x00000002, []byte("EF"))

	c.Close()
	if ev := recv(t, ch); ev.Kind != EventClosed {
		t.Fatalf("got %+v, want EventClosed", ev)
	}
}

// Partially overlapping segments must keep first-arrival bytes and only
// contribute their not-yet-seen suffix.
func TestPartialOverlapFirstArrivalWins(t *testing.T) {
	p := NewPool(1 << 20)
	c := p.NewConn(1000)
	ch, _ := c.Subscribe(16)

	c.Write(1000, []byte("hello world")) // covers 1000..1011
	expectData(t, ch, 1000, []byte("hello world"))

	// Overlaps the tail of the previous segment; only "!!" is new.
	c.Write(1005, []byte("world!!!"))
	expectData(t, ch, 1011, []byte("!!"))

	// Conflicting retransmission of delivered bytes: first arrival wins.
	c.Write(1000, []byte("HELLO WORLD"))
	expectSilence(t, ch)

	// Out-of-order with overlap on both sides of a buffered segment.
	c.Write(1020, []byte("12345678")) // buffered, gap at 1013..1020
	expectSilence(t, ch)
	c.Write(1016, []byte("wxyz1234")) // overlaps 1020..1024, keeps "1234"
	expectSilence(t, ch)              // still missing 1013..1016
	c.Write(1026, []byte("78TAIL"))   // overlaps 1026..1028, keeps "78"
	expectSilence(t, ch)
	c.Write(1013, []byte("abc"))
	expectData(t, ch, 1013, []byte("abcwxyz12345678TAIL"))

	c.Close()
	if ev := recv(t, ch); ev.Kind != EventClosed {
		t.Fatalf("got %+v, want EventClosed", ev)
	}
}

// When the global limit forces eviction, the consumer must be told the
// exact lost interval, and deliveries on either side of it must stay
// separate events.
func TestEvictionReportsExactGap(t *testing.T) {
	p := NewPool(8) // 8 bytes of gap buffering, global
	c := p.NewConn(0)
	ch, _ := c.Subscribe(64)

	c.Write(0, []byte("0123"))
	expectData(t, ch, 0, []byte("0123"))

	c.Write(8, []byte("89AB"))  // buffered: 4
	c.Write(16, []byte("IJKL")) // buffered: 8
	c.Write(24, []byte("QRST")) // 12 > 8: oldest segment [8,12) is evicted
	if got := p.Buffered(); got > 8 {
		t.Fatalf("pool holds %d bytes, limit is 8", got)
	}
	expectSilence(t, ch) // gap not yet reached by the confirmed prefix

	c.Write(4, []byte("4567"))
	expectData(t, ch, 4, []byte("4567"))
	expectGap(t, ch, 8, 12) // exact evicted interval, reported in order

	c.Write(12, []byte("CDEF"))
	expectData(t, ch, 12, []byte("CDEFIJKL")) // contiguous run coalesced

	c.Write(20, []byte("LMNO"))
	expectData(t, ch, 20, []byte("LMNOQRST"))

	// Bytes retransmitted into the lost interval are discarded silently.
	c.Write(8, []byte("89AB"))
	c.Write(0, []byte("0123"))
	expectSilence(t, ch)

	c.Close()
	if got := p.Buffered(); got != 0 {
		t.Fatalf("pool holds %d bytes after close", got)
	}
}

// Many connections contending for one small global pool: the limit must
// hold at all times, every byte must be accounted for by exactly one data
// or gap event, and no delivered byte may deviate from the source stream.
func TestMultiConnectionContention(t *testing.T) {
	const (
		nConns    = 16
		streamLen = 16 * 1024
		chunk     = 64
		limit     = 4096
	)
	p := NewPool(limit)

	var maxBuffered atomic.Int64
	stopMonitor := make(chan struct{})
	var monitorWG sync.WaitGroup
	monitorWG.Add(1)
	go func() {
		defer monitorWG.Done()
		for {
			select {
			case <-stopMonitor:
				return
			default:
				if b := p.Buffered(); b > maxBuffered.Load() {
					maxBuffered.Store(b)
				}
				time.Sleep(time.Millisecond)
			}
		}
	}()

	rng := rand.New(rand.NewSource(1))
	var wg sync.WaitGroup
	errs := make(chan error, nConns)

	for i := 0; i < nConns; i++ {
		i := i
		// Stagger bases so several streams cross the 32-bit wrap.
		base := uint32(0xFFFF0000)*uint32(i%3) + uint32(i)*7919
		src := make([]byte, streamLen)
		rng.Read(src)

		conn := p.NewConn(base)
		ch, _ := conn.Subscribe(1024)

		// Consumer: verify strict event ordering and byte-exact delivery.
		wg.Add(1)
		go func() {
			defer wg.Done()
			expected := base
			for ev := range ch {
				switch ev.Kind {
				case EventData:
					off := ev.Seq - base
					if ev.Seq != expected {
						errs <- fmt.Errorf("conn %d: data at seq %d, expected prefix %d", i, ev.Seq, expected)
						return
					}
					if !bytes.Equal(ev.Data, src[off:off+uint32(len(ev.Data))]) {
						errs <- fmt.Errorf("conn %d: corrupted delivery at offset %d", i, off)
						return
					}
					expected += uint32(len(ev.Data))
				case EventGap:
					if ev.Seq != expected {
						errs <- fmt.Errorf("conn %d: gap starts at %d, expected prefix %d", i, ev.Seq, expected)
						return
					}
					expected = ev.End
				case EventClosed:
					if expected-base != streamLen {
						errs <- fmt.Errorf("conn %d: covered %d of %d bytes", i, expected-base, streamLen)
					}
					return
				}
			}
		}()

		// Writer: shuffled chunks with random retransmissions.
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(i) + 42))
			order := r.Perm(streamLen / chunk)
			for _, idx := range order {
				off := idx * chunk
				conn.Write(base+uint32(off), src[off:off+chunk])
				if r.Intn(4) == 0 {
					conn.Write(base+uint32(off), src[off:off+chunk]) // retransmit
				}
			}
			conn.Close()
		}()
	}

	wg.Wait()
	close(stopMonitor)
	monitorWG.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if got := maxBuffered.Load(); got > limit {
		t.Fatalf("pool buffered %d bytes, limit is %d", got, limit)
	}
	if got := p.Buffered(); got != 0 {
		t.Fatalf("pool leaked %d buffered bytes", got)
	}
}

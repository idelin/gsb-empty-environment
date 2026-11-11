package ip6reasm

import (
	"bytes"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"
)

const (
	nhFragment = 44
	nhUDP      = 17
	nhDestOpts = 60
)

var (
	src1 = netip.MustParseAddr("2001:db8::1")
	dst1 = netip.MustParseAddr("2001:db8::2")
	src2 = netip.MustParseAddr("2001:db8::3")
)

// fakeClock is the clock injected into the reassembler so timeout handling is
// fully deterministic.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// upperLayer is an independent callback used by the embedding stack to
// receive only fully reassembled datagrams; partial fragments never arrive.
type upperLayer struct {
	mu        sync.Mutex
	delivered [][]byte
	drops     []dropEvent
}

type dropEvent struct {
	key    Key
	reason DropReason
}

func (u *upperLayer) onDeliver(p ReassembledPacket) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.delivered = append(u.delivered, append([]byte(nil), p.Packet...))
}

func (u *upperLayer) onDrop(key Key, reason DropReason) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.drops = append(u.drops, dropEvent{key: key, reason: reason})
}

func (u *upperLayer) deliveries() [][]byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([][]byte, len(u.delivered))
	copy(out, u.delivered)
	return out
}

func (u *upperLayer) dropReasons() []DropReason {
	u.mu.Lock()
	defer u.mu.Unlock()
	out := make([]DropReason, len(u.drops))
	for i, ev := range u.drops {
		out[i] = ev.reason
	}
	return out
}

func newReassembler(u *upperLayer, clk *fakeClock, timeout time.Duration, perSource int) *Reassembler {
	return New(Config{
		Timeout:           timeout,
		MaxPerSourceBytes: perSource,
		Now:               clk.now,
		OnDeliver:         u.onDeliver,
		OnDrop:            u.onDrop,
	})
}

// ipv6Header builds a 40-byte IPv6 header whose Next Header field (offset 6)
// points at nh, matching what an embedded stack would hand over as the
// unfragmentable part.
func ipv6Header(nh byte) []byte {
	hdr := make([]byte, 40)
	hdr[0] = 0x60
	hdr[6] = nh
	return hdr
}

func makeInput(id, offset uint32, more bool, payload []byte) FragmentInput {
	return FragmentInput{
		Src:                src1,
		Dst:                dst1,
		Identification:     id,
		Offset:             offset,
		MoreFragments:      more,
		Fragmentable:       payload,
		Unfragmentable:     ipv6Header(nhFragment),
		NextHeaderAt:       6,
		FragmentNextHeader: nhUDP,
	}
}

func countReason(reasons []DropReason, want DropReason) int {
	n := 0
	for _, reason := range reasons {
		if reason == want {
			n++
		}
	}
	return n
}

// TestReassembleNoPrematureUpperLayer verifies the core regression: the UDP
// (upper-layer) callback must never fire while fragments are outstanding, and
// fires exactly once with the contiguous datagram once complete.
func TestReassembleNoPrematureUpperLayer(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	up := &upperLayer{}
	r := newReassembler(up, clk, time.Minute, 0)

	original := make([]byte, 24)
	for i := range original {
		original[i] = byte(0xA0 + i)
	}

	if err := r.Handle(makeInput(1, 0, true, original[:8])); err != nil {
		t.Fatalf("first fragment: %v", err)
	}
	if err := r.Handle(makeInput(1, 8, true, original[8:16])); err != nil {
		t.Fatalf("middle fragment: %v", err)
	}

	if got := up.deliveries(); len(got) != 0 {
		t.Fatalf("upper layer invoked with %d datagrams before reassembly completed", len(got))
	}
	if stats := r.Stats(); stats.PendingDatagrams != 1 || stats.PendingBytes != 16 {
		t.Fatalf("unexpected pending state: %+v", stats)
	}

	if err := r.Handle(makeInput(1, 16, false, original[16:])); err != nil {
		t.Fatalf("last fragment: %v", err)
	}

	delivered := up.deliveries()
	if len(delivered) != 1 {
		t.Fatalf("expected exactly 1 delivery, got %d", len(delivered))
	}

	pkt := delivered[0]
	if len(pkt) != 40+len(original) {
		t.Fatalf("delivered length = %d, want %d", len(pkt), 40+len(original))
	}
	if pkt[6] != nhUDP {
		t.Fatalf("Next Header = %d, want UDP (%d)", pkt[6], nhUDP)
	}
	if !bytes.Equal(pkt[40:], original) {
		t.Fatal("fragmentable part not reassembled contiguously")
	}

	stats := r.Stats()
	if stats.PendingDatagrams != 0 || stats.PendingBytes != 0 || stats.Delivered != 1 {
		t.Fatalf("state not released after delivery: %+v", stats)
	}
}

// TestAtomicFragment verifies that offset 0 with M clear is delivered
// immediately and never creates reassembly state.
func TestAtomicFragment(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	up := &upperLayer{}
	r := newReassembler(up, clk, time.Minute, 8)

	payload := []byte{1, 2, 3, 4, 5}
	in := makeInput(7, 0, false, payload)
	if err := r.Handle(in); err != nil {
		t.Fatalf("atomic fragment: %v", err)
	}

	delivered := up.deliveries()
	if len(delivered) != 1 {
		t.Fatalf("atomic fragment delivered %d times, want 1", len(delivered))
	}
	if got := delivered[0]; got[6] != nhUDP || !bytes.Equal(got[40:], payload) {
		t.Fatalf("atomic payload mismatch: % x", got)
	}

	stats := r.Stats()
	if stats.PendingDatagrams != 0 || stats.PendingBytes != 0 {
		t.Fatalf("atomic fragment created reassembly state: %+v", stats)
	}

	// Advancing the clock must not produce any timeout event for it.
	clk.advance(2 * time.Minute)
	if reclaimed := r.Tick(); reclaimed != 0 {
		t.Fatalf("Tick reclaimed %d datagrams for atomic fragment", reclaimed)
	}
}

// TestOverlapDiscardsDatagram verifies that any overlap, including an exact
// duplicate, discards the whole datagram and that later fragments are rejected
// as orphans instead of silently rebuilding it.
func TestOverlapDiscardsDatagram(t *testing.T) {
	for _, tc := range []struct {
		name   string
		second FragmentInput
		third  FragmentInput
	}{
		{
			name:   "exact duplicate",
			second: makeInput(2, 0, true, []byte{0, 1, 2, 3, 4, 5, 6, 7}),
			third:  makeInput(2, 8, false, []byte{8, 9, 10, 11}),
		},
		{
			name: "partial overlap",
			// A 12-octet payload is only legal on the last fragment, so the
			// overlap check runs first and wins over the alignment check.
			second: makeInput(2, 8, false, make([]byte, 12)),
			third:  makeInput(2, 16, false, []byte{8, 9, 10, 11}),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clk := &fakeClock{t: time.Unix(1000, 0)}
			up := &upperLayer{}
			r := newReassembler(up, clk, time.Minute, 0)

			if err := r.Handle(makeInput(2, 0, true, []byte{0, 1, 2, 3, 4, 5, 6, 7})); err != nil {
				t.Fatalf("first fragment: %v", err)
			}
			if tc.name == "partial overlap" {
				// Cover [8,16); the later last fragment starts inside it.
				if err := r.Handle(makeInput(2, 8, true, make([]byte, 8))); err != nil {
					t.Fatalf("middle fragment: %v", err)
				}
			}
			if err := r.Handle(tc.second); !errors.Is(err, ErrOverlap) {
				t.Fatalf("overlap error = %v, want ErrOverlap", err)
			}

			if got := up.deliveries(); len(got) != 0 {
				t.Fatalf("upper layer received %d datagrams after overlap", len(got))
			}
			if stats := r.Stats(); stats.PendingDatagrams != 0 || stats.PendingBytes != 0 {
				t.Fatalf("overlapping datagram not fully removed: %+v", stats)
			}

			// The late, otherwise valid fragment must not resurrect the datagram.
			if err := r.Handle(tc.third); !errors.Is(err, ErrOrphan) {
				t.Fatalf("late fragment error = %v, want ErrOrphan", err)
			}
			if got := up.deliveries(); len(got) != 0 {
				t.Fatalf("late fragment rebuilt a discarded datagram")
			}

			reasons := up.dropReasons()
			if countReason(reasons, DropOverlap) != 1 || countReason(reasons, DropOrphan) != 1 {
				t.Fatalf("drop events = %v, want one overlap and one orphan", reasons)
			}
		})
	}
}

// TestConflictingLastFragment verifies that an inconsistent second last
// fragment discards the whole datagram.
func TestConflictingLastFragment(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	up := &upperLayer{}
	r := newReassembler(up, clk, time.Minute, 0)

	if err := r.Handle(makeInput(3, 0, true, make([]byte, 8))); err != nil {
		t.Fatalf("first fragment: %v", err)
	}
	// Leave a gap (offset 8 missing) so the first last fragment does not
	// complete the datagram and remains pending.
	if err := r.Handle(makeInput(3, 16, false, []byte{1, 2, 3})); err != nil {
		t.Fatalf("first last fragment: %v", err)
	}
	// Another last fragment with a different payload length at the same offset.
	err := r.Handle(makeInput(3, 16, false, []byte{1, 2, 3, 4}))
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict error = %v, want ErrConflict", err)
	}
	if got := up.deliveries(); len(got) != 0 {
		t.Fatalf("upper layer invoked despite unresolved conflict: %d", len(got))
	}
	if stats := r.Stats(); stats.PendingDatagrams != 0 || stats.PendingBytes != 0 {
		t.Fatalf("conflicting datagram not removed: %+v", stats)
	}
}

// TestExtensionHeaderStraddlingBoundary verifies a fragmentable extension
// header split across a fragment boundary. The upper layer must only see the
// reconstructed header chain, with the IPv6 Next Header field repointed past
// the removed Fragment Header.
func TestExtensionHeaderStraddlingBoundary(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	up := &upperLayer{}
	r := newReassembler(up, clk, time.Minute, 0)

	// Fragmentable part: 16-octet Destination Options header (Next Header
	// UDP, Hdr Ext Len 1 => length 16 octets), followed by a 12-octet UDP
	// payload. The header crosses the fragment boundary between offsets 8
	// and 16.
	fragmentable := make([]byte, 28)
	fragmentable[0] = nhUDP
	fragmentable[1] = 1 // Hdr Ext Len: (1+1)*8 = 16 octets
	for i := 16; i < len(fragmentable); i++ {
		fragmentable[i] = byte(i)
	}

	mk := func(offset uint32, more bool, payload []byte) FragmentInput {
		in := makeInput(4, offset, more, payload)
		in.FragmentNextHeader = nhDestOpts
		return in
	}

	if err := r.Handle(mk(0, true, fragmentable[:8])); err != nil {
		t.Fatalf("first fragment: %v", err)
	}
	if err := r.Handle(mk(8, true, fragmentable[8:16])); err != nil {
		t.Fatalf("boundary fragment: %v", err)
	}
	if got := up.deliveries(); len(got) != 0 {
		t.Fatalf("upper layer invoked before the straddling header was rejoined")
	}
	if err := r.Handle(mk(16, false, fragmentable[16:])); err != nil {
		t.Fatalf("last fragment: %v", err)
	}

	delivered := up.deliveries()
	if len(delivered) != 1 {
		t.Fatalf("expected 1 delivery, got %d", len(delivered))
	}
	pkt := delivered[0]

	// IPv6 Next Header now references Destination Options, not Fragment.
	if pkt[6] != nhDestOpts {
		t.Fatalf("IPv6 Next Header = %d, want Destination Options (%d)", pkt[6], nhDestOpts)
	}
	rebuilt := pkt[40:]
	if !bytes.Equal(rebuilt, fragmentable) {
		t.Fatalf("extension header chain corrupted across boundary\n got % x\nwant % x", rebuilt, fragmentable)
	}
	// The chain inside the fragmentable part still leads into UDP.
	if rebuilt[0] != nhUDP || rebuilt[1] != 1 {
		t.Fatalf("destination options header fields not reconstructed: % x", rebuilt[:2])
	}
}

// TestUnalignedNonFinalFragment verifies that a non-final fragment whose
// payload length is not a multiple of 8 discards the datagram.
func TestUnalignedNonFinalFragment(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	up := &upperLayer{}
	r := newReassembler(up, clk, time.Minute, 0)

	if err := r.Handle(makeInput(5, 0, true, make([]byte, 8))); err != nil {
		t.Fatalf("first fragment: %v", err)
	}
	err := r.Handle(makeInput(5, 8, true, []byte{1, 2, 3}))
	if !errors.Is(err, ErrUnaligned) {
		t.Fatalf("unaligned error = %v, want ErrUnaligned", err)
	}
	if stats := r.Stats(); stats.PendingDatagrams != 0 || stats.PendingBytes != 0 {
		t.Fatalf("unaligned datagram not removed: %+v", stats)
	}

	// A final fragment is allowed to have any payload length.
	if err := r.Handle(makeInput(6, 0, false, []byte{9, 9, 9})); err != nil {
		t.Fatalf("short last fragment rejected: %v", err)
	}
	if len(up.deliveries()) != 1 {
		t.Fatal("short final fragment should deliver immediately as atomic")
	}
}

// TestPerSourceByteLimit verifies outstanding bytes are capped per source and
// are released as datagrams complete or get evicted.
func TestPerSourceByteLimit(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	up := &upperLayer{}
	const limit = 16
	r := newReassembler(up, clk, time.Minute, limit)

	// Datagram 1 buffers 8 bytes from src1.
	if err := r.Handle(makeInput(10, 0, true, make([]byte, 8))); err != nil {
		t.Fatalf("first datagram: %v", err)
	}
	// A 16-byte new datagram would raise src1 usage to 24 > limit.
	err := r.Handle(makeInput(11, 0, true, make([]byte, 16)))
	if !errors.Is(err, ErrSourceLimit) {
		t.Fatalf("limit error = %v, want ErrSourceLimit", err)
	}
	if r.PendingSourceBytes(src1) != 8 {
		t.Fatalf("refused fragment charged to source: %d bytes", r.PendingSourceBytes(src1))
	}

	// The existing datagram remains completable.
	if err := r.Handle(makeInput(10, 8, false, make([]byte, 4))); err != nil {
		t.Fatalf("completing datagram 10: %v", err)
	}
	if r.PendingSourceBytes(src1) != 0 {
		t.Fatalf("bytes not released on completion: %d", r.PendingSourceBytes(src1))
	}

	// After release the same source may buffer again up to the limit.
	if err := r.Handle(makeInput(12, 0, true, make([]byte, 16))); err != nil {
		t.Fatalf("post-release buffer: %v", err)
	}

	// Limits are independent per source.
	other := makeInput(13, 0, true, make([]byte, 8))
	other.Src = src2
	if err := r.Handle(other); err != nil {
		t.Fatalf("other source should have its own budget: %v", err)
	}
	if r.PendingSourceBytes(src2) != 8 {
		t.Fatalf("src2 budget = %d, want 8", r.PendingSourceBytes(src2))
	}

	// A fragment belonging to an existing datagram that would breach the
	// limit is refused without killing the datagram.
	err = r.Handle(makeInput(12, 16, true, make([]byte, 8)))
	if !errors.Is(err, ErrSourceLimit) {
		t.Fatalf("existing-datagram limit error = %v, want ErrSourceLimit", err)
	}
	if stats := r.Stats(); stats.PendingDatagrams != 2 {
		t.Fatalf("refused fragment removed datagram: %+v", stats)
	}

	if n := countReason(up.dropReasons(), DropSourceLimit); n != 2 {
		t.Fatalf("source-limit drops = %d, want 2", n)
	}
}

// TestTimeoutThenLateFragment verifies the injected clock reclaims an
// incomplete datagram and that a late fragment can never join the evicted
// datagram through the independent upper-layer callbacks.
func TestTimeoutThenLateFragment(t *testing.T) {
	clk := &fakeClock{t: time.Unix(2000, 0)}
	up := &upperLayer{}
	const timeout = 30 * time.Second
	r := newReassembler(up, clk, timeout, 0)

	if err := r.Handle(makeInput(20, 0, true, make([]byte, 8))); err != nil {
		t.Fatalf("first fragment: %v", err)
	}
	if err := r.Handle(makeInput(20, 8, true, make([]byte, 8))); err != nil {
		t.Fatalf("second fragment: %v", err)
	}
	if got := up.deliveries(); len(got) != 0 {
		t.Fatalf("upper layer invoked early with %d datagrams", len(got))
	}

	// Before the timeout the clock must not reclaim anything.
	clk.advance(timeout - time.Nanosecond)
	if reclaimed := r.Tick(); reclaimed != 0 {
		t.Fatalf("early Tick reclaimed %d datagrams", reclaimed)
	}

	// At the timeout the datagram and its buffered bytes are reclaimed.
	clk.advance(timeout)
	if reclaimed := r.Tick(); reclaimed != 1 {
		t.Fatalf("Tick reclaimed %d datagrams, want 1", reclaimed)
	}
	if stats := r.Stats(); stats.PendingDatagrams != 0 || stats.PendingBytes != 0 {
		t.Fatalf("eviction did not release state: %+v", stats)
	}
	if n := countReason(up.dropReasons(), DropTimeout); n != 1 {
		t.Fatalf("timeout drops = %d, want 1", n)
	}
	if got := up.deliveries(); len(got) != 0 {
		t.Fatalf("upper layer received an incomplete datagram on timeout")
	}

	// Late fragments of the dead datagram are rejected, regardless of offset.
	if err := r.Handle(makeInput(20, 16, false, make([]byte, 4))); !errors.Is(err, ErrOrphan) {
		t.Fatalf("late last fragment error = %v, want ErrOrphan", err)
	}
	if err := r.Handle(makeInput(20, 8, true, make([]byte, 8))); !errors.Is(err, ErrOrphan) {
		t.Fatalf("late duplicate fragment error = %v, want ErrOrphan", err)
	}

	if got := up.deliveries(); len(got) != 0 {
		t.Fatalf("late fragment resurrected an evicted datagram: %d deliveries", len(got))
	}
	if n := countReason(up.dropReasons(), DropOrphan); n != 2 {
		t.Fatalf("orphan drops = %d, want 2", n)
	}
	if stats := r.Stats(); stats.PendingDatagrams != 0 || stats.PendingBytes != 0 {
		t.Fatalf("late fragments allocated state: %+v", stats)
	}

	// A fresh datagram with the same identification (a true retransmission
	// starting again at offset 0) is accepted as a new context.
	if err := r.Handle(makeInput(20, 0, true, make([]byte, 8))); err != nil {
		t.Fatalf("retransmission start: %v", err)
	}
	if err := r.Handle(makeInput(20, 8, false, make([]byte, 4))); err != nil {
		t.Fatalf("retransmission completion: %v", err)
	}
	if got := up.deliveries(); len(got) != 1 {
		t.Fatalf("retransmitted datagram deliveries = %d, want 1", len(got))
	}
}

// TestMalformedInput verifies structural validation and that no state is
// created for invalid input.
func TestMalformedInput(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	up := &upperLayer{}
	r := newReassembler(up, clk, time.Minute, 0)

	bad := makeInput(30, 3, true, make([]byte, 8)) // offset not multiple of 8
	if err := r.Handle(bad); !errors.Is(err, ErrMalformed) {
		t.Fatalf("unaligned offset error = %v, want ErrMalformed", err)
	}

	bad = makeInput(30, 0, true, make([]byte, 8))
	bad.NextHeaderAt = 99
	if err := r.Handle(bad); !errors.Is(err, ErrMalformed) {
		t.Fatalf("bad NextHeaderAt error = %v, want ErrMalformed", err)
	}

	bad = makeInput(30, 0, true, make([]byte, 8))
	bad.Src = netip.Addr{}
	if err := r.Handle(bad); !errors.Is(err, ErrMalformed) {
		t.Fatalf("zero address error = %v, want ErrMalformed", err)
	}

	if stats := r.Stats(); stats.PendingDatagrams != 0 || stats.PendingBytes != 0 {
		t.Fatalf("malformed input created state: %+v", stats)
	}
}

// TestConcurrentHandleAndTick exercises the mutex under parallel fragment
// injection and clock advancement, including timeout-driven removal.
func TestConcurrentHandleAndTick(t *testing.T) {
	clk := &fakeClock{t: time.Unix(3000, 0)}
	up := &upperLayer{}
	r := newReassembler(up, clk, 10*time.Millisecond, 0)

	stop := make(chan struct{})
	var tickerWG sync.WaitGroup
	producerDone := make(chan struct{})

	go func() {
		defer close(producerDone)
		for id := uint32(0); id < 200; id++ {
			_ = r.Handle(makeInput(id, 0, true, make([]byte, 8)))
			_ = r.Handle(makeInput(id, 8, false, make([]byte, 4)))
		}
	}()

	tickerWG.Add(1)
	go func() {
		defer tickerWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
				clk.advance(time.Millisecond)
				_ = r.Tick()
			}
		}
	}()

	<-producerDone
	close(stop)
	tickerWG.Wait()

	clk.advance(time.Hour)
	r.Tick()

	stats := r.Stats()
	if stats.PendingDatagrams != 0 || stats.PendingBytes != 0 {
		t.Fatalf("state leaked after concurrent run: %+v", stats)
	}
	if int(stats.Delivered) != len(up.deliveries()) {
		t.Fatalf("delivery counter %d disagrees with callbacks %d",
			stats.Delivered, len(up.deliveries()))
	}
}

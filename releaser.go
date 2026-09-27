package reordersim

import "sort"

// ReleaserConfig tunes a Releaser. All durations are integer ticks.
type ReleaserConfig struct {
	// WaitLimit is the maximum number of ticks the releaser waits for a
	// missing head-of-line sequence number before skipping the gap, so a
	// lost packet can never block the stream forever.
	WaitLimit int64
	// CacheLimit is the maximum number of buffered out-of-order packets.
	// When a new packet would exceed it, the gap is skipped immediately and
	// the contiguous prefix is released, so memory stays bounded.
	CacheLimit int
	// ReleasedCap and LateCap size the output channels. Sends never block;
	// packets that would overflow are dropped and counted in Stats.
	// Zero selects a default of 1024.
	ReleasedCap int
	LateCap     int
}

// Stats counts everything the Releaser did, for observability in tests.
type Stats struct {
	Received   uint64 // packets accepted for inspection
	Released   uint64 // packets emitted on Released()
	Late       uint64 // packets diverted to Late()
	Duplicates uint64 // packets dropped because their Seq is already buffered
	Dropped    uint64 // buffered packets discarded by Reset
	Skipped    uint64 // missing sequence numbers jumped over
	Overflow   uint64 // packets dropped because an output channel was full
}

const defaultChanCap = 1024

// Releaser buffers out-of-order packets of one epoch and releases them in
// (EventTime, Seq) order. It is not safe for concurrent use; drive it from
// a single Simulator goroutine.
type Releaser struct {
	cfg      ReleaserConfig
	epoch    uint64
	expected uint64 // next sequence number needed for a contiguous prefix
	buf      map[uint64]Packet

	gapOpen  bool  // a packet beyond a missing head-of-line seq is buffered
	gapSince int64 // tick at which the current head-of-line gap opened

	last    key  // key of the most recently released packet
	hasLast bool // whether last is valid (false right after Reset)

	now      int64
	released chan Packet
	late     chan Packet
	stats    Stats
}

// NewReleaser creates a Releaser for epoch 0 expecting sequence 0.
// Use Reset to move to another epoch.
func NewReleaser(cfg ReleaserConfig) *Releaser {
	if cfg.ReleasedCap <= 0 {
		cfg.ReleasedCap = defaultChanCap
	}
	if cfg.LateCap <= 0 {
		cfg.LateCap = defaultChanCap
	}
	r := &Releaser{
		cfg:      cfg,
		buf:      make(map[uint64]Packet),
		released: make(chan Packet, cfg.ReleasedCap),
		late:     make(chan Packet, cfg.LateCap),
	}
	return r
}

// Released returns the channel of in-order released packets.
func (r *Releaser) Released() <-chan Packet { return r.released }

// Late returns the channel of packets that can never be released without
// modifying the already released sequence (old epoch, already skipped or
// released position). Late packets never touch the released stream.
func (r *Releaser) Late() <-chan Packet { return r.late }

// Stats returns a snapshot of the counters.
func (r *Releaser) Stats() Stats { return r.stats }

// Buffered reports how many packets are currently held.
func (r *Releaser) Buffered() int { return len(r.buf) }

// Reset moves the releaser to a new epoch. Every buffered packet of the old
// epoch is discarded (counted in Stats.Dropped) so nothing from the old
// stream can leak into the new one; late arrivals of the old epoch are
// diverted to the Late channel on Receive.
func (r *Releaser) Reset(epoch uint64, startSeq uint64) {
	r.stats.Dropped += uint64(len(r.buf))
	r.buf = make(map[uint64]Packet)
	r.epoch = epoch
	r.expected = startSeq
	r.gapOpen = false
	r.hasLast = false
}

// OnTick advances the releaser clock to t and fires the wait-limit check.
// The Simulator calls it once per tick before delivering that tick's events.
func (r *Releaser) OnTick(t int64) {
	r.now = t
	if r.gapOpen && len(r.buf) > 0 && t-r.gapSince >= r.cfg.WaitLimit {
		r.skipGap()
	}
}

// Receive inspects one arrived packet at the current tick.
func (r *Releaser) Receive(p Packet) {
	r.stats.Received++
	k := keyOf(p)
	switch {
	case p.Epoch != r.epoch:
		r.toLate(p) // foreign epoch: never enters the current stream
		return
	case r.hasLast && k.beforeOrEqual(r.last):
		r.toLate(p) // would rewrite already released positions
		return
	case p.Seq < r.expected:
		r.toLate(p) // position already released or skipped
		return
	}
	if _, dup := r.buf[p.Seq]; dup {
		r.stats.Duplicates++
		return
	}
	r.buf[p.Seq] = p
	if !r.gapOpen && p.Seq > r.expected {
		r.gapOpen = true
		r.gapSince = r.now
	}
	r.releasePrefix()
	if len(r.buf) > r.cfg.CacheLimit && r.gapOpen {
		r.skipGap() // cache limit forces progress, memory stays bounded
	}
}

// releasePrefix releases the contiguous prefix starting at r.expected,
// ordered by (EventTime, Seq), and repeats while the head is buffered.
func (r *Releaser) releasePrefix() {
	for {
		if _, ok := r.buf[r.expected]; !ok {
			break
		}
		end := r.expected
		for {
			if _, ok := r.buf[end+1]; !ok {
				break
			}
			end++
		}
		prefix := make([]Packet, 0, end-r.expected+1)
		for seq := r.expected; seq <= end; seq++ {
			prefix = append(prefix, r.buf[seq])
			delete(r.buf, seq)
		}
		sort.Slice(prefix, func(i, j int) bool {
			return keyOf(prefix[i]).less(keyOf(prefix[j]))
		})
		for _, p := range prefix {
			r.emit(p)
		}
		r.expected = end + 1
	}
	r.refreshGap()
}

// skipGap jumps the expected sequence over the missing head-of-line gap to
// the smallest buffered sequence, then releases whatever is contiguous.
func (r *Releaser) skipGap() {
	min := ^uint64(0)
	for seq := range r.buf {
		if seq < min {
			min = seq
		}
	}
	r.stats.Skipped += uint64(min - r.expected)
	r.expected = min
	r.gapOpen = false
	r.releasePrefix()
}

// refreshGap re-arms the wait timer for the new head-of-line gap, giving
// every missing sequence number a full WaitLimit of its own.
func (r *Releaser) refreshGap() {
	if len(r.buf) == 0 {
		r.gapOpen = false
		return
	}
	if _, ok := r.buf[r.expected]; !ok {
		r.gapOpen = true
		r.gapSince = r.now
	}
}

func (r *Releaser) emit(p Packet) {
	r.last = keyOf(p)
	r.hasLast = true
	r.stats.Released++
	select {
	case r.released <- p:
	default:
		r.stats.Overflow++
	}
}

func (r *Releaser) toLate(p Packet) {
	r.stats.Late++
	select {
	case r.late <- p:
	default:
		r.stats.Overflow++
	}
}

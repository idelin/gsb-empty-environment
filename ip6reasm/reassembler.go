// Package ip6reasm implements IPv6 fragment reassembly for embedding in a
// userspace protocol stack.
//
// The reassembler is driven explicitly. The stack parses each received
// Fragment Header and feeds the decoded fragment to Reassembler.Handle; a
// stack-owned clock is advanced through Reassembler.Tick. Upper-layer
// protocols are invoked only through the configured delivery callback, and
// only after a datagram has been fully reassembled, so a partially received
// datagram can never reach UDP or another upper-layer handler.
//
// Reassembly policy follows RFC 8200:
//
//   - An atomic fragment (Fragment Offset 0, M flag clear) is delivered to
//     the upper layer immediately and never creates reassembly state.
//   - The fragment with a clear M flag is the last fragment and fixes the
//     total length of the fragmentable part. A second, inconsistent last
//     fragment discards the whole datagram.
//   - Any overlap between fragments (including an exact duplicate of an
//     already received interval) discards the whole datagram, and later
//     fragments that reference the dead datagram are rejected as orphans.
//   - Extension headers are part of the fragmentable part and may straddle a
//     fragment boundary. They are only exposed to the upper layer once the
//     fragmentable part has been reassembled contiguously.
//   - Outstanding buffered payload is bounded per source address and is
//     reclaimed when the injected reassembly clock expires a datagram.
package ip6reasm

import (
	"errors"
	"net/netip"
	"sort"
	"sync"
	"time"
)

// maxFragmentableLen bounds the fragmentable part for plain (non-jumbogram)
// IPv6: the 16-bit Payload Length leaves at most 65535-8 octets after the
// 8-octet Fragment Header.
const maxFragmentableLen = 65535 - 8

// defaultTimeout is the RFC 8200 reassembly timeout.
const defaultTimeout = 60 * time.Second

const unknownLast = ^uint32(0)

// DropReason identifies why a fragment or datagram was discarded.
type DropReason string

const (
	// DropMalformed: the fragment input failed structural validation.
	DropMalformed DropReason = "malformed"
	// DropOversize: the declared fragmentable part exceeds the IPv6 payload limit.
	DropOversize DropReason = "oversize"
	// DropUnaligned: a non-final fragment carries a payload that is not a
	// multiple of 8 octets.
	DropUnaligned DropReason = "unaligned-fragment"
	// DropOverlap: two fragments cover overlapping ranges; the datagram is
	// discarded wholesale.
	DropOverlap DropReason = "overlapping-fragments"
	// DropConflict: inconsistent last fragments were received.
	DropConflict DropReason = "conflicting-last-fragment"
	// DropSourceLimit: accepting the fragment would exceed the configured
	// per-source outstanding byte budget.
	DropSourceLimit DropReason = "source-byte-limit"
	// DropTimeout: the datagram was not completed before the reassembly timeout.
	DropTimeout DropReason = "reassembly-timeout"
	// DropOrphan: a fragment references a datagram that does not exist, for
	// example a late fragment arriving after timeout eviction.
	DropOrphan DropReason = "orphan-late-fragment"
)

// Sentinel errors returned by Handle. A drop callback, when configured, fires
// with the corresponding DropReason in addition to the returned error.
var (
	ErrMalformed   = errors.New("ip6reasm: malformed fragment input")
	ErrOversize    = errors.New("ip6reasm: fragmentable part exceeds the IPv6 payload limit")
	ErrUnaligned   = errors.New("ip6reasm: non-final fragment payload is not a multiple of 8 octets")
	ErrOverlap     = errors.New("ip6reasm: overlapping fragments; datagram discarded")
	ErrConflict    = errors.New("ip6reasm: conflicting last fragments; datagram discarded")
	ErrSourceLimit = errors.New("ip6reasm: per-source outstanding byte limit exceeded")
	ErrOrphan      = errors.New("ip6reasm: fragment belongs to an unknown or discarded datagram")
)

func reasonError(reason DropReason) error {
	switch reason {
	case DropMalformed:
		return ErrMalformed
	case DropOversize:
		return ErrOversize
	case DropUnaligned:
		return ErrUnaligned
	case DropOverlap:
		return ErrOverlap
	case DropConflict:
		return ErrConflict
	case DropSourceLimit:
		return ErrSourceLimit
	case DropOrphan:
		return ErrOrphan
	default:
		return errors.New("ip6reasm: " + string(reason))
	}
}

// Key identifies a reassembly context: the RFC 8200 triple of source
// address, destination address and Fragment Identification.
type Key struct {
	Src            netip.Addr
	Dst            netip.Addr
	Identification uint32
}

// FragmentInput is one decoded IPv6 fragment.
//
// The embedding stack is responsible for parsing the Fragment Header and the
// headers in front of it:
//
//   - Unfragmentable holds the original headers preceding the Fragment Header
//     (typically the IPv6 header and any headers that must be processed by
//     every router). It is delivered verbatim after NextHeaderAt is patched.
//   - NextHeaderAt is the byte index of the Next Header field inside
//     Unfragmentable that currently points at the Fragment Header. It is
//     rewritten to FragmentNextHeader on delivery.
//   - FragmentNextHeader is the Next Header value carried by the Fragment
//     Header, i.e. the first header of the fragmentable part.
//   - Fragmentable holds the fragmentable-part bytes carried by this fragment.
//   - Offset is the fragment offset in octets (the 13-bit wire field times 8).
type FragmentInput struct {
	Src                netip.Addr
	Dst                netip.Addr
	Identification     uint32
	Offset             uint32
	MoreFragments      bool
	Fragmentable       []byte
	Unfragmentable     []byte
	NextHeaderAt       int
	FragmentNextHeader byte
}

func (in FragmentInput) validate() error {
	if !in.Src.IsValid() || !in.Dst.IsValid() ||
		in.Src.BitLen() != 128 || in.Dst.BitLen() != 128 ||
		in.Src.Is4In6() || in.Dst.Is4In6() {
		return ErrMalformed
	}
	if in.Offset%8 != 0 || in.Offset > maxFragmentableLen {
		return ErrMalformed
	}
	if uint64(in.Offset)+uint64(len(in.Fragmentable)) > maxFragmentableLen {
		return ErrMalformed
	}
	if len(in.Unfragmentable) == 0 {
		if in.NextHeaderAt != 0 {
			return ErrMalformed
		}
	} else if in.NextHeaderAt < 0 || in.NextHeaderAt >= len(in.Unfragmentable) {
		return ErrMalformed
	}
	return nil
}

// ReassembledPacket is a fully reassembled datagram handed to the upper layer.
//
// Packet contains the unfragmentable part with its Next Header field patched
// to point at the first header of the fragmentable part, followed by the
// complete, contiguous fragmentable part. The Fragment Header itself is not
// included.
type ReassembledPacket struct {
	Src            netip.Addr
	Dst            netip.Addr
	Identification uint32
	Packet         []byte
}

// Config configures a Reassembler.
type Config struct {
	// Timeout is the maximum lifetime of an incomplete datagram. Zero or
	// negative selects the RFC 8200 default of 60 seconds.
	Timeout time.Duration
	// MaxPerSourceBytes limits the buffered fragment payload per source
	// address across all of its incomplete datagrams. Zero disables the limit.
	MaxPerSourceBytes int
	// Now supplies the clock used to stamp new datagrams. It defaults to
	// time.Now; tests inject a controllable clock.
	Now func() time.Time
	// OnDeliver is invoked with every fully reassembled datagram, including
	// atomic fragments. It must be safe to call from the goroutine running
	// Handle and must not call back into the Reassembler while running.
	OnDeliver func(ReassembledPacket)
	// OnDrop is invoked whenever a fragment or datagram is discarded.
	OnDrop func(Key, DropReason)
}

// Stats is a point-in-time snapshot of reassembler counters.
type Stats struct {
	Delivered        uint64
	Dropped          map[DropReason]uint64
	PendingDatagrams int
	PendingBytes     int
}

type reassemblyEntry struct {
	key       Key
	createdAt time.Time

	// pieces maps fragment offset (octets) to a retained copy of its payload.
	// Inserts are rejected on any interval intersection, so ranges are
	// pairwise disjoint.
	pieces map[uint32][]byte

	lastOff  uint32
	lastLen  uint32
	totalLen uint32

	unfrag []byte
	nhAt   int
	fragNH byte
	bytes  int
}

// Reassembler reassembles IPv6 fragments. The zero value is not usable; create
// one with New.
type Reassembler struct {
	cfg      Config
	now      func() time.Time
	timeout  time.Duration
	mu       sync.Mutex
	entries  map[Key]*reassemblyEntry
	srcBytes map[netip.Addr]int
	stats    Stats
}

// New creates a Reassembler with the given configuration.
func New(cfg Config) *Reassembler {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Reassembler{
		cfg:      cfg,
		now:      now,
		timeout:  timeout,
		entries:  make(map[Key]*reassemblyEntry),
		srcBytes: make(map[netip.Addr]int),
		stats: Stats{
			Dropped: make(map[DropReason]uint64),
		},
	}
}

// Handle processes one fragment.
//
// An atomic fragment is delivered immediately. Fragments of an incomplete
// datagram are buffered until the last fragment completes a gap-free
// fragmentable part, at which point OnDeliver fires exactly once. Structural
// or policy violations discard the affected datagram and return one of the
// sentinel errors.
func (r *Reassembler) Handle(in FragmentInput) error {
	if err := in.validate(); err != nil {
		r.recordDrop(Key{
			Src:            in.Src,
			Dst:            in.Dst,
			Identification: in.Identification,
		}, DropMalformed)
		return err
	}

	key := Key{Src: in.Src, Dst: in.Dst, Identification: in.Identification}
	payloadLen := len(in.Fragmentable)

	// Atomic fragment: offset zero with M clear. It is complete on its own and
	// never consumes reassembly state or the per-source byte budget.
	if in.Offset == 0 && !in.MoreFragments {
		r.emitDeliver(ReassembledPacket{
			Src:            in.Src,
			Dst:            in.Dst,
			Identification: in.Identification,
			Packet: buildPacket(
				in.Unfragmentable, in.NextHeaderAt,
				in.FragmentNextHeader, in.Fragmentable,
			),
		})
		return nil
	}

	var (
		deliver    *ReassembledPacket
		dropReason DropReason
		dropKey    Key
	)

	r.mu.Lock()

	entry, exists := r.entries[key]
	if !exists {
		// A non-initial fragment for an unknown datagram can never be joined
		// to anything; it is typically a late fragment of an evicted datagram.
		if in.Offset != 0 {
			dropReason, dropKey = DropOrphan, key
			r.stats.Dropped[DropOrphan]++
			r.mu.Unlock()
			r.fireDrop(dropKey, dropReason)
			return ErrOrphan
		}

		if r.overSourceLimit(in.Src, payloadLen) {
			dropReason, dropKey = DropSourceLimit, key
			r.stats.Dropped[DropSourceLimit]++
			r.mu.Unlock()
			r.fireDrop(dropKey, dropReason)
			return ErrSourceLimit
		}

		entry = &reassemblyEntry{
			key:       key,
			createdAt: r.now(),
			pieces:    make(map[uint32][]byte),
			lastOff:   unknownLast,
			unfrag:    append([]byte(nil), in.Unfragmentable...),
			nhAt:      in.NextHeaderAt,
			fragNH:    in.FragmentNextHeader,
		}
		r.entries[key] = entry
	} else if r.overSourceLimit(in.Src, payloadLen) {
		// The datagram itself stays alive; only this fragment is refused.
		dropReason, dropKey = DropSourceLimit, key
		r.stats.Dropped[DropSourceLimit]++
		r.mu.Unlock()
		r.fireDrop(dropKey, dropReason)
		return ErrSourceLimit
	}

	abort := func(reason DropReason) error {
		r.removeEntry(entry)
		r.stats.Dropped[reason]++
		dropReason, dropKey = reason, entry.key
		r.mu.Unlock()
		r.fireDrop(dropKey, dropReason)
		return reasonError(reason)
	}

	end := in.Offset + uint32(payloadLen)

	// The last fragment fixes the total length. Reject an incompatible second
	// last fragment and anything extending beyond the known total.
	if !in.MoreFragments {
		if entry.lastOff != unknownLast &&
			(entry.lastOff != in.Offset || entry.lastLen != uint32(payloadLen)) {
			return abort(DropConflict)
		}
	}
	if entry.lastOff != unknownLast && end > entry.totalLen {
		return abort(DropOversize)
	}

	// Every fragment except the last must end on an 8-octet boundary, since
	// subsequent fragments can only start at multiples of 8.
	if in.MoreFragments && payloadLen%8 != 0 {
		return abort(DropUnaligned)
	}

	// Overlap policy: any intersecting range, including an exact duplicate,
	// discards the whole datagram.
	for off, existing := range entry.pieces {
		if in.Offset < off+uint32(len(existing)) && off < end {
			return abort(DropOverlap)
		}
	}

	if !in.MoreFragments {
		if end > maxFragmentableLen {
			return abort(DropOversize)
		}
		entry.lastOff = in.Offset
		entry.lastLen = uint32(payloadLen)
		entry.totalLen = end
	}

	entry.pieces[in.Offset] = append([]byte(nil), in.Fragmentable...)
	entry.bytes += payloadLen
	r.srcBytes[in.Src] += payloadLen

	if assembled, ok := entry.assemble(); ok {
		deliver = &ReassembledPacket{
			Src:            in.Src,
			Dst:            in.Dst,
			Identification: in.Identification,
			Packet: buildPacket(
				entry.unfrag, entry.nhAt, entry.fragNH, assembled,
			),
		}
		r.removeEntry(entry)
		r.stats.Delivered++
	}

	r.mu.Unlock()

	if deliver != nil {
		r.fireDeliver(*deliver)
	}
	return nil
}

func (r *Reassembler) overSourceLimit(src netip.Addr, extra int) bool {
	limit := r.cfg.MaxPerSourceBytes
	if limit <= 0 {
		return false
	}
	return r.srcBytes[src]+extra > limit
}

// assemble returns the contiguous fragmentable part once every octet up to the
// total length is covered. Stored ranges are disjoint, so strict contiguity is
// both necessary and sufficient.
func (e *reassemblyEntry) assemble() ([]byte, bool) {
	if e.lastOff == unknownLast || e.bytes != int(e.totalLen) {
		return nil, false
	}

	offsets := make([]uint32, 0, len(e.pieces))
	for off := range e.pieces {
		offsets = append(offsets, off)
	}
	sort.Slice(offsets, func(i, j int) bool { return offsets[i] < offsets[j] })

	var expect uint32
	for _, off := range offsets {
		if off != expect {
			return nil, false
		}
		expect = off + uint32(len(e.pieces[off]))
	}
	if expect != e.totalLen {
		return nil, false
	}

	out := make([]byte, 0, e.totalLen)
	for _, off := range offsets {
		out = append(out, e.pieces[off]...)
	}
	return out, true
}

// Tick advances the reassembler with the injected clock. Every incomplete
// datagram older than the configured timeout is removed, its buffered bytes
// are returned to the per-source budget, and an OnDrop(DropTimeout) event
// fires. It returns the number of datagrams reclaimed.
func (r *Reassembler) Tick() int {
	now := r.now()

	r.mu.Lock()
	type expired struct {
		key    Key
		reason DropReason
	}
	var events []expired

	for key, entry := range r.entries {
		if now.Sub(entry.createdAt) >= r.timeout {
			r.removeEntry(entry)
			r.stats.Dropped[DropTimeout]++
			events = append(events, expired{key: key, reason: DropTimeout})
		}
	}
	r.mu.Unlock()

	for _, ev := range events {
		r.fireDrop(ev.key, ev.reason)
	}
	return len(events)
}

// Stats returns a point-in-time snapshot of counters and outstanding state.
func (r *Reassembler) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()

	dropped := make(map[DropReason]uint64, len(r.stats.Dropped))
	for reason, count := range r.stats.Dropped {
		dropped[reason] = count
	}

	pendingBytes := 0
	for _, used := range r.srcBytes {
		pendingBytes += used
	}

	return Stats{
		Delivered:        r.stats.Delivered,
		Dropped:          dropped,
		PendingDatagrams: len(r.entries),
		PendingBytes:     pendingBytes,
	}
}

// PendingSourceBytes reports the buffered fragment bytes currently charged to
// src. It is primarily intended for tests and embedded-stack diagnostics.
func (r *Reassembler) PendingSourceBytes(src netip.Addr) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.srcBytes[src]
}

// removeEntry must be called with mu held.
func (r *Reassembler) removeEntry(entry *reassemblyEntry) {
	if r.entries[entry.key] != entry {
		return
	}
	delete(r.entries, entry.key)
	if used := r.srcBytes[entry.key.Src] - entry.bytes; used > 0 {
		r.srcBytes[entry.key.Src] = used
	} else {
		delete(r.srcBytes, entry.key.Src)
	}
}

func (r *Reassembler) emitDeliver(pkt ReassembledPacket) {
	r.mu.Lock()
	r.stats.Delivered++
	r.mu.Unlock()
	r.fireDeliver(pkt)
}

func (r *Reassembler) recordDrop(key Key, reason DropReason) {
	r.mu.Lock()
	r.stats.Dropped[reason]++
	r.mu.Unlock()
	r.fireDrop(key, reason)
}

func (r *Reassembler) fireDeliver(pkt ReassembledPacket) {
	if r.cfg.OnDeliver != nil {
		r.cfg.OnDeliver(pkt)
	}
}

func (r *Reassembler) fireDrop(key Key, reason DropReason) {
	if r.cfg.OnDrop != nil {
		r.cfg.OnDrop(key, reason)
	}
}

// buildPacket concatenates the unfragmentable part (with the Next Header field
// repointed at the fragmentable part) and the reassembled fragmentable part.
// When unfragmentable is empty the fragmentable payload is returned as-is,
// which is useful for stack wrappers that reconstruct headers themselves.
func buildPacket(unfragmentable []byte, nextHeaderAt int, fragmentNextHeader byte, fragmentable []byte) []byte {
	pkt := make([]byte, len(unfragmentable)+len(fragmentable))
	copy(pkt, unfragmentable)
	if len(unfragmentable) > 0 {
		pkt[nextHeaderAt] = fragmentNextHeader
	}
	copy(pkt[len(unfragmentable):], fragmentable)
	return pkt
}

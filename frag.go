// Package mtusim simulates MTU-constrained datagram fragmentation,
// re-fragmentation after path MTU corrections, and reassembly with
// overlap/duplicate detection. Everything runs in a discrete-event
// model with integer ticks; no real network traffic is generated.
package mtusim

import (
	"fmt"
	"sort"
)

// ErrKind classifies the sentinel failures this package reports.
type ErrKind int

const (
	ErrKindNone ErrKind = iota
	// ErrKindMTUBelowHeader: the link MTU cannot even carry one header.
	ErrKindMTUBelowHeader
	// ErrKindInfeasible: no legal fragmentation exists (e.g. the aligned
	// payload unit exceeds the per-frame payload budget).
	ErrKindInfeasible
	// ErrKindNegativeLength: a negative datagram or fragment length.
	ErrKindNegativeLength
	// ErrKindEpochMismatch: a fragment from a different re-fragmentation
	// epoch was offered to a reassembly context.
	ErrKindEpochMismatch
	// ErrKindPacketMismatch: a fragment belongs to another datagram.
	ErrKindPacketMismatch
	// ErrKindOverlapConflict: overlapping fragments disagree on bytes.
	ErrKindOverlapConflict
	// ErrKindMalformed: inconsistent fragment fields.
	ErrKindMalformed
)

// Error is the single error type of the package; Kind is stable and
// machine-checkable (kinds are explicit instead of wrapped sentinels).
type Error struct {
	Kind ErrKind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// KindOf extracts the ErrKind of any error produced by this package.
func KindOf(err error) ErrKind {
	if e, ok := err.(*Error); ok {
		return e.Kind
	}
	return ErrKindNone
}

func errf(kind ErrKind, format string, args ...interface{}) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// FragParams describes per-fragment overhead and alignment on a link.
type FragParams struct {
	HeaderLen int // header bytes prepended to every fragment
	MTU       int // max bytes a frame may carry, header included
	Align     int // payload of every non-final fragment is a multiple of Align
}

// Fragment is one piece of a datagram. Offset and Len identify the exact
// byte range [Offset, Offset+Len) of the original datagram, so identity
// stays traceable across re-fragmentation epochs.
type Fragment struct {
	PacketID int
	Epoch    int
	Offset   int
	Len      int
	Last     bool
	Header   int // header bytes that accompany this fragment on the wire
}

// WireSize is the total number of bytes the fragment occupies on the link.
func (f Fragment) WireSize() int { return f.Header + f.Len }

// Range returns the original-datagram byte range the fragment carries.
func (f Fragment) Range() (lo, hi int) { return f.Offset, f.Offset + f.Len }

// maxPayload is the largest payload one frame can carry.
func (p FragParams) maxPayload() int { return p.MTU - p.HeaderLen }

// alignedPayload is the largest payload satisfying the alignment rule.
func (p FragParams) alignedPayload() int {
	return p.maxPayload() / p.Align * p.Align
}

// Validate rejects parameters that make fragmentation impossible up front:
// MTU smaller than the minimum header fails directly.
func (p FragParams) Validate() error {
	if p.HeaderLen < 0 || p.MTU < 0 || p.Align < 0 {
		return errf(ErrKindNegativeLength, "negative parameter in %+v", p)
	}
	if p.Align == 0 {
		return errf(ErrKindMalformed, "alignment unit must be >= 1")
	}
	if p.MTU < p.HeaderLen {
		return errf(ErrKindMTUBelowHeader,
			"mtu %d below minimum header %d", p.MTU, p.HeaderLen)
	}
	return nil
}

// FragmentCount returns the number of fragments needed to carry totalLen
// payload bytes, or an error if the parameters cannot carry the datagram.
// The count is minimal for these parameters: every non-final fragment is
// filled with the largest aligned payload.
func (p FragParams) FragmentCount(totalLen int) (int, error) {
	if totalLen < 0 {
		return 0, errf(ErrKindNegativeLength, "datagram length %d is negative", totalLen)
	}
	if err := p.Validate(); err != nil {
		return 0, err
	}
	if totalLen <= p.maxPayload() {
		return 1, nil // single fragment needs no alignment
	}
	unit := p.alignedPayload()
	if unit <= 0 {
		return 0, errf(ErrKindInfeasible,
			"aligned payload unit 0: mtu=%d header=%d align=%d, datagram %d bytes does not fit one frame",
			p.MTU, p.HeaderLen, p.Align, totalLen)
	}
	// Minimal count: k-1 aligned fragments of `unit` bytes plus a final
	// fragment that may use the full (unaligned) payload budget.
	return 1 + (totalLen-p.maxPayload()+unit-1)/unit, nil
}

// Fragmentize splits a datagram of totalLen bytes into fragments. Every
// fragment keeps (PacketID, Epoch) plus its byte range so re-fragmentation
// across multiple MTU corrections stays traceable. The final fragment may
// be shorter than the alignment unit.
func (p FragParams) Fragmentize(packetID, epoch, totalLen int) ([]Fragment, error) {
	if _, err := p.FragmentCount(totalLen); err != nil {
		return nil, err
	}
	if totalLen == 0 {
		return []Fragment{{PacketID: packetID, Epoch: epoch, Last: true, Header: p.HeaderLen}}, nil
	}
	unit := p.alignedPayload()
	var frags []Fragment
	for off := 0; off < totalLen; {
		l := totalLen - off
		if l > p.maxPayload() {
			l = unit // unit >= 1 here, guaranteed by FragmentCount
		}
		frags = append(frags, Fragment{
			PacketID: packetID,
			Epoch:    epoch,
			Offset:   off,
			Len:      l,
			Last:     off+l == totalLen,
			Header:   p.HeaderLen,
		})
		off += l
	}
	return frags, nil
}

// VerifyCoverage independently checks that frags form a legal fragmentation
// of a totalLen-byte datagram under p. It shares no logic with Fragmentize:
// fragments must tile [0,totalLen) contiguously, respect the MTU, carry the
// negotiated header length, keep non-final payloads aligned, and mark Last
// exactly on the fragment ending at totalLen.
func VerifyCoverage(p FragParams, totalLen int, frags []Fragment) error {
	if totalLen < 0 {
		return errf(ErrKindNegativeLength, "datagram length %d is negative", totalLen)
	}
	if err := p.Validate(); err != nil {
		return err
	}
	if len(frags) == 0 {
		return errf(ErrKindMalformed, "empty fragment list")
	}
	sorted := make([]Fragment, len(frags))
	copy(sorted, frags)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Offset < sorted[j].Offset })

	expect := 0
	for i, f := range sorted {
		if f.Len < 0 || f.Offset < 0 {
			return errf(ErrKindNegativeLength, "fragment %d has negative offset/len", i)
		}
		if f.Offset != expect {
			return errf(ErrKindMalformed,
				"coverage gap/overlap: fragment %d starts at %d, expected %d", i, f.Offset, expect)
		}
		if f.Len == 0 && totalLen > 0 {
			return errf(ErrKindMalformed, "fragment %d carries no payload", i)
		}
		if f.Header != p.HeaderLen {
			return errf(ErrKindMalformed,
				"fragment %d header %d, expected %d", i, f.Header, p.HeaderLen)
		}
		if f.WireSize() > p.MTU {
			return errf(ErrKindMalformed,
				"fragment %d wire size %d exceeds mtu %d", i, f.WireSize(), p.MTU)
		}
		isLast := f.Offset+f.Len == totalLen
		if f.Last != isLast {
			return errf(ErrKindMalformed, "fragment %d Last flag inconsistent", i)
		}
		if !isLast && f.Len%p.Align != 0 {
			return errf(ErrKindMalformed,
				"fragment %d payload %d not a multiple of align %d", i, f.Len, p.Align)
		}
		expect += f.Len
	}
	if expect != totalLen {
		return errf(ErrKindMalformed, "fragments cover %d bytes, datagram is %d", expect, totalLen)
	}
	return nil
}

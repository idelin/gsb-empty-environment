package taskpkg

import (
	"bytes"
	"fmt"
	"math"
)

// Verify checks that the evidence partitions its half-open alert interval and
// that every output data byte has a complete chain back to one exact identity.
// Gaps must be explicit and conflicts must retain at least two distinct sources.
func (e *Evidence) Verify() error {
	if e == nil || e.store == nil {
		return ErrEvidenceInvalid
	}
	if !e.Alert.Valid() || !e.Alert.Identity.Equal(e.Identity) || e.Start != e.Alert.Start || e.End != e.Alert.End {
		return fmt.Errorf("%w: alert and evidence identity/range differ", ErrEvidenceInvalid)
	}
	e.store.mu.RLock()
	defer e.store.mu.RUnlock()
	if e.store.revokedIdentity(e.Alert.Identity) {
		return ErrRevoked
	}
	return verifyInterval(e.Identity, e.Start, e.End, e.Segments, e.Conflicts, e.snapshots)
}

// Verify checks a delivered snapshot. A Delivery is no longer pending and is
// intentionally not invalidated by later revocations.
func (d *Delivery) Verify() error {
	if d == nil || !d.Alert.Valid() || !d.Alert.Identity.Equal(d.Identity) ||
		d.Start != d.Alert.Start || d.End != d.Alert.End {
		return ErrEvidenceInvalid
	}
	return verifyInterval(d.Identity, d.Start, d.End, d.Segments, d.Conflicts, d.snapshots)
}

func verifyInterval(identity Identity, start, end uint64, segments []Segment, conflicts []Conflict, snapshots map[BlockID]blockSnapshot) error {
	if start > end {
		return fmt.Errorf("%w: interval ends before it starts", ErrEvidenceInvalid)
	}
	if start == end {
		if len(segments) != 0 || len(conflicts) != 0 {
			return fmt.Errorf("%w: empty interval has segments", ErrEvidenceInvalid)
		}
		return nil
	}

	cursor := start
	conflictCount := 0
	for i, seg := range segments {
		if seg.Start != cursor || seg.End <= seg.Start {
			return fmt.Errorf("%w: segment %d does not partition at offset %d", ErrEvidenceInvalid, i, cursor)
		}
		if !seg.Identity.Equal(identity) {
			return fmt.Errorf("%w: segment %d identity %s != %s", ErrChainBroken, i, seg.Identity, identity)
		}
		switch seg.Kind {
		case KindGap:
			if len(seg.Data) != 0 || len(seg.Sources) != 0 || len(seg.Variants) != 0 {
				return fmt.Errorf("%w: gap segment %d carries bytes or sources", ErrEvidenceInvalid, i)
			}
		case KindData:
			if len(seg.Data) != int(seg.End-seg.Start) {
				return fmt.Errorf("%w: data segment %d has %d bytes for range %d", ErrChainBroken, i, len(seg.Data), seg.End-seg.Start)
			}
			if len(seg.Sources) == 0 {
				return fmt.Errorf("%w: data segment %d has no provenance", ErrChainBroken, i)
			}
			for j, src := range seg.Sources {
				if err := verifySource(snapshots, identity, seg, src); err != nil {
					return fmt.Errorf("segment %d source %d: %w", i, j, err)
				}
				if src.Start != seg.Start || src.End != seg.End {
					return fmt.Errorf("%w: segment %d source %d does not cover the whole segment", ErrChainBroken, i, j)
				}
			}
			if !sourcesCover(seg.Sources, seg.Start, seg.End) {
				return fmt.Errorf("%w: segment %d has uncovered data bytes", ErrChainBroken, i)
			}
		case KindOverlapConflict:
			conflictCount++
			if len(seg.Data) != 0 {
				return fmt.Errorf("%w: conflict segment %d contains selected bytes", ErrEvidenceInvalid, i)
			}
			if len(seg.Sources) < 2 || len(seg.Variants) < 2 {
				return fmt.Errorf("%w: conflict segment %d lacks both sides", ErrEvidenceInvalid, i)
			}
			for j, src := range seg.Sources {
				if err := verifySource(snapshots, identity, seg, src); err != nil {
					return fmt.Errorf("conflict segment %d source %d: %w", i, j, err)
				}
			}
			if err := verifyVariants(snapshots, identity, seg); err != nil {
				return fmt.Errorf("conflict segment %d: %w", i, err)
			}
		default:
			return fmt.Errorf("%w: segment %d has unknown kind", ErrEvidenceInvalid, i)
		}
		cursor = seg.End
	}
	if cursor != end {
		return fmt.Errorf("%w: segments end at %d, want %d", ErrEvidenceInvalid, cursor, end)
	}
	if conflictCount != len(conflicts) {
		return fmt.Errorf("%w: %d conflict reports for %d conflict segments", ErrEvidenceInvalid, len(conflicts), conflictCount)
	}
	ci := 0
	for _, seg := range segments {
		if seg.Kind != KindOverlapConflict {
			continue
		}
		report := conflicts[ci]
		ci++
		if !report.Identity.Equal(seg.Identity) || report.Start != seg.Start || report.End != seg.End ||
			!sameSources(report.Sources, seg.Sources) || !sameVariants(report.Variants, seg.Variants) {
			return fmt.Errorf("%w: flattened conflict report does not match segment", ErrEvidenceInvalid)
		}
	}
	return nil
}

func verifySource(snapshots map[BlockID]blockSnapshot, identity Identity, seg Segment, src Source) error {
	if !src.Identity.Equal(identity) || !src.Identity.Equal(seg.Identity) {
		return fmt.Errorf("%w: source belongs to %s", ErrChainBroken, src.Identity)
	}
	if src.End < src.Start || src.Start < seg.Start || src.End > seg.End {
		return fmt.Errorf("%w: source range is outside segment", ErrChainBroken)
	}
	if src.Length > math.MaxUint64-src.Offset {
		return fmt.Errorf("%w: source block end overflows", ErrChainBroken)
	}
	lenContribution := src.End - src.Start
	if uint64(len(src.Data)) != lenContribution {
		return fmt.Errorf("%w: source has %d bytes for range %d", ErrChainBroken, len(src.Data), lenContribution)
	}
	segData := seg.Data
	if seg.Kind == KindOverlapConflict {
		// Conflict segment has no selected data; compare the source against its
		// immutable snapshot instead.
		segData = nil
	}
	if segData != nil {
		want := segData[src.Start-seg.Start : src.End-seg.Start]
		if !bytes.Equal(src.Data, want) {
			return fmt.Errorf("%w: source bytes disagree with segment", ErrChainBroken)
		}
	}

	// The unexported snapshot makes Verify detect tampering with exported
	// segment/source fields, not merely self-consistency between them.
	if snapshots != nil {
		snap, ok := snapshots[src.Block]
		if !ok {
			return fmt.Errorf("%w: source block %s is missing from snapshot", ErrChainBroken, src.Block)
		}
		if !snap.identity.Equal(src.Identity) || snap.offset != src.Offset || uint64(len(snap.data)) != src.Length {
			return fmt.Errorf("%w: source block metadata differs from snapshot", ErrChainBroken)
		}
		if src.Start < snap.offset || src.End < src.Start {
			return fmt.Errorf("%w: source starts before its immutable block", ErrChainBroken)
		}
		lo := src.Start - snap.offset
		hi := src.End - snap.offset
		if hi > uint64(len(snap.data)) {
			return fmt.Errorf("%w: source extends beyond immutable block", ErrChainBroken)
		}
		if !bytes.Equal(src.Data, snap.data[lo:hi]) {
			return fmt.Errorf("%w: source bytes differ from immutable block", ErrChainBroken)
		}
	}
	return nil
}

func verifyVariants(snapshots map[BlockID]blockSnapshot, identity Identity, seg Segment) error {
	seenSources := make(map[BlockID]bool, len(seg.Sources))
	for _, src := range seg.Sources {
		if seenSources[src.Block] {
			return fmt.Errorf("%w: duplicate conflict source %s", ErrEvidenceInvalid, src.Block)
		}
		seenSources[src.Block] = true
	}

	seenVariants := make(map[BlockID]bool, len(seg.Sources))
	for i, variant := range seg.Variants {
		if len(variant.Data) != int(seg.End-seg.Start) {
			return fmt.Errorf("%w: variant %d has %d bytes for range %d", ErrChainBroken, i, len(variant.Data), seg.End-seg.Start)
		}
		if len(variant.Sources) == 0 {
			return fmt.Errorf("%w: variant %d has no sources", ErrChainBroken, i)
		}
		for _, src := range variant.Sources {
			if seenVariants[src.Block] {
				return fmt.Errorf("%w: block %s appears in more than one variant", ErrEvidenceInvalid, src.Block)
			}
			seenVariants[src.Block] = true
			segSrc, ok := sourceByBlock(seg.Sources, src.Block)
			if !ok || !sameSource(src, segSrc) {
				return fmt.Errorf("%w: variant %d source does not match conflict source", ErrChainBroken, i)
			}
			if err := verifySource(snapshots, identity, seg, src); err != nil {
				return fmt.Errorf("%w: variant %d source: %v", ErrChainBroken, i, err)
			}
			if !bytes.Equal(src.Data, variant.Data) {
				return fmt.Errorf("%w: variant %d source bytes differ", ErrChainBroken, i)
			}
		}
	}
	if len(seenVariants) != len(seenSources) {
		return fmt.Errorf("%w: conflict variants do not account for every source", ErrChainBroken)
	}
	for i := 0; i < len(seg.Variants); i++ {
		for j := i + 1; j < len(seg.Variants); j++ {
			if bytes.Equal(seg.Variants[i].Data, seg.Variants[j].Data) {
				return fmt.Errorf("%w: distinct conflict variants contain equal bytes", ErrEvidenceInvalid)
			}
		}
	}
	return nil
}

func sourceByBlock(sources []Source, id BlockID) (Source, bool) {
	for _, src := range sources {
		if src.Block == id {
			return src, true
		}
	}
	return Source{}, false
}

func sameSource(a, b Source) bool {
	return a.Block == b.Block && a.Identity.Equal(b.Identity) &&
		a.Offset == b.Offset && a.Length == b.Length &&
		a.Start == b.Start && a.End == b.End &&
		bytes.Equal(a.Data, b.Data)
}

func sourcesCover(sources []Source, start, end uint64) bool {
	cursor := start
	for cursor < end {
		next := cursor
		for _, src := range sources {
			if src.Start <= cursor && src.End > next {
				next = src.End
			}
		}
		if next == cursor {
			return false
		}
		cursor = next
	}
	return true
}

func sameSources(a, b []Source) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Block != b[i].Block || !a[i].Identity.Equal(b[i].Identity) ||
			a[i].Offset != b[i].Offset || a[i].Length != b[i].Length ||
			a[i].Start != b[i].Start || a[i].End != b[i].End ||
			!bytes.Equal(a[i].Data, b[i].Data) {
			return false
		}
	}
	return true
}

func sameVariants(a, b []Variant) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i].Data, b[i].Data) || !sameSources(a[i].Sources, b[i].Sources) {
			return false
		}
	}
	return true
}

// Trace returns the identity chain for exactly one output byte.
func (e *Evidence) Trace(offset uint64) (Trace, error) {
	if e == nil || e.store == nil {
		return Trace{}, ErrEvidenceInvalid
	}
	e.store.mu.RLock()
	defer e.store.mu.RUnlock()
	if e.store.revokedIdentity(e.Identity) {
		return Trace{}, ErrRevoked
	}
	return traceSegments(e.Identity, e.Start, e.End, e.Segments, offset)
}

// Trace returns the identity chain for one byte of a delivered snapshot.
func (d *Delivery) Trace(offset uint64) (Trace, error) {
	if d == nil {
		return Trace{}, ErrEvidenceInvalid
	}
	return traceSegments(d.Identity, d.Start, d.End, d.Segments, offset)
}

func traceSegments(identity Identity, start, end uint64, segments []Segment, offset uint64) (Trace, error) {
	if offset < start || offset >= end {
		return Trace{}, ErrOutOfRange
	}
	for _, seg := range segments {
		if offset < seg.Start || offset >= seg.End {
			continue
		}
		tr := Trace{
			Offset:   offset,
			Kind:     seg.Kind,
			Identity: seg.Identity,
			Sources:  cloneSources(seg.Sources),
			Variants: cloneVariants(seg.Variants),
		}
		if seg.Kind == KindGap {
			return tr, ErrGap
		}
		return tr, nil
	}
	return Trace{}, ErrOutOfRange
}

// ByteIdentity returns the exact identity that owns one byte of a delivered
// snapshot.
func (d *Delivery) ByteIdentity(offset uint64) (Identity, error) {
	tr, err := d.Trace(offset)
	if err != nil {
		return Identity{}, err
	}
	return byteIdentityFromTrace(tr, offset)
}

// ByteIdentity returns the exact identity that owns one output byte. Gaps and
// conflicts return ErrGap/ErrEvidenceInvalid respectively because no single
// byte value may be delivered from them.
func (e *Evidence) ByteIdentity(offset uint64) (Identity, error) {
	tr, err := e.Trace(offset)
	if err != nil {
		return Identity{}, err
	}
	return byteIdentityFromTrace(tr, offset)
}

func byteIdentityFromTrace(tr Trace, offset uint64) (Identity, error) {
	switch tr.Kind {
	case KindData:
		for _, src := range tr.Sources {
			if src.Start <= offset && offset < src.End {
				return src.Identity, nil
			}
		}
		return Identity{}, ErrChainBroken
	case KindOverlapConflict:
		return Identity{}, ErrEvidenceInvalid
	default:
		return Identity{}, ErrGap
	}
}

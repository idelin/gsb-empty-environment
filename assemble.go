package taskpkg

import (
	"bytes"
	"sort"
)

// Assemble builds evidence for one alert. Only blocks with exactly the same
// tenant and generation are considered. The returned segments partition the
// half-open alert interval; missing bytes are KindGap segments and disagreeing
// overlaps are KindOverlapConflict segments containing every source.
func (s *Store) Assemble(alert Alert) (*Evidence, error) {
	if s == nil {
		return nil, ErrEvidenceInvalid
	}
	if !alert.Valid() {
		if !alert.Identity.Valid() {
			return nil, ErrInvalidIdentity
		}
		return nil, ErrInvalidInterval
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.revokedIdentity(alert.Identity) {
		return nil, ErrRevoked
	}
	if alert.Start == alert.End {
		return &Evidence{
			Alert:     alert,
			Identity:  alert.Identity,
			Start:     alert.Start,
			End:       alert.End,
			store:     s,
			snapshots: make(map[BlockID]blockSnapshot),
		}, nil
	}

	records := s.snapshotIdentity(alert.Identity, alert.Start, alert.End)
	if len(records) == 0 {
		return &Evidence{
			Alert:    alert,
			Identity: alert.Identity,
			Start:    alert.Start,
			End:      alert.End,
			Segments: []Segment{{
				Kind:     KindGap,
				Identity: alert.Identity,
				Start:    alert.Start,
				End:      alert.End,
			}},
			store:     s,
			snapshots: make(map[BlockID]blockSnapshot),
		}, nil
	}

	ev := &Evidence{
		Alert:     alert,
		Identity:  alert.Identity,
		Start:     alert.Start,
		End:       alert.End,
		store:     s,
		snapshots: make(map[BlockID]blockSnapshot),
	}
	for _, rec := range records {
		ev.snapshots[rec.id] = blockSnapshot{
			identity: rec.identity,
			offset:   rec.offset,
			data:     rec.data,
		}
	}

	builders := assembleSpans(alert, records)
	ev.Segments = finalizeBuilders(alert.Identity, builders, records)
	for _, seg := range ev.Segments {
		if seg.Kind != KindOverlapConflict {
			continue
		}
		ev.Conflicts = append(ev.Conflicts, Conflict{
			Identity: alert.Identity,
			Start:    seg.Start,
			End:      seg.End,
			Sources:  cloneSources(seg.Sources),
			Variants: cloneVariants(seg.Variants),
		})
	}
	return ev, nil
}

type spanBuilder struct {
	kind      SegmentKind
	start     uint64
	end       uint64
	data      []byte
	sourceIDs []BlockID
	variants  []variantBuilder
}

type variantBuilder struct {
	data      []byte
	sourceIDs []BlockID
}

type span struct {
	kind     SegmentKind
	start    uint64
	end      uint64
	data     []byte
	blocks   []*blockRecord
	variants []variantBuilder
}

func assembleSpans(alert Alert, records []*blockRecord) []span {
	boundaries := make([]uint64, 0, len(records)*2+2)
	boundaries = append(boundaries, alert.Start, alert.End)
	for _, rec := range records {
		lo := maxUint64(rec.offset, alert.Start)
		hi := minUint64(rec.end(), alert.End)
		if lo < hi {
			boundaries = append(boundaries, lo, hi)
		}
	}
	sort.Slice(boundaries, func(i, j int) bool { return boundaries[i] < boundaries[j] })
	unique := boundaries[:0]
	for _, b := range boundaries {
		if len(unique) == 0 || unique[len(unique)-1] != b {
			unique = append(unique, b)
		}
	}

	result := make([]span, 0, len(unique))
	for i := 0; i+1 < len(unique); i++ {
		lo, hi := unique[i], unique[i+1]
		covered := make([]*blockRecord, 0, len(records))
		for _, rec := range records {
			if rec.offset < hi && rec.end() > lo {
				covered = append(covered, rec)
			}
		}
		if len(covered) == 0 {
			result = append(result, span{kind: KindGap, start: lo, end: hi})
			continue
		}

		values := make([][]byte, len(covered))
		consistent := true
		for j, rec := range covered {
			values[j] = rec.data[lo-rec.offset : hi-rec.offset]
			if j > 0 && !bytes.Equal(values[0], values[j]) {
				consistent = false
			}
		}
		if consistent {
			result = append(result, span{
				kind:   KindData,
				start:  lo,
				end:    hi,
				data:   values[0],
				blocks: covered,
			})
			continue
		}

		variants := groupVariants(covered, values)
		result = append(result, span{
			kind:     KindOverlapConflict,
			start:    lo,
			end:      hi,
			blocks:   covered,
			variants: variants,
		})
	}
	return result
}

func groupVariants(blocks []*blockRecord, values [][]byte) []variantBuilder {
	groups := make([]variantBuilder, 0, len(blocks))
	for i, rec := range blocks {
		placed := false
		for j := range groups {
			if bytes.Equal(groups[j].data, values[i]) {
				groups[j].sourceIDs = append(groups[j].sourceIDs, rec.id)
				placed = true
				break
			}
		}
		if !placed {
			groups = append(groups, variantBuilder{
				data:      cloneBytes(values[i]),
				sourceIDs: []BlockID{rec.id},
			})
		}
	}
	for i := range groups {
		sort.Slice(groups[i].sourceIDs, func(a, b int) bool {
			return groups[i].sourceIDs[a] < groups[i].sourceIDs[b]
		})
	}
	return groups
}

func finalizeBuilders(identity Identity, spans []span, records []*blockRecord) []Segment {
	byID := make(map[BlockID]*blockRecord, len(records))
	for _, rec := range records {
		byID[rec.id] = rec
	}

	builders := make([]*spanBuilder, 0, len(spans))
	for _, sp := range spans {
		ids := sortedIDs(sp.blocks)
		if len(builders) > 0 {
			last := builders[len(builders)-1]
			if last.kind == sp.kind && last.end == sp.start {
				switch sp.kind {
				case KindGap:
					last.end = sp.end
					continue
				case KindData:
					if sameIDs(last.sourceIDs, ids) {
						last.data = append(last.data, sp.data...)
						last.end = sp.end
						continue
					}
				case KindOverlapConflict:
					// Conflict ranges stay separate unless both the contributor
					// set and the grouping of disagreeing values are identical.
					if sameIDs(last.sourceIDs, ids) && sameVariantIDs(last.variants, sp.variants) {
						last.end = sp.end
						for i := range last.variants {
							last.variants[i].data = append(last.variants[i].data, sp.variants[i].data...)
						}
						continue
					}
				}
			}
		}
		builders = append(builders, &spanBuilder{
			kind:      sp.kind,
			start:     sp.start,
			end:       sp.end,
			data:      cloneBytes(sp.data),
			sourceIDs: ids,
			variants:  cloneVariantBuilders(sp.variants),
		})
	}

	segments := make([]Segment, 0, len(builders))
	for _, b := range builders {
		seg := Segment{
			Kind:     b.kind,
			Identity: identity,
			Start:    b.start,
			End:      b.end,
		}
		switch b.kind {
		case KindData:
			seg.Data = cloneBytes(b.data)
			for _, id := range b.sourceIDs {
				rec := byID[id]
				seg.Sources = append(seg.Sources, Source{
					Block:    id,
					Identity: identity,
					Offset:   rec.offset,
					Length:   uint64(len(rec.data)),
					Start:    b.start,
					End:      b.end,
					Data:     cloneBytes(b.data),
				})
			}
		case KindGap:
			// Explicitly empty: no filler bytes and no provenance.
		case KindOverlapConflict:
			for _, id := range b.sourceIDs {
				rec := byID[id]
				seg.Sources = append(seg.Sources, Source{
					Block:    id,
					Identity: identity,
					Offset:   rec.offset,
					Length:   uint64(len(rec.data)),
					Start:    b.start,
					End:      b.end,
					Data:     cloneBytes(rec.data[b.start-rec.offset : b.end-rec.offset]),
				})
			}
			for _, vb := range b.variants {
				seg.Variants = append(seg.Variants, Variant{
					Data:    cloneBytes(vb.data),
					Sources: sourcesFromIDs(vb.sourceIDs, byID, identity, b.start, b.end),
				})
			}
		}
		segments = append(segments, seg)
	}
	return segments
}

func sourcesFromIDs(ids []BlockID, byID map[BlockID]*blockRecord, identity Identity, start, end uint64) []Source {
	sources := make([]Source, 0, len(ids))
	for _, id := range ids {
		rec := byID[id]
		sources = append(sources, Source{
			Block:    id,
			Identity: identity,
			Offset:   rec.offset,
			Length:   uint64(len(rec.data)),
			Start:    start,
			End:      end,
			Data:     cloneBytes(rec.data[start-rec.offset : end-rec.offset]),
		})
	}
	return sources
}

func sortedIDs(blocks []*blockRecord) []BlockID {
	ids := make([]BlockID, len(blocks))
	for i, rec := range blocks {
		ids[i] = rec.id
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func sameIDs(a, b []BlockID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameVariantIDs(a, b []variantBuilder) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sameIDs(a[i].sourceIDs, b[i].sourceIDs) {
			return false
		}
	}
	return true
}

func cloneVariantBuilders(variants []variantBuilder) []variantBuilder {
	out := make([]variantBuilder, len(variants))
	for i, v := range variants {
		out[i] = variantBuilder{
			data:      cloneBytes(v.data),
			sourceIDs: append([]BlockID(nil), v.sourceIDs...),
		}
	}
	return out
}

func cloneSources(sources []Source) []Source {
	out := make([]Source, len(sources))
	for i, s := range sources {
		out[i] = s
		out[i].Data = cloneBytes(s.Data)
	}
	return out
}

func cloneVariants(variants []Variant) []Variant {
	out := make([]Variant, len(variants))
	for i, v := range variants {
		out[i] = Variant{
			Data:    cloneBytes(v.Data),
			Sources: cloneSources(v.Sources),
		}
	}
	return out
}

func cloneSegments(segments []Segment) []Segment {
	out := make([]Segment, len(segments))
	for i, seg := range segments {
		out[i] = seg
		out[i].Data = cloneBytes(seg.Data)
		out[i].Sources = cloneSources(seg.Sources)
		out[i].Variants = cloneVariants(seg.Variants)
	}
	return out
}

func cloneConflicts(conflicts []Conflict) []Conflict {
	out := make([]Conflict, len(conflicts))
	for i, c := range conflicts {
		out[i] = c
		out[i].Sources = cloneSources(c.Sources)
		out[i].Variants = cloneVariants(c.Variants)
	}
	return out
}

// Deliver atomically checks revocation and returns deep-copied segments. If a
// tenant is revoked before the read lock is acquired, no bytes are returned.
func (e *Evidence) Deliver() (*Delivery, error) {
	if e == nil || e.store == nil {
		return nil, ErrEvidenceInvalid
	}
	if !e.Alert.Valid() || !e.Alert.Identity.Equal(e.Identity) ||
		e.Start != e.Alert.Start || e.End != e.Alert.End {
		return nil, ErrEvidenceInvalid
	}

	e.store.mu.RLock()
	defer e.store.mu.RUnlock()
	if e.store.revokedIdentity(e.Alert.Identity) {
		return nil, ErrRevoked
	}
	return &Delivery{
		Alert:     e.Alert,
		Identity:  e.Identity,
		Start:     e.Start,
		End:       e.End,
		Segments:  cloneSegments(e.Segments),
		Conflicts: cloneConflicts(e.Conflicts),
		Epoch:     e.store.revEpoch,
		snapshots: e.snapshots,
	}, nil
}

func maxUint64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

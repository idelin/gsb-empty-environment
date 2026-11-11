package covernorm

import (
	"sort"
	"sync"
)

// Normalizer is an embeddable, concurrency-safe coverage normalization
// service. Load, unload and sample events may arrive out of order: they are
// buffered and re-sorted by event sequence on Flush.
//
// The zero value is not ready; construct one with New.
type Normalizer struct {
	mu sync.Mutex

	pending   []rawEvent
	seen      map[EventSeq]struct{}
	lastFlush EventSeq

	active   []*activeLoad
	allLoads []LoadEvent
	edges    map[EdgeID]*EdgeRecord
	unknown  []*UnknownRecord
	timeline []TimelineEntry
}

type rawEvent struct {
	load   *LoadEvent
	unload *UnloadEvent
	sample *SampleEvent
}

func (e rawEvent) seq() EventSeq {
	switch {
	case e.load != nil:
		return e.load.Seq
	case e.unload != nil:
		return e.unload.Seq
	default:
		return e.sample.Seq
	}
}

// activeLoad is one load whose unload has not been processed yet. It stays
// active even before its confirmation sequence, so that samples arriving early
// can be classified as ReasonBeforeLoad rather than ReasonNoMapping.
type activeLoad struct {
	load      LoadEvent
	unloaded  bool
	unloadSeq EventSeq
}

// New constructs an empty Normalizer.
func New() *Normalizer {
	return &Normalizer{
		seen:  make(map[EventSeq]struct{}),
		edges: make(map[EdgeID]*EdgeRecord),
	}
}

// Ingest buffers one load, unload or sample event. Event types are selected by
// the non-nil argument; exactly one must be non-nil. Structural problems
// (duplicate or late sequence numbers, malformed segments) are rejected here;
// timeline consistency problems that depend on ordering are reported by Flush.
func (n *Normalizer) Ingest(load *LoadEvent, unload *UnloadEvent, sample *SampleEvent) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	event, err := buildRawEvent(load, unload, sample)
	if err != nil {
		return err
	}
	seq := event.seq()
	if seq <= n.lastFlush {
		return ErrLateEvent
	}
	if _, ok := n.seen[seq]; ok {
		return ErrDuplicateSeq
	}
	if load != nil {
		if err := validateLoad(load); err != nil {
			return err
		}
	}
	n.seen[seq] = struct{}{}
	n.pending = append(n.pending, event)
	return nil
}

func buildRawEvent(load *LoadEvent, unload *UnloadEvent, sample *SampleEvent) (rawEvent, error) {
	n := 0
	if load != nil {
		n++
	}
	if unload != nil {
		n++
	}
	if sample != nil {
		n++
	}
	if n != 1 {
		return rawEvent{}, ErrBadEvent
	}
	return rawEvent{load: load, unload: unload, sample: sample}, nil
}

func validateLoad(load *LoadEvent) error {
	if load.Digest == (Digest{}) {
		return ErrEmptyDigest
	}
	if len(load.Segments) == 0 {
		return ErrBadSegment
	}
	for _, segment := range load.Segments {
		if segment.Size == 0 {
			return ErrBadSegment
		}
		if _, ok := segment.End(); !ok {
			return ErrBadSegment
		}
	}
	for i := 0; i < len(load.Segments); i++ {
		for j := i + 1; j < len(load.Segments); j++ {
			if segmentsOverlap(load.Segments[i], load.Segments[j]) {
				return ErrBadSegment
			}
		}
	}
	return nil
}

func segmentsOverlap(a, b Segment) bool {
	aEnd, aOK := a.End()
	bEnd, bOK := b.End()
	if !aOK || !bOK {
		return true
	}
	return a.Base < bEnd && b.Base < aEnd
}

// Flush orders every buffered event by sequence, folds it into the normalized
// archive, and returns an independent read-only snapshot. Flushing again after
// ingesting more events extends the same archive; prior snapshots are never
// mutated, including by unload events.
func (n *Normalizer) Flush() (*Archive, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	sort.SliceStable(n.pending, func(i, j int) bool {
		return n.pending[i].seq() < n.pending[j].seq()
	})
	if err := n.validatePending(); err != nil {
		return nil, err
	}
	for _, event := range n.pending {
		n.apply(event)
		if event.seq() > n.lastFlush {
			n.lastFlush = event.seq()
		}
	}
	n.pending = n.pending[:0]
	return n.snapshot(), nil
}

// validatePending performs the checks that require the full ordered view, in a
// dry run so that a rejected Flush leaves buffered events available for the
// caller to inspect or correct.
func (n *Normalizer) validatePending() error {
	var scratch []*activeLoad
	// Copy the records rather than sharing pointers, so this dry run cannot
	// mutate the real active-load state seen by apply.
	for _, active := range n.active {
		duplicate := *active
		scratch = append(scratch, &duplicate)
	}
	for _, event := range n.pending {
		switch {
		case event.load != nil:
			copyLoad := *event.load
			copyLoad.Segments = append([]Segment(nil), event.load.Segments...)
			scratch = append(scratch, &activeLoad{load: copyLoad})
		case event.unload != nil:
			target := findActive(scratch, event.unload.Module, event.unload.Digest)
			if target == nil {
				return ErrNoSuchLoad
			}
			target.unloaded = true
			target.unloadSeq = event.unload.Seq
		}
	}
	return nil
}

func findActive(active []*activeLoad, module string, digest Digest) *activeLoad {
	for _, candidate := range active {
		if !candidate.unloaded && candidate.load.Module == module && candidate.load.Digest == digest {
			return candidate
		}
	}
	return nil
}

func (n *Normalizer) apply(event rawEvent) {
	switch {
	case event.load != nil:
		copyLoad := *event.load
		copyLoad.Segments = append([]Segment(nil), event.load.Segments...)
		n.allLoads = append(n.allLoads, copyLoad)
		n.active = append(n.active, &activeLoad{load: copyLoad})
		n.timeline = append(n.timeline, TimelineEntry{
			Kind: EntryLoad,
			Seq:  event.load.Seq,
			Load: &copyLoad,
		})

	case event.unload != nil:
		target := findActive(n.active, event.unload.Module, event.unload.Digest)
		target.unloaded = true
		target.unloadSeq = event.unload.Seq
		n.timeline = append(n.timeline, TimelineEntry{
			Kind:   EntryUnload,
			Seq:    event.unload.Seq,
			Unload: event.unload,
		})

	default:
		n.applySample(event.sample)
	}
}

func (n *Normalizer) applySample(sample *SampleEvent) {
	from, fromGen, fromReason := n.resolve(sample.Seq, sample.From)
	to, toGen, toReason := n.resolve(sample.Seq, sample.To)

	if fromReason != ReasonUnknown || toReason != ReasonUnknown {
		unknown := &UnknownRecord{
			Seq:        sample.Seq,
			Thread:     sample.Thread,
			FromRaw:    sample.From,
			ToRaw:      sample.To,
			FromReason: fromReason,
			ToReason:   toReason,
		}
		n.unknown = append(n.unknown, unknown)
		n.timeline = append(n.timeline, TimelineEntry{
			Kind:    EntryUnknown,
			Seq:     sample.Seq,
			Thread:  sample.Thread,
			FromRaw: sample.From,
			ToRaw:   sample.To,
			Unknown: unknown,
		})
		return
	}

	id := EdgeID{From: from, To: to}
	record, ok := n.edges[id]
	if !ok {
		record = &EdgeRecord{
			Edge:            id,
			FirstSeq:        sample.Seq,
			FromModule:      n.moduleOf(from),
			ToModule:        n.moduleOf(to),
			Threads:         make(map[uint64]uint64),
			FromGenerations: make(map[uint64]struct{}),
			ToGenerations:   make(map[uint64]struct{}),
		}
		n.edges[id] = record
	}
	record.LastSeq = sample.Seq
	record.HitCount++
	record.Threads[sample.Thread]++
	record.FromGenerations[fromGen] = struct{}{}
	record.ToGenerations[toGen] = struct{}{}
	edgeCopy := id
	n.timeline = append(n.timeline, TimelineEntry{
		Kind:    EntryEdge,
		Seq:     sample.Seq,
		Thread:  sample.Thread,
		FromRaw: sample.From,
		ToRaw:   sample.To,
		Edge:    &edgeCopy,
	})
}

// resolve binds raw address at sequence seq to one endpoint. It returns the
// content-addressed endpoint, the generation of the specific load that
// resolved it, and the zero ReasonUnknown when resolution succeeds.
func (n *Normalizer) resolve(seq EventSeq, raw uint64) (EndpointRef, uint64, Reason) {
	var confirmed []*activeLoad
	for _, active := range n.active {
		if active.unloaded && seq >= active.unloadSeq {
			continue
		}
		if segmentOf(active.load.Segments, raw) == -1 {
			continue
		}
		if active.load.ConfirmedAt <= seq {
			confirmed = append(confirmed, active)
		}
	}

	if len(confirmed) == 0 {
		return EndpointRef{}, 0, n.classifyUnconfirmed(seq, raw)
	}
	if len(confirmed) > 1 {
		return EndpointRef{}, 0, ReasonAmbiguous
	}

	active := confirmed[0]
	index := segmentOf(active.load.Segments, raw)
	offset, ok := active.load.Segments[index].Relative(raw)
	if !ok {
		return EndpointRef{}, 0, ReasonOffsetOverflow
	}
	return EndpointRef{Digest: active.load.Digest, Offset: offset}, active.load.Generation, ReasonUnknown
}

// classifyUnconfirmed distinguishes "sample arrived before the covering load
// was confirmed" from "nothing ever maps this address".
func (n *Normalizer) classifyUnconfirmed(seq EventSeq, raw uint64) Reason {
	for index := range n.allLoads {
		load := n.allLoads[index]
		if segmentOf(load.Segments, raw) == -1 {
			continue
		}
		if load.ConfirmedAt > seq {
			return ReasonBeforeLoad
		}
	}
	return ReasonNoMapping
}

func segmentOf(segments []Segment, raw uint64) int {
	for index := range segments {
		if segments[index].Contains(raw) {
			return index
		}
	}
	return -1
}

func (n *Normalizer) moduleOf(endpoint EndpointRef) string {
	for index := range n.allLoads {
		load := n.allLoads[index]
		if load.Digest == endpoint.Digest {
			return load.Module
		}
	}
	return ""
}

func (n *Normalizer) snapshot() *Archive {
	edges := make(map[EdgeID]*EdgeRecord, len(n.edges))
	for id, record := range n.edges {
		threads := make(map[uint64]uint64, len(record.Threads))
		for thread, count := range record.Threads {
			threads[thread] = count
		}
		edges[id] = &EdgeRecord{
			Edge:            record.Edge,
			FirstSeq:        record.FirstSeq,
			LastSeq:         record.LastSeq,
			HitCount:        record.HitCount,
			FromModule:      record.FromModule,
			ToModule:        record.ToModule,
			Threads:         threads,
			FromGenerations: copySet(record.FromGenerations),
			ToGenerations:   copySet(record.ToGenerations),
		}
	}

	unknown := make([]*UnknownRecord, len(n.unknown))
	copy(unknown, n.unknown)

	timeline := make([]TimelineEntry, len(n.timeline))
	copy(timeline, n.timeline)

	return &Archive{
		Edges:          edges,
		Unknown:        unknown,
		Timeline:       timeline,
		FlushedThrough: n.lastFlush,
	}
}

// SortedEdges returns archived edge records ordered by endpoint digest and
// relative offset, for deterministic iteration and assertions.
func (a *Archive) SortedEdges() []*EdgeRecord {
	records := make([]*EdgeRecord, 0, len(a.Edges))
	for _, record := range a.Edges {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		return compareEdge(records[i].Edge, records[j].Edge) < 0
	})
	return records
}

func compareEdge(a, b EdgeID) int {
	if c := compareDigest(a.From.Digest, b.From.Digest); c != 0 {
		return c
	}
	if a.From.Offset < b.From.Offset {
		return -1
	}
	if a.From.Offset > b.From.Offset {
		return 1
	}
	if c := compareDigest(a.To.Digest, b.To.Digest); c != 0 {
		return c
	}
	if a.To.Offset < b.To.Offset {
		return -1
	}
	if a.To.Offset > b.To.Offset {
		return 1
	}
	return 0
}

func compareDigest(a, b Digest) int {
	for index := 0; index < len(a); index++ {
		if a[index] != b[index] {
			if a[index] < b[index] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func copySet(source map[uint64]struct{}) map[uint64]struct{} {
	target := make(map[uint64]struct{}, len(source))
	for value := range source {
		target[value] = struct{}{}
	}
	return target
}

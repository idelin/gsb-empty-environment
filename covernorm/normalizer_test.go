package covernorm

import (
	"sync/atomic"
	"testing"
)

func digestOf(seed byte) Digest {
	var digest Digest
	for index := range digest {
		digest[index] = seed
	}
	return digest
}

var (
	digestA = digestOf(0xA1)
	digestB = digestOf(0xB2)
	digestC = digestOf(0xC3)
	digestD = digestOf(0xD4)
)

func edgeID(fromDigest Digest, fromOffset uint64, toDigest Digest, toOffset uint64) EdgeID {
	return EdgeID{
		From: EndpointRef{Digest: fromDigest, Offset: fromOffset},
		To:   EndpointRef{Digest: toDigest, Offset: toOffset},
	}
}

func mustFlush(t *testing.T, normalizer *Normalizer) *Archive {
	t.Helper()
	archive, err := normalizer.Flush()
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}
	return archive
}

func mustIngest(t *testing.T, normalizer *Normalizer, load *LoadEvent, unload *UnloadEvent, sample *SampleEvent) {
	t.Helper()
	if err := normalizer.Ingest(load, unload, sample); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
}

func assertReason(t *testing.T, record *UnknownRecord, wantFrom, wantTo Reason) {
	t.Helper()
	if record.FromReason != wantFrom || record.ToReason != wantTo {
		t.Fatalf("unknown #%d reasons = (%s,%s), want (%s,%s)",
			record.Seq, record.FromReason, record.ToReason, wantFrom, wantTo)
	}
}

// TestFullTimeline walks one complete ordered timeline exercising reloads at
// the same raw address, overlapping mappings, early samples, unloads and a
// cross-module jump.
func TestFullTimeline(t *testing.T) {
	normalizer := New()

	// #1 alpha gen1 confirmed at #2: 0x1000-0x1100.
	mustIngest(t, normalizer, &LoadEvent{
		Seq: 1, ConfirmedAt: 3, Module: "alpha", Digest: digestA, Generation: 1,
		Segments: []Segment{{Base: 0x1000, Size: 0x100, Offset: 0}},
	}, nil, nil)
	// #2 sample arrives before the load confirms at #3: the raw range is
	// declared but not yet usable.
	mustIngest(t, normalizer, nil, nil, &SampleEvent{Seq: 2, Thread: 7, From: 0x1010, To: 0x1020})
	// #3 confirmed edge inside alpha.
	mustIngest(t, normalizer, nil, nil, &SampleEvent{Seq: 3, Thread: 7, From: 0x1010, To: 0x1020})
	// #4 beta load; #5 cross-module jump alpha -> beta.
	mustIngest(t, normalizer, &LoadEvent{
		Seq: 4, ConfirmedAt: 4, Module: "beta", Digest: digestB, Generation: 1,
		Segments: []Segment{{Base: 0x2000, Size: 0x100, Offset: 0}},
	}, nil, nil)
	mustIngest(t, normalizer, nil, nil, &SampleEvent{Seq: 5, Thread: 9, From: 0x1030, To: 0x2040})
	// #6 gamma overlaps alpha over 0x1080-0x1100.
	mustIngest(t, normalizer, &LoadEvent{
		Seq: 6, ConfirmedAt: 6, Module: "gamma", Digest: digestC, Generation: 3,
		Segments: []Segment{{Base: 0x1080, Size: 0x100, Offset: 0}},
	}, nil, nil)
	// #7 ambiguous source inside the overlap; the target stays unique.
	mustIngest(t, normalizer, nil, nil, &SampleEvent{Seq: 7, Thread: 9, From: 0x1090, To: 0x2040})
	// #8 alpha unloaded; 0x1090 now resolves uniquely to gamma.
	mustIngest(t, normalizer, nil, &UnloadEvent{Seq: 8, Module: "alpha", Digest: digestA}, nil)
	mustIngest(t, normalizer, nil, nil, &SampleEvent{Seq: 9, Thread: 3, From: 0x1090, To: 0x10a0})
	// #10 nothing maps 0x4000.
	mustIngest(t, normalizer, nil, nil, &SampleEvent{Seq: 10, Thread: 3, From: 0x1090, To: 0x4000})
	// #11 a different module takes over the exact old alpha base address.
	mustIngest(t, normalizer, &LoadEvent{
		Seq: 11, ConfirmedAt: 11, Module: "delta", Digest: digestD, Generation: 1,
		Segments: []Segment{{Base: 0x1000, Size: 0x80, Offset: 0x1000}},
	}, nil, nil)
	// #12 same raw addresses as the #3 edge but a different digest and offset
	// base: a distinct edge, never merged with the old alpha edge.
	mustIngest(t, normalizer, nil, nil, &SampleEvent{Seq: 12, Thread: 1, From: 0x1010, To: 0x1020})

	archive := mustFlush(t, normalizer)

	if archive.FlushedThrough != 12 {
		t.Fatalf("FlushedThrough = %d, want 12", archive.FlushedThrough)
	}
	if len(archive.Timeline) != 12 {
		t.Fatalf("timeline length = %d, want 12", len(archive.Timeline))
	}
	if len(archive.Unknown) != 3 {
		t.Fatalf("unknown records = %d, want 3", len(archive.Unknown))
	}
	assertReason(t, archive.Unknown[0], ReasonBeforeLoad, ReasonBeforeLoad)
	assertReason(t, archive.Unknown[1], ReasonAmbiguous, ReasonUnknown)
	assertReason(t, archive.Unknown[2], ReasonUnknown, ReasonNoMapping)

	// #3 alpha edge.
	alphaEdge := archive.Edges[edgeID(digestA, 0x10, digestA, 0x20)]
	if alphaEdge == nil {
		t.Fatal("alpha edge missing")
	}
	if alphaEdge.HitCount != 1 || alphaEdge.FirstSeq != 3 || alphaEdge.LastSeq != 3 {
		t.Fatalf("alpha edge stats = %+v", alphaEdge)
	}
	if alphaEdge.FromModule != "alpha" || alphaEdge.ToModule != "alpha" {
		t.Fatalf("alpha edge modules = %q,%q", alphaEdge.FromModule, alphaEdge.ToModule)
	}
	if alphaEdge.Threads[7] != 1 {
		t.Fatalf("alpha edge threads = %v", alphaEdge.Threads)
	}

	// #5 cross-module edge keeps both module digests on one identity.
	crossEdge := archive.Edges[edgeID(digestA, 0x30, digestB, 0x40)]
	if crossEdge == nil || crossEdge.FromModule != "alpha" || crossEdge.ToModule != "beta" {
		t.Fatalf("cross-module edge = %+v", crossEdge)
	}

	// #9 gamma edge after the alpha unload removes the overlap.
	gammaEdge := archive.Edges[edgeID(digestC, 0x10, digestC, 0x20)]
	if gammaEdge == nil {
		t.Fatal("gamma edge missing")
	}
	if _, ok := gammaEdge.FromGenerations[3]; !ok {
		t.Fatalf("gamma generations = %v, want gen 3", gammaEdge.FromGenerations)
	}

	// #12 same raw address, different module: a separate identity.
	deltaEdge := archive.Edges[edgeID(digestD, 0x1010, digestD, 0x1020)]
	if deltaEdge == nil || deltaEdge.FromModule != "delta" {
		t.Fatalf("delta edge = %+v", deltaEdge)
	}

	if len(archive.Edges) != 4 {
		t.Fatalf("edge count = %d, want 4", len(archive.Edges))
	}

	wantKinds := []EntryKind{
		EntryLoad, EntryUnknown, EntryEdge, EntryLoad, EntryEdge,
		EntryLoad, EntryUnknown, EntryUnload, EntryEdge, EntryUnknown,
		EntryLoad, EntryEdge,
	}
	for index, want := range wantKinds {
		if archive.Timeline[index].Kind != want {
			t.Fatalf("timeline[%d] kind = %s, want %s", index, archive.Timeline[index].Kind, want)
		}
	}

	lines := archive.Explain()
	if len(lines) != 12 || lines[11] == "" {
		t.Fatalf("Explain produced %d lines", len(lines))
	}
}

// TestASLRSameContentReload verifies that identical code reloaded at a new
// randomized base with the same digest merges into one edge, while generation
// provenance retains both load generations.
func TestASLRSameContentReload(t *testing.T) {
	normalizer := New()
	mustIngest(t, normalizer, &LoadEvent{
		Seq: 1, ConfirmedAt: 1, Module: "lib", Digest: digestA, Generation: 1,
		Segments: []Segment{{Base: 0x1000, Size: 0x200, Offset: 0}},
	}, nil, nil)
	mustIngest(t, normalizer, nil, nil, &SampleEvent{Seq: 2, Thread: 1, From: 0x1040, To: 0x1080})
	mustIngest(t, normalizer, nil, &UnloadEvent{Seq: 3, Module: "lib", Digest: digestA}, nil)
	mustIngest(t, normalizer, &LoadEvent{
		Seq: 4, ConfirmedAt: 4, Module: "lib", Digest: digestA, Generation: 2,
		Segments: []Segment{{Base: 0x9000, Size: 0x200, Offset: 0}},
	}, nil, nil)
	mustIngest(t, normalizer, nil, nil, &SampleEvent{Seq: 5, Thread: 2, From: 0x9040, To: 0x9080})

	archive := mustFlush(t, normalizer)
	if len(archive.Edges) != 1 {
		t.Fatalf("edge count = %d, want 1 merged edge", len(archive.Edges))
	}
	record := archive.Edges[edgeID(digestA, 0x40, digestA, 0x80)]
	if record == nil {
		t.Fatal("content-addressed edge missing")
	}
	if record.HitCount != 2 || record.FirstSeq != 2 || record.LastSeq != 5 {
		t.Fatalf("merged edge stats = %+v", record)
	}
	if record.Threads[1] != 1 || record.Threads[2] != 1 {
		t.Fatalf("merged edge threads = %v", record.Threads)
	}
	if len(record.FromGenerations) != 2 {
		t.Fatalf("generations = %v, want gen 1 and gen 2", record.FromGenerations)
	}
}

// TestOutOfOrderIngestion feeds one timeline in shuffled order and requires
// the same archive as ordered ingestion.
func TestOutOfOrderIngestion(t *testing.T) {
	load1 := &LoadEvent{
		Seq: 1, ConfirmedAt: 3, Module: "alpha", Digest: digestA, Generation: 1,
		Segments: []Segment{{Base: 0x3000, Size: 0x100, Offset: 0}},
	}
	early := &SampleEvent{Seq: 2, Thread: 4, From: 0x3010, To: 0x3020}
	ok := &SampleEvent{Seq: 4, Thread: 4, From: 0x3010, To: 0x3020}
	unload := &UnloadEvent{Seq: 5, Module: "alpha", Digest: digestA}
	gone := &SampleEvent{Seq: 6, Thread: 4, From: 0x3010, To: 0x3020}

	ordered := New()
	for _, event := range []any{load1, early, ok, unload, gone} {
		ingestAny(t, ordered, event)
	}
	shuffled := New()
	for _, event := range []any{gone, unload, ok, early, load1} {
		ingestAny(t, shuffled, event)
	}

	orderedArchive := mustFlush(t, ordered)
	shuffledArchive := mustFlush(t, shuffled)

	if len(shuffledArchive.Edges) != len(orderedArchive.Edges) {
		t.Fatalf("shuffled edge count = %d, want %d", len(shuffledArchive.Edges), len(orderedArchive.Edges))
	}
	for id, record := range orderedArchive.Edges {
		other, exists := shuffledArchive.Edges[id]
		if !exists {
			t.Fatalf("shuffled archive missing edge %s", id)
		}
		if record.HitCount != other.HitCount || record.FirstSeq != other.FirstSeq || record.LastSeq != other.LastSeq {
			t.Fatalf("edge %s stats diverged: %+v vs %+v", id, record, other)
		}
	}
	if len(shuffledArchive.Unknown) != 2 {
		t.Fatalf("unknown count = %d, want 2", len(shuffledArchive.Unknown))
	}
	if shuffledArchive.Unknown[0].FromReason != ReasonBeforeLoad {
		t.Fatalf("early sample reason = %s, want before-load", shuffledArchive.Unknown[0].FromReason)
	}
	if shuffledArchive.Unknown[1].FromReason != ReasonNoMapping {
		t.Fatalf("post-unload sample reason = %s, want no-mapping", shuffledArchive.Unknown[1].FromReason)
	}
	for index := range orderedArchive.Timeline {
		if shuffledArchive.Timeline[index].Kind != orderedArchive.Timeline[index].Kind ||
			shuffledArchive.Timeline[index].Seq != orderedArchive.Timeline[index].Seq {
			t.Fatalf("timeline diverges at index %d", index)
		}
	}
}

func ingestAny(t *testing.T, normalizer *Normalizer, event any) {
	t.Helper()
	var err error
	switch typed := event.(type) {
	case *LoadEvent:
		err = normalizer.Ingest(typed, nil, nil)
	case *UnloadEvent:
		err = normalizer.Ingest(nil, typed, nil)
	case *SampleEvent:
		err = normalizer.Ingest(nil, nil, typed)
	default:
		t.Fatalf("unknown event type %T", event)
	}
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
}

// TestUnloadKeepsArchiveImmutable checks that unloading and later flushes
// never rewrite archived edges or earlier snapshots.
func TestUnloadKeepsArchiveImmutable(t *testing.T) {
	normalizer := New()
	mustIngest(t, normalizer, &LoadEvent{
		Seq: 1, ConfirmedAt: 1, Module: "alpha", Digest: digestA, Generation: 1,
		Segments: []Segment{{Base: 0x1000, Size: 0x100, Offset: 0}},
	}, nil, nil)
	mustIngest(t, normalizer, nil, nil, &SampleEvent{Seq: 2, Thread: 1, From: 0x1010, To: 0x1020})
	first := mustFlush(t, normalizer)

	mustIngest(t, normalizer, nil, &UnloadEvent{Seq: 3, Module: "alpha", Digest: digestA}, nil)
	mustIngest(t, normalizer, nil, nil, &SampleEvent{Seq: 4, Thread: 1, From: 0x1010, To: 0x1020})
	second := mustFlush(t, normalizer)

	archived := first.Edges[edgeID(digestA, 0x10, digestA, 0x20)]
	if archived == nil || archived.HitCount != 1 {
		t.Fatalf("first snapshot edge changed: %+v", archived)
	}
	if len(first.Timeline) != 2 || first.FlushedThrough != 2 {
		t.Fatalf("first snapshot timeline changed: len=%d through=%d", len(first.Timeline), first.FlushedThrough)
	}
	if len(second.Unknown) != 1 || second.Unknown[0].FromReason != ReasonNoMapping {
		t.Fatalf("post-unload sample not classified unknown: %+v", second.Unknown)
	}
	if second.Edges[edgeID(digestA, 0x10, digestA, 0x20)].HitCount != 1 {
		t.Fatal("unload must not delete or alter the archived edge")
	}
}

func TestRejectedEvents(t *testing.T) {
	normalizer := New()
	load := &LoadEvent{
		Seq: 1, ConfirmedAt: 1, Module: "alpha", Digest: digestA, Generation: 1,
		Segments: []Segment{{Base: 0x1000, Size: 0x100, Offset: 0}},
	}
	if err := normalizer.Ingest(load, nil, nil); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if err := normalizer.Ingest(&LoadEvent{
		Seq: 1, ConfirmedAt: 1, Module: "beta", Digest: digestB,
		Segments: []Segment{{Base: 0x2000, Size: 0x10, Offset: 0}},
	}, nil, nil); err != ErrDuplicateSeq {
		t.Fatalf("duplicate seq err = %v, want ErrDuplicateSeq", err)
	}
	if _, err := normalizer.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := normalizer.Ingest(nil, nil, &SampleEvent{Seq: 1, From: 1, To: 2}); err != ErrLateEvent {
		t.Fatalf("late event err = %v, want ErrLateEvent", err)
	}

	if err := normalizer.Ingest(nil, &UnloadEvent{Seq: 9, Module: "ghost", Digest: digestB}, nil); err != nil {
		t.Fatalf("Ingest unload: %v", err)
	}
	if _, err := normalizer.Flush(); err != ErrNoSuchLoad {
		t.Fatalf("Flush err = %v, want ErrNoSuchLoad", err)
	}

	bad := New()
	if err := bad.Ingest(&LoadEvent{Seq: 1, ConfirmedAt: 1, Module: "x", Segments: []Segment{{Base: 1, Size: 1}}}, nil, nil); err != ErrEmptyDigest {
		t.Fatalf("empty digest err = %v, want ErrEmptyDigest", err)
	}
	if err := bad.Ingest(&LoadEvent{
		Seq: 2, ConfirmedAt: 2, Module: "x", Digest: digestA,
		Segments: []Segment{{Base: 0x100, Size: 0x100, Offset: 0}, {Base: 0x180, Size: 0x100, Offset: 0}},
	}, nil, nil); err != ErrBadSegment {
		t.Fatalf("overlapping segment err = %v, want ErrBadSegment", err)
	}
	if err := bad.Ingest(nil, nil, nil); err != ErrBadEvent {
		t.Fatalf("empty event err = %v, want ErrBadEvent", err)
	}
}

func TestSegmentRelativeAndOverflow(t *testing.T) {
	segment := Segment{Base: 0x1000, Size: 0x100, Offset: 0x4000}
	if !segment.Contains(0x1040) || segment.Contains(0x1100) {
		t.Fatal("Contains boundary handling wrong")
	}
	offset, ok := segment.Relative(0x1040)
	if !ok || offset != 0x4040 {
		t.Fatalf("Relative = %#x,%v", offset, ok)
	}

	overflow := Segment{Base: 0x10, Size: ^uint64(0), Offset: 0}
	if _, ok := overflow.End(); ok {
		t.Fatal("overflowing segment end should be rejected")
	}
	if overflow.Contains(0x20) {
		t.Fatal("overflowing segment must not claim addresses")
	}
}

func TestParseDigest(t *testing.T) {
	digest, err := ParseDigest("0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	if err != nil {
		t.Fatalf("ParseDigest: %v", err)
	}
	if digest[0] != 0x01 || digest[31] != 0x20 {
		t.Fatalf("parsed digest bytes wrong: %x", digest)
	}
	if digest.Hex()[16:] != "090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20" {
		t.Fatalf("Hex round trip failed: %s", digest.Hex())
	}
	if _, err := ParseDigest("abc"); err == nil {
		t.Fatal("short digest must fail")
	}
}

// TestConcurrentIngest exercises the mutex-guarded embedding surface from
// multiple producer goroutines. Producers draw sequence numbers from one
// atomic counter so every sequence stays above the flush watermark; a single
// flusher drains the archive afterwards.
func TestConcurrentIngest(t *testing.T) {
	normalizer := New()
	if err := normalizer.Ingest(&LoadEvent{
		Seq: 1, ConfirmedAt: 1, Module: "shared", Digest: digestA, Generation: 1,
		Segments: []Segment{
			{Base: 0x5000, Size: 0x100, Offset: 0},
			{Base: 0x6000, Size: 0x100, Offset: 0x1000},
		},
	}, nil, nil); err != nil {
		t.Fatalf("Ingest load: %v", err)
	}

	const producers = 8
	const perProducer = 50
	var nextSeq atomic.Uint64
	nextSeq.Store(2)
	var producerDone [producers]chan struct{}
	for producer := range producerDone {
		producerDone[producer] = make(chan struct{})
		go func(producer int) {
			defer close(producerDone[producer])
			for index := range perProducer {
				seq := EventSeq(nextSeq.Add(1) - 1)
				err := normalizer.Ingest(nil, nil, &SampleEvent{
					Seq:    seq,
					Thread: uint64(producer),
					From:   uint64(0x5000 + producer),
					To:     uint64(0x6000 + index),
				})
				if err != nil {
					t.Errorf("Ingest seq %d: %v", seq, err)
					return
				}
			}
		}(producer)
	}
	for _, waiter := range producerDone {
		<-waiter
	}

	archive := mustFlush(t, normalizer)
	totalHits := uint64(0)
	for _, record := range archive.Edges {
		totalHits += record.HitCount
	}
	if totalHits != producers*perProducer {
		t.Fatalf("total archived hits = %d, want %d", totalHits, producers*perProducer)
	}
	wantThrough := EventSeq(producers*perProducer + 1)
	if archive.FlushedThrough != wantThrough {
		t.Fatalf("FlushedThrough = %d, want %d", archive.FlushedThrough, wantThrough)
	}
}

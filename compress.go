package eventlog

// EventSummary is the replayable digest of one original event. It carries
// exactly what Validate needs to re-apply the event.
type EventSummary struct {
	Kind OpKind
	Arg  int
	Tag  string // only meaningful for OpObserve
}

// SummarizeEvent strips an original event down to its replayable summary.
func SummarizeEvent(ev Event) EventSummary {
	return EventSummary{Kind: ev.Kind, Arg: ev.Arg, Tag: ev.Tag}
}

// Event expands the summary back into an applicable event.
func (s EventSummary) Event() Event {
	return Event{Kind: s.Kind, Arg: s.Arg, Tag: s.Tag}
}

// Segment is the compressed form of the events between two observation
// points. Merged is the compact executable form; Originals lists the
// replayable summaries of every original event the segment stands for, in
// their original order. Except in the final segment, the last original is
// the observe event that closed the segment.
type Segment struct {
	Merged    []Event
	Originals []EventSummary
}

// CompressedLog is a lossless, verifiable compression of an event log.
// Segments has exactly len(Observations)+1 entries: segment i holds the
// events between observation i-1 and observation i, observation i included.
type CompressedLog struct {
	Initial      Summary
	Final        Summary
	Observations []Observation
	Segments     []Segment
}

// Compress replays events from the given initial state and produces the
// compressed log, capturing the initial/final summaries and every public
// observation point.
func Compress(initial int, events []Event) CompressedLog {
	m := NewMachine(initial)
	for _, ev := range events {
		m.Apply(ev)
	}
	cl := CompressedLog{
		Initial:      NewSummary(initial, 0, 0),
		Final:        m.Summary(),
		Observations: m.Observations(),
	}
	var seg Segment
	flush := func() {
		cl.Segments = append(cl.Segments, seg)
		seg = Segment{}
	}
	for _, ev := range events {
		if ev.Kind == OpObserve {
			seg.Originals = append(seg.Originals, SummarizeEvent(ev))
			flush()
			continue
		}
		seg.Originals = append(seg.Originals, SummarizeEvent(ev))
		appendMerged(&seg, ev)
	}
	flush()
	return cl
}

// appendMerged folds ev into the segment's compressed stream, merging only
// maximal runs of the same commutative operation. Any change of operation
// kind — and any OpSet — closes the current run, because such operations
// do not commute with their neighbours and may never be merged by type
// counts alone.
func appendMerged(seg *Segment, ev Event) {
	if n := len(seg.Merged); n > 0 && tryMerge(&seg.Merged[n-1], ev) {
		return
	}
	seg.Merged = append(seg.Merged, Event{Kind: ev.Kind, Arg: ev.Arg, Count: 1})
}

// tryMerge folds ev into the previous compressed event when both are the
// same commutative arithmetic operation. It reports whether the fold
// happened.
func tryMerge(last *Event, ev Event) bool {
	if last.Kind != ev.Kind {
		return false
	}
	switch ev.Kind {
	case OpAdd:
		last.Arg += ev.Arg
	case OpMul:
		last.Arg *= ev.Arg
	default:
		// OpSet and OpObserve never merge.
		return false
	}
	last.Count += ev.count()
	return true
}

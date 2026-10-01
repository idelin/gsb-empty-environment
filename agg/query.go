package agg

import (
	"fmt"
	"math"
	"sort"
)

// BucketResult is the per-bucket part of a query result. A bucket with no
// events has Present == false (a gap), which is distinct from a bucket whose
// values sum to zero.
type BucketResult struct {
	Index int64
	Start int64
	End   int64
	Summary
}

// Result is the aggregate over [Start, End) at Version, plus per-bucket
// breakdown. Max is the peak evidence; Mean() is Sum/Count.
type Result struct {
	Version int
	Start   int64
	End     int64
	Summary
	Buckets []BucketResult
}

// Query aggregates [start, end) at the latest version.
func (s *Store) Query(start, end int64) Result {
	return s.QueryAt(start, end, s.version)
}

// QueryAt aggregates [start, end) as of the given version, so old snapshots
// remain usable after later corrections. The query is cross-layer: buckets
// fully inside the range are served from bucket summaries, while partial
// (edge) buckets are served from event details, so nothing is counted twice.
func (s *Store) QueryAt(start, end int64, version int) Result {
	if version > s.version {
		version = s.version
	}
	if version < 0 {
		version = 0
	}
	res := Result{Version: version, Start: start, End: end}
	if end <= start {
		return res
	}
	first := s.bucketIndex(start)
	last := s.bucketIndex(end - 1)
	for idx := first; idx <= last; idx++ {
		bs := idx * s.width
		be := bs + s.width
		lo := max64(start, bs)
		hi := min64(end, be)
		var sum Summary
		if version == s.version && lo == bs && hi == be {
			// Internal bucket at the latest version: summary layer.
			if b := s.buckets[idx]; b != nil {
				sum = b.summary()
			}
		} else if version == s.version {
			// Edge bucket at the latest version: detail layer.
			if b := s.buckets[idx]; b != nil {
				sum = summarizeEvents(eventsInRange(b.events, lo, hi))
			}
		} else {
			// Historical snapshot: replay visible details for the bucket.
			sum = summarizeEvents(s.bucketDetailsAt(idx, lo, hi, version))
		}
		res.Buckets = append(res.Buckets, BucketResult{Index: idx, Start: bs, End: be, Summary: sum})
		res.Summary = combine(res.Summary, sum)
	}
	return res
}

// bucketDetailsAt returns the events of bucket idx visible at version with
// lo <= TS < hi, scanned from the versioned history.
func (s *Store) bucketDetailsAt(idx, lo, hi int64, version int) []Event {
	var out []Event
	for _, ve := range s.history {
		if ve.addedAt > version {
			continue
		}
		if ve.removedAt != 0 && ve.removedAt <= version {
			continue
		}
		if ve.TS < lo || ve.TS >= hi {
			continue
		}
		if s.bucketIndex(ve.TS) != idx {
			continue
		}
		out = append(out, ve.Event)
	}
	return out
}

// Details returns the events visible in [start, end) at the given version,
// sorted by (TS, ID). Pass a version <= 0 for an empty result and a version
// greater than the current one for the latest state.
func (s *Store) Details(start, end int64, version int) []Event {
	if version > s.version {
		version = s.version
	}
	var out []Event
	if end <= start {
		return out
	}
	for _, ve := range s.history {
		if ve.addedAt > version {
			continue
		}
		if ve.removedAt != 0 && ve.removedAt <= version {
			continue
		}
		if ve.TS < start || ve.TS >= end {
			continue
		}
		out = append(out, ve.Event)
	}
	sort.Slice(out, func(i, j int) bool { return lessEvent(out[i], out[j]) })
	return out
}

// Reference aggregates events in [start, end) by fully sorting them. It is
// the ground-truth counterpart used to cross-check the layered query on
// small streams.
func Reference(events []Event, start, end int64) Summary {
	filtered := make([]Event, 0, len(events))
	for _, e := range events {
		if e.TS >= start && e.TS < end {
			filtered = append(filtered, e)
		}
	}
	sort.Slice(filtered, func(i, j int) bool { return lessEvent(filtered[i], filtered[j]) })
	return summarizeEvents(filtered)
}

// Validate recomputes the result from event details only and checks that the
// layered query agrees: edge details plus internal summaries must neither
// double count nor miss anything.
func (s *Store) Validate(res Result) error {
	want := Reference(s.Details(res.Start, res.End, res.Version), res.Start, res.End)
	if err := compareSummary(want, res.Summary); err != nil {
		return fmt.Errorf("agg: layered result mismatch: %v", err)
	}
	total := 0
	for _, b := range res.Buckets {
		total += b.Count
	}
	if total != res.Count {
		return fmt.Errorf("agg: bucket counts %d != total %d (double count or gap)", total, res.Count)
	}
	return nil
}

func compareSummary(want, got Summary) error {
	if want.Present != got.Present {
		return fmt.Errorf("present: want %v got %v", want.Present, got.Present)
	}
	if !want.Present {
		return nil
	}
	if want.Count != got.Count {
		return fmt.Errorf("count: want %d got %d", want.Count, got.Count)
	}
	if math.Abs(want.Sum-got.Sum) > 1e-9*(1+math.Abs(want.Sum)) {
		return fmt.Errorf("sum: want %v got %v", want.Sum, got.Sum)
	}
	if want.Min != got.Min {
		return fmt.Errorf("min: want %+v got %+v", want.Min, got.Min)
	}
	if want.Max != got.Max {
		return fmt.Errorf("max: want %+v got %+v", want.Max, got.Max)
	}
	if want.First != got.First {
		return fmt.Errorf("first: want %+v got %+v", want.First, got.First)
	}
	if want.Last != got.Last {
		return fmt.Errorf("last: want %+v got %+v", want.Last, got.Last)
	}
	return nil
}

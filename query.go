package telemetry

// BucketView is the per-bucket breakdown of a query. Present == false
// means the bucket is a gap (no events at all), which is deliberately
// distinct from a present bucket whose values happen to be 0.
type BucketView struct {
	Bucket  int64
	Start   int64 // bucket lower bound (inclusive)
	Present bool
	Partial bool // edge bucket: computed from raw details, not a summary
	Summary BucketSummary
}

// Result is the aggregate over a queried time range.
type Result struct {
	Start   int64
	End     int64
	Count   int
	Sum     float64
	Min     Evidence
	Max     Evidence
	First   Evidence
	Last    Evidence
	Buckets []BucketView
}

// Mean is sum/count over the whole range — never an average of per-bucket
// averages. ok == false when the range holds no events.
func (r Result) Mean() (mean float64, ok bool) {
	if r.Count == 0 {
		return 0, false
	}
	return r.Sum / float64(r.Count), true
}

// Query aggregates [start, end) at the current version. Buckets fully
// covered by the range are served from maintained summaries; the two edge
// buckets are computed from raw details, so nothing is counted twice.
func (s *Store) Query(start, end int64) Result {
	res := Result{Start: start, End: end}
	if end <= start {
		return res
	}
	var agg aggregator
	lo, hi := s.bucketOf(start), s.bucketOf(end-1)
	for b := lo; b <= hi; b++ {
		bs := b * s.width
		view := BucketView{Bucket: b, Start: bs}
		if bs >= start && bs+s.width <= end {
			if sum, ok := s.summaries[b]; ok {
				view.Present = true
				view.Summary = *sum
				agg.mergeSummary(*sum)
			}
		} else {
			view.Partial = true
			var part BucketSummary
			for _, se := range sortedEvents(s.byBucket[b]) {
				if se.TS >= start && se.TS < end {
					part.add(se.Event)
				}
			}
			if part.Count > 0 {
				view.Present = true
				view.Summary = part
				agg.mergeSummary(part)
			}
		}
		res.Buckets = append(res.Buckets, view)
	}
	res.Count, res.Sum = agg.count, agg.sum
	res.Min, res.Max, res.First, res.Last = agg.min, agg.max, agg.first, agg.last
	return res
}

// QueryAsOf aggregates [start, end) as of the given version, keeping old
// snapshots usable after later corrections and deletions. v <= 0 or v
// beyond the current version means "latest".
func (s *Store) QueryAsOf(start, end int64, v int) Result {
	if v <= 0 || v > s.version {
		v = s.version
	}
	return s.resultFromEvents(start, end, s.eventsAsOf(v))
}

// resultFromEvents builds a Result purely from raw events. It is used for
// historical queries and doubles as the reference path in tests.
func (s *Store) resultFromEvents(start, end int64, events []Event) Result {
	res := Result{Start: start, End: end}
	if end <= start {
		return res
	}
	byBucket := map[int64]*BucketSummary{}
	for _, e := range events {
		if e.TS < start || e.TS >= end {
			continue
		}
		b := s.bucketOf(e.TS)
		sum := byBucket[b]
		if sum == nil {
			sum = &BucketSummary{}
			byBucket[b] = sum
		}
		sum.add(e)
	}
	var agg aggregator
	lo, hi := s.bucketOf(start), s.bucketOf(end-1)
	for b := lo; b <= hi; b++ {
		bs := b * s.width
		view := BucketView{Bucket: b, Start: bs}
		if bs < start || bs+s.width > end {
			view.Partial = true
		}
		if sum, ok := byBucket[b]; ok {
			view.Present = true
			view.Summary = *sum
			agg.mergeSummary(*sum)
		}
		res.Buckets = append(res.Buckets, view)
	}
	res.Count, res.Sum = agg.count, agg.sum
	res.Min, res.Max, res.First, res.Last = agg.min, agg.max, agg.first, agg.last
	return res
}

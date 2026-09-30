package telemetry

// BucketSummary is the maintained aggregate for one fixed-width bucket.
// Count == 0 is never stored: an empty bucket is simply absent, which is
// how a gap is distinguished from a bucket full of zero-valued events.
type BucketSummary struct {
	Count int
	Sum   float64
	Min   Evidence
	Max   Evidence
	First Evidence // earliest by (TS, ID)
	Last  Evidence // latest by (TS, ID)
}

func (b *BucketSummary) add(e Event) {
	ev := Evidence{ID: e.ID, TS: e.TS, Value: e.Value}
	b.Sum += e.Value
	b.Count++
	if b.Count == 1 {
		b.Min, b.Max, b.First, b.Last = ev, ev, ev, ev
		return
	}
	if lessValue(ev, b.Min) {
		b.Min = ev
	}
	if lessValue(b.Max, ev) {
		b.Max = ev
	}
	if lessTime(ev, b.First) {
		b.First = ev
	}
	if lessTime(b.Last, ev) {
		b.Last = ev
	}
}

// aggregator merges whole bucket summaries (internal buckets) and single
// events (edge details) without double counting: each event reaches it
// exactly once by construction of the query layer.
type aggregator struct {
	count int
	sum   float64
	min   Evidence
	max   Evidence
	first Evidence
	last  Evidence
}

func (a *aggregator) mergeSummary(s BucketSummary) {
	if s.Count == 0 {
		return
	}
	a.sum += s.Sum
	a.count += s.Count
	if a.count == s.Count {
		a.min, a.max, a.first, a.last = s.Min, s.Max, s.First, s.Last
		return
	}
	if lessValue(s.Min, a.min) {
		a.min = s.Min
	}
	if lessValue(a.max, s.Max) {
		a.max = s.Max
	}
	if lessTime(s.First, a.first) {
		a.first = s.First
	}
	if lessTime(a.last, s.Last) {
		a.last = s.Last
	}
}

func (a *aggregator) addEvent(e Event) {
	var s BucketSummary
	s.add(e)
	a.mergeSummary(s)
}

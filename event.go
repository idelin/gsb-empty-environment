package telemetry

// Event is a single telemetry sample. ID is the dedup key: overlapping
// batches that carry the same ID are ingested only once.
type Event struct {
	ID    string
	TS    int64 // event time (e.g. unix seconds or millis)
	Value float64
}

// Evidence points back to the exact event behind an aggregate extreme,
// so a query can prove where a min/max/first/last came from.
type Evidence struct {
	ID    string
	TS    int64
	Value float64
}

// lessValue orders by value, then event time, then ID so evidence
// selection is fully deterministic.
func lessValue(a, b Evidence) bool {
	if a.Value != b.Value {
		return a.Value < b.Value
	}
	return lessTime(a, b)
}

// lessTime orders by event time with ID as the tie-breaker. First/last
// use this ordering.
func lessTime(a, b Evidence) bool {
	if a.TS != b.TS {
		return a.TS < b.TS
	}
	return a.ID < b.ID
}

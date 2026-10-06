package histogram

// Recorder accumulates samples in batches against a fixed boundary set.
// Batches can be undone; the histogram is always recomputed from the
// raw samples of the remaining batches, so an undo is exact rather than
// an approximate subtraction of cumulative counts.
type Recorder struct {
	boundaries []float64
	batches    map[int][]float64
	nextID     int
}

// NewRecorder creates a Recorder with the given boundary set.
func NewRecorder(boundaries []float64) *Recorder {
	return &Recorder{
		boundaries: normalize(boundaries),
		batches:    make(map[int][]float64),
	}
}

// RecordBatch stores a batch of raw samples and returns its ID.
func (r *Recorder) RecordBatch(samples []float64) int {
	id := r.nextID
	r.nextID++
	cp := make([]float64, len(samples))
	copy(cp, samples)
	r.batches[id] = cp
	return id
}

// Undo removes a batch. It reports whether the batch existed.
func (r *Recorder) Undo(id int) bool {
	if _, ok := r.batches[id]; !ok {
		return false
	}
	delete(r.batches, id)
	return true
}

// Histogram rebuilds the cumulative histogram from the raw samples of
// all remaining batches.
func (r *Recorder) Histogram() *Histogram {
	var samples []float64
	for _, batch := range r.batches {
		samples = append(samples, batch...)
	}
	return New(r.boundaries, samples)
}

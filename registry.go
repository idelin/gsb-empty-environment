package metrics

// Registry tracks raw samples per batch so that undoing a batch allows the
// histogram to be rebuilt exactly from the remaining raw samples.
type Registry struct {
	bounds  []float64
	batches map[string][]float64
}

// NewRegistry creates a Registry that builds histograms over the given
// bounds. Bounds are normalized (sorted, deduplicated).
func NewRegistry(bounds []float64) *Registry {
	return &Registry{bounds: normalizeBounds(bounds), batches: make(map[string][]float64)}
}

// Bounds returns the registry's normalized boundary set.
func (r *Registry) Bounds() []float64 {
	out := make([]float64, len(r.bounds))
	copy(out, r.bounds)
	return out
}

// AddBatch records a batch of raw samples under id. Samples are copied.
func (r *Registry) AddBatch(id string, samples []float64) error {
	if _, ok := r.batches[id]; ok {
		return errBatchExists
	}
	s := make([]float64, len(samples))
	copy(s, samples)
	r.batches[id] = s
	return nil
}

// UndoBatch removes the batch with the given id. It reports whether the
// batch existed.
func (r *Registry) UndoBatch(id string) bool {
	if _, ok := r.batches[id]; !ok {
		return false
	}
	delete(r.batches, id)
	return true
}

// Samples returns the concatenation of the raw samples of all live batches.
func (r *Registry) Samples() []float64 {
	var out []float64
	for _, s := range r.batches {
		out = append(out, s...)
	}
	return out
}

// Histogram rebuilds the cumulative histogram from the raw samples of all
// live batches. Undoing a batch therefore yields exactly the histogram that
// the remaining raw samples imply.
func (r *Registry) Histogram() *Histogram {
	return NewFromSamples(r.bounds, r.Samples())
}

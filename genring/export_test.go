package genring

// SetSequenceCeiling lowers the maximum per-epoch sequence so tests can
// exercise exhaustion and rollover deterministically.
func (r *Ring) SetSequenceCeiling(ceiling uint32) {
	r.seqCeiling = ceiling
}

// SequenceCeiling reports the current per-epoch sequence ceiling.
func (r *Ring) SequenceCeiling() uint32 {
	return r.seqCeiling
}

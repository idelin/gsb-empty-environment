package interp

// Status describes the state of a sample stream snapshot.
type Status int

const (
	// Insufficient means there are no active samples, so no polynomial can
	// be determined.
	Insufficient Status = iota
	// Consistent means all active samples agree on a single lowest-degree
	// polynomial.
	Consistent
	// Contradictory means two active samples share an x-coordinate with
	// different y values, so no interpolating polynomial exists.
	Contradictory
)

func (s Status) String() string {
	switch s {
	case Insufficient:
		return "insufficient"
	case Consistent:
		return "consistent"
	case Contradictory:
		return "contradictory"
	}
	return "unknown"
}

type revisionedPoint struct {
	rev uint64
	x   int64
	y   int64
}

// Stream consumes a sequence of samples identified by a point id and a
// monotonically increasing revision. Applying a newer revision for an id
// revokes that id's previous contribution; stale revisions are ignored.
type Stream struct {
	f      Field
	points map[string]revisionedPoint
}

// NewStream returns an empty Stream over field f.
func NewStream(f Field) *Stream {
	return &Stream{f: f, points: make(map[string]revisionedPoint)}
}

// Apply records sample (x, y) as revision rev of point id. If id already has
// a revision >= rev the update is stale and ignored; otherwise the old
// contribution of id (if any) is revoked and replaced.
func (s *Stream) Apply(id string, rev uint64, x, y int64) {
	if cur, ok := s.points[id]; ok && cur.rev >= rev {
		return
	}
	s.points[id] = revisionedPoint{rev: rev, x: x, y: y}
}

// Snapshot returns an immutable view of the stream's current state. Later
// Apply calls never affect an already returned Snapshot.
func (s *Stream) Snapshot() Snapshot {
	xs := make([]int64, 0, len(s.points))
	ys := make([]int64, 0, len(s.points))
	for _, pt := range s.points {
		xs = append(xs, pt.x)
		ys = append(ys, pt.y)
	}
	snap := Snapshot{f: s.f, n: len(xs)}
	coeffs, err := Interpolate(s.f, xs, ys)
	switch err {
	case nil:
		snap.status = Consistent
		snap.coeffs = coeffs
	case ErrNoSamples:
		snap.status = Insufficient
	default:
		snap.status = Contradictory
	}
	return snap
}

// Snapshot is an immutable, queryable view of a Stream at the moment it was
// taken.
type Snapshot struct {
	f      Field
	status Status
	coeffs []int64 // canonical lowest-degree coefficients, only when Consistent
	n      int     // number of active samples
}

// Status reports whether the snapshot is Consistent, Contradictory, or
// Insufficient.
func (s Snapshot) Status() Status { return s.status }

// Samples returns the number of active samples in the snapshot.
func (s Snapshot) Samples() int { return s.n }

// Polynomial returns the canonical lowest-degree interpolating coefficients
// (coeffs[i] is the coefficient of x^i, trailing zeros trimmed). It returns
// nil unless the snapshot status is Consistent. The returned slice is a copy.
func (s Snapshot) Polynomial() []int64 {
	if s.status != Consistent {
		return nil
	}
	out := make([]int64, len(s.coeffs))
	copy(out, s.coeffs)
	return out
}

// Eval evaluates the snapshot's polynomial at x. It panics unless the
// snapshot status is Consistent.
func (s Snapshot) Eval(x int64) int64 {
	if s.status != Consistent {
		panic("interp: Eval on non-consistent snapshot")
	}
	return s.f.Eval(s.coeffs, x)
}

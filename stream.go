package primeinterp

import "sort"

// Sample is one streamed observation. Samples are keyed by ID; a newer
// Revision for the same ID revokes the previous contribution.
type Sample struct {
	ID       string
	Revision uint64
	X, Y     int64
}

// Status is the outcome of a snapshot query.
type Status int

const (
	// StatusInsufficient means the snapshot holds no usable samples.
	StatusInsufficient Status = iota
	// StatusConsistent means a unique lowest-degree interpolant exists.
	StatusConsistent
	// StatusContradictory means two live samples share an x-coordinate
	// but disagree on y.
	StatusContradictory
)

func (s Status) String() string {
	switch s {
	case StatusInsufficient:
		return "insufficient"
	case StatusConsistent:
		return "consistent"
	case StatusContradictory:
		return "contradictory"
	}
	return "unknown"
}

// Result is the answer to a snapshot query.
type Result struct {
	Status Status
	// Coeffs holds the trimmed lowest-degree coefficients when
	// Status is StatusConsistent.
	Coeffs []int64
	// Conflicts lists the x-coordinates with contradictory y values
	// when Status is StatusContradictory.
	Conflicts []int64
}

type record struct {
	revision uint64
	x, y     int64
}

// Stream consumes revisioned samples over GF(p) and tracks the live set
// of points. The zero value is not usable; construct with NewStream.
type Stream struct {
	p      int64
	latest map[string]record
}

// NewStream creates a Stream over GF(p). p must be prime.
func NewStream(p int64) (*Stream, error) {
	if !isPrime(p) {
		return nil, ErrNotPrime
	}
	return &Stream{p: p, latest: make(map[string]record)}, nil
}

// Apply folds a sample into the stream. A sample is ignored (returns
// false) when its ID is already known at an equal or newer revision;
// otherwise the new revision revokes the old contribution (returns true).
func (s *Stream) Apply(smp Sample) bool {
	if prev, ok := s.latest[smp.ID]; ok && prev.revision >= smp.Revision {
		return false
	}
	s.latest[smp.ID] = record{revision: smp.Revision, x: smp.X, y: smp.Y}
	return true
}

// Snapshot captures the current live point set. The returned Snapshot is
// immutable: later Apply calls never affect queries against it.
func (s *Stream) Snapshot() Snapshot {
	pts := make([]Point, 0, len(s.latest))
	for _, rec := range s.latest {
		pts = append(pts, Point{X: rec.x, Y: rec.y})
	}
	return Snapshot{p: s.p, pts: pts}
}

// Snapshot is an immutable view of the stream at one moment in time.
type Snapshot struct {
	p   int64
	pts []Point
}

// Query resolves the snapshot into one of the three states. On success it
// interpolates the live points and returns the canonical lowest-degree
// coefficients.
func (sn Snapshot) Query() Result {
	if len(sn.pts) == 0 {
		return Result{Status: StatusInsufficient}
	}
	coeffs, err := Interpolate(sn.p, sn.pts)
	if err != nil {
		if _, ok := err.(*ConflictError); ok {
			return Result{Status: StatusContradictory, Conflicts: conflictXs(sn.p, sn.pts)}
		}
		// The stream is constructed with a validated prime, so no other
		// error is reachable; surface it as contradictory defensively.
		return Result{Status: StatusContradictory}
	}
	return Result{Status: StatusConsistent, Coeffs: coeffs}
}

// conflictXs reports every x-coordinate (reduced mod p) that carries two
// or more distinct y values, in ascending order.
func conflictXs(p int64, pts []Point) []int64 {
	byX := make(map[int64]map[int64]bool)
	for _, pt := range pts {
		x := mod(pt.X, p)
		ys := byX[x]
		if ys == nil {
			ys = make(map[int64]bool)
			byX[x] = ys
		}
		ys[mod(pt.Y, p)] = true
	}
	var out []int64
	for x, ys := range byX {
		if len(ys) > 1 {
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

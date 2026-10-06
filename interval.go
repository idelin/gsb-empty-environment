// Package railsim simulates exclusive, half-open track-segment occupancy
// for trains moving along fixed routes, plus delay-driven replanning.
//
// Model:
//   - Time is discrete (integer ticks) and bounded by a finite horizon.
//   - A train occupies a track segment over a half-open interval
//     [Start, End): a segment released at tick T may be entered by another
//     train at the same tick T.
//   - Occupancy is exclusive per segment: no two reservations may overlap,
//     regardless of travel direction (same-direction included).
//   - Stations sit between segments; a train may wait there arbitrarily
//     long, so gaps between consecutive occupancies of one train are legal.
package railsim

// Interval is a half-open time interval [Start, End).
type Interval struct {
	Start int
	End   int
}

// Valid reports whether the interval is well-formed.
func (iv Interval) Valid() bool { return 0 <= iv.Start && iv.Start < iv.End }

// Len returns the duration of the interval.
func (iv Interval) Len() int { return iv.End - iv.Start }

// Overlaps reports whether two half-open intervals share any tick.
// Touching endpoints ([a,b) and [b,c)) do not overlap.
func (a Interval) Overlaps(b Interval) bool { return a.Start < b.End && b.Start < a.End }

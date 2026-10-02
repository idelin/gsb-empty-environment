package analyze

import (
	"hashring-analyzer/hashfn"
	"hashring-analyzer/ring"
)

// WidthPoint is one sample of the bit-width variance curve: the load
// statistics measured on a ring whose position space is BitWidth bits.
type WidthPoint struct {
	BitWidth uint
	Stats    *LoadStats
}

// WidthCurve evaluates the hash on rings of increasing bit width. The
// reduced chi-square of each point forms the variance curve: for a hash
// with full avalanche it stays near 1 at every width, while a hash with
// deficient high bits diverges as the ring narrows onto those bits.
func WidthCurve(h hashfn.Hash64, nodes []string, replicas int, widths []uint,
	gen KeyGen, keyLen, samples int) []WidthPoint {
	points := make([]WidthPoint, 0, len(widths))
	for _, w := range widths {
		r := ring.New(h, nodes, replicas, w)
		points = append(points, WidthPoint{
			BitWidth: w,
			Stats:    Run(r, gen, keyLen, samples),
		})
	}
	return points
}

// FailingWidths returns the curve points that reject the uniformity
// hypothesis at significance level alpha.
func FailingWidths(points []WidthPoint, alpha float64) []uint {
	var out []uint
	for _, p := range points {
		if !p.Stats.Pass(alpha) {
			out = append(out, p.BitWidth)
		}
	}
	return out
}

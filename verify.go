package netsec

import "fmt"

// Verification is the exhaustive, point-wise audit of a diff result. It is
// intended to run on a shrunken universe (see [Universe.Shrunk]) where every
// tuple can be enumerated.
type Verification struct {
	OK bool
	// UniversePoints is the Cartesian-product size of the audited universe.
	UniversePoints int
	// TargetPoints is the number of points that are old-denied and new-allowed.
	TargetPoints int
	// CoveredPoints is the number of distinct target points covered by
	// findings plus the supplied remainder.
	CoveredPoints int
	// FindingsPoints sums the volume of the finding rectangles.
	FindingsPoints int
	// OverlapPoints counts points hit by more than one finding rectangle.
	OverlapPoints int
	// MissingSamples names up to a few uncovered target points.
	MissingSamples []Point
	// ExtraSamples names up to a few covered points that are not targets.
	ExtraSamples []Point
	// AnnotationErrors names points whose attached hits disagree with the
	// actual rules that fire.
	AnnotationErrors []string
}

// Verify exhaustively audits a diff shard against the same two policies.
// findings are the rectangle pages already produced; remainder is the report's
// pending remainder (nil when the report was complete). Coverage is computed
// over findings plus remainder, disjointness over findings alone.
func Verify(u *Universe, oldP, newP Policy, findings []Finding, remainder []Rect) (Verification, error) {
	oldCompiled, err := compilePolicy(u, oldP)
	if err != nil {
		return Verification{}, fmt.Errorf("netsec: old policy: %w", err)
	}
	newCompiled, err := compilePolicy(u, newP)
	if err != nil {
		return Verification{}, fmt.Errorf("netsec: new policy: %w", err)
	}

	findingRects := make([]rect, len(findings))
	for i, finding := range findings {
		indexRect, err := u.toIndex(finding.Rect)
		if err != nil {
			return Verification{}, fmt.Errorf("netsec: finding %d: %w", i, err)
		}
		findingRects[i] = indexRect
	}
	remainderRects := make([]rect, len(remainder))
	for i, named := range remainder {
		indexRect, err := u.toIndex(named)
		if err != nil {
			return Verification{}, fmt.Errorf("netsec: remainder %d: %w", i, err)
		}
		remainderRects[i] = indexRect
	}

	verification := Verification{OK: true, UniversePoints: u.Size()}

	u.enumerate(func(point Point) {
		indexPoint, err := u.indexPoint(point)
		if err != nil {
			panic(err)
		}
		oldDecision := oldCompiled.decide(indexPoint)
		newDecision := newCompiled.decide(indexPoint)
		isTarget := !oldDecision.Allowed && newDecision.Allowed
		if isTarget {
			verification.TargetPoints++
		}

		hits := 0
		for _, r := range findingRects {
			if r.containsPoint(indexPoint) {
				hits++
			}
		}
		inRemainder := false
		for _, r := range remainderRects {
			if r.containsPoint(indexPoint) {
				inRemainder = true
			}
		}
		covered := hits > 0 || inRemainder

		if hits > 1 {
			verification.OverlapPoints++
		}
		if hits > 0 {
			verification.FindingsPoints++
		}
		if covered {
			verification.CoveredPoints++
		}

		if isTarget && !covered && len(verification.MissingSamples) < 5 {
			verification.MissingSamples = append(verification.MissingSamples, append(Point(nil), point...))
		}
		if !isTarget && covered && len(verification.ExtraSamples) < 5 {
			verification.ExtraSamples = append(verification.ExtraSamples, append(Point(nil), point...))
		}

		if hits > 0 {
			for i, finding := range findings {
				if !findingRects[i].containsPoint(indexPoint) {
					continue
				}
				if !sameHits(finding.OldHits, oldDecision.Hits) || !sameHits(finding.NewHits, newDecision.Hits) {
					if len(verification.AnnotationErrors) < 5 {
						verification.AnnotationErrors = append(verification.AnnotationErrors,
							fmt.Sprintf("%v: finding hits do not match fired rules", point))
					}
				}
			}
		}
	})

	verification.OK = len(verification.MissingSamples) == 0 &&
		len(verification.ExtraSamples) == 0 &&
		len(verification.AnnotationErrors) == 0 &&
		verification.OverlapPoints == 0 &&
		verification.CoveredPoints == verification.TargetPoints
	return verification, nil
}

func sameHits(a, b []Hit) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

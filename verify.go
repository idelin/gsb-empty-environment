package netsec

import "fmt"

// Verify exhaustively checks a Diff Result against both policies by
// enumerating every point of dom. Use it on shrunk domains only: the
// enumeration cost is the product of all dimension sizes.
//
// It proves, for every point of the domain:
//   - no misses: every newly allowed point lies in exactly one
//     Covered rectangle, or (when the result is incomplete) in the
//     Remainder;
//   - no overlaps: no point lies in two result rectangles;
//   - no false positives: no Covered rectangle contains a point that
//     is not newly allowed.
//
// It also checks that every Covered rectangle carries at least one
// GrantedBy rule. A nil return means the result is exact.
func Verify(res Result, newPol, oldPol *Policy, dom Domain) error {
	all := coveredRects(res.Covered)
	all = append(all, res.Remainder...)
	for i := range all {
		for j := i + 1; j < len(all); j++ {
			if all[i].Intersects(all[j]) {
				return fmt.Errorf("verify: rectangles %d and %d overlap: %v vs %v", i, j, all[i].Values, all[j].Values)
			}
		}
	}
	for i, cr := range res.Covered {
		if len(cr.GrantedBy) == 0 {
			return fmt.Errorf("verify: covered rectangle %d has no granting rule", i)
		}
	}
	for _, pt := range dom.Points() {
		want := newPol.Allows(pt) && !oldPol.Allows(pt)
		covered := 0
		for _, cr := range res.Covered {
			if cr.Rect.Contains(pt) {
				covered++
			}
		}
		inRemainder := false
		for _, r := range res.Remainder {
			if r.Contains(pt) {
				inRemainder = true
				break
			}
		}
		switch {
		case covered > 1:
			return fmt.Errorf("verify: point %v covered %d times", pt, covered)
		case covered == 1 && !want:
			return fmt.Errorf("verify: point %v covered but not newly allowed", pt)
		case covered == 1 && inRemainder:
			return fmt.Errorf("verify: point %v in both covered and remainder", pt)
		case want && covered == 0 && res.Complete:
			return fmt.Errorf("verify: newly allowed point %v not covered", pt)
		case want && covered == 0 && !inRemainder:
			return fmt.Errorf("verify: newly allowed point %v in neither covered nor remainder", pt)
		}
	}
	return nil
}

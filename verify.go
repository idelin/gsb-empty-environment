package movint

import (
	"fmt"
	"math/big"
)

// Verify cross-checks the result of IntersectionTimes by sampling:
//
//   - an interior point of every returned interval (the midpoint, or the
//     point itself for a degenerate instant) must satisfy IntersectsAt;
//   - an interior point of every gap inside [0, T] not covered by the
//     result must NOT satisfy IntersectsAt.
//
// It also validates that the intervals are sorted, disjoint, non-touching,
// and contained in [0, T]. A nil error means the samples are consistent.
func Verify(a, b MovingInterval, T *big.Rat, got []TimeInterval) error {
	if T.Sign() < 0 {
		return fmt.Errorf("negative T: %v", T)
	}
	zero := new(big.Rat)

	// Structural checks + interior sampling.
	prevHi := zero
	for i, iv := range got {
		if iv.Lo == nil || iv.Hi == nil {
			return fmt.Errorf("interval %d has nil bound", i)
		}
		if iv.Lo.Cmp(iv.Hi) > 0 {
			return fmt.Errorf("interval %d is inverted: [%v, %v]", i, iv.Lo, iv.Hi)
		}
		if iv.Lo.Cmp(zero) < 0 || iv.Hi.Cmp(T) > 0 {
			return fmt.Errorf("interval %d [%v, %v] escapes [0, %v]", i, iv.Lo, iv.Hi, T)
		}
		if i > 0 {
			if iv.Lo.Cmp(prevHi) <= 0 {
				return fmt.Errorf("intervals %d and %d touch or overlap; should be merged", i-1, i)
			}
			if err := checkGap(a, b, prevHi, iv.Lo); err != nil {
				return err
			}
		}
		if !a.IntersectsAt(b, midpoint(iv.Lo, iv.Hi)) {
			return fmt.Errorf("interior sample of [%v, %v] does not intersect", iv.Lo, iv.Hi)
		}
		prevHi = iv.Hi
	}

	// Exterior sampling: leading and trailing gaps.
	firstLo := zero
	if len(got) > 0 {
		firstLo = got[0].Lo
	}
	if err := checkGap(a, b, zero, firstLo); err != nil {
		return err
	}
	lastHi := T
	if len(got) > 0 {
		lastHi = got[len(got)-1].Hi
	}
	if err := checkGap(a, b, lastHi, T); err != nil {
		return err
	}
	return nil
}

// checkGap samples the midpoint of the open gap (l, r) and requires the
// intervals to be disjoint there. Points exactly on the boundary belong to
// the closed result and are not sampled.
func checkGap(a, b MovingInterval, l, r *big.Rat) error {
	if l.Cmp(r) >= 0 {
		return nil
	}
	m := midpoint(l, r)
	if a.IntersectsAt(b, m) {
		return fmt.Errorf("gap sample t=%v in (%v, %v) unexpectedly intersects", m, l, r)
	}
	return nil
}

// midpoint returns (x+y)/2; for x == y it is the point itself.
func midpoint(x, y *big.Rat) *big.Rat {
	m := new(big.Rat).Add(x, y)
	m.Quo(m, big.NewRat(2, 1))
	return m
}

package main

import (
	"fmt"
	"math/big"
)

// Verify independently recomputes every claimed quantity from the placement
// evidence using exact rational arithmetic. It returns the list of problems
// found; an empty slice means the solution document is consistent.
//
// Note: Verify checks internal consistency and constraint satisfaction. The
// minimality claims (fewest bins, minimum max eccentricity) are established
// by the solver's exhaustive search, not re-proven here.
func Verify(in *Input, sol *Solution) []string {
	var probs []string
	fail := func(format string, args ...interface{}) {
		probs = append(probs, fmt.Sprintf(format, args...))
	}

	if sol.Status != "optimal" {
		fail("status is %q, want \"optimal\"", sol.Status)
	}
	if sol.BinsUsed != len(sol.Bins) {
		fail("bins_used=%d but %d bin reports present", sol.BinsUsed, len(sol.Bins))
	}

	byID := map[string]Item{}
	for _, it := range in.Items {
		byID[it.ID] = it
	}
	placed := map[string]int{}
	maxEcc := new(big.Rat)

	for _, b := range sol.Bins {
		usedPos := map[string]bool{}
		W := new(big.Rat)
		M := new(big.Rat)
		for _, pl := range b.Placements {
			it, ok := byID[pl.Item]
			if !ok {
				fail("bin %d: unknown item %q", b.Index, pl.Item)
				continue
			}
			placed[pl.Item]++
			pr, ok := new(big.Rat).SetString(pl.Position)
			if !ok {
				fail("bin %d: invalid position %q", b.Index, pl.Position)
				continue
			}
			if !isPosition(in, pr) {
				fail("bin %d: position %s is not one of the discrete slots", b.Index, pr.RatString())
			}
			key := pr.RatString()
			if usedPos[key] {
				fail("bin %d: slot %s used more than once", b.Index, key)
			}
			usedPos[key] = true
			W.Add(W, it.Weight)
			M.Add(M, new(big.Rat).Mul(it.Weight, pr))
		}
		if len(b.Placements) == 0 {
			fail("bin %d: empty bin reported", b.Index)
			continue
		}

		if W.Cmp(in.Capacity) > 0 {
			fail("bin %d: total weight %s exceeds capacity %s", b.Index, W.RatString(), in.Capacity.RatString())
		}
		cog := new(big.Rat).Quo(M, W)
		within := cog.Cmp(in.Lo) >= 0 && cog.Cmp(in.Hi) <= 0
		if !within {
			fail("bin %d: center of gravity %s outside safe interval [%s, %s]",
				b.Index, cog.RatString(), in.Lo.RatString(), in.Hi.RatString())
		}
		ecc := new(big.Rat).Sub(cog, in.Center)
		ecc.Abs(ecc)
		if ecc.Cmp(maxEcc) > 0 {
			maxEcc.Set(ecc)
		}

		checkRat(fail, b.Index, "total_weight", b.TotalWeight, W)
		checkRat(fail, b.Index, "cog", b.Cog, cog)
		checkRat(fail, b.Index, "eccentricity", b.Eccentricity, ecc)
		if b.WithinSafeInterval != within {
			fail("bin %d: within_safe_interval=%v but computed %v", b.Index, b.WithinSafeInterval, within)
		}
	}

	for _, it := range in.Items {
		switch placed[it.ID] {
		case 0:
			fail("item %q is not placed", it.ID)
		case 1:
		default:
			fail("item %q is placed %d times", it.ID, placed[it.ID])
		}
	}

	if rep, ok := new(big.Rat).SetString(sol.MaxEccentricity); !ok {
		fail("max_eccentricity %q is not a valid rational", sol.MaxEccentricity)
	} else if rep.Cmp(maxEcc) != 0 {
		fail("reported max_eccentricity %s != computed %s", rep.RatString(), maxEcc.RatString())
	}

	return probs
}

func checkRat(fail func(string, ...interface{}), bin int, field, reported string, computed *big.Rat) {
	rep, ok := new(big.Rat).SetString(reported)
	if !ok {
		fail("bin %d: %s %q is not a valid rational", bin, field, reported)
		return
	}
	if rep.Cmp(computed) != 0 {
		fail("bin %d: reported %s %s != computed %s", bin, field, rep.RatString(), computed.RatString())
	}
}

func isPosition(in *Input, r *big.Rat) bool {
	for _, p := range in.Positions {
		if p.Cmp(r) == 0 {
			return true
		}
	}
	return false
}

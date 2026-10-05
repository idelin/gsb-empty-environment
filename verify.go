package main

import (
	"fmt"
	"math/big"
)

// verifyChecks is the human-readable evidence trail of an independent
// re-check of a solution. Every check uses exact rational arithmetic.
type verifyReport struct {
	OK     bool     `json:"ok"`
	Checks []string `json:"checks"`
}

// verify independently re-checks a solution against the instance:
// every item placed exactly once, distinct in-range slots per bin,
// capacity, center of gravity recomputed exactly and inside the safe
// interval, and the reported eccentricities matching recomputation.
func verify(in *Instance, sol *solution) verifyReport {
	var rep verifyReport
	ok := true
	fail := func(format string, args ...interface{}) {
		ok = false
		rep.Checks = append(rep.Checks, "FAIL: "+fmt.Sprintf(format, args...))
	}
	pass := func(format string, args ...interface{}) {
		rep.Checks = append(rep.Checks, "ok: "+fmt.Sprintf(format, args...))
	}

	placed := map[int]int{}
	for bi, bin := range sol.bins {
		if len(bin.items) != len(bin.slots) {
			fail("bin %d: %d items but %d slots", bi, len(bin.items), len(bin.slots))
			continue
		}
		usedSlots := map[int]bool{}
		num := new(big.Rat)
		den := new(big.Rat)
		for k, it := range bin.items {
			if it < 0 || it >= len(in.Items) {
				fail("bin %d: item index %d out of range", bi, it)
				continue
			}
			if _, dup := placed[it]; dup {
				fail("bin %d: item %q placed more than once", bi, in.Items[it].ID)
			}
			placed[it] = bi
			sl := bin.slots[k]
			if sl < 0 || sl >= len(in.Positions) {
				fail("bin %d: slot %d out of range", bi, sl)
				continue
			}
			if usedSlots[sl] {
				fail("bin %d: slot %d used twice", bi, sl)
			}
			usedSlots[sl] = true
			w := in.Items[it].Weight
			num.Add(num, new(big.Rat).Mul(w, in.Positions[sl]))
			den.Add(den, w)
		}
		if den.Cmp(in.Capacity) > 0 {
			fail("bin %d: weight %s exceeds capacity %s",
				bi, ratStr(den), ratStr(in.Capacity))
		} else {
			pass("bin %d: weight %s <= capacity %s",
				bi, ratStr(den), ratStr(in.Capacity))
		}
		if den.Sign() == 0 {
			fail("bin %d: empty bin", bi)
			continue
		}
		cog := new(big.Rat).Quo(num, den)
		if cog.Cmp(bin.cog) != 0 {
			fail("bin %d: reported cog %s != recomputed %s",
				bi, ratStr(bin.cog), ratStr(cog))
		}
		if cog.Cmp(in.SafeLo) < 0 || cog.Cmp(in.SafeHi) > 0 {
			fail("bin %d: cog %s outside safe interval [%s, %s]",
				bi, ratStr(cog), ratStr(in.SafeLo), ratStr(in.SafeHi))
		} else {
			pass("bin %d: cog %s in [%s, %s]",
				bi, ratStr(cog), ratStr(in.SafeLo), ratStr(in.SafeHi))
		}
		ecc := new(big.Rat).Sub(cog, in.Target)
		ecc.Abs(ecc)
		if ecc.Cmp(bin.ecc) != 0 {
			fail("bin %d: reported eccentricity %s != recomputed %s",
				bi, ratStr(bin.ecc), ratStr(ecc))
		}
	}
	if len(placed) != len(in.Items) {
		fail("placed %d of %d items", len(placed), len(in.Items))
	} else {
		pass("all %d items placed exactly once", len(in.Items))
	}
	maxEcc := new(big.Rat)
	for _, bin := range sol.bins {
		if bin.ecc != nil && bin.ecc.Cmp(maxEcc) > 0 {
			maxEcc = bin.ecc
		}
	}
	if sol.maxEcc == nil || maxEcc.Cmp(sol.maxEcc) != 0 {
		fail("reported max eccentricity %s != recomputed %s",
			ratStr(sol.maxEcc), ratStr(maxEcc))
	} else {
		pass("max eccentricity %s confirmed", ratStr(maxEcc))
	}
	rep.OK = ok
	return rep
}

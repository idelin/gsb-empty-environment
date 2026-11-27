package capgate

import (
	"fmt"
	"sort"
)

// truth is a 0/1 table over feature flags for a fixed platform assignment;
// -1 marks cells not belonging to the set being decomposed.
type truth struct {
	platPV uint64
	cells  []int8 // indexed by flag value
}

// decomposeCubes turns the set of (pv, fv) combos belonging to one region into
// disjoint cubes. It partitions by platform assignment, then recursively
// Shannon-expands each feature truth table; the recursion returns cubes that
// are disjoint by construction.
func (u *Universe) decomposeCubes(members map[uint64]map[uint64]bool) []Cube {
	platKeys := make([]uint64, 0, len(members))
	for pv := range members {
		platKeys = append(platKeys, pv)
	}
	sort.Slice(platKeys, func(i, j int) bool { return platKeys[i] < platKeys[j] })

	var cubes []Cube
	for _, pv := range platKeys {
		flagSet := members[pv]
		fn := 1 << u.flagCount
		t := truth{platPV: pv, cells: make([]int8, fn)}
		for fv := range flagSet {
			t.cells[fv] = 1
		}
		platMask := masks{platVal: pv, platCare: (uint64(1) << u.platCount) - 1}
		flagCubes := u.expandFlags(t, 0, masks{})
		for _, fm := range flagCubes {
			c := u.cubeFromMasks(joinMasks(platMask, fm))
			cubes = append(cubes, c)
		}
	}
	return cubes
}

// expandFlags recursively decomposes a feature truth table over flag index i.
// prefix records the fixed flag bits chosen so far.
func (u *Universe) expandFlags(t truth, i int, prefix masks) []masks {
	if i == u.flagCount {
		fv := prefix.flagVal
		if t.cells[fv] == 1 {
			return []masks{prefix}
		}
		return nil
	}
	bit := uint64(1) << i
	careAll := true
	for _, c := range t.cells {
		if c == 0 {
			careAll = false
			break
		}
	}
	if careAll {
		// Every remaining assignment is in-set for this prefix: collapse all
		// remaining flag bits to "don't care".
		return []masks{prefix}
	}
	var on, off masks
	on = prefix
	on.flagCare |= bit
	on.flagVal |= bit
	off = prefix
	off.flagCare |= bit
	var out []masks
	out = append(out, u.expandFlags(t, i+1, off)...)
	out = append(out, u.expandFlags(t, i+1, on)...)
	return out
}

func joinMasks(a, b masks) masks {
	return masks{
		platVal:  a.platVal | b.platVal,
		platCare: a.platCare | b.platCare,
		flagVal:  a.flagVal | b.flagVal,
		flagCare: a.flagCare | b.flagCare,
	}
}

// cubeContains reports whether a cube covers the given combo masks.
func cubeContains(c Cube, pv, fv uint64, u *Universe) bool {
	for _, p := range u.job.Platforms {
		want, ok := c.Platforms[p]
		if ok && *want != bitOn(pv, u.platIndex[p]) {
			return false
		}
	}
	for _, f := range u.job.Flags {
		want, ok := c.Flags[f]
		if ok && *want != bitOn(fv, u.flagIndex[f]) {
			return false
		}
	}
	return true
}

// deltaSignature is the set of new capability instance keys for one combo.
func deltaSignature(newRes, oldRes EvalResult) []string {
	set := map[string]bool{}
	for k := range newRes.Capable {
		if _, had := oldRes.Capable[k]; !had {
			set[k] = true
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sigKey(sig []string) string {
	return fmt.Sprintf("%v", sig)
}

type regionBuilder struct {
	added   []string
	members map[uint64]map[uint64]bool
	combos  []string
	chains  map[string]GrantChain
}

// buildRegions groups combos by identical added-capability signature and
// decomposes each group into disjoint cubes.
func (u *Universe) buildRegions(
	rows []comboRow,
) []Region {
	groups := map[string]*regionBuilder{}
	order := []string{}
	for _, row := range rows {
		sig := deltaSignature(row.newRes, row.oldRes)
		key := sigKey(sig)
		rb, ok := groups[key]
		if !ok {
			rb = &regionBuilder{
				added:   sig,
				members: map[uint64]map[uint64]bool{},
				chains:  map[string]GrantChain{},
			}
			groups[key] = rb
			order = append(order, key)
		}
		if _, ok := rb.members[row.pv]; !ok {
			rb.members[row.pv] = map[uint64]bool{}
		}
		rb.members[row.pv][row.fv] = true
		rb.combos = append(rb.combos, u.comboID(row.pv, row.fv))
		for k, chains := range row.newRes.Capable {
			if _, had := row.oldRes.Capable[k]; had {
				continue
			}
			for _, ch := range chains {
				rb.chains[ch.TxnID+"|"+ch.Digest+"|"+k] = ch
			}
		}
	}

	sort.Strings(order)
	regions := make([]Region, 0, len(order))
	for idx, key := range order {
		rb := groups[key]
		cubes := u.decomposeCubes(rb.members)
		chainKeys := make([]string, 0, len(rb.chains))
		for ck := range rb.chains {
			chainKeys = append(chainKeys, ck)
		}
		sort.Strings(chainKeys)
		chains := make([]GrantChain, 0, len(chainKeys))
		for _, ck := range chainKeys {
			chains = append(chains, rb.chains[ck])
		}
		sort.Strings(rb.combos)
		regions = append(regions, Region{
			ID:       fmt.Sprintf("R%d", idx+1),
			Cubes:    cubes,
			Added:    rb.added,
			Chains:   chains,
			ComboIDs: rb.combos,
		})
	}
	return regions
}

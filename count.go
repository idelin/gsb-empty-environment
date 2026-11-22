package layout

import "math/big"

// prefixCounter counts completions after some regions have been fixed.
//
// Regions in the remaining set are considered in increasing start order. The
// page position p is the maximum end of all already placed remaining
// intervals, so the next remaining interval may start anywhere at or after p.
// This counts every layout exactly once: each layout has one sorted sequence
// of starts.
//
// Modular constraints couple starts. For those we keep the starts of placed
// regions that still have an unplaced constrained partner. When the modular
// constraint graph touching the remaining regions is empty, the frontier is
// empty and a much faster subset/page DP is used.
type prefixCounter struct {
	p *Planner

	assignedMask  uint8
	assignedStart [MaxRegions]int
	prefixBits    uint64
	remainingMask uint8

	domains    [MaxRegions][]int
	validStart [MaxRegions][MaxPages]bool

	nextFree [MaxPages + 1]uint8

	memo map[dpKey]*big.Int
}

type dpKey struct {
	done   uint8
	p      uint8
	starts [MaxRegions]int16
}

func (c *prefixCounter) build() {
	for i := 0; i < c.p.n; i++ {
		if c.remainingMask&(1<<uint(i)) == 0 {
			continue
		}
		for _, s := range c.p.domains[i] {
			if c.p.span[i][s]&c.prefixBits != 0 {
				continue
			}
			if !c.compatibleWithAssigned(i, s) {
				continue
			}
			c.domains[i] = append(c.domains[i], s)
			c.validStart[i][s] = true
		}
	}

	c.nextFree[c.p.pages] = uint8(c.p.pages)
	for q := c.p.pages - 1; q >= 0; q-- {
		if c.prefixBits&(uint64(1)<<uint(q)) != 0 {
			c.nextFree[q] = c.nextFree[q+1]
		} else {
			c.nextFree[q] = uint8(q)
		}
	}
}

func (c *prefixCounter) compatibleWithAssigned(i, s int) bool {
	for j := 0; j < c.p.n; j++ {
		if c.assignedMask&(1<<uint(j)) == 0 {
			continue
		}
		if c.p.modAdj[i]&(1<<uint(j)) == 0 {
			continue
		}
		sj := c.assignedStart[j]
		if !c.p.modOK[i][j][s][sj] || !c.p.modOK[j][i][sj][s] {
			return false
		}
	}
	return true
}

func (c *prefixCounter) count() *big.Int {
	if c.noRemainingModularConstraints() {
		return c.countFast()
	}
	return c.countGeneral()
}

func (c *prefixCounter) noRemainingModularConstraints() bool {
	for i := 0; i < c.p.n; i++ {
		if c.remainingMask&(1<<uint(i)) == 0 {
			continue
		}
		if c.p.modAdj[i]&c.remainingMask != 0 {
			return false
		}
	}
	return true
}

// countFast is the subset/page DP used when no modular constraint couples
// two remaining regions. Constraints against fixed regions were already
// applied while filtering each remaining region's candidate starts.
func (c *prefixCounter) countFast() *big.Int {
	var f [1 << MaxRegions][MaxPages + 1]*big.Int
	full := int(c.remainingMask)
	one := big.NewInt(1)

	for done := full; done >= 0; done-- {
		if done&^full != 0 {
			continue
		}
		if done == full {
			for q := 0; q <= c.p.pages; q++ {
				f[done][q] = one
			}
			continue
		}
		for q := 0; q <= c.p.pages; q++ {
			f[done][q] = new(big.Int)
		}
		for i := 0; i < c.p.n; i++ {
			if done&(1<<uint(i)) != 0 || c.remainingMask&(1<<uint(i)) == 0 {
				continue
			}
			nextDone := done | 1<<uint(i)
			length := c.p.cfg.Regions[i].Length
			sum := new(big.Int)
			for q := c.p.pages; q >= 0; q-- {
				if q < c.p.pages && c.validStart[i][q] {
					nq := c.nextFree[q+length]
					sum.Add(sum, f[nextDone][nq])
				}
				f[done][q].Add(f[done][q], sum)
			}
		}
	}
	return f[0][0]
}

func (c *prefixCounter) countGeneral() *big.Int {
	c.memo = make(map[dpKey]*big.Int)
	return c.countState(0, 0, [MaxRegions]int16{})
}

func (c *prefixCounter) countState(done uint8, p int, starts [MaxRegions]int16) *big.Int {
	if done == c.remainingMask {
		return big.NewInt(1)
	}
	if p < 0 {
		p = 0
	}
	if p > c.p.pages {
		p = c.p.pages
	}
	p = int(c.nextFree[p])

	key := dpKey{done: done, p: uint8(p), starts: starts}
	if v, ok := c.memo[key]; ok {
		return v
	}

	total := new(big.Int)
	unplaced := c.remainingMask &^ done
	for i := 0; i < c.p.n; i++ {
		if unplaced&(1<<uint(i)) == 0 {
			continue
		}
		length := c.p.cfg.Regions[i].Length
		for _, s := range c.domains[i] {
			if s < p {
				continue
			}
			if !c.compatibleWithDone(i, s, done, starts) {
				continue
			}
			ns := starts
			ns[i] = int16(s)
			nd := done | 1<<uint(i)
			future := c.remainingMask &^ nd
			for j := 0; j < c.p.n; j++ {
				if nd&(1<<uint(j)) != 0 && c.p.modAdj[j]&future == 0 {
					ns[j] = 0
				}
			}
			total.Add(total, c.countState(nd, s+length, ns))
		}
	}
	c.memo[key] = total
	return total
}

func (c *prefixCounter) compatibleWithDone(i, s int, done uint8, starts [MaxRegions]int16) bool {
	for j := 0; j < c.p.n; j++ {
		if done&(1<<uint(j)) == 0 {
			continue
		}
		if c.p.modAdj[i]&(1<<uint(j)) == 0 {
			continue
		}
		sj := int(starts[j])
		if !c.p.modOK[i][j][s][sj] || !c.p.modOK[j][i][sj][s] {
			return false
		}
	}
	return true
}

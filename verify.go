package tracer

import (
	"fmt"
	"math/big"
)

// Verify independently checks global conservation invariants against the
// recorded introduction data (not against the transfer mechanics):
//
//  1. For every solute, the summed mass over all containers equals the
//     total mass introduced at container creation (transfers and water
//     introduce none).
//  2. For every source batch, the summed volume-equivalents over all
//     containers equal the batch's introduced volume.
//  3. Every container's volume equals the sum of its batch
//     volume-equivalents, and no volume or mass is negative.
//  4. An empty container holds no solute mass.
func (s *System) Verify() error {
	massTotals := map[string]*big.Rat{}
	batchTotals := map[string]*big.Rat{}
	for id, c := range s.containers {
		if c.volume.Sign() < 0 {
			return fmt.Errorf("container %q: negative volume %s", id, c.volume)
		}
		sum := new(big.Rat)
		for b, v := range c.batchVol {
			if v.Sign() < 0 {
				return fmt.Errorf("container %q: negative share of batch %q", id, b)
			}
			sum.Add(sum, v)
			addTo(batchTotals, b, v)
		}
		if sum.Cmp(c.volume) != 0 {
			return fmt.Errorf("container %q: volume %s differs from summed batch shares %s",
				id, c.volume, sum)
		}
		for name, m := range c.masses {
			if m.Sign() < 0 {
				return fmt.Errorf("container %q: negative mass of solute %q", id, name)
			}
			if c.volume.Sign() == 0 && m.Sign() != 0 {
				return fmt.Errorf("container %q: mass of %q in empty container", id, name)
			}
			addTo(massTotals, name, m)
		}
	}
	for _, name := range unionKeys(massTotals, s.introduced) {
		got := massTotals[name]
		if got == nil {
			got = new(big.Rat)
		}
		want := s.introduced[name]
		if want == nil {
			want = new(big.Rat)
		}
		if got.Cmp(want) != 0 {
			return fmt.Errorf("solute %q: conserved mass %s differs from introduced %s",
				name, got, want)
		}
	}
	for label, want := range s.batches {
		got := batchTotals[label]
		if got == nil {
			got = new(big.Rat)
		}
		if got.Cmp(want) != 0 {
			return fmt.Errorf("batch %q: conserved volume %s differs from introduced %s",
				label, got, want)
		}
	}
	for label, got := range batchTotals {
		if _, ok := s.batches[label]; !ok {
			return fmt.Errorf("unknown batch %q with volume-equivalent %s", label, got)
		}
	}
	return nil
}

func unionKeys(a, b map[string]*big.Rat) []string {
	seen := map[string]bool{}
	var out []string
	for k := range a {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for k := range b {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

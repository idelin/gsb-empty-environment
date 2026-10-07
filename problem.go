package policy

import (
	"errors"
	"fmt"
	"math/big"
)

const (
	// MaxWorlds is the maximum number of possible worlds.
	MaxWorlds = 16
	// MaxActions is the maximum number of terminal actions.
	MaxActions = 8
	// MaxProbeDepth is the maximum number of probes before acting.
	MaxProbeDepth = 2
	// MaxProbes is the maximum number of distinct probes.
	MaxProbes = 32
)

// Probe is a test that may be performed before acting. Performing it in
// world w reveals the symbol Obs[w] and adds Cost to the total loss.
type Probe struct {
	Name string   // optional display name; defaults to "P<i>"
	Cost int      // integer cost added to the total loss
	Obs  []string // Obs[w] is the symbol observed in world w
}

// Problem describes a control decision instance.
type Problem struct {
	NumWorlds  int
	NumActions int
	Loss       [][]int    // Loss[a][w]: integer loss of action a in world w
	ActObs     [][]string // ActObs[a][w]: symbol observed when action a is taken in world w
	Probes     []Probe
	MaxProbes  int        // probe budget before acting, at most MaxProbeDepth
	Prob       []*big.Rat // Prob[w]: rational probability (or weight) of world w
}

// Validate checks the structural constraints of the problem.
func (p *Problem) Validate() error {
	if p.NumWorlds < 1 || p.NumWorlds > MaxWorlds {
		return fmt.Errorf("NumWorlds=%d out of range [1,%d]", p.NumWorlds, MaxWorlds)
	}
	if p.NumActions < 1 || p.NumActions > MaxActions {
		return fmt.Errorf("NumActions=%d out of range [1,%d]", p.NumActions, MaxActions)
	}
	if len(p.Loss) != p.NumActions || len(p.ActObs) != p.NumActions {
		return errors.New("Loss and ActObs must have NumActions rows")
	}
	for a := 0; a < p.NumActions; a++ {
		if len(p.Loss[a]) != p.NumWorlds {
			return fmt.Errorf("Loss[%d] has %d entries, want %d", a, len(p.Loss[a]), p.NumWorlds)
		}
		if len(p.ActObs[a]) != p.NumWorlds {
			return fmt.Errorf("ActObs[%d] has %d entries, want %d", a, len(p.ActObs[a]), p.NumWorlds)
		}
	}
	if len(p.Probes) > MaxProbes {
		return fmt.Errorf("too many probes: %d > %d", len(p.Probes), MaxProbes)
	}
	for i, pr := range p.Probes {
		if len(pr.Obs) != p.NumWorlds {
			return fmt.Errorf("Probes[%d].Obs has %d entries, want %d", i, len(pr.Obs), p.NumWorlds)
		}
	}
	if p.MaxProbes < 0 || p.MaxProbes > MaxProbeDepth {
		return fmt.Errorf("MaxProbes=%d out of range [0,%d]", p.MaxProbes, MaxProbeDepth)
	}
	if len(p.Prob) != p.NumWorlds {
		return fmt.Errorf("Prob has %d entries, want %d", len(p.Prob), p.NumWorlds)
	}
	sum := new(big.Rat)
	for w, r := range p.Prob {
		if r == nil {
			return fmt.Errorf("Prob[%d] is nil", w)
		}
		if r.Sign() < 0 {
			return fmt.Errorf("Prob[%d]=%s is negative", w, r.RatString())
		}
		sum.Add(sum, r)
	}
	if sum.Sign() <= 0 {
		return errors.New("world probabilities must have positive total mass")
	}
	return nil
}

// probeName returns the display name of probe i.
func (p *Problem) probeName(i int) string {
	if p.Probes[i].Name != "" {
		return p.Probes[i].Name
	}
	return fmt.Sprintf("P%d", i)
}

// eachWorld calls f for every world index set in mask.
func eachWorld(mask uint32, f func(w int)) {
	for m := mask; m != 0; m &= m - 1 {
		f(trailingZeros32(m))
	}
}

func trailingZeros32(x uint32) int {
	n := 0
	for x&1 == 0 {
		x >>= 1
		n++
	}
	return n
}

// partitionMask splits the worlds in mask into cells that share the same
// observation symbol. It returns the sorted symbols and their world masks.
func partitionMask(mask uint32, obs []string) ([]string, []uint32) {
	cells := make(map[string]uint32)
	eachWorld(mask, func(w int) { cells[obs[w]] |= 1 << uint(w) })
	keys := make([]string, 0, len(cells))
	for k := range cells {
		keys = append(keys, k)
	}
	sortStrings(keys)
	masks := make([]uint32, len(keys))
	for i, k := range keys {
		masks[i] = cells[k]
	}
	return keys, masks
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

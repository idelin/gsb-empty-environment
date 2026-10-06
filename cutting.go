// Package cutting solves the one-dimensional cutting stock problem for small
// orders: stock bars come in several integer lengths with prices, the saw
// loses kerf per cut, and at most MaxParts parts may be ordered. It minimizes
// purchase cost first, then leftover waste. The last piece on each bar needs
// no trailing trim cut, so a bar holding n parts takes n-1 cuts.
package cutting

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// MaxParts is the largest number of order parts Optimize accepts.
const MaxParts = 12

// Stock is one purchasable bar length.
type Stock struct {
	Length int // integer length units
	Price  int // purchase cost per bar
}

// Part is one ordered piece. Parts from different orders may share a length;
// the Order field keeps them distinguishable.
type Part struct {
	Order  string
	Length int
}

// Bar is one stock bar with its cutting layout.
type Bar struct {
	StockIndex int    // index into the Optimize stocks slice
	Length     int    // stock length
	Price      int    // stock price
	Parts      []Part // pieces in cutting order
	Cuts       int    // number of saw cuts: len(Parts)-1 (no end trim)
	KerfLoss   int    // Cuts * kerf
	Used       int    // sum of part lengths
	Leftover   int    // Length - Used - KerfLoss
}

// Solution is the optimal set of bars.
type Solution struct {
	Bars       []Bar
	TotalCost  int // sum of bar prices
	TotalWaste int // sum of bar leftovers
}

type candidate struct {
	cost  int
	waste int
	sub   uint32 // chosen sub-mask
}

// Optimize finds the cheapest way to cut all parts from stock bars, breaking
// ties by least total leftover waste.
func Optimize(stocks []Stock, kerf int, parts []Part) (Solution, error) {
	if kerf < 0 {
		return Solution{}, errors.New("cutting: kerf must be >= 0")
	}
	if len(parts) == 0 {
		return Solution{}, errors.New("cutting: no parts")
	}
	if len(parts) > MaxParts {
		return Solution{}, fmt.Errorf("cutting: %d parts exceeds MaxParts=%d", len(parts), MaxParts)
	}
	if len(stocks) == 0 {
		return Solution{}, errors.New("cutting: no stock types")
	}
	for i, s := range stocks {
		if s.Length <= 0 || s.Price < 0 {
			return Solution{}, fmt.Errorf("cutting: invalid stock %d", i)
		}
	}
	for i, p := range parts {
		if p.Length <= 0 {
			return Solution{}, fmt.Errorf("cutting: part %d has non-positive length", i)
		}
	}

	n := len(parts)
	full := 1 << uint(n)

	// Per-subset facts: total part length, piece count, and the cheapest
	// stock (ties: shortest) that fits sum(lengths) + kerf*(count-1).
	used := make([]int, full)
	count := make([]int, full)
	barStock := make([]int, full) // stock index, -1 if the subset fits nothing
	barCost := make([]int, full)
	barWaste := make([]int, full)
	for mask := 1; mask < full; mask++ {
		low := mask & (-mask)
		bit := 0
		for (1 << uint(bit)) != low {
			bit++
		}
		prev := mask ^ low
		used[mask] = used[prev] + parts[bit].Length
		count[mask] = count[prev] + 1
		need := used[mask] + kerf*(count[mask]-1)
		best := -1
		for si, s := range stocks {
			if s.Length < need {
				continue
			}
			if best < 0 || s.Price < stocks[best].Price ||
				(s.Price == stocks[best].Price && s.Length < stocks[best].Length) {
				best = si
			}
		}
		barStock[mask] = best
		if best >= 0 {
			barCost[mask] = stocks[best].Price
			barWaste[mask] = stocks[best].Length - need
		}
	}

	// Every single part must fit somewhere, otherwise no solution exists.
	for i := 0; i < n; i++ {
		if barStock[1<<uint(i)] < 0 {
			return Solution{}, fmt.Errorf("cutting: part %d (order %q, length %d) fits no stock",
				i, parts[i].Order, parts[i].Length)
		}
	}

	// Subset DP: dp[mask] = best (cost, waste) covering exactly mask.
	// Restricting sub-masks to those containing the lowest set bit of mask
	// enumerates each set partition once.
	const inf = int(^uint(0)>>1) / 4
	dp := make([]candidate, full)
	for mask := 1; mask < full; mask++ {
		dp[mask] = candidate{cost: inf, waste: inf}
	}
	for mask := 1; mask < full; mask++ {
		low := mask & (-mask)
		best := candidate{cost: inf, waste: inf}
		for sub := mask; sub > 0; sub = (sub - 1) & mask {
			if sub&low == 0 || barStock[sub] < 0 {
				continue
			}
			rest := dp[mask^sub]
			if rest.cost == inf {
				continue
			}
			cost := rest.cost + barCost[sub]
			waste := rest.waste + barWaste[sub]
			if cost < best.cost || (cost == best.cost && waste < best.waste) {
				best = candidate{cost: cost, waste: waste, sub: uint32(sub)}
			}
		}
		dp[mask] = best
	}

	// Reconstruct bars from the chosen sub-masks.
	sol := Solution{TotalCost: dp[full-1].cost, TotalWaste: dp[full-1].waste}
	for mask := full - 1; mask > 0; {
		sub := int(dp[mask].sub)
		si := barStock[sub]
		bar := Bar{
			StockIndex: si,
			Length:     stocks[si].Length,
			Price:      stocks[si].Price,
			Cuts:       count[sub] - 1,
			KerfLoss:   kerf * (count[sub] - 1),
			Used:       used[sub],
			Leftover:   stocks[si].Length - used[sub] - kerf*(count[sub]-1),
		}
		for i := 0; i < n; i++ {
			if sub&(1<<uint(i)) != 0 {
				bar.Parts = append(bar.Parts, parts[i])
			}
		}
		sol.Bars = append(sol.Bars, bar)
		mask ^= sub
	}
	sort.Slice(sol.Bars, func(i, j int) bool {
		if sol.Bars[i].Length != sol.Bars[j].Length {
			return sol.Bars[i].Length > sol.Bars[j].Length
		}
		return sol.Bars[i].Used > sol.Bars[j].Used
	})
	return sol, nil
}

// String renders the per-bar cutting layout.
func (s Solution) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "cost=%d waste=%d bars=%d\n", s.TotalCost, s.TotalWaste, len(s.Bars))
	for i, bar := range s.Bars {
		fmt.Fprintf(&b, "bar %d: stock=%d price=%d cuts=%d kerfLoss=%d used=%d leftover=%d\n",
			i+1, bar.Length, bar.Price, bar.Cuts, bar.KerfLoss, bar.Used, bar.Leftover)
		for j, p := range bar.Parts {
			fmt.Fprintf(&b, "  piece %d: order=%s len=%d\n", j+1, p.Order, p.Length)
		}
	}
	return b.String()
}

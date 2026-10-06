package cutting

import "fmt"

// Verify re-checks a solution by subset-group enumeration: it matches every
// laid-out piece back to a distinct order part (forming one subset per bar),
// confirms the subsets partition the full part set, and recomputes each bar's
// cut count, kerf loss and leftover from first principles
//
//	cuts     = pieces - 1            (no trailing trim cut on the last piece)
//	kerfLoss = cuts * kerf
//	leftover = stockLength - sum(pieceLengths) - kerfLoss
//
// It also validates stock indices, prices and the reported totals.
func Verify(sol Solution, stocks []Stock, kerf int, parts []Part) error {
	n := len(parts)
	if n > MaxParts {
		return fmt.Errorf("cutting: verify supports at most %d parts, got %d", MaxParts, n)
	}

	usedMask := 0
	totalCost := 0
	totalWaste := 0

	for bi, bar := range sol.Bars {
		if bar.StockIndex < 0 || bar.StockIndex >= len(stocks) {
			return fmt.Errorf("bar %d: stock index %d out of range", bi+1, bar.StockIndex)
		}
		st := stocks[bar.StockIndex]
		if bar.Length != st.Length || bar.Price != st.Price {
			return fmt.Errorf("bar %d: length/price mismatch with stock %d", bi+1, bar.StockIndex)
		}
		if len(bar.Parts) == 0 {
			return fmt.Errorf("bar %d: empty layout", bi+1)
		}

		// Group this bar's pieces into a subset of the order parts.
		group := 0
		for _, piece := range bar.Parts {
			found := -1
			for i := 0; i < n; i++ {
				if usedMask&(1<<uint(i)) == 0 && group&(1<<uint(i)) == 0 &&
					parts[i].Order == piece.Order && parts[i].Length == piece.Length {
					found = i
					break
				}
			}
			if found < 0 {
				return fmt.Errorf("bar %d: piece order=%q len=%d matches no unconsumed part",
					bi+1, piece.Order, piece.Length)
			}
			group |= 1 << uint(found)
		}
		if usedMask&group != 0 {
			return fmt.Errorf("bar %d: reuses parts from an earlier bar", bi+1)
		}
		usedMask |= group

		// Recompute layout numbers from the subset.
		sum := 0
		cnt := 0
		for i := 0; i < n; i++ {
			if group&(1<<uint(i)) != 0 {
				sum += parts[i].Length
				cnt++
			}
		}
		cuts := cnt - 1 // last piece on the bar needs no end trim cut
		kerfLoss := cuts * kerf
		leftover := bar.Length - sum - kerfLoss

		if bar.Cuts != cuts {
			return fmt.Errorf("bar %d: cuts=%d, want %d", bi+1, bar.Cuts, cuts)
		}
		if bar.KerfLoss != kerfLoss {
			return fmt.Errorf("bar %d: kerfLoss=%d, want %d", bi+1, bar.KerfLoss, kerfLoss)
		}
		if bar.Used != sum {
			return fmt.Errorf("bar %d: used=%d, want %d", bi+1, bar.Used, sum)
		}
		if leftover < 0 {
			return fmt.Errorf("bar %d: layout overflows stock by %d", bi+1, -leftover)
		}
		if bar.Leftover != leftover {
			return fmt.Errorf("bar %d: leftover=%d, want %d", bi+1, bar.Leftover, leftover)
		}

		totalCost += bar.Price
		totalWaste += leftover
	}

	if usedMask != (1<<uint(n))-1 {
		return fmt.Errorf("layout does not cover all %d parts (mask %#x)", n, usedMask)
	}
	if sol.TotalCost != totalCost {
		return fmt.Errorf("total cost=%d, want %d", sol.TotalCost, totalCost)
	}
	if sol.TotalWaste != totalWaste {
		return fmt.Errorf("total waste=%d, want %d", sol.TotalWaste, totalWaste)
	}
	return nil
}

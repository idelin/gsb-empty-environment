package cutstock

import (
	"math/rand"
	"testing"
)

func solveOrDie(t *testing.T, stocks []Stock, orders []Order, kerf int) *Solution {
	t.Helper()
	sol, err := Solve(stocks, orders, kerf)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if err := Verify(stocks, orders, kerf, sol); err != nil {
		t.Fatalf("Verify: %v\n%s", err, sol.Layout())
	}
	return sol
}

// The last piece on a bar needs no trailing cut: two 5s with kerf 1 need
// 5+1+5=11 > 10, so they must split; 4+5 fits exactly as 4+1+5=10.
func TestLastPieceNoTrailingCut(t *testing.T) {
	stocks := []Stock{{Length: 10, Price: 7}}

	sol := solveOrDie(t, stocks, []Order{{Length: 5, Qty: 2}}, 1)
	if sol.TotalCost != 14 || len(sol.Bars) != 2 {
		t.Fatalf("want 2 bars cost 14, got %d bars cost %d\n%s", len(sol.Bars), sol.TotalCost, sol.Layout())
	}

	sol = solveOrDie(t, stocks, []Order{{Length: 4, Qty: 1}, {Length: 5, Qty: 1}}, 1)
	if len(sol.Bars) != 1 || sol.Bars[0].Cuts != 1 || sol.Bars[0].Leftover != 0 {
		t.Fatalf("want 1 bar, 1 cut, 0 leftover\n%s", sol.Layout())
	}
}

// Cost dominates; waste breaks ties between equal-cost plans.
func TestCostThenWaste(t *testing.T) {
	stocks := []Stock{{Length: 12, Price: 5}, {Length: 10, Price: 5}}
	sol := solveOrDie(t, stocks, []Order{{Length: 10, Qty: 1}}, 1)
	if sol.TotalCost != 5 || sol.TotalWaste != 0 || sol.Bars[0].Length != 10 {
		t.Fatalf("want the length-10 stock, waste 0\n%s", sol.Layout())
	}

	// Two cheap short bars beat one expensive long bar on cost, even
	// though the long bar would leave less waste.
	stocks = []Stock{{Length: 20, Price: 11}, {Length: 6, Price: 5}}
	sol = solveOrDie(t, stocks, []Order{{Length: 6, Qty: 2}}, 1)
	if sol.TotalCost != 10 || len(sol.Bars) != 2 {
		t.Fatalf("want 2 cheap bars cost 10\n%s", sol.Layout())
	}
}

// Equal-length parts from different orders stay distinguishable.
func TestSameLengthDifferentOrders(t *testing.T) {
	stocks := []Stock{{Length: 10, Price: 3}}
	orders := []Order{{Length: 4, Qty: 1}, {Length: 4, Qty: 1}}
	sol := solveOrDie(t, stocks, orders, 1)
	if len(sol.Bars) != 1 || len(sol.Bars[0].Pieces) != 2 {
		t.Fatalf("want both parts on one bar\n%s", sol.Layout())
	}
	seen := map[int]bool{}
	for _, p := range sol.Bars[0].Pieces {
		seen[p.Order] = true
	}
	if !seen[0] || !seen[1] {
		t.Fatalf("order identities lost: %v", sol.Bars[0].Pieces)
	}
}

func TestKerfAccounting(t *testing.T) {
	stocks := []Stock{{Length: 13, Price: 4}}
	sol := solveOrDie(t, stocks, []Order{{Length: 4, Qty: 3}}, 1)
	// 4+1+4+1+4 = 14 > 13, so one bar takes two parts (4+1+4=9, left 4)
	// and another takes one (left 9): cost 8, waste 13, kerf 1.
	if sol.TotalCost != 8 || sol.TotalWaste != 13 || sol.TotalKerf != 1 {
		t.Fatalf("bad totals\n%s", sol.Layout())
	}
}

func TestErrors(t *testing.T) {
	if _, err := Solve(nil, []Order{{Length: 3, Qty: 1}}, 0); err == nil {
		t.Fatal("want error for no stock")
	}
	if _, err := Solve([]Stock{{Length: 5, Price: 1}}, []Order{{Length: 9, Qty: 1}}, 0); err == nil {
		t.Fatal("want error for oversized part")
	}
	orders := make([]Order, MaxOrders+1)
	for i := range orders {
		orders[i] = Order{Length: 1, Qty: 1}
	}
	if _, err := Solve([]Stock{{Length: 100, Price: 1}}, orders, 0); err == nil {
		t.Fatal("want error for too many orders")
	}
}

// Verify must reject tampered solutions.
func TestVerifyRejects(t *testing.T) {
	stocks := []Stock{{Length: 10, Price: 5}}
	orders := []Order{{Length: 4, Qty: 2}}
	sol := solveOrDie(t, stocks, orders, 1)

	bad := *sol
	bad.Bars = append([]Bar(nil), sol.Bars...)
	bad.Bars[0].Leftover++
	if err := Verify(stocks, orders, 1, &bad); err == nil {
		t.Fatal("Verify accepted a wrong leftover")
	}

	bad2 := *sol
	bad2.TotalCost++
	if err := Verify(stocks, orders, 1, &bad2); err == nil {
		t.Fatal("Verify accepted a wrong total cost")
	}

	bad3 := *sol
	bad3.Bars = append([]Bar(nil), sol.Bars...)
	bad3.Bars[0].Pieces = bad3.Bars[0].Pieces[:1]
	if err := Verify(stocks, orders, 1, &bad3); err == nil {
		t.Fatal("Verify accepted uncovered demand")
	}
}

// brute is an independent exact solver: it enumerates every set partition
// of the (distinguishable) parts into bars and every stock assignment.
func brute(stocks []Stock, orders []Order, kerf int) (bestCost, bestWaste int) {
	var parts []int
	for _, o := range orders {
		for i := 0; i < o.Qty; i++ {
			parts = append(parts, o.Length)
		}
	}
	type bar struct{ stock, sum, pieces int }
	bars := []bar{}
	bestCost, bestWaste = -1, -1
	var rec func(pi int)
	rec = func(pi int) {
		if pi == len(parts) {
			cost, waste := 0, 0
			for _, b := range bars {
				cost += stocks[b.stock].Price
				waste += stocks[b.stock].Length - b.sum - (b.pieces-1)*kerf
			}
			if bestCost < 0 || cost < bestCost || (cost == bestCost && waste < bestWaste) {
				bestCost, bestWaste = cost, waste
			}
			return
		}
		L := parts[pi]
		for i := range bars {
			if bars[i].sum+L+bars[i].pieces*kerf <= stocks[bars[i].stock].Length {
				bars[i].sum += L
				bars[i].pieces++
				rec(pi + 1)
				bars[i].sum -= L
				bars[i].pieces--
			}
		}
		for si, st := range stocks {
			if L <= st.Length {
				bars = append(bars, bar{stock: si, sum: L, pieces: 1})
				rec(pi + 1)
				bars = bars[:len(bars)-1]
			}
		}
	}
	rec(0)
	return bestCost, bestWaste
}

// Cross-check Solve against the brute-force partition enumeration on many
// small random instances.
func TestAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		var stocks []Stock
		for i := 0; i < 1+rng.Intn(3); i++ {
			stocks = append(stocks, Stock{Length: 5 + rng.Intn(16), Price: 1 + rng.Intn(12)})
		}
		var orders []Order
		total := 0
		for i := 0; i < 1+rng.Intn(4); i++ {
			q := rng.Intn(3)
			orders = append(orders, Order{Length: 1 + rng.Intn(9), Qty: q})
			total += q
		}
		kerf := rng.Intn(3)
		// Keep parts fittable and the instance small for the brute force.
		maxLen := 0
		for _, s := range stocks {
			if s.Length > maxLen {
				maxLen = s.Length
			}
		}
		ok := total <= 8
		for _, o := range orders {
			if o.Qty > 0 && o.Length > maxLen {
				ok = false
			}
		}
		if !ok {
			continue
		}
		sol := solveOrDie(t, stocks, orders, kerf)
		bc, bw := brute(stocks, orders, kerf)
		if sol.TotalCost != bc || sol.TotalWaste != bw {
			t.Fatalf("stocks=%v orders=%v kerf=%d: Solve=(%d,%d) brute=(%d,%d)\n%s",
				stocks, orders, kerf, sol.TotalCost, sol.TotalWaste, bc, bw, sol.Layout())
		}
	}
}

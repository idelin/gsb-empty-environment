package cutting

import (
	"math/rand"
	"strings"
	"testing"
)

func TestBasicLayout(t *testing.T) {
	stocks := []Stock{{Length: 3000, Price: 50}, {Length: 1500, Price: 30}}
	parts := []Part{
		{Order: "A", Length: 800},
		{Order: "B", Length: 1200},
		{Order: "A", Length: 800},
		{Order: "C", Length: 900},
	}
	sol, err := Optimize(stocks, 3, parts)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(sol, stocks, 3, parts); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// All four fit one 3000 bar: 800+1200+800+900 + 3 cuts*3 = 3709 > 3000,
	// so the optimum is one 3000 (three parts) plus one 1500 (one part).
	if sol.TotalCost != 80 {
		t.Fatalf("cost=%d, want 80\n%s", sol.TotalCost, sol)
	}
	if len(sol.Bars) != 2 {
		t.Fatalf("bars=%d, want 2\n%s", len(sol.Bars), sol)
	}
	t.Logf("\n%s", sol)
}

func TestSameLengthDifferentOrders(t *testing.T) {
	stocks := []Stock{{Length: 1000, Price: 10}}
	parts := []Part{
		{Order: "X", Length: 400},
		{Order: "Y", Length: 400},
		{Order: "Z", Length: 400},
	}
	sol, err := Optimize(stocks, 5, parts)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(sol, stocks, 5, parts); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// 400*3 + 2*5 = 1210 > 1000, so two bars: one with two parts, one with one.
	if sol.TotalCost != 20 {
		t.Fatalf("cost=%d, want 20\n%s", sol.TotalCost, sol)
	}
	seen := map[string]int{}
	for _, bar := range sol.Bars {
		for _, p := range bar.Parts {
			seen[p.Order]++
		}
	}
	for _, o := range []string{"X", "Y", "Z"} {
		if seen[o] != 1 {
			t.Fatalf("order %s appears %d times", o, seen[o])
		}
	}
}

func TestNoTrailingTrimCut(t *testing.T) {
	// A part exactly as long as the stock fits with zero cuts.
	stocks := []Stock{{Length: 500, Price: 7}}
	parts := []Part{{Order: "A", Length: 500}}
	sol, err := Optimize(stocks, 10, parts)
	if err != nil {
		t.Fatal(err)
	}
	if sol.TotalCost != 7 || sol.TotalWaste != 0 {
		t.Fatalf("got cost=%d waste=%d, want 7/0", sol.TotalCost, sol.TotalWaste)
	}
	bar := sol.Bars[0]
	if bar.Cuts != 0 || bar.KerfLoss != 0 || bar.Leftover != 0 {
		t.Fatalf("bar=%+v, want 0 cuts/loss/leftover", bar)
	}
	if err := Verify(sol, stocks, 10, parts); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestLastPieceKerfAccounting(t *testing.T) {
	// Two 490 parts on a 1000 bar: 490+490+1*20 = 1000 exactly, because the
	// final piece needs no trim cut. With a trailing cut it would not fit.
	stocks := []Stock{{Length: 1000, Price: 10}}
	parts := []Part{{Order: "A", Length: 490}, {Order: "B", Length: 490}}
	sol, err := Optimize(stocks, 20, parts)
	if err != nil {
		t.Fatal(err)
	}
	if sol.TotalCost != 10 || len(sol.Bars) != 1 {
		t.Fatalf("got cost=%d bars=%d, want one bar at 10\n%s", sol.TotalCost, len(sol.Bars), sol)
	}
	if sol.Bars[0].Cuts != 1 || sol.Bars[0].Leftover != 0 {
		t.Fatalf("bar=%+v, want 1 cut and 0 leftover", sol.Bars[0])
	}
	if err := Verify(sol, stocks, 20, parts); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestCheapestStockThenLeastWaste(t *testing.T) {
	// Both stocks fit one part; the cheaper wins. Among equal price, the
	// shorter (less waste) wins.
	stocks := []Stock{{Length: 2000, Price: 10}, {Length: 1200, Price: 10}, {Length: 900, Price: 8}}
	parts := []Part{{Order: "A", Length: 800}}
	sol, err := Optimize(stocks, 0, parts)
	if err != nil {
		t.Fatal(err)
	}
	if sol.TotalCost != 8 || sol.Bars[0].Length != 900 {
		t.Fatalf("got %+v, want the 900mm/8 stock", sol.Bars[0])
	}
	if err := Verify(sol, stocks, 0, parts); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestPartTooLong(t *testing.T) {
	_, err := Optimize([]Stock{{Length: 100, Price: 1}}, 0, []Part{{Order: "A", Length: 101}})
	if err == nil || !strings.Contains(err.Error(), "fits no stock") {
		t.Fatalf("err=%v, want fits-no-stock error", err)
	}
}

func TestTooManyParts(t *testing.T) {
	parts := make([]Part, MaxParts+1)
	for i := range parts {
		parts[i] = Part{Order: "A", Length: 1}
	}
	if _, err := Optimize([]Stock{{Length: 100, Price: 1}}, 0, parts); err == nil {
		t.Fatal("want error for > MaxParts parts")
	}
}

// bruteForce enumerates every set partition of the parts and returns the best
// (cost, waste), used to cross-check the DP.
func bruteForce(stocks []Stock, kerf int, parts []Part) (int, int) {
	n := len(parts)
	bestCost, bestWaste := -1, 0
	var rec func(i int, bars [][]int)
	rec = func(i int, bars [][]int) {
		if i == n {
			cost, waste := 0, 0
			for _, bar := range bars {
				sum := 0
				for _, p := range bar {
					sum += parts[p].Length
				}
				need := sum + kerf*(len(bar)-1)
				price, left := -1, 0
				for _, s := range stocks {
					if s.Length >= need && (price < 0 || s.Price < price ||
						(s.Price == price && s.Length-need < left)) {
						price, left = s.Price, s.Length-need
					}
				}
				if price < 0 {
					return
				}
				cost += price
				waste += left
			}
			if bestCost < 0 || cost < bestCost || (cost == bestCost && waste < bestWaste) {
				bestCost, bestWaste = cost, waste
			}
			return
		}
		for b := range bars {
			bars[b] = append(bars[b], i)
			rec(i+1, bars)
			bars[b] = bars[b][:len(bars[b])-1]
		}
		rec(i+1, append(bars, []int{i}))
	}
	rec(0, nil)
	return bestCost, bestWaste
}

func TestAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 60; trial++ {
		stocks := []Stock{
			{Length: 50 + rng.Intn(50), Price: 5 + rng.Intn(20)},
			{Length: 100 + rng.Intn(100), Price: 15 + rng.Intn(30)},
			{Length: 200 + rng.Intn(150), Price: 30 + rng.Intn(40)},
		}
		kerf := rng.Intn(6)
		n := 1 + rng.Intn(8)
		parts := make([]Part, n)
		feasible := false
		for i := range parts {
			parts[i] = Part{Order: string(rune('A' + rng.Intn(3))), Length: 10 + rng.Intn(180)}
			for _, s := range stocks {
				if s.Length >= parts[i].Length {
					feasible = true
				}
			}
		}
		if !feasible {
			continue
		}
		sol, err := Optimize(stocks, kerf, parts)
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		if err := Verify(sol, stocks, kerf, parts); err != nil {
			t.Fatalf("trial %d verify: %v\n%s", trial, err, sol)
		}
		wantCost, wantWaste := bruteForce(stocks, kerf, parts)
		if sol.TotalCost != wantCost || sol.TotalWaste != wantWaste {
			t.Fatalf("trial %d: got cost=%d waste=%d, brute force wants %d/%d\nstocks=%v kerf=%d parts=%v\n%s",
				trial, sol.TotalCost, sol.TotalWaste, wantCost, wantWaste, stocks, kerf, parts, sol)
		}
	}
}

func TestMaxPartsTwelve(t *testing.T) {
	stocks := []Stock{{Length: 6000, Price: 100}, {Length: 3000, Price: 60}}
	parts := make([]Part, 12)
	for i := range parts {
		parts[i] = Part{Order: string(rune('A' + i%4)), Length: 700 + (i%3)*100}
	}
	sol, err := Optimize(stocks, 4, parts)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(sol, stocks, 4, parts); err != nil {
		t.Fatalf("verify: %v", err)
	}
	t.Logf("\n%s", sol)
}

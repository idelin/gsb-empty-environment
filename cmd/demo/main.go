// Demo: SUM(sales) OVER (PARTITION BY store ORDER BY date ROWS 10 PRECEDING)
// over 100k rows of synthetic time-series data.
package main

import (
	"fmt"
	"math"
	"math/rand"
	"time"

	"windowfn/window"
)

func genData(stores, days int, seed int64) *window.Input {
	n := stores * days
	in := &window.Input{
		PartitionKeys: make([]string, n),
		OrderKeys:     make([]float64, n),
		Values:        make([]float64, n),
	}
	rng := rand.New(rand.NewSource(seed))
	i := 0
	for s := 0; s < stores; s++ {
		store := fmt.Sprintf("store-%03d", s)
		for d := 0; d < days; d++ {
			in.PartitionKeys[i] = store
			in.OrderKeys[i] = float64(d) // day index, e.g. days since 2024-01-01
			in.Values[i] = math.Round(rng.Float64()*10000) / 100
			i++
		}
	}
	return in
}

func main() {
	const stores, days = 50, 2000 // 100_000 rows
	in := genData(stores, days, 42)
	fmt.Printf("input: %d rows (%d stores x %d days)\n\n", len(in.Values), stores, days)

	frame := window.Frame{
		Mode:  window.Rows,
		Start: window.Bound{Kind: window.Preceding, Offset: 10},
		End:   window.Bound{Kind: window.CurrentRow},
	}

	start := time.Now()
	sums, err := window.Sum(in, frame)
	if err != nil {
		panic(err)
	}
	fmt.Printf("SUM(sales) OVER (PARTITION BY store ORDER BY date ROWS 10 PRECEDING)\n")
	fmt.Printf("  computed in %v\n\n", time.Since(start))

	start = time.Now()
	avgs, err := window.Avg(in, frame)
	if err != nil {
		panic(err)
	}
	fmt.Printf("AVG(sales) OVER (... ROWS 10 PRECEDING) computed in %v\n", time.Since(start))

	start = time.Now()
	ranks, err := window.Rank(in.PartitionKeys, in.OrderKeys)
	if err != nil {
		panic(err)
	}
	fmt.Printf("RANK() OVER (PARTITION BY store ORDER BY date) computed in %v\n\n", time.Since(start))

	fmt.Printf("%-10s %6s %10s %14s %14s %6s\n", "store", "day", "sales", "sum_10d", "avg_10d", "rank")
	for i := 0; i < 12; i++ {
		fmt.Printf("%-10s %6.0f %10.2f %14.2f %14.2f %6d\n",
			in.PartitionKeys[i], in.OrderKeys[i], in.Values[i], sums[i], avgs[i], ranks[i])
	}
	fmt.Println("...")
	// Spot-check row 11 of store-000 against a direct re-sum.
	i := 11
	direct := 0.0
	for j := i - 10; j <= i; j++ {
		direct += in.Values[j]
	}
	fmt.Printf("spot check row %d: windowed=%.2f direct=%.2f match=%v\n",
		i, sums[i], direct, math.Abs(sums[i]-direct) < 1e-9)
}

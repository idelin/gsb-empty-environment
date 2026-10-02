// Demo runs the acceptance query on 100k generated time-series rows:
//
//	SUM(sales) OVER (PARTITION BY store ORDER BY date ROWS 10 PRECEDING)
package main

import (
	"fmt"
	"math"
	"math/rand"
	"time"

	"windowfn"
)

func main() {
	const stores = 50
	const daysPerStore = 2000 // 50 * 2000 = 100,000 rows

	rng := rand.New(rand.NewSource(42))
	store := make([]string, 0, stores*daysPerStore)
	date := make([]float64, 0, stores*daysPerStore)
	sales := make([]float64, 0, stores*daysPerStore)
	for s := 0; s < stores; s++ {
		for d := 0; d < daysPerStore; d++ {
			store = append(store, fmt.Sprintf("store_%02d", s))
			date = append(date, float64(d)) // day index as the order key
			sales = append(sales, math.Round(rng.Float64()*10000)/100)
		}
	}
	fmt.Printf("rows: %d (%d stores x %d days)\n", len(sales), stores, daysPerStore)

	start := time.Now()
	w := windowfn.NewWindow(store, date)
	result := w.Sum(sales, windowfn.RowsPreceding(10))
	elapsed := time.Since(start)

	fmt.Printf("SUM(sales) OVER (PARTITION BY store ORDER BY date ROWS 10 PRECEDING)\n")
	fmt.Printf("elapsed: %v (%.0f rows/sec)\n\n", elapsed, float64(len(sales))/elapsed.Seconds())

	fmt.Println("sample (store_00, first 12 days):")
	for i := 0; i < 12; i++ {
		fmt.Printf("  store=%s date=%4.0f sales=%8.2f  rolling_sum=%10.2f\n",
			store[i], date[i], sales[i], result[i])
	}
	fmt.Println("...")
	fmt.Printf("last row: store=%s date=%.0f rolling_sum=%.2f\n",
		store[len(store)-1], date[len(date)-1], result[len(result)-1])
}

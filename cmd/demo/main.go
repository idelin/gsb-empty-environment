// Demo of the star-join optimizer on the acceptance scenario: a sales fact
// table joined to time, store and product dimensions.
package main

import (
	"fmt"
	"math/rand"
	"time"

	"starjoin"
)

func main() {
	const factRows = 2_000_000
	rng := rand.New(rand.NewSource(2026))

	// ---- Dimension: time (730 days) ----
	days := 730
	dateKey := make([]int64, days)
	month := make([]int64, days)
	for d := 0; d < days; d++ {
		dateKey[d] = int64(d)
		month[d] = int64(d / 30)
	}
	dimTime, _ := starjoin.NewTable("dim_time",
		starjoin.IntColumn("date_key", dateKey),
		starjoin.IntColumn("month", month),
	)

	// ---- Dimension: store (500 stores, 5 regions) ----
	stores := 500
	regions := []string{"East", "West", "North", "South", "Central"}
	storeKey := make([]int64, stores)
	region := make([]string, stores)
	for s := 0; s < stores; s++ {
		storeKey[s] = int64(s)
		region[s] = regions[s%len(regions)]
	}
	dimStore, _ := starjoin.NewTable("dim_store",
		starjoin.IntColumn("store_key", storeKey),
		starjoin.StrColumn("region", region),
	)

	// ---- Dimension: product (5000 products, 10 categories) ----
	products := 5000
	prodKey := make([]int64, products)
	category := make([]string, products)
	for p := 0; p < products; p++ {
		prodKey[p] = int64(p)
		category[p] = fmt.Sprintf("cat_%d", p%10)
	}
	dimProduct, _ := starjoin.NewTable("dim_product",
		starjoin.IntColumn("product_key", prodKey),
		starjoin.StrColumn("category", category),
	)

	// ---- Fact: sales ----
	fkDate := make([]int64, factRows)
	fkStore := make([]int64, factRows)
	fkProd := make([]int64, factRows)
	qty := make([]int64, factRows)
	price := make([]int64, factRows)
	for i := 0; i < factRows; i++ {
		fkDate[i] = int64(rng.Intn(days))
		fkStore[i] = int64(rng.Intn(stores))
		fkProd[i] = int64(rng.Intn(products))
		qty[i] = int64(1 + rng.Intn(10))
		price[i] = int64(100 + rng.Intn(9900))
	}
	fact, _ := starjoin.NewTable("fact_sales",
		starjoin.IntColumn("date_key", fkDate),
		starjoin.IntColumn("store_key", fkStore),
		starjoin.IntColumn("product_key", fkProd),
		starjoin.IntColumn("qty", qty),
		starjoin.IntColumn("price", price),
	)

	// SELECT ... FROM fact_sales
	// JOIN dim_time    USING (date_key)    WHERE month = 3
	// JOIN dim_store   USING (store_key)   AND region = 'East'
	// JOIN dim_product USING (product_key) AND category IN ('cat_1','cat_7')
	q := &starjoin.StarQuery{
		Fact: fact,
		Joins: []starjoin.JoinSpec{
			{Dim: dimTime, DimPK: "date_key", FactFK: "date_key",
				Predicate: starjoin.BetweenInt("month", 3, 3)},
			{Dim: dimStore, DimPK: "store_key", FactFK: "store_key",
				Predicate: starjoin.EqStr("region", "East")},
			{Dim: dimProduct, DimPK: "product_key", FactFK: "product_key",
				Predicate: starjoin.InStr("category", "cat_1", "cat_7")},
		},
	}

	fmt.Printf("Star schema: fact_sales(%d rows) x dim_time(%d) x dim_store(%d) x dim_product(%d)\n\n",
		fact.NRows, dimTime.NRows, dimStore.NRows, dimProduct.NRows)

	// ---- Optimized: bitmap join filters + single filtered fact scan ----
	start := time.Now()
	plan, err := starjoin.Optimize(q)
	if err != nil {
		panic(err)
	}
	opt := plan.Execute()
	optDur := time.Since(start)

	fmt.Println("Optimized plan (Bitmap-Join-Filter per dimension):")
	for _, f := range plan.Filters {
		fmt.Printf("  %-12s dim rows %5d  matched %5d  selectivity %.4f\n",
			f.DimName, f.DimRows, f.MatchRows, f.Selectivity)
	}
	fmt.Printf("  fact rows scanned : %d\n", opt.Stats.FactRowsScanned)
	fmt.Printf("  survived filter   : %d\n", opt.Stats.RowsAfterFilter)
	fmt.Printf("  eliminated        : %d (%.2f%%)\n", opt.Stats.Eliminated, opt.Stats.EliminatedPct)
	fmt.Printf("  output rows       : %d\n", opt.Stats.OutputRows)
	fmt.Printf("  elapsed           : %v\n\n", optDur)

	// ---- Naive baseline: full hash joins, predicate after join ----
	start = time.Now()
	naive := starjoin.ExecuteNaive(q)
	naiveDur := time.Since(start)

	fmt.Println("Naive plan (hash join everything, filter afterwards):")
	fmt.Printf("  joined tuples before predicate: %d\n", naive.Stats.RowsAfterFilter)
	fmt.Printf("  output rows                   : %d\n", naive.Stats.OutputRows)
	fmt.Printf("  elapsed                       : %v\n\n", naiveDur)

	fmt.Printf("Results identical: %v | speedup: %.1fx | early elimination: %.2f%% (target >= 95%%)\n",
		opt.Stats.OutputRows == naive.Stats.OutputRows,
		float64(naiveDur)/float64(optDur),
		opt.Stats.EliminatedPct)
}

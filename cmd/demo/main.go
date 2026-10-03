// Command demo builds a sales star schema, then runs the same
// three-dimension join naively and with the star-join optimizer,
// printing bitmap-filter statistics and timings.
package main

import (
	"fmt"
	"math/rand"
	"time"

	"starjoin"
)

func main() {
	const factRows = 500_000

	timeDim := &starjoin.Table{Name: "time_dim"}
	for d := 1; d <= 360; d++ {
		timeDim.Rows = append(timeDim.Rows, starjoin.Row{
			"time_id": d, "quarter": fmt.Sprintf("Q%d", (d-1)/90+1),
		})
	}
	storeDim := &starjoin.Table{Name: "store_dim"}
	for s := 1; s <= 100; s++ {
		region := "West"
		if (s-1)%4 == 0 {
			region = "East"
		}
		storeDim.Rows = append(storeDim.Rows, starjoin.Row{"store_id": s, "region": region})
	}
	productDim := &starjoin.Table{Name: "product_dim"}
	for p := 1; p <= 2000; p++ {
		category := "Grocery"
		if (p-1)%20 == 0 {
			category = "Electronics"
		}
		productDim.Rows = append(productDim.Rows, starjoin.Row{"product_id": p, "category": category})
	}

	rng := rand.New(rand.NewSource(42))
	sales := &starjoin.Table{Name: "sales"}
	for i := 0; i < factRows; i++ {
		sales.Rows = append(sales.Rows, starjoin.Row{
			"sale_id":    i,
			"time_id":    1 + rng.Intn(360),
			"store_id":   1 + rng.Intn(100),
			"product_id": 1 + rng.Intn(2000),
			"amount":     float64(rng.Intn(100000)) / 100,
		})
	}

	edges := []starjoin.JoinEdge{
		{DimTable: "time_dim", FactKey: "time_id", DimKey: "time_id",
			DimFilter: func(r starjoin.Row) bool { return r["quarter"] == "Q1" }},
		{DimTable: "store_dim", FactKey: "store_id", DimKey: "store_id",
			DimFilter: func(r starjoin.Row) bool { return r["region"] == "East" }},
		{DimTable: "product_dim", FactKey: "product_id", DimKey: "product_id",
			DimFilter: func(r starjoin.Row) bool { return r["category"] == "Electronics" }},
	}
	tables := map[string]*starjoin.Table{
		"sales": sales, "time_dim": timeDim, "store_dim": storeDim, "product_dim": productDim,
	}

	fmt.Println("=== Star-Join Optimization Demo ===")
	fmt.Printf("fact: sales=%d rows | dims: time_dim=%d store_dim=%d product_dim=%d\n",
		sales.Len(), timeDim.Len(), storeDim.Len(), productDim.Len())
	fmt.Println("predicates: time_dim.quarter='Q1' AND store_dim.region='East' AND product_dim.category='Electronics'")
	fmt.Println()

	plan, err := starjoin.BuildPlan(tables, edges)
	if err != nil {
		panic(err)
	}
	fmt.Printf("optimizer: detected fact table %q, built %d Bitmap-Join-Filters\n",
		plan.FactTable, len(plan.Filters))
	for _, f := range plan.Filters {
		fmt.Printf("  %-12s keys=%-4d bitmap=%d bits (%.1f bytes)\n",
			f.DimTable, f.Keys(), f.BitmapSize(), float64(f.BitmapSize())/8)
	}
	fmt.Println()

	start := time.Now()
	naiveRows, nst := starjoin.NaiveJoin(sales, tables, edges)
	naiveDur := time.Since(start)
	fmt.Printf("naive hash join : scanned=%d probes=%d joined=%d took=%s\n",
		nst.Scanned, nst.Probed, len(naiveRows), naiveDur)

	start = time.Now()
	optRows := plan.Execute(sales)
	optDur := time.Since(start)
	fmt.Printf("bitmap optimized: scanned=%d bitmap-passed=%d excluded=%d (%.2f%%) joined=%d took=%s\n",
		plan.Scanned, plan.BitmapPassed, plan.Excluded(), 100*plan.ExclusionRate(),
		len(optRows), optDur)
	fmt.Println()

	if len(optRows) != len(naiveRows) {
		panic(fmt.Sprintf("result mismatch: optimized=%d naive=%d", len(optRows), len(naiveRows)))
	}
	fmt.Printf("results match (%d rows). join-stage speedup: %.1fx\n",
		len(optRows), float64(naiveDur)/float64(optDur))
}

package starjoin

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// buildSalesStar constructs the acceptance-test star schema:
//
//	sales(fact)  -> time_dim    (360 rows, filter quarter='Q1'   -> 90 keys, 25%)
//	             -> store_dim   (100 rows, filter region='East'  -> 25 keys, 25%)
//	             -> product_dim (2000 rows, filter category='Electronics' -> 100 keys, 5%)
//
// Combined selectivity ~= 0.25 * 0.25 * 0.05 = 0.31%, so the bitmap
// stage must exclude well over 95% of fact rows.
func buildSalesStar(factRows int) (map[string]*Table, []JoinEdge) {
	timeDim := &Table{Name: "time_dim", Columns: []string{"time_id", "quarter"}}
	for d := 1; d <= 360; d++ {
		timeDim.Rows = append(timeDim.Rows, Row{
			"time_id": d,
			"quarter": fmt.Sprintf("Q%d", (d-1)/90+1),
		})
	}

	storeDim := &Table{Name: "store_dim", Columns: []string{"store_id", "region"}}
	for s := 1; s <= 100; s++ {
		region := "West"
		if (s-1)%4 == 0 {
			region = "East" // exactly 25 of 100
		}
		storeDim.Rows = append(storeDim.Rows, Row{"store_id": s, "region": region})
	}

	productDim := &Table{Name: "product_dim", Columns: []string{"product_id", "category"}}
	for p := 1; p <= 2000; p++ {
		category := "Grocery"
		if (p-1)%20 == 0 {
			category = "Electronics" // exactly 100 of 2000
		}
		productDim.Rows = append(productDim.Rows, Row{"product_id": p, "category": category})
	}

	rng := rand.New(rand.NewSource(42))
	sales := &Table{Name: "sales", Columns: []string{"sale_id", "time_id", "store_id", "product_id", "amount"}}
	for i := 0; i < factRows; i++ {
		sales.Rows = append(sales.Rows, Row{
			"sale_id":    i,
			"time_id":    1 + rng.Intn(360),
			"store_id":   1 + rng.Intn(100),
			"product_id": 1 + rng.Intn(2000),
			"amount":     float64(rng.Intn(100000)) / 100,
		})
	}

	edges := []JoinEdge{
		{DimTable: "time_dim", FactKey: "time_id", DimKey: "time_id",
			DimFilter: func(r Row) bool { return r["quarter"] == "Q1" }},
		{DimTable: "store_dim", FactKey: "store_id", DimKey: "store_id",
			DimFilter: func(r Row) bool { return r["region"] == "East" }},
		{DimTable: "product_dim", FactKey: "product_id", DimKey: "product_id",
			DimFilter: func(r Row) bool { return r["category"] == "Electronics" }},
	}
	return map[string]*Table{
		"sales": sales, "time_dim": timeDim, "store_dim": storeDim, "product_dim": productDim,
	}, edges
}

func TestBitmap(t *testing.T) {
	b := NewBitmap(130)
	for _, i := range []uint64{0, 63, 64, 129} {
		b.Set(i)
	}
	for _, i := range []uint64{0, 63, 64, 129} {
		if !b.Test(i) {
			t.Fatalf("bit %d should be set", i)
		}
	}
	for _, i := range []uint64{1, 62, 65, 128, 130, 1000} {
		if b.Test(i) {
			t.Fatalf("bit %d should be unset/out of range", i)
		}
	}
	b.Set(64) // idempotent
	if b.Count() != 4 {
		t.Fatalf("Count() = %d, want 4", b.Count())
	}
}

func TestStarSchemaDetection(t *testing.T) {
	tables, edges := buildSalesStar(1000)
	plan, err := BuildPlan(tables, edges)
	if err != nil {
		t.Fatal(err)
	}
	if plan.FactTable != "sales" {
		t.Fatalf("FactTable = %q, want %q", plan.FactTable, "sales")
	}
	if len(plan.Filters) != 3 {
		t.Fatalf("len(Filters) = %d, want 3", len(plan.Filters))
	}
}

func TestBitmapJoinFilterContents(t *testing.T) {
	tables, edges := buildSalesStar(1000)
	plan, err := BuildPlan(tables, edges)
	if err != nil {
		t.Fatal(err)
	}
	wantKeys := map[string]int{"time_dim": 90, "store_dim": 25, "product_dim": 100}
	for _, f := range plan.Filters {
		if f.Hashed {
			t.Fatalf("%s: integer keys should use a direct bitmap", f.DimTable)
		}
		if got := f.Keys(); got != wantKeys[f.DimTable] {
			t.Fatalf("%s: Keys() = %d, want %d", f.DimTable, got, wantKeys[f.DimTable])
		}
		if got := f.PopCount(); got != uint64(wantKeys[f.DimTable]) {
			t.Fatalf("%s: PopCount() = %d, want %d", f.DimTable, got, wantKeys[f.DimTable])
		}
	}
}

func TestOptimizedMatchesNaive(t *testing.T) {
	tables, edges := buildSalesStar(100_000)
	plan, err := BuildPlan(tables, edges)
	if err != nil {
		t.Fatal(err)
	}
	got := plan.Execute(tables["sales"])
	want, _ := NaiveJoin(tables["sales"], tables, edges)

	key := func(rows []Row) []string {
		ks := make([]string, len(rows))
		for i, r := range rows {
			ks[i] = fmt.Sprintf("%v|%v|%v|%v|%v|%v|%v",
				r["sale_id"], r["time_id"], r["store_id"], r["product_id"],
				r["time_dim.quarter"], r["store_dim.region"], r["product_dim.category"])
		}
		sort.Strings(ks)
		return ks
	}
	if !reflect.DeepEqual(key(got), key(want)) {
		t.Fatalf("optimized join produced %d rows, naive produced %d rows (or content differs)",
			len(got), len(want))
	}
}

func TestExclusionRateAbove95Percent(t *testing.T) {
	tables, edges := buildSalesStar(200_000)
	plan, err := BuildPlan(tables, edges)
	if err != nil {
		t.Fatal(err)
	}
	plan.Execute(tables["sales"])

	t.Logf("scanned=%d bitmap-passed=%d excluded=%d (%.2f%%)",
		plan.Scanned, plan.BitmapPassed, plan.Excluded(), 100*plan.ExclusionRate())
	if plan.Scanned != 200_000 {
		t.Fatalf("Scanned = %d, want 200000", plan.Scanned)
	}
	if rate := plan.ExclusionRate(); rate < 0.95 {
		t.Fatalf("ExclusionRate = %.4f, want >= 0.95", rate)
	}
}

func TestHashedBitmapForStringKeys(t *testing.T) {
	region := &Table{Name: "region_dim"}
	for i := 0; i < 50; i++ {
		region.Rows = append(region.Rows, Row{
			"code":   fmt.Sprintf("R%03d", i),
			"active": i%5 == 0, // 10 active codes
		})
	}
	rng := rand.New(rand.NewSource(7))
	fact := &Table{Name: "facts"}
	for i := 0; i < 10_000; i++ {
		fact.Rows = append(fact.Rows, Row{"id": i, "region_code": fmt.Sprintf("R%03d", rng.Intn(50))})
	}
	edges := []JoinEdge{
		{DimTable: "region_dim", FactKey: "region_code", DimKey: "code",
			DimFilter: func(r Row) bool { return r["active"] == true }},
	}
	tables := map[string]*Table{"facts": fact, "region_dim": region}

	plan, err := BuildPlan(tables, edges)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Filters[0].Hashed {
		t.Fatal("string keys should use a hashed bitmap")
	}
	got := plan.Execute(fact)
	want, _ := NaiveJoin(fact, tables, edges)
	if len(got) != len(want) {
		t.Fatalf("hashed-bitmap join = %d rows, naive = %d rows (false positives not removed)",
			len(got), len(want))
	}
	if plan.ExclusionRate() < 0.70 {
		t.Fatalf("hashed bitmap excluded only %.2f%%, want a useful pre-filter",
			100*plan.ExclusionRate())
	}
}

func TestBuildPlanErrors(t *testing.T) {
	if _, err := BuildPlan(map[string]*Table{"f": {Name: "f"}}, nil); err == nil {
		t.Fatal("expected error for empty edges")
	}
	edges := []JoinEdge{{DimTable: "missing", FactKey: "k", DimKey: "k"}}
	if _, err := BuildPlan(map[string]*Table{"f": {Name: "f"}}, edges); err == nil {
		t.Fatal("expected error for unknown dimension table")
	}
}

func BenchmarkNaiveJoin(b *testing.B) {
	tables, edges := buildSalesStar(200_000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		NaiveJoin(tables["sales"], tables, edges)
	}
}

func BenchmarkOptimizedJoin(b *testing.B) {
	tables, edges := buildSalesStar(200_000)
	plan, err := BuildPlan(tables, edges)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		plan.Execute(tables["sales"])
	}
}

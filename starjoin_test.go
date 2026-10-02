package starjoin

import (
	"math/rand"
	"testing"
)

func TestBitmap(t *testing.T) {
	b := NewBitmap(130)
	if b.Count() != 0 || b.Test(5) {
		t.Fatal("fresh bitmap must be empty")
	}
	b.Set(5)
	b.Set(129)
	b.Set(5) // idempotent
	if !b.Test(5) || !b.Test(129) || b.Test(6) {
		t.Fatal("set/test mismatch")
	}
	if b.Count() != 2 {
		t.Fatalf("count = %d, want 2", b.Count())
	}
	// Out of range must be safe.
	b.Set(-1)
	b.Set(130)
	if b.Test(-1) || b.Test(130) || b.Count() != 2 {
		t.Fatal("out-of-range access must be ignored")
	}
}

func TestPredicates(t *testing.T) {
	tbl, err := NewTable("d",
		IntColumn("id", []int64{1, 2, 3}),
		StrColumn("region", []string{"East", "West", "East"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !EqInt("id", 2)(tbl, 1) || EqInt("id", 2)(tbl, 0) {
		t.Fatal("EqInt")
	}
	if !BetweenInt("id", 2, 3)(tbl, 2) || BetweenInt("id", 2, 3)(tbl, 0) {
		t.Fatal("BetweenInt")
	}
	if !EqStr("region", "East")(tbl, 0) || EqStr("region", "East")(tbl, 1) {
		t.Fatal("EqStr")
	}
	if !InStr("region", "East", "North")(tbl, 2) || InStr("region", "East")(tbl, 1) {
		t.Fatal("InStr")
	}
	and := And(EqStr("region", "East"), BetweenInt("id", 3, 9))
	if !and(tbl, 2) || and(tbl, 0) {
		t.Fatal("And")
	}
	or := Or(EqInt("id", 1), EqInt("id", 3))
	if !or(tbl, 0) || or(tbl, 1) || !or(tbl, 2) {
		t.Fatal("Or")
	}
	if EqInt("nope", 1)(tbl, 0) {
		t.Fatal("missing column must not match")
	}
}

// buildStarSchema creates the acceptance-test schema:
// sales fact (date_key, store_key, product_key, qty, price) joined to
// time / store / product dimensions.
func buildStarSchema(factRows int, seed int64) (*StarQuery, *Table, *Table, *Table) {
	rng := rand.New(rand.NewSource(seed))

	// Time dimension: 730 days, filter keeps one 30-day month (~4.1%).
	days := 730
	dateKey := make([]int64, days)
	month := make([]int64, days)
	for d := 0; d < days; d++ {
		dateKey[d] = int64(d)
		month[d] = int64(d / 30)
	}
	dimTime, _ := NewTable("dim_time",
		IntColumn("date_key", dateKey),
		IntColumn("month", month),
	)

	// Store dimension: 500 stores in 5 regions, filter keeps "East" (20%).
	stores := 500
	regions := []string{"East", "West", "North", "South", "Central"}
	storeKey := make([]int64, stores)
	region := make([]string, stores)
	for s := 0; s < stores; s++ {
		storeKey[s] = int64(s)
		region[s] = regions[s%len(regions)]
	}
	dimStore, _ := NewTable("dim_store",
		IntColumn("store_key", storeKey),
		StrColumn("region", region),
	)

	// Product dimension: 5000 products in 10 categories, filter keeps 2 (20%).
	products := 5000
	prodKey := make([]int64, products)
	category := make([]string, products)
	for p := 0; p < products; p++ {
		prodKey[p] = int64(p)
		category[p] = catName(p % 10)
	}
	dimProduct, _ := NewTable("dim_product",
		IntColumn("product_key", prodKey),
		StrColumn("category", category),
	)

	// Fact table: uniformly distributed foreign keys.
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
	fact, _ := NewTable("fact_sales",
		IntColumn("date_key", fkDate),
		IntColumn("store_key", fkStore),
		IntColumn("product_key", fkProd),
		IntColumn("qty", qty),
		IntColumn("price", price),
	)

	q := &StarQuery{
		Fact: fact,
		Joins: []JoinSpec{
			{Dim: dimTime, DimPK: "date_key", FactFK: "date_key",
				Predicate: BetweenInt("month", 3, 3)}, // one month of 30 days
			{Dim: dimStore, DimPK: "store_key", FactFK: "store_key",
				Predicate: EqStr("region", "East")},
			{Dim: dimProduct, DimPK: "product_key", FactFK: "product_key",
				Predicate: InStr("category", "cat_1", "cat_7")},
		},
	}
	return q, dimTime, dimStore, dimProduct
}

func catName(i int) string {
	return "cat_" + string(rune('0'+i))
}

func TestStarJoinCorrectness(t *testing.T) {
	q, _, _, _ := buildStarSchema(50_000, 42)

	plan, err := Optimize(q)
	if err != nil {
		t.Fatal(err)
	}
	got := plan.Execute()
	want := ExecuteNaive(q)

	if got.Stats.OutputRows != want.Stats.OutputRows {
		t.Fatalf("optimized %d rows != naive %d rows", got.Stats.OutputRows, want.Stats.OutputRows)
	}
	for i := range got.Rows {
		if got.Rows[i].FactRow != want.Rows[i].FactRow ||
			!equalInts(got.Rows[i].DimRows, want.Rows[i].DimRows) {
			t.Fatalf("row %d differs: %+v vs %+v", i, got.Rows[i], want.Rows[i])
		}
	}
	if got.Stats.OutputRows == 0 {
		t.Fatal("expected non-empty join result")
	}
}

// TestStarJoinEliminates95Pct is the acceptance boundary: the combined
// bitmap-join-filter must discard at least 95% of fact rows before any
// join work happens.
func TestStarJoinEliminates95Pct(t *testing.T) {
	q, _, _, _ := buildStarSchema(1_000_000, 7)

	plan, err := Optimize(q)
	if err != nil {
		t.Fatal(err)
	}
	res := plan.Execute()

	t.Logf("fact rows scanned: %d", res.Stats.FactRowsScanned)
	t.Logf("rows after bitmap filter: %d", res.Stats.RowsAfterFilter)
	t.Logf("eliminated: %d (%.2f%%)", res.Stats.Eliminated, res.Stats.EliminatedPct)
	for _, f := range plan.Filters {
		t.Logf("filter %-12s dim rows %d matched %d (selectivity %.4f)",
			f.DimName, f.DimRows, f.MatchRows, f.Selectivity)
	}

	if res.Stats.EliminatedPct < 95.0 {
		t.Fatalf("eliminated %.2f%% of fact rows, want >= 95%%", res.Stats.EliminatedPct)
	}
	if res.Stats.RowsAfterFilter != res.Stats.OutputRows {
		t.Fatalf("survivors %d != output %d: FK integrity expected", res.Stats.RowsAfterFilter, res.Stats.OutputRows)
	}
	// The bitmap build must only touch the small dimensions, never the fact.
	if res.Stats.DimRowsScanned != 730+500+5000 {
		t.Fatalf("dim rows scanned = %d, want %d", res.Stats.DimRowsScanned, 730+500+5000)
	}
}

func TestOptimizeRejectsNonStar(t *testing.T) {
	big, _ := NewTable("big_dim", IntColumn("k", []int64{1, 2, 3, 4, 5}))
	small, _ := NewTable("small_fact", IntColumn("k", []int64{1, 2}))
	q := &StarQuery{Fact: small, Joins: []JoinSpec{{Dim: big, DimPK: "k", FactFK: "k"}}}
	if _, err := Optimize(q); err == nil {
		t.Fatal("expected error: dimension larger than fact is not a star pattern")
	}

	dup, _ := NewTable("dup_dim", IntColumn("k", []int64{1, 1}))
	fact, _ := NewTable("fact", IntColumn("k", []int64{1, 1, 1, 1}))
	q2 := &StarQuery{Fact: fact, Joins: []JoinSpec{{Dim: dup, DimPK: "k", FactFK: "k"}}}
	if _, err := Optimize(q2); err == nil {
		t.Fatal("expected error: duplicate dimension primary key")
	}

	q3 := &StarQuery{Fact: fact, Joins: []JoinSpec{{Dim: dup, DimPK: "missing", FactFK: "k"}}}
	if _, err := Optimize(q3); err == nil {
		t.Fatal("expected error: missing PK column")
	}
}

func BenchmarkBitmapFilteredScan(b *testing.B) {
	q, _, _, _ := buildStarSchema(2_000_000, 1)
	plan, err := Optimize(q)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		plan.Execute()
	}
}

func BenchmarkNaiveJoin(b *testing.B) {
	q, _, _, _ := buildStarSchema(2_000_000, 1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ExecuteNaive(q)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

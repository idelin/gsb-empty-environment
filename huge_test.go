package colarchive

import "testing"

func hugeBlock(rows int64, width int) *Archive {
	planes := make([]int, width)
	for i := range planes {
		planes[i] = i
	}
	return &Archive{Groups: []Group{{Blocks: []Block{{
		Rows:   rows,
		Width:  width,
		Planes: planes,
		Nulls:  []NullRun{{Rows: rows, Null: false}},
	}}}}}
}

func TestHugeUniformBlockContiguousPlanes(t *testing.T) {
	const rows = int64(1) << 48
	a := hugeBlock(rows, 8)
	dev := Device{StartupCost: 1000, ByteCost: 1, MaxBytes: 1 << 20}
	q := Query{
		Rows: []RowSpan{{Start: 0, Step: 1, Count: rows}},
		Bits: []int{0, 3, 7},
	}
	plan, err := a.Plan(q, dev)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(plan.Mapping), 3; got != want {
		t.Fatalf("mapping pieces = %d, want %d", got, want)
	}
	if len(plan.Reads) > 8 {
		t.Fatalf("read plan not compressed: %d items", len(plan.Reads))
	}
	const planeBytes = rows / 8
	const chunks = planeBytes / (1 << 20)
	if got, want := plan.ReadCount, int64(3*chunks); got != want {
		t.Fatalf("read count = %d, want %d", got, want)
	}
	if got, want := plan.TotalBytes, int64(3*planeBytes); got != want {
		t.Fatalf("bytes = %d, want %d", got, want)
	}
	wantCost := plan.ReadCount*dev.StartupCost + plan.TotalBytes*dev.ByteCost
	if plan.Cost != wantCost {
		t.Fatalf("cost = %d, want %d", plan.Cost, wantCost)
	}
	for _, m := range plan.Mapping {
		if m.RowCount != rows || m.RowStep != 1 || m.PhysicalStep != 1 {
			t.Fatalf("bad mapping piece: %+v", m)
		}
	}
}

func TestHugeArithmeticQueryUsesRepeatSegments(t *testing.T) {
	const rows = int64(1) << 48
	a := hugeBlock(rows, 1)
	dev := Device{StartupCost: 1, ByteCost: 1, MaxBytes: 1 << 10}
	const step = int64(1) << 20
	const count = int64(1) << 28
	q := Query{
		Rows: []RowSpan{{Start: 0, Step: step, Count: count}},
		Bits: []int{0},
	}
	plan, err := a.Plan(q, dev)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Reads) != 1 {
		t.Fatalf("reads = %v, want one repeat segment", plan.Reads)
	}
	r := plan.Reads[0]
	if r.Length != 1 || r.Repeat != count || r.Stride != step/8 {
		t.Fatalf("read = %+v", r)
	}
	if plan.ReadCount != count {
		t.Fatalf("read count = %d, want %d", plan.ReadCount, count)
	}
	if plan.TotalBytes != count {
		t.Fatalf("bytes = %d, want %d", plan.TotalBytes, count)
	}
	if plan.Cost != 2*count {
		t.Fatalf("cost = %d, want %d", plan.Cost, 2*count)
	}
	if len(plan.Mapping) != 1 || plan.Mapping[0].RowCount != count || plan.Mapping[0].PhysicalStep != step {
		t.Fatalf("mapping = %+v", plan.Mapping)
	}
}

func TestPeriodicGapMerging(t *testing.T) {
	const rows = int64(1) << 30
	a := hugeBlock(rows, 1)
	const step = int64(9)
	const count = int64(1) << 24 // multiple of the 8-row byte period
	dev := Device{StartupCost: 10, ByteCost: 1, MaxBytes: 100}
	q := Query{
		Rows: []RowSpan{{Start: 0, Step: step, Count: count}},
		Bits: []int{0},
	}
	plan, err := a.Plan(q, dev)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Reads) > 2 {
		t.Fatalf("reads = %v, want compressed groups", plan.Reads)
	}
	// Each period touches 8 bytes and has a 1-byte gap to the next period.
	// Eleven periods fit in 100 bytes (98 bytes), and merging saves startup.
	if plan.Reads[0].Length != 98 {
		t.Fatalf("first read = %+v, want length 98", plan.Reads[0])
	}
	periods := count / 8
	groups := periods / 11
	rem := periods % 11
	if plan.Reads[0].Repeat != groups {
		t.Fatalf("repeat = %d, want %d", plan.Reads[0].Repeat, groups)
	}
	wantBytes := groups * 98
	if rem > 0 {
		wantBytes += 8 + (rem-1)*9
	}
	if plan.TotalBytes != wantBytes {
		t.Fatalf("bytes = %d, want %d", plan.TotalBytes, wantBytes)
	}
}

func TestHugeNullRunBoundary(t *testing.T) {
	a := &Archive{Groups: []Group{{Blocks: []Block{{
		Rows:   1000,
		Width:  4,
		Planes: []int{0, 1, 2, 3},
		Nulls: []NullRun{
			{Rows: 2, Null: false},
			{Rows: 1, Null: true},
			{Rows: 997, Null: false},
		},
	}}}}}
	plan, err := a.Plan(Query{
		Rows: []RowSpan{{Start: 0, Step: 1, Count: 6}},
		Bits: []int{0, 1, 2, 3},
	}, Device{StartupCost: 1, ByteCost: 1, MaxBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	var nullRows []int64
	for _, ns := range plan.Nulls {
		for k := int64(0); k < ns.RowCount; k++ {
			nullRows = append(nullRows, ns.RowAt(k))
		}
	}
	if len(nullRows) != 1 || nullRows[0] != 2 {
		t.Fatalf("null rows = %v, want [2]", nullRows)
	}
	// Rows before and after the null run still map to consecutive physical
	// bits within each plane.  The two pieces are split at the null run.
	var pieces []MapPiece
	for _, m := range plan.Mapping {
		if m.LogicalBit != 0 {
			continue
		}
		pieces = append(pieces, m)
	}
	if len(pieces) != 2 {
		t.Fatalf("logical bit 0 pieces = %d, want 2", len(pieces))
	}
	if pieces[0].RowStart != 0 || pieces[0].RowCount != 2 || pieces[0].PhysicalStep != 1 {
		t.Fatalf("pre-null piece = %+v", pieces[0])
	}
	if pieces[1].RowStart != 3 || pieces[1].RowCount != 3 || pieces[1].PhysicalStep != 1 {
		t.Fatalf("post-null piece = %+v", pieces[1])
	}
	if pieces[1].PhysicalStart != pieces[0].PhysicalStart+2 {
		t.Fatalf("q did not continue across null run: %d then %d",
			pieces[0].PhysicalStart, pieces[1].PhysicalStart)
	}
}

func TestPlanePermutationSwap(t *testing.T) {
	a := &Archive{Groups: []Group{{Blocks: []Block{{
		Rows:   3,
		Width:  2,
		Planes: []int{1, 0},
		Nulls:  []NullRun{{Rows: 3, Null: false}},
	}}}}}
	values := [][]uint64{{0b01, 0b10, 0b11}}
	data := buildPhysical(t, a, values)
	plan, err := a.Plan(Query{
		Rows: []RowSpan{{Start: 0, Step: 1, Count: 3}},
		Bits: []int{0, 1},
	}, Device{StartupCost: 1, ByteCost: 1, MaxBytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range plan.Mapping {
		for k := int64(0); k < 3; k++ {
			got := getBit(data, m.PhysicalBitAt(k))
			want := values[0][k]&(1<<uint(m.LogicalBit)) != 0
			if got != want {
				t.Fatalf("plane swap: bit %d row %d = %v, want %v", m.LogicalBit, k, got, want)
			}
		}
	}
}

func TestHugeRepeatReadSplitAtGapByte(t *testing.T) {
	const rows = int64(1) << 30
	a := hugeBlock(rows, 1)
	const step = int64(9)
	const count = int64(1) << 24
	dev := Device{
		StartupCost: 10,
		ByteCost:    1,
		MaxBytes:    100,
		Bad:         []ByteInterval{{Start: 89, Length: 1}},
	}
	plan, err := a.Plan(Query{
		Rows: []RowSpan{{Start: 0, Step: step, Count: count}},
		Bits: []int{0},
	}, dev)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Reads) > 4 {
		t.Fatalf("bad-byte split expanded the plan: %v", plan.Reads)
	}
	for _, r := range plan.Reads {
		if readIntersectsBad(r, mustMergeBad(t, dev.Bad)) {
			t.Fatalf("read %+v still intersects bad byte", r)
		}
		// No transaction may contain byte 89.
		for k := int64(0); k < r.Repeat; k++ {
			iv := r.Interval(k)
			if iv.Start <= 89 && 89 < iv.End() {
				t.Fatalf("transaction %d intersects bad byte: %+v", k, iv)
			}
		}
	}
	// A bad byte that is itself required makes the query infeasible.
	dev.Bad = []ByteInterval{{Start: 90, Length: 1}}
	_, err = a.Plan(Query{
		Rows: []RowSpan{{Start: 0, Step: step, Count: count}},
		Bits: []int{0},
	}, dev)
	if err == nil {
		t.Fatal("expected infeasible query for required byte inside bad region")
	}
}

func TestHugeArithmeticTailStaysCompressed(t *testing.T) {
	const rows = int64(1) << 42
	a := hugeBlock(rows, 1)
	const step = int64(9)
	const fullPeriods = int64(1) << 20
	const count = 8*fullPeriods + 3
	dev := Device{StartupCost: 10, ByteCost: 1, MaxBytes: 100}
	plan, err := a.Plan(Query{
		Rows: []RowSpan{{Start: 0, Step: step, Count: count}},
		Bits: []int{0},
	}, dev)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Reads) > 8 {
		t.Fatalf("tail expanded the plan: %v", plan.Reads)
	}
	if len(plan.Mapping) != 1 || plan.Mapping[0].RowCount != count {
		t.Fatalf("mapping = %+v", plan.Mapping)
	}
	wantCost, wantBytes, wantCount, err := totals(plan.Reads, dev)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Cost != wantCost || plan.TotalBytes != wantBytes || plan.ReadCount != wantCount {
		t.Fatalf("totals mismatch: plan %d/%d/%d reads %d/%d/%d",
			plan.Cost, plan.TotalBytes, plan.ReadCount, wantCost, wantBytes, wantCount)
	}
	// The first full periods and the three-row tail must all be covered.
	// Total required bytes are 8*fullPeriods + 3; joining over the one-byte
	// period gaps may add at most a handful of bytes at boundaries.
	if plan.TotalBytes < count {
		t.Fatalf("bytes = %d, less than required %d", plan.TotalBytes, count)
	}
}

func TestMultiplePlanePeriodicSegments(t *testing.T) {
	const rows = int64(1) << 20
	a := hugeBlock(rows, 3)
	const step = int64(9)
	const count = int64(10003 * 8)
	dev := Device{StartupCost: 7, ByteCost: 2, MaxBytes: 97}
	plan, err := a.Plan(Query{
		Rows: []RowSpan{{Start: 0, Step: step, Count: count}},
		Bits: []int{0, 1, 2},
	}, dev)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Mapping) != 3 {
		t.Fatalf("mapping = %d pieces", len(plan.Mapping))
	}
	if len(plan.Reads) > 64 {
		t.Fatalf("plan unexpectedly large: %d items", len(plan.Reads))
	}
	wantCost, wantBytes, wantCount, err := totals(plan.Reads, dev)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Cost != wantCost || plan.TotalBytes != wantBytes || plan.ReadCount != wantCount {
		t.Fatalf("totals mismatch")
	}
}

func TestUniformCompressedMatchesExactPlan(t *testing.T) {
	a := hugeBlock(10000, 2)
	devices := []Device{
		{StartupCost: 10, ByteCost: 1, MaxBytes: 100},
		{StartupCost: 3, ByteCost: 5, MaxBytes: 17},
		{StartupCost: 100, ByteCost: 1, MaxBytes: 50},
		{StartupCost: 1, ByteCost: 1, MaxBytes: 9},
	}
	// These steps produce one uniform run per period, for which the compact
	// planner has an exact arithmetic solver.
	steps := []int64{2, 3, 4, 5, 6, 7, 8, 9, 10}
	for _, step := range steps {
		count := int64(400)
		q := Query{
			Rows: []RowSpan{{Start: 0, Step: step, Count: count}},
			Bits: []int{0, 1},
		}
		for di, dev := range devices {
			exact, err := (&Planner{Device: dev, ExactLimit: 1 << 20}).Plan(a, q)
			if err != nil {
				t.Fatalf("step %d dev %d exact: %v", step, di, err)
			}
			comp, err := (&Planner{Device: dev, ExactLimit: 1}).Plan(a, q)
			if err != nil {
				t.Fatalf("step %d dev %d compressed: %v", step, di, err)
			}
			if exact.Cost != comp.Cost || exact.TotalBytes != comp.TotalBytes || exact.ReadCount != comp.ReadCount {
				t.Fatalf("step %d dev %d: exact=%d/%d/%d compressed=%d/%d/%d",
					step, di,
					exact.Cost, exact.TotalBytes, exact.ReadCount,
					comp.Cost, comp.TotalBytes, comp.ReadCount)
			}
		}
	}
}

func TestCompressedTailPlanIsValidAndCompact(t *testing.T) {
	const rows = int64(1) << 42
	a := hugeBlock(rows, 2)
	const step = int64(11)
	const count = int64(1)<<30 + 3
	dev := Device{StartupCost: 10, ByteCost: 1, MaxBytes: 100}
	plan, err := a.Plan(Query{
		Rows: []RowSpan{{Start: 0, Step: step, Count: count}},
		Bits: []int{0, 1},
	}, dev)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Reads) > 64 {
		t.Fatalf("tail plan expanded: %d items", len(plan.Reads))
	}
	wantCost, wantBytes, wantCount, err := totals(plan.Reads, dev)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Cost != wantCost || plan.TotalBytes != wantBytes || plan.ReadCount != wantCount {
		t.Fatal("plan totals are inconsistent")
	}
}

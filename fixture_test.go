package colarchive

import (
	"errors"
	"testing"
)

func setBit(buf []byte, off int64, value bool) {
	b := off >> 3
	mask := byte(1) << uint(7-(off&7))
	if value {
		buf[b] |= mask
	} else {
		buf[b] &^= mask
	}
}

func getBit(buf []byte, off int64) bool {
	return buf[off>>3]&(byte(1)<<uint(7-(off&7))) != 0
}

// buildPhysical constructs the archive bit by bit directly from the format
// description.  It deliberately does not call Archive.Layout: the tests use
// it as an independent physical oracle.
func buildPhysical(t *testing.T, a *Archive, values [][]uint64) []byte {
	t.Helper()
	buf := make([]byte, a.BaseOffset)
	for i := range buf {
		buf[i] = 0xFF
	}
	ensure := func(n int64) {
		for int64(len(buf)) < n {
			buf = append(buf, 0xFF)
		}
	}
	startByte := a.BaseOffset
	globalBlock := 0
	for gi := range a.Groups {
		var dataBits int64
		bitCursor := startByte * 8
		for bi := range a.Groups[gi].Blocks {
			b := &a.Groups[gi].Blocks[bi]
			var nn int64
			for _, r := range b.Nulls {
				if !r.Null {
					nn += r.Rows
				}
			}
			q := int64(0)
			for row := int64(0); row < b.Rows; row++ {
				null := false
				var at int64
				for _, r := range b.Nulls {
					if row < at+r.Rows {
						null = r.Null
						break
					}
					at += r.Rows
				}
				if null {
					continue
				}
				v := values[globalBlock][row]
				for p, logical := range b.Planes {
					off := bitCursor + int64(p)*nn + q
					ensure((off >> 3) + 1)
					setBit(buf, off, v&(uint64(1)<<uint(logical)) != 0)
				}
				q++
			}
			planeBits := int64(b.Width) * nn
			dataBits += planeBits
			bitCursor += planeBits
			globalBlock++
		}
		startByte = (startByte + (dataBits+7)/8 + 7) &^ 7
	}
	ensure(startByte)
	return buf
}

func TestPhysicalFixtureMappingAndPadding(t *testing.T) {
	a := &Archive{
		Groups: []Group{
			{Blocks: []Block{
				{
					Rows:  5,
					Width: 3,
					// physical plane 0 -> logical 2, plane 1 -> logical 0,
					// plane 2 -> logical 1.
					Planes: []int{2, 0, 1},
					Nulls: []NullRun{
						{Rows: 1, Null: false},
						{Rows: 1, Null: true},
						{Rows: 1, Null: false},
						{Rows: 1, Null: true},
						{Rows: 1, Null: false},
					},
				},
				{
					Rows:   2,
					Width:  2,
					Planes: []int{1, 0},
					Nulls:  []NullRun{{Rows: 2, Null: false}},
				},
			}},
			{Blocks: []Block{{
				Rows:   1,
				Width:  1,
				Planes: []int{0},
				Nulls:  []NullRun{{Rows: 1, Null: false}},
			}}},
		},
	}
	values := [][]uint64{
		{0b101, 0, 0b110, 0, 0b011},
		{0b10, 0b01},
		{1},
	}
	data := buildPhysical(t, a, values)
	layout, err := a.Layout()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := layout.TotalRows, int64(8); got != want {
		t.Fatalf("total rows = %d, want %d", got, want)
	}
	if len(data) != 16 {
		t.Fatalf("fixture bytes = %d, want 16", len(data))
	}

	// Bad bytes cover group-0 tail padding and group-1 tail padding.  Reads
	// must never enter them, and padding is deliberately all ones in the file.
	dev := Device{
		StartupCost: 3,
		ByteCost:    1,
		MaxBytes:    4,
		Bad: []ByteInterval{
			{Start: 2, Length: 6}, // group 0 data ends in byte 2
			{Start: 9, Length: 7}, // group 1 data ends in byte 9
		},
	}

	queries := []Query{
		{Rows: []RowSpan{{Start: 0, Step: 1, Count: 5}}, Bits: []int{0, 1, 2}},
		{Rows: []RowSpan{{Start: 5, Step: 1, Count: 2}}, Bits: []int{0, 1}},
		{Rows: []RowSpan{{Start: 7, Step: 1, Count: 1}}, Bits: []int{0}},
	}
	for qi, q := range queries {
		plan, err := a.Plan(q, dev)
		if err != nil {
			t.Fatalf("query %d: %v", qi, err)
		}
		reads, err := plan.Expand(1 << 20)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range reads {
			if badIntersects(r.Start, r.End(), mustMergeBad(t, dev.Bad)) {
				t.Fatalf("query %d: read [%d,%d) intersects bad/padding", qi, r.Start, r.End())
			}
		}
		for _, m := range plan.Mapping {
			if m.Null {
				t.Fatal("null map pieces are represented by NullSpans")
			}
			for k := int64(0); k < m.RowCount; k++ {
				row := m.RowAt(k)
				bit := m.LogicalBit
				got := getBit(data, m.PhysicalBitAt(k))
				bl, _, err := layout.blockAt(m.Block)
				if err != nil {
					t.Fatal(err)
				}
				want := values[m.Block][row-bl.RowBase]&(uint64(1)<<uint(bit)) != 0
				if got != want {
					t.Fatalf("query %d block %d row %d bit %d: physical bit %d = %v, want %v",
						qi, m.Block, row, bit, m.PhysicalBitAt(k), got, want)
				}
				refs := plan.LogicalAt(m.PhysicalBitAt(k))
				found := false
				for _, ref := range refs {
					if ref.Block == m.Block && ref.Row == row && ref.LogicalBit == bit {
						found = true
					}
				}
				if !found {
					t.Fatalf("reverse mapping missing block %d row %d bit %d", m.Block, row, bit)
				}
			}
		}
		// Query 0 must identify the two null rows.
		if qi == 0 {
			var nullRows []int64
			for _, ns := range plan.Nulls {
				for k := int64(0); k < ns.RowCount; k++ {
					nullRows = append(nullRows, ns.RowAt(k))
				}
			}
			if len(nullRows) != 2 || nullRows[0] != 1 || nullRows[1] != 3 {
				t.Fatalf("null rows = %v, want [1 3]", nullRows)
			}
		}
	}
}

func mustMergeBad(t *testing.T, in []ByteInterval) []ByteInterval {
	t.Helper()
	out, err := mergeByteIntervals(in)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestExactPlannerMatchesBruteForce(t *testing.T) {
	cases := []struct {
		runs []ByteInterval
		dev  Device
	}{
		{
			runs: []ByteInterval{{Start: 0, Length: 10}, {Start: 20, Length: 10}},
			dev:  Device{StartupCost: 5, ByteCost: 1, MaxBytes: 100},
		},
		{
			runs: []ByteInterval{{Start: 0, Length: 10}, {Start: 20, Length: 10}},
			dev:  Device{StartupCost: 20, ByteCost: 1, MaxBytes: 100},
		},
		{
			runs: []ByteInterval{{Start: 3, Length: 25}, {Start: 40, Length: 7}, {Start: 55, Length: 2}},
			dev:  Device{StartupCost: 7, ByteCost: 2, MaxBytes: 10},
		},
		{
			runs: []ByteInterval{{Start: 0, Length: 1}, {Start: 2, Length: 1}, {Start: 4, Length: 1}, {Start: 6, Length: 1}},
			dev:  Device{StartupCost: 0, ByteCost: 1, MaxBytes: 3},
		},
		{
			// Equal cost: joining pays the 10-byte gap and saves one startup.
			// Fewer bytes therefore wins and the runs stay separate.
			runs: []ByteInterval{{Start: 0, Length: 10}, {Start: 20, Length: 10}},
			dev:  Device{StartupCost: 10, ByteCost: 1, MaxBytes: 100},
		},
	}
	for ci, tc := range cases {
		got, err := planRunsExact(tc.runs, tc.dev, nil)
		if err != nil {
			t.Fatalf("case %d: %v", ci, err)
		}
		gotCost, gotBytes, gotCount, err := totals(got, tc.dev)
		if err != nil {
			t.Fatal(err)
		}
		best := bruteForceRuns(tc.runs, tc.dev)
		if gotCost != best.cost || gotBytes != best.bytes || gotCount != best.count {
			t.Fatalf("case %d: got cost/bytes/count %d/%d/%d, want %d/%d/%d\nreads=%v",
				ci, gotCost, gotBytes, gotCount, best.cost, best.bytes, best.count, got)
		}
		assertCovered(t, tc.runs, got, tc.dev)
	}
}

type bruteResult struct {
	cost, bytes, count int64
	splits             []int
}

// bruteForceRuns enumerates every partition of the required runs into
// contiguous groups.  For a group, the optimal number of transactions is
// ceil(span/MaxBytes) and the bytes read are the whole group span.
func bruteForceRuns(runs []ByteInterval, d Device) bruteResult {
	best := bruteResult{cost: -1}
	n := len(runs)
	for mask := 0; mask < (1 << uint(n-1)); mask++ {
		start := 0
		var cost, bytes, count int64
		var splits []int
		for i := 1; i <= n; i++ {
			if i < n && mask&(1<<uint(i-1)) == 0 {
				continue
			}
			span := runs[i-1].End() - runs[start].Start
			k := ceilDiv64(span, d.MaxBytes)
			cost += k*d.StartupCost + d.ByteCost*span
			bytes += span
			count += k
			splits = append(splits, i)
			start = i
		}
		if best.cost < 0 || cost < best.cost || (cost == best.cost && bytes < best.bytes) {
			best = bruteResult{cost: cost, bytes: bytes, count: count, splits: splits}
		}
	}
	return best
}

func assertCovered(t *testing.T, required []ByteInterval, reads []Read, d Device) {
	t.Helper()
	expanded, err := expandReads(reads, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := mergeByteIntervals(expanded)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range required {
		pos := r.Start
		for pos < r.End() {
			found := false
			for _, m := range merged {
				if m.Start <= pos && pos < m.End() {
					pos = m.End()
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("required byte %d not covered", pos)
			}
		}
	}
}

func expandReads(reads []Read, limit int64) ([]ByteInterval, error) {
	var out []ByteInterval
	var n int64
	for _, r := range reads {
		n += r.Repeat
		if n > limit {
			return nil, errors.New("too many")
		}
		for k := int64(0); k < r.Repeat; k++ {
			out = append(out, r.Interval(k))
		}
	}
	return out, nil
}

func TestBadRegionsAndInfeasible(t *testing.T) {
	runs := []ByteInterval{{Start: 0, Length: 10}, {Start: 20, Length: 10}}
	dev := Device{StartupCost: 100, ByteCost: 1, MaxBytes: 100, Bad: []ByteInterval{{Start: 12, Length: 4}}}
	reads, err := planRunsExact(runs, dev, mustMergeBad(t, dev.Bad))
	if err != nil {
		t.Fatal(err)
	}
	if len(reads) != 2 {
		t.Fatalf("reads = %v, want two separate reads", reads)
	}
	for _, r := range reads {
		if badIntersects(r.Start, r.End(), dev.Bad) {
			t.Fatalf("read crosses bad region: %v", r)
		}
	}
	inside := Device{StartupCost: 100, ByteCost: 1, MaxBytes: 100, Bad: []ByteInterval{{Start: 5, Length: 3}}}
	_, err = planRunsExact([]ByteInterval{{Start: 0, Length: 10}}, inside, mustMergeBad(t, inside.Bad))
	if !errors.Is(err, ErrInfeasible) {
		t.Fatalf("intersecting required run error = %v, want ErrInfeasible", err)
	}
}

func TestLongContiguousRunIsCompressed(t *testing.T) {
	runs := []ByteInterval{{Start: 100, Length: 25}}
	dev := Device{StartupCost: 1, ByteCost: 1, MaxBytes: 10}
	reads, err := planRunsExact(runs, dev, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(reads) != 2 {
		t.Fatalf("reads = %v, want compressed first short + repeated max", reads)
	}
	if reads[0].Start != 100 || reads[0].Length != 5 || reads[0].Repeat != 1 {
		t.Fatalf("first read = %+v", reads[0])
	}
	if reads[1].Start != 105 || reads[1].Length != 10 || reads[1].Repeat != 2 || reads[1].Stride != 10 {
		t.Fatalf("second read = %+v", reads[1])
	}
}

func TestQueryAcrossBlocks(t *testing.T) {
	a := &Archive{Groups: []Group{{Blocks: []Block{
		{
			Rows:   3,
			Width:  2,
			Planes: []int{0, 1},
			Nulls:  []NullRun{{Rows: 3, Null: false}},
		},
		{
			Rows:   2,
			Width:  2,
			Planes: []int{1, 0},
			Nulls:  []NullRun{{Rows: 2, Null: false}},
		},
	}}}}
	values := [][]uint64{{0b00, 0b01, 0b10}, {0b11, 0b10}}
	data := buildPhysical(t, a, values)
	plan, err := a.Plan(Query{
		Rows: []RowSpan{{Start: 0, Step: 1, Count: 5}},
		Bits: []int{0, 1},
	}, Device{StartupCost: 1, ByteCost: 1, MaxBytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	layout, err := a.Layout()
	if err != nil {
		t.Fatal(err)
	}
	seenRows := map[int64]bool{}
	for _, m := range plan.Mapping {
		for k := int64(0); k < m.RowCount; k++ {
			row := m.RowAt(k)
			bl, _, err := layout.blockAt(m.Block)
			if err != nil {
				t.Fatal(err)
			}
			got := getBit(data, m.PhysicalBitAt(k))
			want := values[m.Block][row-bl.RowBase]&(1<<uint(m.LogicalBit)) != 0
			if got != want {
				t.Fatalf("block %d row %d bit %d: got %v want %v", m.Block, row, m.LogicalBit, got, want)
			}
			seenRows[row] = true
		}
	}
	if len(seenRows) != 5 {
		t.Fatalf("covered rows = %v", seenRows)
	}
}

func TestQueryRowOutOfRange(t *testing.T) {
	a := &Archive{Groups: []Group{{Blocks: []Block{{
		Rows:   2,
		Width:  1,
		Planes: []int{0},
		Nulls:  []NullRun{{Rows: 2, Null: false}},
	}}}}}
	_, err := a.Plan(Query{
		Rows: []RowSpan{{Start: 1, Step: 2, Count: 2}},
		Bits: []int{0},
	}, Device{StartupCost: 1, ByteCost: 1, MaxBytes: 8})
	if !errors.Is(err, ErrRowOutOfRange) {
		t.Fatalf("error = %v, want ErrRowOutOfRange", err)
	}
}

func TestExactPlannerRandomPartitions(t *testing.T) {
	seed := uint64(0x1234abcd)
	next := func(n int64) int64 {
		seed = seed*6364136223846793005 + 1442695040888963407
		return int64(seed % uint64(n))
	}
	for iter := 0; iter < 200; iter++ {
		n := int(next(7)) + 1
		runs := make([]ByteInterval, 0, n)
		pos := next(5)
		for i := 0; i < n; i++ {
			length := next(9) + 1
			runs = append(runs, ByteInterval{Start: pos, Length: length})
			pos += length + next(10)
		}
		dev := Device{
			StartupCost: next(20) + 1,
			ByteCost:    next(4),
			MaxBytes:    next(12) + 1,
		}
		got, err := planRunsExact(runs, dev, nil)
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		gotCost, gotBytes, gotCount, err := totals(got, dev)
		if err != nil {
			t.Fatal(err)
		}
		want := bruteForceRuns(runs, dev)
		if gotCost != want.cost || gotBytes != want.bytes || gotCount != want.count {
			t.Fatalf("iter %d runs=%v dev=%+v: got %d/%d/%d want %d/%d/%d",
				iter, runs, dev, gotCost, gotBytes, gotCount, want.cost, want.bytes, want.count)
		}
		assertCovered(t, runs, got, dev)
	}
}

func TestNullRunEdgeWithArithmeticRows(t *testing.T) {
	a := &Archive{Groups: []Group{{Blocks: []Block{{
		Rows:   6,
		Width:  1,
		Planes: []int{0},
		Nulls: []NullRun{
			{Rows: 2, Null: false},
			{Rows: 2, Null: true},
			{Rows: 2, Null: false},
		},
	}}}}}
	plan, err := a.Plan(Query{
		Rows: []RowSpan{{Start: 0, Step: 2, Count: 3}},
		Bits: []int{0},
	}, Device{StartupCost: 1, ByteCost: 1, MaxBytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Nulls) != 1 || plan.Nulls[0].RowStart != 2 || plan.Nulls[0].RowStep != 2 || plan.Nulls[0].RowCount != 1 {
		t.Fatalf("nulls = %+v", plan.Nulls)
	}
	if len(plan.Mapping) != 2 {
		t.Fatalf("mapping = %+v", plan.Mapping)
	}
	if plan.Mapping[0].RowStart != 0 || plan.Mapping[0].PhysicalStart != 0 {
		t.Fatalf("pre-null mapping = %+v", plan.Mapping[0])
	}
	if plan.Mapping[1].RowStart != 4 || plan.Mapping[1].PhysicalStart != 2 {
		t.Fatalf("post-null mapping = %+v", plan.Mapping[1])
	}
}

func TestTailPaddingAdjacentToBadRegion(t *testing.T) {
	a := &Archive{Groups: []Group{{Blocks: []Block{{
		Rows:   5,
		Width:  3,
		Planes: []int{0, 1, 2},
		Nulls:  []NullRun{{Rows: 5, Null: false}},
	}}}}}
	values := [][]uint64{{1, 2, 3, 4, 5}}
	data := buildPhysical(t, a, values)
	if len(data) != 8 {
		t.Fatalf("fixture length = %d, want 8", len(data))
	}
	// Data occupies bits 0..14, so padding is bytes 2..7.  The bad region is
	// immediately after the group.
	dev := Device{
		StartupCost: 100,
		ByteCost:    1,
		MaxBytes:    64,
		Bad:         []ByteInterval{{Start: 8, Length: 8}},
	}
	plan, err := a.Plan(Query{
		Rows: []RowSpan{{Start: 0, Step: 1, Count: 5}},
		Bits: []int{0, 1, 2},
	}, dev)
	if err != nil {
		t.Fatal(err)
	}
	reads, err := plan.Expand(1 << 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range reads {
		if r.End() > 2 {
			t.Fatalf("read %v enters tail padding", r)
		}
		if badIntersects(r.Start, r.End(), mustMergeBad(t, dev.Bad)) {
			t.Fatalf("read %v enters bad region", r)
		}
	}
	for _, m := range plan.Mapping {
		for k := int64(0); k < m.RowCount; k++ {
			got := getBit(data, m.PhysicalBitAt(k))
			want := values[0][m.RowAt(k)]&(1<<uint(m.LogicalBit)) != 0
			if got != want {
				t.Fatalf("block row %d bit %d mismatch", m.RowAt(k), m.LogicalBit)
			}
		}
	}
}

func TestBaseOffsetAndAbsoluteGroupAlignment(t *testing.T) {
	a := &Archive{BaseOffset: 5, Groups: []Group{{Blocks: []Block{{
		Rows:   1,
		Width:  1,
		Planes: []int{0},
		Nulls:  []NullRun{{Rows: 1, Null: false}},
	}}}}}
	values := [][]uint64{{1}}
	data := buildPhysical(t, a, values)
	if len(data) != 8 {
		t.Fatalf("fixture length = %d, want absolute alignment at 8", len(data))
	}
	layout, err := a.Layout()
	if err != nil {
		t.Fatal(err)
	}
	if layout.Groups[0].StartByte != 5 || layout.Groups[0].EndByte != 8 {
		t.Fatalf("group bytes = [%d,%d), want [5,8)", layout.Groups[0].StartByte, layout.Groups[0].EndByte)
	}
	plan, err := a.Plan(Query{
		Rows: []RowSpan{{Start: 0, Step: 1, Count: 1}},
		Bits: []int{0},
	}, Device{StartupCost: 1, ByteCost: 1, MaxBytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Mapping) != 1 || plan.Mapping[0].PhysicalStart != 40 {
		t.Fatalf("mapping = %+v, want physical bit 40", plan.Mapping)
	}
	if len(plan.Reads) != 1 || plan.Reads[0].Start != 5 || plan.Reads[0].Length != 1 {
		t.Fatalf("reads = %+v, want byte 5", plan.Reads)
	}
	if !getBit(data, plan.Mapping[0].PhysicalStart) {
		t.Fatal("fixture bit was not constructed at base offset")
	}
}

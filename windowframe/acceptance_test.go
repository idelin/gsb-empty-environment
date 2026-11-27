package windowframe

import (
	"fmt"
	"math/rand"
	"testing"
)

func valueString(v Value) string {
	switch v.kind {
	case KindNull:
		return "NULL"
	case KindInt:
		return fmt.Sprintf("int(%d)", v.i)
	case KindFloat:
		return fmt.Sprintf("float(%v)", v.f)
	case KindBool:
		return fmt.Sprintf("bool(%t)", v.b)
	default:
		return fmt.Sprintf("str(%q)", v.s)
	}
}

type columnGen struct {
	kind Kind
	null float64
}

func (g columnGen) make(rng *rand.Rand) Value {
	if rng.Float64() < g.null {
		return Null()
	}
	switch g.kind {
	case KindInt:
		return Int(int64(rng.Intn(7) - 3))
	case KindFloat:
		return Float(float64(rng.Intn(9)-4) + 0.5)
	case KindBool:
		return Bool(rng.Intn(2) == 1)
	default:
		return String([]string{"a", "b", "c", "d"}[rng.Intn(4)])
	}
}

func randomRows(rng *rand.Rand) ([][]Value, []columnGen) {
	ncols := 3 + rng.Intn(3)
	gens := make([]columnGen, ncols)
	for i := range gens {
		switch rng.Intn(4) {
		case 0:
			gens[i] = columnGen{kind: KindFloat, null: 0.25}
		case 1:
			gens[i] = columnGen{kind: KindBool, null: 0.2}
		case 2:
			gens[i] = columnGen{kind: KindString, null: 0.2}
		default:
			gens[i] = columnGen{kind: KindInt, null: 0.3}
		}
	}
	nrows := rng.Intn(41)
	rows := make([][]Value, nrows)
	for r := range rows {
		row := make([]Value, ncols)
		for c, g := range gens {
			row[c] = g.make(rng)
		}
		rows[r] = row
	}
	return rows, gens
}

func randomOrder(rng *rand.Rand, ncols int) []OrderSpec {
	k := rng.Intn(3)
	cols := rng.Perm(ncols)
	orders := make([]OrderSpec, k)
	for i := 0; i < k; i++ {
		dir := Asc
		if rng.Intn(2) == 1 {
			dir = Desc
		}
		var nulls NullsOrder
		switch rng.Intn(3) {
		case 0:
			nulls = NullsDefault
		case 1:
			nulls = NullsFirst
		default:
			nulls = NullsLast
		}
		orders[i] = OrderSpec{Column: cols[i], Direction: dir, Nulls: nulls}
	}
	return orders
}

func randomBound(rng *rand.Rand, allowValue bool, maxOff int64) FrameBound {
	if !allowValue {
		switch rng.Intn(2) {
		case 0:
			return UnboundedPreceding()
		default:
			return CurrentRow()
		}
	}
	switch rng.Intn(3) {
	case 0:
		return UnboundedPreceding()
	case 1:
		return CurrentRow()
	default:
		return Preceding(Int(rng.Int63n(maxOff + 1)))
	}
}

func randomEndBound(rng *rand.Rand, allowValue bool, maxOff int64) FrameBound {
	if !allowValue {
		if rng.Intn(2) == 0 {
			return UnboundedFollowing()
		}
		return CurrentRow()
	}
	switch rng.Intn(3) {
	case 0:
		return UnboundedFollowing()
	case 1:
		return CurrentRow()
	default:
		return Following(Int(rng.Int63n(maxOff + 1)))
	}
}

func randomFrame(rng *rand.Rand, order []OrderSpec, gens []columnGen) *FrameSpec {
	if rng.Intn(4) == 0 {
		return nil
	}
	mode := FrameMode(rng.Intn(3))
	if len(order) == 0 {
		if mode != RowsFrame {
			coin := rng.Intn(2)
			if coin == 0 {
				mode = RowsFrame
			} else {
				frame := &FrameSpec{
					Mode:      RangeFrame,
					Start:     UnboundedPreceding(),
					End:       boundPtr(UnboundedFollowing()),
					Exclusion: Exclusion(rng.Intn(4)),
				}
				return frame
			}
		}
	}
	frame := &FrameSpec{Mode: mode, Exclusion: Exclusion(rng.Intn(4))}

	valueAllowed := true
	switch mode {
	case RowsFrame:
		valueAllowed = true
	case GroupsFrame:
		valueAllowed = true
	case RangeFrame:
		switch {
		case len(order) > 1:
			valueAllowed = false
		default:
			valueAllowed = gens[order[0].Column].kind == KindInt || gens[order[0].Column].kind == KindFloat
		}
	}
	maxOff := int64(5)
	if mode == GroupsFrame {
		maxOff = 3
	}
	frame.Start = randomBound(rng, valueAllowed, maxOff)
	if rng.Intn(2) == 1 {
		end := randomEndBound(rng, valueAllowed, maxOff)
		frame.End = &end
	}
	return frame
}

func numericColumn(gens []columnGen) int {
	for i, g := range gens {
		if g.kind == KindInt || g.kind == KindFloat {
			return i
		}
	}
	return -1
}

func trialFunctions(rng *rand.Rand, ncols int, gens []columnGen) []FuncExpr {
	num := numericColumn(gens)
	other := (num + 1) % ncols
	fns := []FuncExpr{
		{Func: FuncCount, CountAll: true},
		{Func: FuncCount, Column: other},
		{Func: FuncMin, Column: other},
		{Func: FuncMax, Column: other},
		{Func: FuncFirstValue, Column: other},
		{Func: FuncLastValue, Column: other},
		{Func: FuncFirstValue, Column: other, IgnoreNulls: true},
		{Func: FuncLastValue, Column: other, IgnoreNulls: true},
		{Func: FuncNthValue, Column: other, N: 1 + rng.Intn(3)},
		{Func: FuncNthValue, Column: other, N: 1 + rng.Intn(3), IgnoreNulls: true},
		{Func: FuncRowNumber},
		{Func: FuncRank},
		{Func: FuncDenseRank},
		{Func: FuncNtile, N: 1 + rng.Intn(4)},
	}
	if num >= 0 {
		numeric := []FuncExpr{
			{Func: FuncSum, Column: num},
			{Func: FuncAvg, Column: num},
			{Func: FuncMin, Column: num},
			{Func: FuncMax, Column: num},
		}
		fns = append(fns, numeric...)
	}
	off := 1 + rng.Intn(3)
	fns = append(fns,
		FuncExpr{Func: FuncLag, Column: other, Offset: off},
		FuncExpr{Func: FuncLead, Column: other, Offset: off},
		FuncExpr{Func: FuncLag, Column: other, Offset: off, IgnoreNulls: true},
		FuncExpr{Func: FuncLead, Column: other, Offset: off, IgnoreNulls: true},
		FuncExpr{Func: FuncLag, Column: other, Offset: off, HasDefault: true, Default: Int(99)},
		FuncExpr{Func: FuncLead, Column: other, Offset: off, HasDefault: true, Default: String("dflt")},
	)
	return fns
}

func TestRandomMatchesBruteForceReference(t *testing.T) {
	const trials = 1000
	for trial := 0; trial < trials; trial++ {
		seed := int64(1000 + trial*97)
		rng := rand.New(rand.NewSource(seed))
		rows, gens := randomRows(rng)
		ncols := len(gens)

		var partitionCols []int
		if rng.Intn(2) == 1 && ncols > 0 {
			partitionCols = rng.Perm(ncols)[:1+rng.Intn(2)]
		}
		order := randomOrder(rng, ncols)
		frame := randomFrame(rng, order, gens)
		spec := WindowSpec{PartitionColumns: partitionCols, Order: order, Frame: frame}

		for _, fn := range trialFunctions(rng, ncols, gens) {
			got, err := Evaluate(rows, spec, fn)
			if err != nil {
				t.Fatalf("seed=%d fn=%d unexpected error: %v", seed, fn.Func, err)
			}
			want := refEvaluate(rows, spec, fn)
			if len(got) != len(want) {
				t.Fatalf("seed=%d fn=%d length got %d want %d", seed, fn.Func, len(got), len(want))
			}
			for i := range got {
				if !Equal(got[i], want[i]) {
					t.Fatalf("seed=%d fn=%+v row=%d got %s want %s\nspec=%+v frame=%+v\nrows=%v",
						seed, fn, i, valueString(got[i]), valueString(want[i]), spec, frame, dumpRows(rows))
				}
			}
		}
	}
}

func dumpRows(rows [][]Value) string {
	out := ""
	for i, r := range rows {
		out += fmt.Sprintf("\n  %d:", i)
		for _, v := range r {
			out += " " + valueString(v)
		}
	}
	return out
}

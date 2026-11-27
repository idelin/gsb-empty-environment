package windowframe

import "errors"

// Evaluate runs fn over rows using spec and returns one output Value per input
// row, indexed by the original row position.
func Evaluate(rows [][]Value, spec WindowSpec, fn FuncExpr) ([]Value, error) {
	numColumns := 0
	for _, row := range rows {
		if len(row) > numColumns {
			numColumns = len(row)
		}
	}
	if len(rows) == 0 {
		return []Value{}, nil
	}
	if err := spec.Validate(numColumns); err != nil {
		return nil, err
	}
	if err := fn.Validate(numColumns); err != nil {
		return nil, err
	}
	results := make([]Value, len(rows))
	if len(rows) == 0 {
		return results, nil
	}
	frame := resolveFrame(spec)
	partitions := buildPartitions(rows, spec.PartitionColumns)

	if isFrameIndependent(fn.Func) {
		for _, indices := range partitions {
			p, err := preparePartition(rows, indices, spec.Order)
			if err != nil {
				return nil, err
			}
			if err := evaluateIndependent(p, fn, results); err != nil {
				return nil, err
			}
		}
		return results, nil
	}

	for _, indices := range partitions {
		p, err := preparePartition(rows, indices, spec.Order)
		if err != nil {
			return nil, err
		}
		for cur := range p.entries {
			mask, err := p.frameMask(spec, frame, cur)
			if err != nil {
				return nil, err
			}
			out, err := evaluateFrameFunction(p, mask, cur, fn)
			if err != nil {
				return nil, err
			}
			results[p.entries[cur].rowIndex] = out
		}
	}
	return results, nil
}

func isFrameIndependent(kind FuncKind) bool {
	switch kind {
	case FuncRowNumber, FuncRank, FuncDenseRank, FuncNtile, FuncLag, FuncLead:
		return true
	default:
		return false
	}
}

func evaluateIndependent(p partition, fn FuncExpr, results []Value) error {
	n := len(p.entries)
	switch fn.Func {
	case FuncRowNumber:
		for i := range p.entries {
			results[p.entries[i].rowIndex] = Int(int64(i + 1))
		}
	case FuncRank:
		for i := range p.entries {
			results[p.entries[i].rowIndex] = Int(int64(p.groups[p.groupOf[i]][0] + 1))
		}
	case FuncDenseRank:
		for i := range p.entries {
			results[p.entries[i].rowIndex] = Int(int64(p.groupOf[i] + 1))
		}
	case FuncNtile:
		for i := range p.entries {
			results[p.entries[i].rowIndex] = Int(ntileBucket(i, n, fn.N))
		}
	case FuncLag, FuncLead:
		return evaluateLagLead(p, fn, results)
	}
	return nil
}

// ntileBucket returns the 1-based bucket of sorted position i for n rows and
// b buckets, giving the earlier buckets the extra rows when n is not divisible.
func ntileBucket(i, n, b int) int64 {
	base := n / b
	rem := n % b
	cut := rem * (base + 1)
	if i < cut {
		return int64(i/(base+1) + 1)
	}
	return int64(rem + (i-cut)/base + 1)
}

func evaluateLagLead(p partition, fn FuncExpr, results []Value) error {
	offset := fn.Offset
	if offset <= 0 {
		offset = 1
	}
	n := len(p.entries)
	for i := range p.entries {
		var target int
		if fn.Func == FuncLag {
			target = i - offset
			if fn.IgnoreNulls {
				target = skipNulls(p, fn.Column, i-1, -1, offset)
			}
		} else {
			target = i + offset
			if fn.IgnoreNulls {
				target = skipNulls(p, fn.Column, i+1, 1, offset)
			}
		}
		var out Value
		if target < 0 || target >= n {
			if fn.HasDefault {
				out = fn.Default
			} else {
				out = Null()
			}
		} else {
			out = p.entries[target].row[fn.Column]
		}
		results[p.entries[i].rowIndex] = out
	}
	return nil
}

// skipNulls returns the position of the k-th non-null value starting at from
// and stepping by dir, or -1 when fewer than k such values remain.
func skipNulls(p partition, col, from, dir, k int) int {
	found := 0
	for i := from; i >= 0 && i < len(p.entries); i += dir {
		if !p.entries[i].row[col].IsNull() {
			found++
			if found == k {
				return i
			}
		}
	}
	return -1
}

func evaluateFrameFunction(p partition, mask []bool, cur int, fn FuncExpr) (Value, error) {
	switch fn.Func {
	case FuncCount:
		return Int(countRows(p, mask, fn)), nil
	case FuncSum:
		return sumRows(p, mask, fn.Column)
	case FuncAvg:
		return avgRows(p, mask, fn.Column)
	case FuncMin:
		return extremum(p, mask, fn.Column, true)
	case FuncMax:
		return extremum(p, mask, fn.Column, false)
	case FuncFirstValue:
		return valueAt(p, mask, 1, fn)
	case FuncLastValue:
		return lastValue(p, mask, fn.IgnoreNulls, fn.Column)
	case FuncNthValue:
		return valueAt(p, mask, fn.N, fn)
	default:
		return Null(), errors.New("windowframe: function is not a frame function")
	}
}

func countRows(p partition, mask []bool, fn FuncExpr) int64 {
	var count int64
	for i, ok := range mask {
		if !ok {
			continue
		}
		if fn.CountAll || !p.entries[i].row[fn.Column].IsNull() {
			count++
		}
	}
	return count
}

func sumRows(p partition, mask []bool, col int) (Value, error) {
	allInt := true
	var total float64
	var intTotal int64
	any := false
	for i, ok := range mask {
		if !ok {
			continue
		}
		v := p.entries[i].row[col]
		if v.IsNull() {
			continue
		}
		num, ok := v.AsNumber()
		if !ok {
			return Null(), &TypeError{msg: "windowframe: SUM requires numeric values"}
		}
		if v.kind != KindInt {
			allInt = false
		}
		total += num
		if allInt {
			intTotal += v.i
		}
		any = true
	}
	if !any {
		return Null(), nil
	}
	if allInt {
		return Int(intTotal), nil
	}
	return Float(total), nil
}

func avgRows(p partition, mask []bool, col int) (Value, error) {
	var total float64
	var count int64
	for i, ok := range mask {
		if !ok {
			continue
		}
		v := p.entries[i].row[col]
		if v.IsNull() {
			continue
		}
		num, ok := v.AsNumber()
		if !ok {
			return Null(), &TypeError{msg: "windowframe: AVG requires numeric values"}
		}
		total += num
		count++
	}
	if count == 0 {
		return Null(), nil
	}
	return Float(total / float64(count)), nil
}

func extremum(p partition, mask []bool, col int, minimum bool) (Value, error) {
	best := Null()
	for i, ok := range mask {
		if !ok {
			continue
		}
		v := p.entries[i].row[col]
		if v.IsNull() {
			continue
		}
		if best.IsNull() {
			best = v
			continue
		}
		c, err := Compare(v, best)
		if err != nil {
			return Null(), err
		}
		if (minimum && c == CmpLT) || (!minimum && c == CmpGT) {
			best = v
		}
	}
	return best, nil
}

// valueAt implements FIRST_VALUE (n == 1) and NTH_VALUE. With RESPECT NULLS
// every included row counts; with IGNORE NULLS only non-null values count.
func valueAt(p partition, mask []bool, n int, fn FuncExpr) (Value, error) {
	seen := 0
	for i, ok := range mask {
		if !ok {
			continue
		}
		v := p.entries[i].row[fn.Column]
		if fn.IgnoreNulls && v.IsNull() {
			continue
		}
		seen++
		if seen == n {
			return v, nil
		}
	}
	return Null(), nil
}

func lastValue(p partition, mask []bool, ignoreNulls bool, col int) (Value, error) {
	for i := len(mask) - 1; i >= 0; i-- {
		if !mask[i] {
			continue
		}
		v := p.entries[i].row[col]
		if ignoreNulls && v.IsNull() {
			continue
		}
		return v, nil
	}
	return Null(), nil
}

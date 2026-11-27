package windowframe

// Brute-force, definition-first reference evaluator used only by the tests.
// Frame membership is decided by per-row predicates rather than interval
// arithmetic so it independently cross-checks the production implementation.

type refPartition struct {
	rows    [][]Value
	indices []int
	groupOf []int
	groups  [][2]int
}

func refNullsFirst(o OrderSpec) bool {
	switch o.Nulls {
	case NullsFirst:
		return true
	case NullsLast:
		return false
	default:
		return o.Direction == Desc
	}
}

func refCompareOrder(a, b Value, o OrderSpec) int {
	if a.IsNull() || b.IsNull() {
		if a.IsNull() && b.IsNull() {
			return 0
		}
		// NULL placement is independent of ASC/DESC.
		if refNullsFirst(o) {
			if a.IsNull() {
				return -1
			}
			return 1
		}
		if a.IsNull() {
			return 1
		}
		return -1
	}
	c, _ := Compare(a, b)
	r := int(c)
	if o.Direction == Desc {
		r = -r
	}
	return r
}

func refSamePartition(a, b []Value, cols []int) bool {
	for _, c := range cols {
		if !Equal(a[c], b[c]) {
			return false
		}
	}
	return true
}

func refSameKey(a, b []Value, order []OrderSpec) bool {
	for _, o := range order {
		if !Equal(a[o.Column], b[o.Column]) {
			return false
		}
	}
	return true
}

func refLess(a, b []Value, order []OrderSpec) bool {
	for _, o := range order {
		c := refCompareOrder(a[o.Column], b[o.Column], o)
		if c != 0 {
			return c < 0
		}
	}
	return false
}

func refBuild(rows [][]Value, spec WindowSpec) []refPartition {
	assigned := make([]bool, len(rows))
	var parts []refPartition
	for i := range rows {
		if assigned[i] {
			continue
		}
		var members []int
		for j := i; j < len(rows); j++ {
			if !assigned[j] && refSamePartition(rows[i], rows[j], spec.PartitionColumns) {
				members = append(members, j)
				assigned[j] = true
			}
		}
		sorted := make([]int, len(members))
		copy(sorted, members)
		for x := 1; x < len(sorted); x++ {
			y := x
			for y > 0 && refLess(rows[sorted[y]], rows[sorted[y-1]], spec.Order) {
				sorted[y-1], sorted[y] = sorted[y], sorted[y-1]
				y--
			}
		}
		groupOf := make([]int, len(sorted))
		var groups [][2]int
		if len(sorted) > 0 {
			start, gi := 0, 0
			for x := 1; x <= len(sorted); x++ {
				if x == len(sorted) || !refSameKey(rows[sorted[x-1]], rows[sorted[x]], spec.Order) {
					groups = append(groups, [2]int{start, x - 1})
					for k := start; k < x; k++ {
						groupOf[k] = gi
					}
					gi++
					start = x
				}
			}
		}
		partRows := make([][]Value, len(sorted))
		for x, idx := range sorted {
			partRows[x] = rows[idx]
		}
		parts = append(parts, refPartition{rows: partRows, indices: sorted, groupOf: groupOf, groups: groups})
	}
	return parts
}

func refEvaluate(rows [][]Value, spec WindowSpec, fn FuncExpr) []Value {
	frame := resolveFrame(spec)
	parts := refBuild(rows, spec)
	out := make([]Value, len(rows))
	for _, p := range parts {
		refPartitionResult(frame, spec, fn, p, out)
	}
	return out
}

func refPartitionResult(frame *FrameSpec, spec WindowSpec, fn FuncExpr, p refPartition, out []Value) {
	n := len(p.indices)
	for cur := 0; cur < n; cur++ {
		if isFrameIndependent(fn.Func) {
			out[p.indices[cur]] = refIndependent(p, fn, cur)
			continue
		}
		member := refFrameMembers(spec, frame, p, cur)
		out[p.indices[cur]] = refFrameFunc(p, fn, member)
	}
}

func refFrameMembers(spec WindowSpec, frame *FrameSpec, p refPartition, cur int) []bool {
	n := len(p.indices)
	member := make([]bool, n)
	end := frame.End
	if end == nil {
		end = boundPtr(CurrentRow())
	}
	for q := 0; q < n; q++ {
		if refInFrame(spec, frame, *end, p, cur, q) && refExcluded(frame, p, cur, q) {
			member[q] = true
		}
	}
	return member
}

func refInFrame(spec WindowSpec, frame *FrameSpec, end FrameBound, p refPartition, cur, q int) bool {
	switch frame.Mode {
	case RowsFrame:
		s := refRowsPosition(frame.Start, cur, len(p.indices))
		e := refRowsPosition(end, cur, len(p.indices))
		return q >= s && q <= e
	case GroupsFrame:
		ng := len(p.groups)
		sg := refGroupPositionRaw(frame.Start, p.groupOf[cur], ng, true)
		eg := refGroupPositionRaw(end, p.groupOf[cur], ng, false)
		if sg < 0 {
			sg = 0
		}
		if eg > ng-1 {
			eg = ng - 1
		}
		gq := p.groupOf[q]
		return sg <= gq && gq <= eg
	default:
		return refInRange(spec, frame, end, p, cur, q)
	}
}

func refRowsPosition(b FrameBound, cur, n int) int {
	switch {
	case b.unbounded():
		if b.Kind == BoundPreceding {
			return 0
		}
		return n - 1
	case b.Kind == BoundCurrentRow:
		return cur
	case b.Kind == BoundPreceding:
		return cur - int(b.Offset.i)
	default:
		return cur + int(b.Offset.i)
	}
}

func refGroupPositionRaw(b FrameBound, g, ng int, start bool) int {
	switch {
	case b.unbounded():
		if start {
			return 0
		}
		return ng - 1
	case b.Kind == BoundCurrentRow:
		return g
	case b.Kind == BoundPreceding:
		return g - int(b.Offset.i)
	default:
		return g + int(b.Offset.i)
	}
}

func refExcluded(frame *FrameSpec, p refPartition, cur, q int) bool {
	g := p.groupOf[cur]
	gs, ge := p.groups[g][0], p.groups[g][1]
	switch frame.Exclusion {
	case ExcludeCurrentRow:
		return q != cur
	case ExcludeGroup:
		return q < gs || q > ge
	case ExcludeTies:
		if q == cur {
			return true
		}
		return q < gs || q > ge
	default:
		return true
	}
}

func refInRange(spec WindowSpec, frame *FrameSpec, end FrameBound, p refPartition, cur, q int) bool {
	n := len(p.indices)
	if len(spec.Order) == 0 {
		return true
	}
	col := spec.Order[0].Column
	o := spec.Order[0]
	curKey := p.rows[cur][col]
	g := p.groupOf[cur]

	if curKey.IsNull() {
		s := p.groups[g][0]
		if frame.Start.unbounded() {
			s = 0
		}
		e := p.groups[g][1]
		if end.unbounded() {
			e = n - 1
		}
		return q >= s && q <= e
	}

	s := refRangeEdge(frame.Start, p, col, o, cur, g, true)
	e := refRangeEdge(end, p, col, o, cur, g, false)
	return q >= s && q <= e
}

func refRangeEdge(b FrameBound, p refPartition, col int, o OrderSpec, cur, g int, start bool) int {
	n := len(p.indices)
	if b.unbounded() {
		if start {
			return 0
		}
		return n - 1
	}
	if b.Kind == BoundCurrentRow {
		if start {
			return p.groups[g][0]
		}
		return p.groups[g][1]
	}
	threshold := refThreshold(p.rows[cur][col], b)
	if start {
		if o.Direction == Desc {
			for gi := 0; gi < len(p.groups); gi++ {
				rep := p.rows[p.groups[gi][0]][col]
				if rep.IsNull() {
					continue
				}
				c, _ := Compare(rep, threshold)
				if c >= CmpEQ {
					return p.groups[gi][0]
				}
			}
			return n
		}
		for gi := 0; gi < len(p.groups); gi++ {
			rep := p.rows[p.groups[gi][0]][col]
			if rep.IsNull() {
				continue
			}
			c, _ := Compare(rep, threshold)
			if c >= CmpEQ {
				return p.groups[gi][0]
			}
		}
		return n
	}
	if o.Direction == Desc {
		for gi := len(p.groups) - 1; gi >= 0; gi-- {
			rep := p.rows[p.groups[gi][0]][col]
			if rep.IsNull() {
				continue
			}
			c, _ := Compare(rep, threshold)
			if c <= CmpEQ {
				return p.groups[gi][1]
			}
		}
		return -1
	}
	for gi := len(p.groups) - 1; gi >= 0; gi-- {
		rep := p.rows[p.groups[gi][0]][col]
		if rep.IsNull() {
			continue
		}
		c, _ := Compare(rep, threshold)
		if c <= CmpEQ {
			return p.groups[gi][1]
		}
	}
	return -1
}

func refThreshold(cur Value, b FrameBound) Value {
	num, _ := cur.AsNumber()
	off, _ := b.Offset.AsNumber()
	if cur.kind == KindInt && b.Offset.kind == KindInt {
		if b.Kind == BoundFollowing {
			return Int(cur.i + b.Offset.i)
		}
		return Int(cur.i - b.Offset.i)
	}
	if b.Kind == BoundFollowing {
		return Float(num + off)
	}
	return Float(num - off)
}

func refIndependent(p refPartition, fn FuncExpr, cur int) Value {
	n := len(p.indices)
	switch fn.Func {
	case FuncRowNumber:
		return Int(int64(cur + 1))
	case FuncRank:
		return Int(int64(p.groups[p.groupOf[cur]][0] + 1))
	case FuncDenseRank:
		return Int(int64(p.groupOf[cur] + 1))
	case FuncNtile:
		return Int(ntileBucket(cur, n, fn.N))
	case FuncLag, FuncLead:
		return refLagLead(p, fn, cur)
	}
	return Null()
}

func refLagLead(p refPartition, fn FuncExpr, cur int) Value {
	offset := fn.Offset
	if offset <= 0 {
		offset = 1
	}
	dir := -1
	if fn.Func == FuncLead {
		dir = 1
	}
	target := cur + dir*offset
	if fn.IgnoreNulls {
		found := 0
		target = -1
		for q := cur + dir; q >= 0 && q < len(p.indices); q += dir {
			if p.rows[q][fn.Column].IsNull() {
				continue
			}
			found++
			if found == offset {
				target = q
				break
			}
		}
	}
	n := len(p.indices)
	if target < 0 || target >= n {
		if fn.HasDefault {
			return fn.Default
		}
		return Null()
	}
	return p.rows[target][fn.Column]
}

func refFrameFunc(p refPartition, fn FuncExpr, member []bool) Value {
	switch fn.Func {
	case FuncCount:
		var count int64
		for i, ok := range member {
			if !ok {
				continue
			}
			if fn.CountAll || !p.rows[i][fn.Column].IsNull() {
				count++
			}
		}
		return Int(count)
	case FuncSum:
		var total float64
		var intTotal int64
		allInt, any := true, false
		for i, ok := range member {
			if !ok {
				continue
			}
			v := p.rows[i][fn.Column]
			if v.IsNull() {
				continue
			}
			num, _ := v.AsNumber()
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
			return Null()
		}
		if allInt {
			return Int(intTotal)
		}
		return Float(total)
	case FuncAvg:
		var total float64
		var count int64
		for i, ok := range member {
			if !ok {
				continue
			}
			v := p.rows[i][fn.Column]
			if v.IsNull() {
				continue
			}
			num, _ := v.AsNumber()
			total += num
			count++
		}
		if count == 0 {
			return Null()
		}
		return Float(total / float64(count))
	case FuncMin, FuncMax:
		best := Null()
		for i, ok := range member {
			if !ok {
				continue
			}
			v := p.rows[i][fn.Column]
			if v.IsNull() {
				continue
			}
			if best.IsNull() {
				best = v
				continue
			}
			c, _ := Compare(v, best)
			if (fn.Func == FuncMin && c == CmpLT) || (fn.Func == FuncMax && c == CmpGT) {
				best = v
			}
		}
		return best
	case FuncFirstValue, FuncNthValue:
		seen := 0
		for i, ok := range member {
			if !ok {
				continue
			}
			v := p.rows[i][fn.Column]
			if fn.IgnoreNulls && v.IsNull() {
				continue
			}
			seen++
			n := 1
			if fn.Func == FuncNthValue {
				n = fn.N
			}
			if seen == n {
				return v
			}
		}
		return Null()
	case FuncLastValue:
		for i := len(member) - 1; i >= 0; i-- {
			if !member[i] {
				continue
			}
			v := p.rows[i][fn.Column]
			if fn.IgnoreNulls && v.IsNull() {
				continue
			}
			return v
		}
		return Null()
	}
	return Null()
}

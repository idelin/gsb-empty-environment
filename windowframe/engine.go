package windowframe

import (
	"math"
	"sort"
)

type entry struct {
	rowIndex int
	row      []Value
}

type partition struct {
	entries []entry
	groups  [][2]int
	groupOf []int
}

func nullsFirst(o OrderSpec) bool {
	switch o.Nulls {
	case NullsFirst:
		return true
	case NullsLast:
		return false
	default:
		return o.Direction == Desc
	}
}

// compareKeys returns a negative, zero or positive result for two rows under
// the window ordering. NULL placement is resolved before value comparison.
func compareKeys(a, b []Value, order []OrderSpec) (int, error) {
	for _, o := range order {
		av, bv := a[o.Column], b[o.Column]
		if av.IsNull() || bv.IsNull() {
			if av.IsNull() && bv.IsNull() {
				continue
			}
			if nullsFirst(o) {
				if av.IsNull() {
					return -1, nil
				}
				return 1, nil
			}
			if av.IsNull() {
				return 1, nil
			}
			return -1, nil
		}
		c, err := Compare(av, bv)
		if err != nil {
			return 0, err
		}
		if c != CmpEQ {
			r := int(c)
			if o.Direction == Desc {
				r = -r
			}
			return r, nil
		}
	}
	return 0, nil
}

func sameKey(a, b []Value, order []OrderSpec) bool {
	for _, o := range order {
		if !Equal(a[o.Column], b[o.Column]) {
			return false
		}
	}
	return true
}

func buildPartitions(rows [][]Value, cols []int) [][]int {
	if len(cols) == 0 {
		all := make([]int, len(rows))
		for i := range rows {
			all[i] = i
		}
		return [][]int{all}
	}
	indexByKey := map[string]int{}
	var parts [][]int
	for i, row := range rows {
		key := partitionKey(row, cols)
		id, ok := indexByKey[key]
		if !ok {
			id = len(parts)
			indexByKey[key] = id
			parts = append(parts, nil)
		}
		parts[id] = append(parts[id], i)
	}
	return parts
}

func partitionKey(row []Value, cols []int) string {
	buf := make([]byte, 0, 16*len(cols))
	for _, c := range cols {
		v := row[c]
		buf = append(buf, byte(v.kind))
		switch v.kind {
		case KindNull:
		case KindInt:
			buf = appendVarint(buf, v.i)
		case KindFloat:
			buf = appendUint64(buf, math.Float64bits(v.f))
		case KindBool:
			if v.b {
				buf = append(buf, 1)
			} else {
				buf = append(buf, 0)
			}
		case KindString:
			buf = appendVarint(buf, int64(len(v.s)))
			buf = append(buf, v.s...)
		}
		buf = append(buf, '|')
	}
	return string(buf)
}

func appendVarint(buf []byte, v int64) []byte {
	uv := uint64(v) << 1
	if v < 0 {
		uv = ^uv
	}
	for uv >= 0x80 {
		buf = append(buf, byte(uv)|0x80)
		uv >>= 7
	}
	return append(buf, byte(uv))
}

func appendUint64(buf []byte, u uint64) []byte {
	for u != 0 {
		buf = append(buf, byte(u&0xff))
		u >>= 8
	}
	return buf
}

type entrySorter struct {
	entries []entry
	order   []OrderSpec
	err     error
}

func (s entrySorter) Len() int { return len(s.entries) }

func (s entrySorter) Less(i, j int) bool {
	c, err := compareKeys(s.entries[i].row, s.entries[j].row, s.order)
	if err != nil {
		s.err = err
		return false
	}
	return c < 0
}

func (s entrySorter) Swap(i, j int) {
	s.entries[i], s.entries[j] = s.entries[j], s.entries[i]
}

func preparePartition(rows [][]Value, indices []int, order []OrderSpec) (partition, error) {
	entries := make([]entry, len(indices))
	for i, rowIndex := range indices {
		entries[i] = entry{rowIndex: rowIndex, row: rows[rowIndex]}
	}
	sorter := entrySorter{entries: entries, order: order}
	sort.Stable(sorter)
	if sorter.err != nil {
		return partition{}, sorter.err
	}
	entries = sorter.entries
	var groups [][2]int
	groupOf := make([]int, len(entries))
	if len(entries) > 0 {
		start := 0
		gi := 0
		for i := 1; i <= len(entries); i++ {
			if i == len(entries) || !sameKey(entries[i-1].row, entries[i].row, order) {
				groups = append(groups, [2]int{start, i - 1})
				for k := start; k < i; k++ {
					groupOf[k] = gi
				}
				gi++
				start = i
			}
		}
	}
	return partition{entries: entries, groups: groups, groupOf: groupOf}, nil
}

// resolveFrame applies the SQL default frame rules when no frame was given.
func resolveFrame(spec WindowSpec) *FrameSpec {
	if spec.Frame != nil {
		return spec.Frame
	}
	if len(spec.Order) == 0 {
		return &FrameSpec{
			Mode:      RangeFrame,
			Start:     UnboundedPreceding(),
			End:       boundPtr(UnboundedFollowing()),
			Exclusion: ExcludeNoOthers,
		}
	}
	return &FrameSpec{
		Mode:      RangeFrame,
		Start:     UnboundedPreceding(),
		End:       boundPtr(CurrentRow()),
		Exclusion: ExcludeNoOthers,
	}
}

func boundPtr(b FrameBound) *FrameBound { return &b }

// frameMask computes inclusion of every sorted position for the frame anchored
// at cur, including the EXCLUDE clause.
func (p partition) frameMask(spec WindowSpec, frame *FrameSpec, cur int) ([]bool, error) {
	n := len(p.entries)
	mask := make([]bool, n)
	start, end, err := p.frameBounds(spec, frame, cur)
	if err != nil {
		return nil, err
	}
	for i := start; i <= end; i++ {
		mask[i] = true
	}
	g := p.groupOf[cur]
	gs, ge := p.groups[g][0], p.groups[g][1]
	switch frame.Exclusion {
	case ExcludeCurrentRow:
		mask[cur] = false
	case ExcludeGroup:
		for i := gs; i <= ge; i++ {
			mask[i] = false
		}
	case ExcludeTies:
		for i := gs; i <= ge; i++ {
			if i != cur {
				mask[i] = false
			}
		}
	case ExcludeNoOthers:
	}
	return mask, nil
}

// frameBounds returns the inclusive sorted-position interval of the frame
// before exclusions. An inverted interval yields start > end, i.e. an empty
// frame.
func (p partition) frameBounds(spec WindowSpec, frame *FrameSpec, cur int) (int, int, error) {
	n := len(p.entries)
	endBound := frame.End
	if endBound == nil {
		endBound = boundPtr(CurrentRow())
	}
	switch frame.Mode {
	case RowsFrame:
		start, end := p.rowsBounds(frame.Start, *endBound, cur, n)
		return start, end, nil
	case GroupsFrame:
		start, end := p.groupsBounds(frame.Start, *endBound, cur)
		return start, end, nil
	default:
		return p.rangeBounds(spec, frame.Start, *endBound, cur)
	}
}

func (p partition) rowsBounds(startBound, endBound FrameBound, cur, n int) (int, int) {
	start := rowsPosition(startBound, cur, n, true)
	end := rowsPosition(endBound, cur, n, false)
	if start > end {
		return 1, 0
	}
	if start < 0 {
		start = 0
	}
	if end > n-1 {
		end = n - 1
	}
	if start > end {
		return 1, 0
	}
	return start, end
}

func rowsPosition(b FrameBound, cur, n int, start bool) int {
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

func (p partition) groupsBounds(startBound, endBound FrameBound, cur int) (int, int) {
	g := p.groupOf[cur]
	ng := len(p.groups)
	sg := groupPosition(startBound, g, ng, true)
	eg := groupPosition(endBound, g, ng, false)
	if sg < 0 {
		sg = 0
	}
	if eg > ng-1 {
		eg = ng - 1
	}
	if sg > eg || sg >= ng || eg < 0 {
		return 1, 0
	}
	return p.groups[sg][0], p.groups[eg][1]
}

func groupPosition(b FrameBound, g, ng int, start bool) int {
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

func (p partition) rangeBounds(spec WindowSpec, startBound, endBound FrameBound, cur int) (int, int, error) {
	n := len(p.entries)
	if len(spec.Order) == 0 {
		return 0, n - 1, nil
	}
	g := p.groupOf[cur]
	col := spec.Order[0].Column
	desc := spec.Order[0].Direction == Desc
	currentKey := p.entries[cur].row[col]

	if currentKey.IsNull() {
		// Bounded and CURRENT ROW bounds cannot leave the NULL peer group, but
		// an UNBOUNDED end still reaches the partition edge.
		start := p.groups[g][0]
		if startBound.unbounded() {
			start = 0
		}
		end := p.groups[g][1]
		if endBound.unbounded() {
			end = n - 1
		}
		if start > end {
			return 1, 0, nil
		}
		return start, end, nil
	}

	start, err := p.rangeEdge(startBound, col, desc, cur, g, true)
	if err != nil {
		return 0, 0, err
	}
	end, err := p.rangeEdge(endBound, col, desc, cur, g, false)
	if err != nil {
		return 0, 0, err
	}
	if start > end {
		return 1, 0, nil
	}
	return start, end, nil
}

// rangeBoundValue computes the raw numeric value of a bounded frame edge for
// the current key. PRECEDING subtracts, FOLLOWING adds; CURRENT ROW keeps the
// current key. Direction is intentionally irrelevant at this layer.
func rangeBoundValue(cur Value, b FrameBound) (Value, error) {
	if b.Kind == BoundCurrentRow {
		return cur, nil
	}
	if _, ok := cur.AsNumber(); !ok {
		return Value{}, &TypeError{msg: "windowframe: RANGE offsets require a numeric ORDER BY column"}
	}
	if _, ok := b.Offset.AsNumber(); !ok {
		return Value{}, &TypeError{msg: "windowframe: RANGE offsets must be numeric"}
	}
	if cur.kind == KindInt && b.Offset.kind == KindInt {
		if b.Kind == BoundFollowing {
			return Int(cur.i + b.Offset.i), nil
		}
		return Int(cur.i - b.Offset.i), nil
	}
	cn, _ := cur.AsNumber()
	on, _ := b.Offset.AsNumber()
	if b.Kind == BoundFollowing {
		return Float(cn + on), nil
	}
	return Float(cn - on), nil
}

func (p partition) rangeEdge(b FrameBound, col int, desc bool, cur, g int, start bool) (int, error) {
	n := len(p.entries)
	if b.unbounded() {
		if start {
			return 0, nil
		}
		return n - 1, nil
	}
	if b.Kind == BoundCurrentRow {
		if start {
			return p.groups[g][0], nil
		}
		return p.groups[g][1], nil
	}
	threshold, err := rangeBoundValue(p.entries[cur].row[col], b)
	if err != nil {
		return 0, err
	}
	return p.rangeThresholdEdge(threshold, col, desc, start, n)
}

// rangeThresholdEdge maps a raw numeric threshold onto a sorted position.
// A start edge wants the first ordered group reaching the threshold; an end
// edge wants the last ordered group still within it. The comparator is flipped
// for DESC because ordered keys decrease along the sorted positions.
func (p partition) rangeThresholdEdge(threshold Value, col int, desc, start bool, n int) (int, error) {
	if start {
		for gi := 0; gi < len(p.groups); gi++ {
			rep := p.entries[p.groups[gi][0]].row[col]
			if rep.IsNull() {
				continue
			}
			c, err := Compare(rep, threshold)
			if err != nil {
				return 0, err
			}
			if !desc && c >= CmpEQ || desc && c >= CmpEQ {
				return p.groups[gi][0], nil
			}
		}
		return n, nil
	}
	for gi := len(p.groups) - 1; gi >= 0; gi-- {
		rep := p.entries[p.groups[gi][0]].row[col]
		if rep.IsNull() {
			continue
		}
		c, err := Compare(rep, threshold)
		if err != nil {
			return 0, err
		}
		if !desc && c <= CmpEQ || desc && c <= CmpEQ {
			return p.groups[gi][1], nil
		}
	}
	return -1, nil
}

// TypeError reports a value type incompatible with the requested operation.
type TypeError struct{ msg string }

func (e *TypeError) Error() string { return e.msg }

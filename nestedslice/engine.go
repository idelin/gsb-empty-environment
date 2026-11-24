package nestedslice

import "sort"

type elemRequirement struct {
	bufferID  string
	kind      BufferKind
	width     int64
	rows      PSet
	baseIndex int64
}

type psShadow struct {
	bufferID string
	kind     BufferKind
	width    int64
	rows     PSet
}

type dictWork struct {
	node       *Node
	parents    []*LayerSpec
	selections []PSet
	shadows    [][]psShadow
	paths      [][]string
	pathOK     bool
}

type engine struct {
	opts           Options
	limit          int64
	buffers        map[string]*Buffer
	reqs           map[string][]elemRequirement
	forbiddenRows  map[string][]psShadow
	forbiddenBytes map[string][]ByteRange
	dictWork       map[string]*dictWork
	dictLayer      map[string]*LayerSpec
}

func newEngine(opts Options, buffers map[string]*Buffer) *engine {
	forbidden := map[string][]ByteRange{}
	for id, ranges := range opts.Forbidden {
		forbidden[id] = append([]ByteRange(nil), ranges...)
	}
	return &engine{
		opts:           opts,
		limit:          opts.expandLimit(),
		buffers:        buffers,
		reqs:           map[string][]elemRequirement{},
		forbiddenRows:  map[string][]psShadow{},
		forbiddenBytes: forbidden,
		dictWork:       map[string]*dictWork{},
		dictLayer:      map[string]*LayerSpec{},
	}
}

func (e *engine) addRequirement(r elemRequirement) {
	if r.rows.IsEmpty() {
		return
	}
	e.reqs[r.bufferID] = append(e.reqs[r.bufferID], r)
}

func (e *engine) addShadow(bufferID string, kind BufferKind, width int64, rows PSet) {
	if rows.IsEmpty() {
		return
	}
	e.forbiddenRows[bufferID] = append(e.forbiddenRows[bufferID], psShadow{
		bufferID: bufferID, kind: kind, width: width, rows: rows,
	})
}

func (e *engine) planNode(n *Node, rows PSet, paths [][]string, shadows []psShadow, pathOK bool) (*LayerSpec, error) {
	for _, sh := range shadows {
		e.addShadow(sh.bufferID, sh.kind, sh.width, sh.rows)
	}

	spec := &LayerSpec{
		NodeID:      n.ID,
		Kind:        n.Kind,
		Rows:        rows,
		OrderedRows: rows,
	}
	if lo, _, ok := rows.Hull(); ok {
		spec.SliceStart = lo
		spec.BaseIndex = lo
	}

	if n.Validity != nil {
		spec.Validity = &ValiditySpec{
			BufferSpec: BufferSpec{
				BufferID:  n.Validity.ID,
				Kind:      ValidityBuffer,
				Rows:      rows,
				BaseIndex: spec.BaseIndex,
			},
			NullRows: n.NullRows,
		}
		e.addRequirement(elemRequirement{
			bufferID: n.Validity.ID, kind: ValidityBuffer,
			rows: rows, baseIndex: spec.BaseIndex,
		})
	}

	switch n.Kind {
	case PrimitiveNode:
		if pathOK {
			spec.Values = &BufferSpec{
				BufferID:  n.Value.ID,
				Kind:      ValuesBuffer,
				Rows:      rows,
				BaseIndex: spec.BaseIndex,
				Width:     n.ValueWidth,
			}
			e.addRequirement(elemRequirement{
				bufferID: n.Value.ID, kind: ValuesBuffer, width: n.ValueWidth,
				rows: rows, baseIndex: spec.BaseIndex,
			})
		}
	case ListNode:
		return e.planList(n, rows, paths, shadows, spec, pathOK)
	case StructNode:
		return e.planStruct(n, rows, paths, shadows, spec, pathOK)
	case DenseUnionNode:
		return e.planDenseUnion(n, rows, paths, shadows, spec, pathOK)
	case DictionaryNode:
		return e.planDictionary(n, rows, paths, shadows, spec, pathOK)
	default:
		panic("nestedslice: unknown node kind")
	}
	return spec, nil
}

func (e *engine) planList(n *Node, rows PSet, paths [][]string, shadows []psShadow, spec *LayerSpec, pathOK bool) (*LayerSpec, error) {
	entrySet := Union(rows, rows.Shift(1))
	spec.Offsets = &OffsetSpec{
		BufferSpec: BufferSpec{
			BufferID:  n.Offsets.ID,
			Kind:      OffsetsBuffer,
			Rows:      entrySet,
			BaseIndex: spec.BaseIndex,
			Width:     8,
		},
		FixedSize:   n.FixedSize,
		OffsetFirst: n.OffsetFirst,
		Stride:      n.Stride,
	}
	if !n.FixedSize {
		e.addRequirement(elemRequirement{
			bufferID: n.Offsets.ID, kind: OffsetsBuffer, width: 8,
			rows: entrySet, baseIndex: spec.BaseIndex,
		})
	}

	childRows, err := e.listChildRows(n, rows)
	if err != nil {
		return nil, err
	}
	if !n.NullRows.IsEmpty() {
		if err := e.shadowListChildren(n, n.NullRows); err != nil {
			return nil, err
		}
	}
	childShadows := translateShadows(shadows, n)
	if n.Children != nil {
		child, err := e.planNode(n.Children[0], childRows, paths, childShadows, pathOK)
		if err != nil {
			return nil, err
		}
		spec.Children = []ChildLayer{{Name: n.Children[0].Name, Layer: child}}
	}
	return spec, nil
}

func (e *engine) listChildRows(n *Node, rows PSet) (PSet, error) {
	if n.FixedSize {
		return rows.Affine(n.Stride, n.OffsetFirst), nil
	}
	values, ok := rows.Expand(e.limit)
	if !ok {
		return PSet{}, ErrIrregularTooLarge
	}
	aps := make([]AP, 0, len(values))
	lo, hi := int64(0), int64(0)
	for i, row := range values {
		start := n.offsetAt(row)
		end := n.offsetAt(row + 1)
		if end < start {
			panic("nestedslice: list offsets must be non-decreasing")
		}
		if end > start {
			aps = append(aps, AP{First: start, Step: 1, Count: end - start})
		}
		if i == 0 || start < lo {
			lo = start
		}
		if end > hi {
			hi = end
		}
	}
	// APs describe distinct physical list intervals; keep them separate so the
	// projection retains the gaps (which may hold shadowed null data).
	return PSet{Lo: lo, Hi: hi, APs: aps}, nil
}

func (e *engine) shadowListChildren(n *Node, nullRows PSet) error {
	child := n.Children[0]
	if n.FixedSize {
		e.shadowSubtree(child, nullRows.Affine(n.Stride, n.OffsetFirst))
		return nil
	}
	values, ok := nullRows.Expand(e.limit)
	if !ok {
		return ErrIrregularTooLarge
	}
	aps := make([]AP, 0, len(values))
	for _, row := range values {
		start := n.offsetAt(row)
		end := n.offsetAt(row + 1)
		if end > start {
			aps = append(aps, AP{First: start, Step: 1, Count: end - start})
		}
	}
	if len(aps) == 0 {
		return nil
	}
	normalized := aps
	lo, hi, _ := (PSet{APs: normalized}).Hull()
	e.shadowSubtree(child, PSet{Lo: lo, Hi: hi, APs: normalized})
	return nil
}

func (e *engine) planStruct(n *Node, rows PSet, paths [][]string, shadows []psShadow, spec *LayerSpec, pathOK bool) (*LayerSpec, error) {
	if !n.NullRows.IsEmpty() {
		for _, child := range n.Children {
			e.shadowSubtree(child, n.NullRows)
		}
	}
	for _, child := range n.Children {
		childPaths, childOK := filterPaths(paths, child.Name, pathOK)
		if !childOK {
			continue
		}
		layer, err := e.planNode(child, rows, childPaths, shadows, true)
		if err != nil {
			return nil, err
		}
		spec.Children = append(spec.Children, ChildLayer{Name: child.Name, Layer: layer})
	}
	return spec, nil
}

func (e *engine) planDenseUnion(n *Node, rows PSet, paths [][]string, shadows []psShadow, spec *LayerSpec, pathOK bool) (*LayerSpec, error) {
	validRows := rows
	if !n.NullRows.IsEmpty() {
		// A null union row may hold an illegal type id byte. Both union
		// buffers of null rows are forbidden.
		e.addShadow(n.TypeIDs.ID, TypeIDBuffer, 1, n.NullRows)
		e.addShadow(n.UnionOffsets.ID, UnionOffsetBuffer, 8, n.NullRows)
		validRows = subtractSet(rows, n.NullRows)
	}

	spec.TypeIDs = &BufferSpec{
		BufferID:  n.TypeIDs.ID,
		Kind:      TypeIDBuffer,
		Rows:      validRows,
		BaseIndex: spec.BaseIndex,
		Width:     1,
	}
	spec.UnionOffset = &BufferSpec{
		BufferID:  n.UnionOffsets.ID,
		Kind:      UnionOffsetBuffer,
		Rows:      validRows,
		BaseIndex: spec.BaseIndex,
		Width:     8,
	}
	e.addRequirement(elemRequirement{
		bufferID: n.TypeIDs.ID, kind: TypeIDBuffer, width: 1,
		rows: validRows, baseIndex: spec.BaseIndex,
	})
	e.addRequirement(elemRequirement{
		bufferID: n.UnionOffsets.ID, kind: UnionOffsetBuffer, width: 8,
		rows: validRows, baseIndex: spec.BaseIndex,
	})

	grouped, err := e.groupUnionRows(n, validRows)
	if err != nil {
		return nil, err
	}
	for typeID, group := range grouped {
		child := n.Children[typeID]
		childPaths, childOK := filterPaths(paths, child.Name, pathOK)
		if !childOK {
			continue
		}
		unionShadows := translateShadows(shadows, n)
		layer, err := e.planNode(child, group, childPaths, unionShadows, true)
		if err != nil {
			return nil, err
		}
		spec.Children = append(spec.Children, ChildLayer{Name: child.Name, Layer: layer})
	}
	sort.Slice(spec.Children, func(i, j int) bool { return spec.Children[i].Name < spec.Children[j].Name })
	return spec, nil
}

func (e *engine) groupUnionRows(n *Node, rows PSet) (map[int64]PSet, error) {
	values, ok := rows.Expand(e.limit)
	if !ok {
		return nil, ErrIrregularTooLarge
	}
	grouped := map[int64][]AP{}
	lo, hi := int64(0), int64(0)
	for i, row := range values {
		typeID := n.typeIDAt(row)
		if typeID < 0 || int(typeID) >= len(n.Children) {
			panic("nestedslice: dense union type id out of range")
		}
		off := n.unionOffsetAt(row)
		grouped[typeID] = append(grouped[typeID], AP{First: off, Step: 1, Count: 1})
		if len(values) == 1 || i == 0 {
			lo, hi = off, off+1
		} else if off < lo {
			lo = off
		} else if off+1 > hi {
			hi = off + 1
		}
	}
	out := map[int64]PSet{}
	for typeID, aps := range grouped {
		normalized := normalizeAPs(aps)
		alo, ahi, ok := (PSet{APs: normalized}).Hull()
		if ok {
			lo, hi = alo, ahi
		}
		out[typeID] = PSet{Lo: lo, Hi: hi, APs: normalized}
	}
	return out, nil
}

func (e *engine) planDictionary(n *Node, rows PSet, paths [][]string, shadows []psShadow, spec *LayerSpec, pathOK bool) (*LayerSpec, error) {
	var valueRows PSet
	if n.IndexAffine {
		valueRows = rows.Affine(n.IndexStep, n.IndexFirst)
	} else {
		mapped, err := rows.Memo(e.limit, n.dictIndexAt)
		if err != nil {
			return nil, err
		}
		valueRows = mapped
	}
	if lo, hi, ok := valueRows.Hull(); ok && (lo < 0 || hi > n.Dictionary.Length) {
		return nil, errf("dictionary node %q index out of bounds: hull [%d,%d) vs length %d",
			n.ID, lo, hi, n.Dictionary.Length)
	}
	spec.DictIndex = &BufferSpec{
		BufferID:  n.Index.ID,
		Kind:      DictIndexBuffer,
		Rows:      rows,
		BaseIndex: spec.BaseIndex,
		Width:     8,
	}
	e.addRequirement(elemRequirement{
		bufferID: n.Index.ID, kind: DictIndexBuffer, width: 8,
		rows: rows, baseIndex: spec.BaseIndex,
	})
	if !n.NullRows.IsEmpty() {
		if err := e.shadowDictValues(n, n.NullRows); err != nil {
			return nil, err
		}
	}

	work := e.dictWork[n.Dictionary.ID]
	if work == nil {
		work = &dictWork{node: n.Dictionary}
		e.dictWork[n.Dictionary.ID] = work
	}
	work.parents = append(work.parents, spec)
	work.selections = append(work.selections, valueRows)
	work.shadows = append(work.shadows, translateShadows(shadows, n))
	if work.pathOK {
		work.paths = mergePaths(work.paths, paths)
	} else {
		work.paths = paths
	}
	work.pathOK = work.pathOK || pathOK

	spec.Dict = &DictSpec{OrderedIndices: valueRows}
	return spec, nil
}

func (e *engine) shadowDictValues(n *Node, nullRows PSet) error {
	if n.IndexAffine {
		e.shadowSubtree(n.Dictionary, nullRows.Affine(n.IndexStep, n.IndexFirst))
		return nil
	}
	mapped, err := nullRows.Memo(e.limit, n.dictIndexAt)
	if err != nil {
		return err
	}
	e.shadowSubtree(n.Dictionary, mapped)
	return nil
}

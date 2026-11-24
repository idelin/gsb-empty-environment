package nestedslice

import "sort"

// shadowSubtree marks rows as forbidden on every buffer reachable from n.
func (e *engine) shadowSubtree(n *Node, rows PSet) {
	if rows.IsEmpty() {
		return
	}
	for _, ref := range n.bufferRefs() {
		e.addShadow(ref.Buffer.ID, ref.Buffer.Kind, ref.Buffer.elemWidth(n.ValueWidth), rows)
	}
	switch n.Kind {
	case ListNode:
		if n.Children != nil {
			e.shadowSubtree(n.Children[0], rows)
		}
	case StructNode, DenseUnionNode:
		for _, child := range n.Children {
			e.shadowSubtree(child, rows)
		}
	case DictionaryNode:
		e.shadowSubtree(n.Dictionary, rows)
	}
}

// translateShadows moves ancestor shadowed element sets into a child's
// coordinates. Identity-mapped nodes keep coordinates; fixed-size lists and
// affine dictionaries map them symbolically.
func translateShadows(shadows []psShadow, parent *Node) []psShadow {
	if len(shadows) == 0 {
		return nil
	}
	out := make([]psShadow, 0, len(shadows))
	mapRows := func(rows PSet) PSet {
		switch {
		case parent.Kind == ListNode && parent.FixedSize:
			return rows.Affine(parent.Stride, parent.OffsetFirst)
		case parent.Kind == DictionaryNode && parent.IndexAffine:
			return rows.Affine(parent.IndexStep, parent.IndexFirst)
		default:
			return rows
		}
	}
	for _, sh := range shadows {
		out = append(out, psShadow{
			bufferID: sh.bufferID,
			kind:     sh.kind,
			width:    sh.width,
			rows:     mapRows(sh.rows),
		})
	}
	return out
}

// subtractSet returns the elements of s that are not in drop. Both sets must
// be materializable within the planner limit; null rows are always small
// relative to the selection.
func subtractSet(s, drop PSet) PSet {
	if s.IsEmpty() {
		return s
	}
	values, ok := s.Expand(MaxExpand)
	if !ok {
		panic("nestedslice: cannot subtract from set larger than materialization limit")
	}
	kept := make([]int64, 0, len(values))
	for _, v := range values {
		if !drop.Contains(v) {
			kept = append(kept, v)
		}
	}
	if len(kept) == 0 {
		return EmptySet(s.Lo, s.Hi)
	}
	aps := make([]AP, len(kept))
	for i, v := range kept {
		aps[i] = AP{First: v, Step: 1, Count: 1}
	}
	normalized := normalizeAPs(aps)
	lo, hi, _ := (PSet{APs: normalized}).Hull()
	return PSet{Lo: lo, Hi: hi, APs: normalized}
}

// filterPaths consumes one struct/union field name from each path.
func filterPaths(paths [][]string, field string, parentOK bool) ([][]string, bool) {
	if parentOK && len(paths) == 0 {
		return nil, true
	}
	if !parentOK {
		return nil, false
	}
	var remaining [][]string
	for _, p := range paths {
		if len(p) == 0 {
			continue
		}
		if p[0] == field {
			remaining = append(remaining, p[1:])
		}
	}
	return remaining, len(remaining) > 0
}

func mergePaths(existing, extra [][]string) [][]string {
	if len(extra) == 0 {
		return existing
	}
	out := append([][]string(nil), existing...)
	for _, p := range extra {
		duplicate := false
		for _, q := range out {
			if pathEqual(p, q) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			out = append(out, p)
		}
	}
	return out
}

func pathEqual(a, b []string) bool {
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

// planSharedDictionaries plans each shared dictionary subtree exactly once
// with the union of every parent selection, never copying its buffers.
func (e *engine) planSharedDictionaries() error {
	ids := make([]string, 0, len(e.dictWork))
	for id := range e.dictWork {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		work := e.dictWork[id]
		unionRows := work.selections[0]
		for _, sel := range work.selections[1:] {
			unionRows = Union(unionRows, sel)
		}
		shadows := mergeShadowLists(work.shadows)
		layer, err := e.planNode(work.node, unionRows, work.paths, shadows, work.pathOK)
		if err != nil {
			return err
		}
		e.dictLayer[id] = layer
	}
	return nil
}

func mergeShadowLists(lists [][]psShadow) []psShadow {
	var out []psShadow
	for _, list := range lists {
		out = append(out, list...)
	}
	return out
}

// wireDictionaries attaches the single shared dictionary layer to every parent
// DictSpec while preserving each parent's own ordered index progression.
func (e *engine) wireDictionaries() {
	for id, work := range e.dictWork {
		layer := e.dictLayer[id]
		for _, parent := range work.parents {
			parent.Dict.DictionaryLayer = layer
		}
	}
}

func (e *engine) requiredByteRanges() map[string][]ByteRange {
	out := map[string][]ByteRange{}
	ids := make([]string, 0, len(e.reqs))
	for id := range e.reqs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		var ranges []ByteRange
		for _, req := range e.reqs[id] {
			lo, hi, ok := req.rows.Hull()
			if !ok {
				continue
			}
			buf := e.bufferByID(id)
			if lo < 0 {
				lo = 0
			}
			if hi > buf.Length {
				hi = buf.Length
			}
			if lo >= hi {
				continue
			}
			ranges = append(ranges, buf.ByteRange(lo, hi, req.width))
		}
		// Keep each layer requirement separate: merging happens only during
		// segment optimization, so the projection retains precise hulls.
		out[id] = ranges
	}
	return out
}

func (e *engine) forbiddenByteRanges() map[string][]ByteRange {
	merged := map[string][]ByteRange{}
	for id, ranges := range e.forbiddenBytes {
		merged[id] = append(merged[id], ranges...)
	}
	ids := make([]string, 0, len(e.forbiddenRows))
	for id := range e.forbiddenRows {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		buf := e.bufferByID(id)
		for _, sh := range e.forbiddenRows[id] {
			for _, ap := range sh.rows.APs {
				lo, hi := ap.Hull()
				if lo < 0 {
					lo = 0
				}
				if hi > buf.Length {
					hi = buf.Length
				}
				if lo >= hi {
					continue
				}
				merged[id] = append(merged[id], buf.ByteRange(lo, hi, sh.width))
			}
		}
	}
	out := map[string][]ByteRange{}
	for id, ranges := range merged {
		out[id] = mergeRanges(ranges)
	}
	return out
}

func (e *engine) bufferByID(id string) *Buffer {
	if b, ok := e.buffers[id]; ok {
		return b
	}
	panic("nestedslice: unknown buffer id " + id)
}

func (e *engine) optimize() ([]ReadSegment, int64) {
	required := e.requiredByteRanges()
	forbidden := e.forbiddenByteRanges()
	weights := costWeights{startup: e.opts.StartupCost, perByte: e.opts.PerByteCost}

	ids := make([]string, 0)
	for id := range required {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var segments []ReadSegment
	var total int64
	for _, id := range ids {
		planned := optimizeSegments(required[id], forbidden[id], weights)
		for i := range planned {
			planned[i].BufferID = id
			total = addChecked(total, planned[i].Cost)
		}
		segments = append(segments, planned...)
	}
	return segments, total
}

func collectBuffers(n *Node, out map[string]*Buffer) {
	for _, ref := range n.bufferRefs() {
		if existing, ok := out[ref.Buffer.ID]; ok && existing != ref.Buffer {
			panic("nestedslice: buffer id reused with different metadata: " + ref.Buffer.ID)
		}
		out[ref.Buffer.ID] = ref.Buffer
	}
	switch n.Kind {
	case ListNode:
		if n.Children != nil {
			collectBuffers(n.Children[0], out)
		}
	case StructNode, DenseUnionNode:
		for _, child := range n.Children {
			collectBuffers(child, out)
		}
	case DictionaryNode:
		collectBuffers(n.Dictionary, out)
	}
}

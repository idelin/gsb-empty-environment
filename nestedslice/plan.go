package nestedslice

// Query selects TopRows of the root array and restricts the projection to the
// given leaf field paths. An empty Paths value projects every leaf. Path
// elements name struct fields (or dense-union type names); list, dictionary
// and union navigation itself does not consume a path element.
type Query struct {
	TopRows PSet
	Paths   [][]string
}

// Options controls planning. Forbidden lists explicit byte ranges that must
// never be read for each buffer; the planner additionally derives shadow
// ranges from null parents.
type Options struct {
	// MaxExpand caps materialization; zero means MaxExpand.
	MaxExpand int64
	// StartupCost is charged once per physical read segment.
	StartupCost int64
	// PerByteCost is charged per read byte.
	PerByteCost int64
	Forbidden   map[string][]ByteRange
}

func (o Options) expandLimit() int64 {
	if o.MaxExpand <= 0 {
		return MaxExpand
	}
	return o.MaxExpand
}

// BufferSpec identifies the required elements of one physical buffer.
type BufferSpec struct {
	BufferID string
	Kind     BufferKind
	// Rows are the required element indices as a periodic set.
	Rows PSet
	// BaseIndex is the independent offset base of this layer slice.
	BaseIndex int64
	// Width records the fixed element width for value buffers.
	Width int64
}

// ValiditySpec carries the bitmap rows and the null subset.
type ValiditySpec struct {
	BufferSpec
	// NullRows are the logical null rows within Rows.
	NullRows PSet
}

// OffsetSpec carries list boundary entries. FixedSize lists additionally
// expose the affine boundary rule so no offset bytes are required to restore
// logical boundaries.
type OffsetSpec struct {
	BufferSpec
	FixedSize   bool
	OffsetFirst int64
	Stride      int64
}

// ChildLayer binds a named child to its projection layer.
type ChildLayer struct {
	Name  string
	Layer *LayerSpec
}

// DictSpec records how a dictionary-array layer references the shared
// dictionary projection.
type DictSpec struct {
	// DictionaryLayer is the single shared projection of the dictionary
	// subtree, even when several parent layers reference it.
	DictionaryLayer *LayerSpec
	// OrderedIndices keeps the parent iteration order; negative-step APs
	// encode reversed selections.
	OrderedIndices PSet
}

// LayerSpec is the projection of one array layer. Every layer owns an
// independent slice start and offset base.
type LayerSpec struct {
	NodeID string
	Kind   NodeKind

	// SliceStart is the minimum logical row of the slice.
	SliceStart int64
	// BaseIndex is the physical index mapped to logical row zero of the
	// projected slice.
	BaseIndex int64
	// Rows is the set of physical rows contributing to the slice, including
	// rows shadowed by null ancestors (they remain marked forbidden).
	Rows PSet
	// OrderedRows keeps parent iteration order when it differs from the set
	// ordering, notably reversed dictionary selections.
	OrderedRows PSet

	Validity    *ValiditySpec
	Values      *BufferSpec
	Offsets     *OffsetSpec
	TypeIDs     *BufferSpec
	UnionOffset *BufferSpec
	DictIndex   *BufferSpec

	Children []ChildLayer
	Dict     *DictSpec
}

// ReadSegment is one physical read of a buffer with an all-in byte cost.
type ReadSegment struct {
	BufferID string
	ByteRange
	Bytes int64
	Cost  int64
}

// Plan is the full projection result.
type Plan struct {
	Root     *LayerSpec
	Segments []ReadSegment
	// RequiredRanges are the unmerged byte hulls requested by the layers.
	RequiredRanges map[string][]ByteRange
	// ForbiddenRanges merges caller-supplied and null-derived shadows.
	ForbiddenRanges map[string][]ByteRange
	Cost            int64
}

// BuildPlan projects root for q under opts and returns the minimum-cost read
// plan plus the layer projection needed to restore logical boundaries.
func BuildPlan(root *Node, q Query, opts Options) (*Plan, error) {
	buffers := map[string]*Buffer{}
	collectBuffers(root, buffers)
	e := newEngine(opts, buffers)
	layer, err := e.planNode(root, q.TopRows, q.Paths, nil, true)
	if err != nil {
		return nil, err
	}
	if err := e.planSharedDictionaries(); err != nil {
		return nil, err
	}
	e.wireDictionaries()
	segments, cost := e.optimize()
	return &Plan{
		Root:            layer,
		Segments:        segments,
		RequiredRanges:  e.requiredByteRanges(),
		ForbiddenRanges: e.forbiddenByteRanges(),
		Cost:            cost,
	}, nil
}

type costWeights struct {
	startup int64
	perByte int64
}

// optimizeSegments solves the minimum-cost segment covering problem. Required
// hulls may overlap or touch; forbidden ranges are hard barriers. Between
// barriers the dynamic program chooses either one spanning read or a split at
// every gap, which is optimal for positive linear read costs.
func optimizeSegments(required, forbidden []ByteRange, w costWeights) []ReadSegment {
	req := mergeRanges(required)
	fb := mergeRanges(forbidden)

	// Cut every required hull at forbidden barriers.
	var pieces []ByteRange
	for _, r := range req {
		pieces = append(pieces, subtractRanges(r, fb)...)
	}
	pieces = mergeRanges(pieces)

	// Group barrier-free pieces: consecutive pieces belong together only while
	// no forbidden byte lies in the gap between them.
	var groups [][]ByteRange
	for _, p := range pieces {
		if len(groups) == 0 {
			groups = append(groups, []ByteRange{p})
			continue
		}
		last := groups[len(groups)-1]
		prev := last[len(last)-1]
		if anyForbiddenBetween(prev.End, p.Start, fb) {
			groups = append(groups, []ByteRange{p})
		} else {
			groups[len(groups)-1] = append(last, p)
		}
	}

	var segments []ReadSegment
	for _, group := range groups {
		segments = append(segments, bestGroupSegments(group, w)...)
	}
	return segments
}

func subtractRanges(r ByteRange, forbidden []ByteRange) []ByteRange {
	pieces := []ByteRange{r}
	for _, f := range forbidden {
		if f.End <= r.Start || f.Start >= r.End {
			continue
		}
		var next []ByteRange
		for _, p := range pieces {
			lo := max64(p.Start, f.Start)
			hi := min64(p.End, f.End)
			if lo >= hi {
				next = append(next, p)
				continue
			}
			if p.Start < lo {
				next = append(next, ByteRange{Start: p.Start, End: lo})
			}
			if hi < p.End {
				next = append(next, ByteRange{Start: hi, End: p.End})
			}
		}
		pieces = next
	}
	return pieces
}

func anyForbiddenBetween(a, b int64, forbidden []ByteRange) bool {
	for _, f := range forbidden {
		if f.Start < b && f.End > a {
			return true
		}
	}
	return false
}

func bestGroupSegments(group []ByteRange, w costWeights) []ReadSegment {
	n := len(group)
	type choice struct {
		cost  int64
		split int
	}
	dp := make([]choice, n+1)
	for j := 1; j <= n; j++ {
		best := choice{cost: maxInt64, split: -1}
		for i := 0; i < j; i++ {
			span := ByteRange{Start: group[i].Start, End: group[j-1].End}
			c := addChecked(w.startup, mulChecked(w.perByte, span.len()))
			total := addChecked(dp[i].cost, c)
			if total < best.cost {
				best = choice{cost: total, split: i}
			}
		}
		dp[j] = best
	}

	var spans []ByteRange
	j := n
	for j > 0 {
		i := dp[j].split
		spans = append(spans, ByteRange{Start: group[i].Start, End: group[j-1].End})
		j = i
	}
	reverseRanges(spans)

	segments := make([]ReadSegment, 0, len(spans))
	for _, span := range spans {
		segments = append(segments, ReadSegment{
			ByteRange: span,
			Bytes:     span.len(),
			Cost:      addChecked(w.startup, mulChecked(w.perByte, span.len())),
		})
	}
	return segments
}

func reverseRanges(r []ByteRange) {
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

const maxInt64 = int64(^uint64(0) >> 1)

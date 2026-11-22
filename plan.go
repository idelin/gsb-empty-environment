package colarchive

import (
	"errors"
	"fmt"
	"sort"
)

// ErrInfeasible is returned when no read plan can satisfy a query, for
// example because a required byte lies in a bad region or a read limit is
// too small.
var ErrInfeasible = errors.New("colarchive: infeasible read plan")

// ErrTooComplex is returned when a query is too fragmented for the compact
// planner and exceeds the configured explicit interval limit.
var ErrTooComplex = errors.New("colarchive: query too fragmented")

// Device describes the storage device and its cost model.
//
// A read costs StartupCost + ByteCost*Length.  Length is at most MaxBytes.
// Bad regions are absolute byte ranges that a read may not intersect or
// cross.  Reading extra bytes between two required ranges is allowed, but
// those bytes are still charged.
type Device struct {
	StartupCost int64 // fixed cost per read transaction
	ByteCost    int64 // cost per byte read
	MaxBytes    int64 // maximum bytes in one transaction
	Bad         []ByteInterval
}

func (d Device) normalizedBad() ([]ByteInterval, error) {
	if d.StartupCost < 0 || d.ByteCost < 0 {
		return nil, fmt.Errorf("colarchive: negative device cost")
	}
	if d.MaxBytes <= 0 {
		return nil, fmt.Errorf("colarchive: device max bytes must be positive")
	}
	bad := append([]ByteInterval(nil), d.Bad...)
	for _, r := range bad {
		if err := r.Validate(); err != nil {
			return nil, err
		}
	}
	merged, err := mergeByteIntervals(bad)
	if err != nil {
		return nil, err
	}
	out := merged[:0]
	for _, r := range merged {
		if r.Length > 0 {
			out = append(out, r)
		}
	}
	return out, nil
}

// Read is a compact read plan item.  It denotes Repeat transactions; copy k
// (0 <= k < Repeat) reads [Start+k*Stride, Start+k*Stride+Length).
//
// A single transaction has Repeat == 1 and Stride == 0.  Repeated
// transactions are non-overlapping and sorted; Stride >= Length whenever
// Repeat > 1.
type Read struct {
	Start  int64
	Length int64
	Repeat int64
	Stride int64
}

// Interval returns copy k of the read item.
func (r Read) Interval(k int64) ByteInterval {
	return ByteInterval{Start: r.Start + k*r.Stride, Length: r.Length}
}

// End returns the end of the last transaction in the item.
func (r Read) End() int64 { return r.Start + (r.Repeat-1)*r.Stride + r.Length }

func (r Read) validate() error {
	if r.Start < 0 || r.Length <= 0 || r.Repeat <= 0 {
		return fmt.Errorf("colarchive: invalid read item")
	}
	if r.Repeat > 1 && r.Stride < r.Length {
		return fmt.Errorf("colarchive: overlapping repeated read item")
	}
	if r.Repeat > 1 && r.Stride <= 0 {
		return fmt.Errorf("colarchive: non-positive read stride")
	}
	if r.Repeat > 1 {
		last, ok := mulInt64(r.Repeat-1, r.Stride)
		if !ok {
			return fmt.Errorf("colarchive: read span overflows")
		}
		if _, ok := addInt64(r.Start, last); !ok {
			return fmt.Errorf("colarchive: read span overflows")
		}
	}
	return nil
}

// Plan is the result of planning a query.  Cost and TotalBytes account for
// the compact read items, including repeated transactions.
type Plan struct {
	Reads        []Read
	Mapping      []MapPiece
	Nulls        []NullSpan
	Cost         int64
	TotalBytes   int64
	ReadCount    int64
	RequiredRuns []ByteInterval // maximal required byte runs when small enough, otherwise nil
}

// Expand returns every transaction in the plan.  It refuses to expand more
// than limit transactions.
func (p *Plan) Expand(limit int64) ([]ByteInterval, error) {
	if p.ReadCount > limit {
		return nil, fmt.Errorf("colarchive: refusing to expand %d reads (limit %d)", p.ReadCount, limit)
	}
	out := make([]ByteInterval, 0, int(p.ReadCount))
	for _, r := range p.Reads {
		for k := int64(0); k < r.Repeat; k++ {
			out = append(out, r.Interval(k))
		}
	}
	return out, nil
}

// LogicalAt returns all requested logical bits that occupy a physical bit.
// The mapping is compact; the scan is over mapping pieces, not over rows.
func (p *Plan) LogicalAt(physicalBit int64) []BitRef {
	var out []BitRef
	for _, m := range p.Mapping {
		if m.Null || m.RowCount <= 0 || m.PhysicalStep <= 0 {
			continue
		}
		delta := physicalBit - m.PhysicalStart
		if delta < 0 || delta%m.PhysicalStep != 0 {
			continue
		}
		k := delta / m.PhysicalStep
		if k < m.RowCount {
			out = append(out, BitRef{Block: m.Block, Row: m.RowAt(k), LogicalBit: m.LogicalBit})
		}
	}
	return out
}

// Planner holds device parameters and the explicit-expansion threshold.
type Planner struct {
	Device     Device
	ExactLimit int64
}

// NewPlanner returns a planner with the default explicit interval limit.
func NewPlanner(d Device) *Planner {
	return &Planner{Device: d, ExactLimit: 4096}
}

// Plan plans reads for the query using the default exact interval limit.
func (a *Archive) Plan(q Query, d Device) (*Plan, error) {
	return NewPlanner(d).Plan(a, q)
}

// Plan plans reads for the query.
func (p *Planner) Plan(a *Archive, q Query) (*Plan, error) {
	bad, err := p.Device.normalizedBad()
	if err != nil {
		return nil, err
	}
	l, err := a.Layout()
	if err != nil {
		return nil, err
	}
	m, err := buildMapping(l, q)
	if err != nil {
		return nil, err
	}
	runs, err := runsFromMapping(m)
	if err != nil {
		return nil, err
	}
	runs, err = coalesceRuns(runs)
	if err != nil {
		return nil, err
	}
	if err := validateRunsBad(runs, bad); err != nil {
		return nil, err
	}
	limit := p.ExactLimit
	if limit <= 0 {
		limit = 4096
	}
	total, ok := totalIntervals(runs)
	if !ok {
		return nil, fmt.Errorf("%w: interval count overflow", ErrTooComplex)
	}
	var reads []Read
	var required []ByteInterval
	if total <= limit {
		intervals, _, err := expandRuns(runs, limit)
		if err != nil {
			return nil, err
		}
		required = intervals
		reads, err = planRunsExact(intervals, p.Device, bad)
		if err != nil {
			return nil, err
		}
	} else {
		reads, err = planCompressed(runs, p.Device, bad, limit)
		if err != nil {
			return nil, err
		}
	}
	reads, err = canonicalizeReads(reads, p.Device, bad)
	if err != nil {
		return nil, err
	}
	cost, bytes, count, err := totals(reads, p.Device)
	if err != nil {
		return nil, err
	}
	return &Plan{
		Reads:        reads,
		Mapping:      m.pieces,
		Nulls:        m.nulls,
		Cost:         cost,
		TotalBytes:   bytes,
		ReadCount:    count,
		RequiredRuns: required,
	}, nil
}

// validateRunsBad rejects required bytes that fall inside a bad region.  It
// works arithmetically on span runs and never enumerates their copies.
func validateRunsBad(runs []spanRun, bad []ByteInterval) error {
	if len(bad) == 0 {
		return nil
	}
	for _, r := range runs {
		if r.Count == 1 {
			if badIntersects(r.Start, r.End, bad) {
				return fmt.Errorf("%w: required bytes [%d,%d) intersect a bad region", ErrInfeasible, r.Start, r.End)
			}
			continue
		}
		req := periodicReq{
			Origin:    r.Start,
			Period:    r.Step,
			Intervals: []ByteInterval{{Start: 0, Length: r.length()}},
			Copies:    r.Count,
		}
		if err := validatePeriodicBad(req, bad); err != nil {
			return err
		}
	}
	return nil
}

// planRunsExact computes the exact optimum over maximal required byte runs.
//
// Grouping consecutive runs i..j means reading every byte from run[i].Start
// to run[j].End.  A group of span S needs ceil(S/MaxBytes) transactions and
// costs reads*StartupCost + ByteCost*S.  Splitting a group at a gap removes
// the gap bytes but adds a startup cost, so the DP is over group boundaries.
func planRunsExact(runs []ByteInterval, d Device, bad []ByteInterval) ([]Read, error) {
	if len(runs) == 0 {
		return nil, nil
	}
	merged, err := mergeByteIntervals(runs)
	if err != nil {
		return nil, err
	}
	for _, r := range merged {
		if badIntersects(r.Start, r.End(), bad) {
			return nil, fmt.Errorf("%w: required bytes [%d,%d) intersect a bad region", ErrInfeasible, r.Start, r.End())
		}
	}
	var out []Read
	segStart := 0
	for i := 1; i <= len(merged); i++ {
		split := i == len(merged)
		if !split {
			// A bad region in the gap forbids one transaction from crossing it.
			if badBetween(merged[i-1].End(), merged[i].Start, bad) {
				split = true
			}
		}
		if !split {
			continue
		}
		part, err := planGoodSegment(merged[segStart:i], d)
		if err != nil {
			return nil, err
		}
		out = append(out, part...)
		segStart = i
	}
	return out, nil
}

func planGoodSegment(runs []ByteInterval, d Device) ([]Read, error) {
	n := len(runs)
	if n == 0 {
		return nil, nil
	}
	if d.StartupCost == 0 {
		// Transactions are free.  The secondary objectives are total bytes
		// and then lexicographically smallest intervals, so read every
		// required byte separately and never read a gap.
		var out []Read
		for _, r := range runs {
			if r.Length == 0 {
				continue
			}
			out = append(out, Read{Start: r.Start, Length: 1, Repeat: r.Length, Stride: 1})
		}
		return out, nil
	}
	const inf = int64(1<<62 - 1)
	sufCost := make([]int64, n+1)
	sufBytes := make([]int64, n+1)
	choice := make([]int, n)
	for i := n - 1; i >= 0; i-- {
		bestCost := inf
		bestBytes := inf
		bestFirstEnd := inf
		bestJ := -1
		for j := i; j < n; j++ {
			span := runs[j].End() - runs[i].Start
			k := ceilDiv64(span, d.MaxBytes)
			groupCost, ok := mulInt64(k, d.StartupCost)
			if !ok {
				return nil, fmt.Errorf("%w: cost overflow", ErrInfeasible)
			}
			more, ok := mulInt64(d.ByteCost, span)
			if !ok {
				return nil, fmt.Errorf("%w: cost overflow", ErrInfeasible)
			}
			groupCost, ok = addInt64(groupCost, more)
			if !ok {
				return nil, fmt.Errorf("%w: cost overflow", ErrInfeasible)
			}
			totalCost, ok := addInt64(groupCost, sufCost[j+1])
			if !ok {
				return nil, fmt.Errorf("%w: cost overflow", ErrInfeasible)
			}
			totalBytes, ok := addInt64(span, sufBytes[j+1])
			if !ok {
				return nil, fmt.Errorf("%w: byte count overflow", ErrInfeasible)
			}
			firstLen := span - (k-1)*d.MaxBytes
			firstEnd := runs[i].Start + firstLen
			if totalCost < bestCost ||
				(totalCost == bestCost && totalBytes < bestBytes) ||
				(totalCost == bestCost && totalBytes == bestBytes && firstEnd < bestFirstEnd) ||
				(totalCost == bestCost && totalBytes == bestBytes && firstEnd == bestFirstEnd && j < bestJ) {
				bestCost, bestBytes, bestFirstEnd, bestJ = totalCost, totalBytes, firstEnd, j
			}
		}
		if bestJ < 0 {
			return nil, fmt.Errorf("%w: cannot cover runs", ErrInfeasible)
		}
		sufCost[i], sufBytes[i], choice[i] = bestCost, bestBytes, bestJ
	}
	var out []Read
	for i := 0; i < n; {
		j := choice[i]
		start := runs[i].Start
		span := runs[j].End() - start
		part, err := contiguousReads(start, span, d)
		if err != nil {
			return nil, err
		}
		out = append(out, part...)
		i = j + 1
	}
	return out, nil
}

func planCompressed(runs []spanRun, d Device, bad []ByteInterval, limit int64) ([]Read, error) {
	if part, ok, err := tryUniformPlan(runs, d); err != nil {
		return nil, err
	} else if ok {
		return part, nil
	}
	periods, singles, err := buildPeriodic(runs)
	if err != nil {
		return nil, err
	}
	var out []Read
	for _, req := range periods {
		if err := validatePeriodicBad(req, bad); err != nil {
			return nil, err
		}
		part, err := planPeriodic(req, d)
		if err != nil {
			return nil, err
		}
		out = append(out, part...)
	}
	if len(singles) > 0 {
		merged, err := mergeByteIntervals(singles)
		if err != nil {
			return nil, err
		}
		singleLimit := limit
		if singleLimit < 4096 {
			singleLimit = 4096
		}
		if int64(len(merged)) > singleLimit {
			return nil, fmt.Errorf("%w: %d explicit runs", ErrTooComplex, len(merged))
		}
		part, err := planRunsExact(merged, d, bad)
		if err != nil {
			return nil, err
		}
		out = append(out, part...)
	}
	return out, nil
}

// validatePeriodicBad checks that no required interval of the periodic
// request intersects a bad region.  It is arithmetic in Copies.
func validatePeriodicBad(req periodicReq, bad []ByteInterval) error {
	if req.Copies <= 0 || len(bad) == 0 {
		return nil
	}
	for _, iv := range req.Intervals {
		a := req.Origin + iv.Start
		length := iv.Length
		for _, br := range bad {
			// Need k in [0,Copies) with a+kP < br.End and a+kP+length > br.Start.
			loNum := br.Start - a - length
			// k > loNum/P and k < hiNum/P.
			lo := floorDiv64(loNum, req.Period) + 1
			hi := floorDiv64(br.End()-a-1, req.Period)
			if lo < 0 {
				lo = 0
			}
			if hi >= req.Copies {
				hi = req.Copies - 1
			}
			if lo <= hi {
				return fmt.Errorf("%w: required bytes intersect a bad region", ErrInfeasible)
			}
		}
	}
	return nil
}

// planPeriodic plans a constant-pattern periodic request.  It first solves
// one period exactly, then decides uniformly whether adjacent periods should
// be joined.  It returns compact repeated read items.
func planPeriodic(req periodicReq, d Device) ([]Read, error) {
	if req.Copies <= 0 {
		return nil, nil
	}
	if req.Period <= 0 {
		return nil, fmt.Errorf("colarchive: invalid period")
	}
	rel, err := mergeByteIntervals(req.Intervals)
	if err != nil {
		return nil, err
	}
	if len(rel) == 0 {
		return nil, nil
	}
	if d.StartupCost == 0 {
		var out []Read
		for _, iv := range rel {
			if iv.Length > 1<<20 {
				return nil, fmt.Errorf("%w: free-startup periodic pattern has a huge dense interval", ErrTooComplex)
			}
			for off := int64(0); off < iv.Length; off++ {
				out = append(out, Read{
					Start:  req.Origin + iv.Start + off,
					Length: 1,
					Repeat: req.Copies,
					Stride: req.Period,
				})
			}
		}
		return out, nil
	}
	// A full period is a single contiguous span.
	if len(rel) == 1 && rel[0].Start == 0 && rel[0].Length == req.Period {
		span, ok := mulInt64(req.Copies, req.Period)
		if !ok {
			return nil, fmt.Errorf("%w: periodic span overflow", ErrInfeasible)
		}
		return contiguousReads(req.Origin, span, d)
	}
	absolute := make([]ByteInterval, len(rel))
	for i, iv := range rel {
		absolute[i] = ByteInterval{Start: req.Origin + iv.Start, Length: iv.Length}
	}
	rawBase, err := planGoodSegment(absolute, d)
	if err != nil {
		return nil, err
	}
	if len(rawBase) == 0 {
		return nil, nil
	}
	// planGoodSegment may tile a long within-period span.  The cross-period
	// boundary logic below works on individual transactions, so expand the
	// (normally tiny) one-period plan first.
	const maxBaseTransactions = int64(1 << 18)
	var base []ByteInterval
	for _, r := range rawBase {
		if r.Repeat > maxBaseTransactions || int64(len(base))+r.Repeat > maxBaseTransactions {
			return nil, fmt.Errorf("%w: period has too many internal transactions", ErrTooComplex)
		}
		for k := int64(0); k < r.Repeat; k++ {
			base = append(base, r.Interval(k))
		}
	}
	// Work relative to the origin for compact output.
	for i := range base {
		base[i].Start -= req.Origin
	}
	copies := req.Copies
	if copies == 1 {
		out := make([]Read, 0, len(base))
		for i := range base {
			out = append(out, Read{Start: base[i].Start + req.Origin, Length: base[i].Length, Repeat: 1})
		}
		return out, nil
	}
	first := base[0]
	last := base[len(base)-1]
	gap := req.Period + first.Start - last.End()
	merge := false
	if d.StartupCost > 0 {
		byteGap, ok := mulInt64(d.ByteCost, gap)
		if !ok {
			return nil, fmt.Errorf("%w: cost overflow", ErrInfeasible)
		}
		merge = d.StartupCost > byteGap
	}
	if len(base) == 1 {
		length := first.Length
		if !merge {
			return []Read{{Start: req.Origin + first.Start, Length: length, Repeat: copies, Stride: req.Period}}, nil
		}
		k := int64(1)
		if length < d.MaxBytes {
			k = (d.MaxBytes-length)/req.Period + 1
		}
		if k < 1 {
			k = 1
		}
		if k == 1 {
			return []Read{{Start: req.Origin + first.Start, Length: length, Repeat: copies, Stride: req.Period}}, nil
		}
		groups := copies / k
		rem := copies % k
		var out []Read
		if groups > 0 {
			out = append(out, Read{
				Start:  req.Origin + first.Start,
				Length: length + (k-1)*req.Period,
				Repeat: groups,
				Stride: k * req.Period,
			})
		}
		if rem > 0 {
			out = append(out, Read{
				Start:  req.Origin + first.Start + groups*k*req.Period,
				Length: length + (rem-1)*req.Period,
				Repeat: 1,
			})
		}
		return out, nil
	}
	// More than one read per period.
	mergedLen := req.Period + base[0].End() - last.Start
	canMerge := merge && mergedLen <= d.MaxBytes
	if !canMerge {
		var out []Read
		for _, r := range base {
			out = append(out, Read{Start: req.Origin + r.Start, Length: r.Length, Repeat: copies, Stride: req.Period})
		}
		return out, nil
	}
	var out []Read
	// Read zero of the first period is the only unmerged read zero.
	out = append(out, Read{Start: req.Origin + base[0].Start, Length: base[0].Length, Repeat: 1})
	for j := 1; j < len(base)-1; j++ {
		out = append(out, Read{Start: req.Origin + base[j].Start, Length: base[j].Length, Repeat: copies, Stride: req.Period})
	}
	out = append(out, Read{
		Start:  req.Origin + last.Start,
		Length: mergedLen,
		Repeat: copies - 1,
		Stride: req.Period,
	})
	out = append(out, Read{
		Start:  req.Origin + (copies-1)*req.Period + last.Start,
		Length: last.Length,
		Repeat: 1,
	})
	return out, nil
}

// contiguousReads represents the minimal-transaction cover of one contiguous
// span.  The first transaction is as short as possible while still leaving
// room for the remaining ceil(span/MaxBytes)-1 transactions, which is the
// lexicographically smallest interval sequence for that fixed cost.
func contiguousReads(start, span int64, d Device) ([]Read, error) {
	if span <= 0 {
		return nil, nil
	}
	k := ceilDiv64(span, d.MaxBytes)
	firstLen := span - (k-1)*d.MaxBytes
	out := []Read{{Start: start, Length: firstLen, Repeat: 1}}
	rest := k - 1
	if rest > 0 {
		stride := int64(0)
		if rest > 1 {
			stride = d.MaxBytes
		}
		out = append(out, Read{
			Start:  start + firstLen,
			Length: d.MaxBytes,
			Repeat: rest,
			Stride: stride,
		})
	}
	return out, nil
}

func canonicalizeReads(reads []Read, d Device, bad []ByteInterval) ([]Read, error) {
	if len(reads) == 0 {
		return nil, nil
	}
	for _, r := range reads {
		if err := r.validate(); err != nil {
			return nil, err
		}
	}
	// Validate ordering and split reads at bad regions.
	var staged []Read
	for _, r := range reads {
		pieces, err := splitReadAtBad(r, bad)
		if err != nil {
			return nil, err
		}
		staged = append(staged, pieces...)
	}
	// Check non-overlap and sort.
	sort.Slice(staged, func(i, j int) bool {
		if staged[i].Start != staged[j].Start {
			return staged[i].Start < staged[j].Start
		}
		return staged[i].End() < staged[j].End()
	})
	for i := 1; i < len(staged); i++ {
		if staged[i].Start < staged[i-1].End() {
			return nil, fmt.Errorf("%w: internal overlapping read plan", ErrInfeasible)
		}
	}
	// Greedily join adjacent transactions when doing so reduces cost.
	changed := true
	for changed {
		changed = false
		for i := 0; i+1 < len(staged); i++ {
			a, b := staged[i], staged[i+1]
			// Joining two large repeated runs one copy at a time could
			// expand an obviously periodic plan, so only bridge a repeated
			// run with a single boundary transaction.
			if a.Repeat > 1 && b.Repeat > 1 {
				continue
			}
			aLast := a.Interval(a.Repeat - 1)
			bFirst := b.Interval(0)
			gap := bFirst.Start - aLast.End()
			if gap < 0 {
				return nil, fmt.Errorf("%w: overlapping reads", ErrInfeasible)
			}
			if badBetween(aLast.End(), bFirst.Start, bad) {
				continue
			}
			joinedLen := bFirst.End() - aLast.Start
			if joinedLen > d.MaxBytes {
				continue
			}
			byteGap, ok := mulInt64(d.ByteCost, gap)
			if !ok {
				return nil, fmt.Errorf("%w: cost overflow", ErrInfeasible)
			}
			beneficial := d.StartupCost > byteGap
			if !beneficial {
				continue
			}
			var mid []Read
			if a.Repeat > 1 {
				a.Repeat--
				mid = append(mid, a)
			}
			mid = append(mid, Read{Start: aLast.Start, Length: joinedLen, Repeat: 1})
			if b.Repeat > 1 {
				b.Start += b.Stride
				b.Repeat--
				mid = append(mid, b)
			}
			next := make([]Read, 0, len(staged))
			next = append(next, staged[:i]...)
			next = append(next, mid...)
			next = append(next, staged[i+2:]...)
			staged = next
			changed = true
			break
		}
	}
	for i := range staged {
		if staged[i].Repeat == 1 {
			staged[i].Stride = 0
		}
	}
	return staged, nil
}

// splitReadAtBad removes bad bytes from a (possibly repeated) read item
// without expanding copies that do not touch a bad region.
func splitReadAtBad(r Read, bad []ByteInterval) ([]Read, error) {
	if len(bad) == 0 || !readIntersectsBad(r, bad) {
		return []Read{r}, nil
	}
	if r.Repeat == 1 {
		pieces, err := subtractBad(r.Interval(0), bad)
		if err != nil {
			return nil, err
		}
		out := make([]Read, 0, len(pieces))
		for _, p := range pieces {
			out = append(out, Read{Start: p.Start, Length: p.Length, Repeat: 1})
		}
		return out, nil
	}
	type span struct{ lo, hi int64 } // inclusive copy indices
	var spans []span
	for _, b := range bad {
		// Copy k intersects [b.Start,b.End) iff
		//   Start+k*Stride < b.End  and  Start+k*Stride+Length > b.Start.
		lo := floorDiv64(b.Start-r.Start-r.Length, r.Stride) + 1
		hi := floorDiv64(b.End()-r.Start-1, r.Stride)
		if lo < 0 {
			lo = 0
		}
		if hi >= r.Repeat {
			hi = r.Repeat - 1
		}
		if lo <= hi {
			spans = append(spans, span{lo, hi})
		}
	}
	if len(spans) == 0 {
		return []Read{r}, nil
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].lo < spans[j].lo })
	merged := spans[:1]
	for _, s := range spans[1:] {
		last := &merged[len(merged)-1]
		if s.lo <= last.hi+1 {
			if s.hi > last.hi {
				last.hi = s.hi
			}
		} else {
			merged = append(merged, s)
		}
	}
	const maxAffectedCopies = int64(1 << 18)
	var affected int64
	for _, s := range merged {
		if s.hi-s.lo+1 > maxAffectedCopies-affected {
			return nil, fmt.Errorf("%w: bad region touches too many copies of one repeated read", ErrTooComplex)
		}
		affected += s.hi - s.lo + 1
	}
	var out []Read
	pos := int64(0)
	for _, s := range merged {
		if pos < s.lo {
			n := s.lo - pos
			out = append(out, Read{
				Start:  r.Start + pos*r.Stride,
				Length: r.Length,
				Repeat: n,
				Stride: r.Stride,
			})
		}
		for k := s.lo; k <= s.hi; k++ {
			pieces, err := subtractBad(r.Interval(k), bad)
			if err != nil {
				return nil, err
			}
			for _, p := range pieces {
				out = append(out, Read{Start: p.Start, Length: p.Length, Repeat: 1})
			}
		}
		pos = s.hi + 1
	}
	if pos < r.Repeat {
		out = append(out, Read{
			Start:  r.Start + pos*r.Stride,
			Length: r.Length,
			Repeat: r.Repeat - pos,
			Stride: r.Stride,
		})
	}
	return out, nil
}

func readIntersectsBad(r Read, bad []ByteInterval) bool {
	lo := r.Start
	hi := r.End()
	for _, b := range bad {
		if b.End() <= lo {
			continue
		}
		if b.Start >= hi {
			break
		}
		return true
	}
	return false
}

func subtractBad(iv ByteInterval, bad []ByteInterval) ([]ByteInterval, error) {
	if !badIntersects(iv.Start, iv.End(), bad) {
		return []ByteInterval{iv}, nil
	}
	var out []ByteInterval
	pos := iv.Start
	end := iv.End()
	for _, b := range bad {
		if b.End() <= pos {
			continue
		}
		if b.Start >= end {
			break
		}
		if b.Start > pos {
			out = append(out, ByteInterval{Start: pos, Length: b.Start - pos})
		}
		if b.End() > pos {
			pos = b.End()
		}
		if pos >= end {
			break
		}
	}
	if pos < end {
		out = append(out, ByteInterval{Start: pos, Length: end - pos})
	}
	return out, nil
}

func badIntersects(start, end int64, bad []ByteInterval) bool {
	for _, b := range bad {
		if b.End() <= start {
			continue
		}
		if b.Start >= end {
			break
		}
		return true
	}
	return false
}

func badBetween(start, end int64, bad []ByteInterval) bool {
	if end <= start {
		return false
	}
	for _, b := range bad {
		if b.End() <= start {
			continue
		}
		if b.Start >= end {
			break
		}
		return true
	}
	return false
}

func totals(reads []Read, d Device) (cost, bytes, count int64, err error) {
	for _, r := range reads {
		itemBytes, ok := mulInt64(r.Length, r.Repeat)
		if !ok {
			return 0, 0, 0, fmt.Errorf("%w: byte count overflow", ErrInfeasible)
		}
		bytes, ok = addInt64(bytes, itemBytes)
		if !ok {
			return 0, 0, 0, fmt.Errorf("%w: byte count overflow", ErrInfeasible)
		}
		count, ok = addInt64(count, r.Repeat)
		if !ok {
			return 0, 0, 0, fmt.Errorf("%w: read count overflow", ErrInfeasible)
		}
	}
	startup, ok := mulInt64(count, d.StartupCost)
	if !ok {
		return 0, 0, 0, fmt.Errorf("%w: cost overflow", ErrInfeasible)
	}
	byteCost, ok := mulInt64(bytes, d.ByteCost)
	if !ok {
		return 0, 0, 0, fmt.Errorf("%w: cost overflow", ErrInfeasible)
	}
	cost, ok = addInt64(startup, byteCost)
	if !ok {
		return 0, 0, 0, fmt.Errorf("%w: cost overflow", ErrInfeasible)
	}
	return cost, bytes, count, nil
}

func floorDiv64(a, b int64) int64 {
	q := a / b
	r := a % b
	if r != 0 && (r < 0) != (b < 0) {
		q--
	}
	return q
}

// planUniformRun plans a single arithmetic run of equal-length byte intervals.
// Grouping k consecutive intervals reads the span k*Step-(Step-Length); the
// span is covered by ceil(span/MaxBytes) transactions.  Candidate group sizes
// are the largest number of intervals that fit in j transactions.  Trying all
// such j, plus the all-in-one group, gives the optimum for the uniform case.
func planUniformRun(r spanRun, d Device) ([]Read, error) {
	n := r.Count
	if n <= 0 {
		return nil, nil
	}
	if r.Step <= 0 {
		return nil, fmt.Errorf("colarchive: invalid uniform run")
	}
	length := r.length()
	if length <= 0 {
		return nil, fmt.Errorf("colarchive: empty uniform run")
	}
	gap := r.Step - length
	if gap < 0 {
		return nil, fmt.Errorf("colarchive: uniform run overlaps")
	}
	maxJ := int64(100000)
	maxJLimit := ceilDiv64(n*r.Step, d.MaxBytes) + 1
	if maxJLimit < maxJ {
		maxJ = maxJLimit
	}
	if maxJ < 1 {
		maxJ = 1
	}
	bestCost, bestBytes := int64(1<<62-1), int64(1<<62-1)
	bestFirstEnd := int64(1<<62 - 1)
	bestK := int64(0)
	var spanFor = func(k int64) (span, reads int64) {
		span = k*r.Step - gap
		reads = ceilDiv64(span, d.MaxBytes)
		return span, reads
	}
	consider := func(k int64) error {
		if k < 1 {
			return nil
		}
		if k > n {
			k = n
		}
		if k == 0 || k == bestK {
			return nil
		}
		span, reads := spanFor(k)
		groups := n / k
		// Multiple transaction slots repeated over multiple groups would
		// interleave in byte order; keep the output representable as a
		// sorted, non-overlapping list of compact items.
		if groups > 1 && reads > 1 && k != n {
			return nil
		}
		groupCost, ok := mulInt64(reads, d.StartupCost)
		if !ok {
			return fmt.Errorf("%w: cost overflow", ErrInfeasible)
		}
		more, ok := mulInt64(d.ByteCost, span)
		if !ok {
			return fmt.Errorf("%w: cost overflow", ErrInfeasible)
		}
		groupCost, ok = addInt64(groupCost, more)
		if !ok {
			return fmt.Errorf("%w: cost overflow", ErrInfeasible)
		}
		rem := n % k
		totalCost, ok := mulInt64(groups, groupCost)
		if !ok {
			return fmt.Errorf("%w: cost overflow", ErrInfeasible)
		}
		totalBytes, ok := mulInt64(groups, span)
		if !ok {
			return fmt.Errorf("%w: byte count overflow", ErrInfeasible)
		}
		if rem > 0 {
			remSpan := rem*r.Step - gap
			remReads := ceilDiv64(remSpan, d.MaxBytes)
			rc, ok := mulInt64(remReads, d.StartupCost)
			if !ok {
				return fmt.Errorf("%w: cost overflow", ErrInfeasible)
			}
			rb, ok := mulInt64(d.ByteCost, remSpan)
			if !ok {
				return fmt.Errorf("%w: cost overflow", ErrInfeasible)
			}
			rc, ok = addInt64(rc, rb)
			if !ok {
				return fmt.Errorf("%w: cost overflow", ErrInfeasible)
			}
			totalCost, ok = addInt64(totalCost, rc)
			if !ok {
				return fmt.Errorf("%w: cost overflow", ErrInfeasible)
			}
			totalBytes, ok = addInt64(totalBytes, remSpan)
			if !ok {
				return fmt.Errorf("%w: byte count overflow", ErrInfeasible)
			}
		}
		firstLen := span - (reads-1)*d.MaxBytes
		firstEnd := r.Start + firstLen
		if totalCost < bestCost ||
			(totalCost == bestCost && totalBytes < bestBytes) ||
			(totalCost == bestCost && totalBytes == bestBytes && firstEnd < bestFirstEnd) ||
			(totalCost == bestCost && totalBytes == bestBytes && firstEnd == bestFirstEnd && k < bestK) {
			bestCost, bestBytes, bestFirstEnd, bestK = totalCost, totalBytes, firstEnd, k
		}
		return nil
	}
	for j := int64(1); j <= maxJ; j++ {
		k := (j*d.MaxBytes + gap) / r.Step
		if k < 1 {
			k = 1
		}
		if err := consider(k); err != nil {
			return nil, err
		}
	}
	if err := consider(1); err != nil {
		return nil, err
	}
	if err := consider(n); err != nil {
		return nil, err
	}
	if bestK == 0 {
		return nil, fmt.Errorf("%w: no uniform grouping", ErrInfeasible)
	}
	k := bestK
	span, reads := spanFor(k)
	groups := n / k
	rem := n % k
	var out []Read
	if groups > 0 {
		firstLen := span - (reads-1)*d.MaxBytes
		if reads > 1<<20 {
			return nil, fmt.Errorf("%w: uniform group needs too many transaction slots", ErrTooComplex)
		}
		for t := int64(0); t < reads; t++ {
			lengthT := d.MaxBytes
			startT := r.Start + firstLen + (t-1)*d.MaxBytes
			if t == 0 {
				lengthT = firstLen
				startT = r.Start
			}
			out = append(out, Read{
				Start:  startT,
				Length: lengthT,
				Repeat: groups,
				Stride: k * r.Step,
			})
		}
	}
	if rem > 0 {
		start := r.Start + groups*k*r.Step
		remSpan := rem*r.Step - gap
		part, err := contiguousReads(start, remSpan, d)
		if err != nil {
			return nil, err
		}
		out = append(out, part...)
	}
	return out, nil
}

// tryUniformPlan recognises large requests made of independent uniform
// arithmetic runs separated by gaps that the exact planner would never join.
func tryUniformPlan(runs []spanRun, d Device) ([]Read, bool, error) {
	type block struct {
		start, end int64
		uniform    *spanRun
	}
	var blocks []block
	var singles []ByteInterval
	for i := range runs {
		r := runs[i]
		if r.Count == 1 {
			singles = append(singles, ByteInterval{Start: r.Start, Length: r.length()})
			continue
		}
		b := block{start: r.Start, end: r.Start + (r.Count-1)*r.Step + r.length(), uniform: &runs[i]}
		blocks = append(blocks, b)
	}
	if len(singles) > 0 {
		merged, err := mergeByteIntervals(singles)
		if err != nil {
			return nil, false, err
		}
		for _, iv := range merged {
			blocks = append(blocks, block{start: iv.Start, end: iv.End()})
		}
	}
	if len(blocks) == 0 {
		return nil, true, nil
	}
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].start < blocks[j].start })
	// cheap[i] is true when joining blocks i-1 and i cannot reduce cost, so
	// the exact planner would keep them independent.
	cheap := make([]bool, len(blocks))
	for i := 1; i < len(blocks); i++ {
		if blocks[i].start < blocks[i-1].end {
			return nil, false, nil
		}
		gap := blocks[i].start - blocks[i-1].end
		byteGap, ok := mulInt64(d.ByteCost, gap)
		if !ok {
			return nil, false, fmt.Errorf("%w: cost overflow", ErrInfeasible)
		}
		cheap[i] = d.StartupCost <= byteGap
	}
	for i := 1; i < len(blocks); i++ {
		if !cheap[i] {
			return nil, false, nil
		}
	}
	var out []Read
	for _, b := range blocks {
		if b.uniform != nil {
			part, err := planUniformRun(*b.uniform, d)
			if err != nil {
				return nil, false, err
			}
			out = append(out, part...)
			continue
		}
		part, err := contiguousReads(b.start, b.end-b.start, d)
		if err != nil {
			return nil, false, err
		}
		out = append(out, part...)
	}
	return out, true, nil
}

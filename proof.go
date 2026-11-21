package admission

import (
	"fmt"
	"math"
)

const (
	maxProofClaims = 1 << 18
	maxFlatten     = 1 << 16
)

// Snapshot is an immutable compiled graph version.
type Snapshot struct {
	graph   *compiledGraph
	version uint64
}

// Compile validates a complete graph and returns an immutable snapshot.
func Compile(g Graph) (*Snapshot, error) {
	cg, err := compileGraph(g)
	if err != nil {
		return nil, err
	}
	return &Snapshot{graph: cg}, nil
}

// Version returns the version assigned by Store.Publish. Compile returns zero.
func (s *Snapshot) Version() uint64 { return s.version }

// Handle binds a volume name to this exact graph version.
func (s *Snapshot) Handle(name string) (*Handle, error) {
	if s == nil || s.graph == nil {
		return nil, fmt.Errorf("%w: nil snapshot", ErrInvalid)
	}
	if _, err := s.graph.volume(name); err != nil {
		return nil, err
	}
	return &Handle{snapshot: s, volume: name}, nil
}

// Roots returns the graph roots in declaration order.
func (s *Snapshot) Roots() []string {
	if s == nil || s.graph == nil {
		return nil
	}
	return append([]string(nil), s.graph.roots...)
}

// Proof builds a replayable certificate for [start,start+length).
func (s *Snapshot) Proof(volume string, start, length uint64) (Proof, error) {
	if s == nil || s.graph == nil {
		return Proof{}, fmt.Errorf("%w: nil snapshot", ErrInvalid)
	}
	cv, err := s.graph.volume(volume)
	if err != nil {
		return Proof{}, err
	}
	end, ok := addOK(start, length)
	if !ok || end > cv.length || length > MaxSectors || start > MaxSectors {
		return Proof{}, fmt.Errorf("%w: virtual range [%d,%d)", ErrOverflow, start, end)
	}

	p := Proof{
		Volume:   volume,
		Virtual:  Span{Start: start, Length: length},
		Writable: true,
		Version:  s.version,
	}
	if length == 0 {
		return p, nil
	}

	b := &proofBuilder{graph: s.graph}
	cursor := start
	for _, seg := range cv.segments {
		segEnd := seg.virtualStart + seg.length
		if segEnd <= start {
			continue
		}
		if seg.virtualStart >= end {
			break
		}
		lo := seg.virtualStart
		if lo < start {
			lo = start
		}
		hi := segEnd
		if hi > end {
			hi = end
		}
		if lo > cursor {
			return Proof{}, fmt.Errorf("%w: hole at sector %d", ErrUnmapped, cursor)
		}
		if lo < cursor {
			lo = cursor
		}
		if hi <= lo {
			continue
		}
		cls := contiguousClass(lo, hi-lo)
		if err := b.walkSegment(cv, seg, cls, identityClass, nil, PermReadWrite); err != nil {
			return Proof{}, err
		}
		cursor = hi
		if cursor == end {
			break
		}
	}
	if cursor != end {
		return Proof{}, fmt.Errorf("%w: hole at sector %d", ErrUnmapped, cursor)
	}

	p.Claims = b.claims
	for _, c := range p.Claims {
		if !c.Access.AllowsWrite() {
			p.Writable = false
			break
		}
	}
	return p, nil
}

// CheckWrite accepts a virtual write exactly when every mapped physical sector
// in the range is effective-writable.
func (s *Snapshot) CheckWrite(volume string, start, length uint64) (Proof, bool, error) {
	p, err := s.Proof(volume, start, length)
	if err != nil {
		return Proof{}, false, err
	}
	return p, p.Writable, nil
}

// Handle is a stable binding to a Snapshot. Publishing a newer graph does not
// mutate the snapshot or proof used by an existing Handle.
type Handle struct {
	snapshot *Snapshot
	volume   string
}

// Volume returns the handle's virtual volume name.
func (h *Handle) Volume() string { return h.volume }

// Snapshot returns the graph version bound to this handle.
func (h *Handle) Snapshot() *Snapshot { return h.snapshot }

// Proof returns the segment proof for this handle's old graph version.
func (h *Handle) Proof(start, length uint64) (Proof, error) {
	if h == nil || h.snapshot == nil {
		return Proof{}, fmt.Errorf("%w: nil handle", ErrInvalid)
	}
	return h.snapshot.Proof(h.volume, start, length)
}

// CheckWrite checks a virtual write against this handle's immutable version.
func (h *Handle) CheckWrite(start, length uint64) (Proof, bool, error) {
	if h == nil || h.snapshot == nil {
		return Proof{}, false, fmt.Errorf("%w: nil handle", ErrInvalid)
	}
	return h.snapshot.CheckWrite(h.volume, start, length)
}

type proofBuilder struct {
	graph  *compiledGraph
	claims []Claim
}

type inverseClass func(Class) (Class, error)

func identityClass(c Class) (Class, error) { return c, nil }

func (b *proofBuilder) addClaim(c Claim) error {
	if len(b.claims) >= maxProofClaims {
		return ErrProofTooComplex
	}
	b.claims = append(b.claims, c)
	return nil
}

func (b *proofBuilder) walkVolume(name string, cls Class, inv inverseClass, steps []Step, eff Permission) error {
	cv, err := b.graph.volume(name)
	if err != nil {
		return err
	}
	want, ok := cls.Size()
	if !ok {
		return ErrOverflow
	}
	var covered uint64
	for _, seg := range cv.segments {
		if seg.length == 0 {
			continue
		}
		mask := contiguousClass(seg.virtualStart, seg.length)
		parts, err := intersectClasses(cls, mask, maxProofClaims)
		if err != nil {
			return err
		}
		for _, part := range parts {
			if err := b.walkSegment(cv, seg, part, inv, steps, eff); err != nil {
				return err
			}
			n, ok := part.Size()
			if !ok {
				return ErrOverflow
			}
			covered, ok = addOK(covered, n)
			if !ok {
				return ErrOverflow
			}
		}
	}
	if covered != want {
		return fmt.Errorf("%w: child volume %q has a hole", ErrUnmapped, name)
	}
	return nil
}

func (b *proofBuilder) walkSegment(vol *compiledVolume, seg compiledSegment, cls Class,
	inv inverseClass, steps []Step, eff Permission) error {
	after := eff & seg.permission
	step := Step{
		Volume:       vol.name,
		Segment:      seg.index,
		SegmentStart: seg.virtualStart,
		SegmentLen:   seg.length,
		Kind:         seg.mapping.Kind,
		Mapping:      seg.mapping,
		SegmentPerm:  seg.permission,
		Before:       eff,
		After:        after,
		Narrowed:     after != eff,
	}
	path := appendStep(steps, step)

	switch seg.mapping.Kind {
	case MapLinear:
		delta := int64(seg.mapping.ChildOffset) - int64(seg.virtualStart)
		nextInv := func(c Class) (Class, error) {
			return inv(shiftClass(c, -delta))
		}
		return b.walkVolume(seg.mapping.Child, shiftClass(cls, delta), nextInv, path, after)

	case MapStripe:
		want, ok := cls.Size()
		if !ok {
			return ErrOverflow
		}
		var covered uint64
		for _, side := range []StripeSide{StripeLeft, StripeRight} {
			masks := stripeMasks(seg, side)
			for _, mask := range masks {
				parts, err := intersectClasses(cls, mask, maxProofClaims)
				if err != nil {
					return err
				}
				for _, part := range parts {
					n, ok := part.Size()
					if !ok {
						return ErrOverflow
					}
					covered, ok = addOK(covered, n)
					if !ok {
						return ErrOverflow
					}
					sideStep := step
					sideStep.Side = side
					child := seg.mapping.Left
					if side == StripeRight {
						child = seg.mapping.Right
					}
					childClass, err := stripeChildClass(part, seg, side)
					if err != nil {
						return err
					}
					nextInv := func(c Class) (Class, error) {
						parentClass, err := stripeParentClass(c, seg, side)
						if err != nil {
							return Class{}, err
						}
						return inv(parentClass)
					}
					if err := b.walkVolume(child, childClass, nextInv,
						appendStep(steps, sideStep), after); err != nil {
						return err
					}
				}
			}
		}
		if covered != want {
			return fmt.Errorf("%w: stripe segment %d did not cover its virtual class", ErrUnmapped, seg.index)
		}
		return nil

	case MapPhysical:
		leafPerm := after & seg.tenantPermission
		leafStep := step
		leafStep.After = leafPerm
		leafStep.Narrowed = leafStep.Before != leafStep.After
		leafPath := appendStep(steps, leafStep)
		delta := int64(seg.mapping.PhysicalStart) - int64(seg.virtualStart)
		physical := shiftClass(cls, delta)
		virtual, err := inv(cls)
		if err != nil {
			return err
		}
		virtualSize, ok := virtual.Size()
		if !ok {
			return ErrOverflow
		}
		physicalSize, ok := physical.Size()
		if !ok || virtualSize != physicalSize {
			return fmt.Errorf("%w: claim cardinality mismatch", ErrInvalid)
		}
		return b.addClaim(Claim{
			Virtual:          virtual,
			Physical:         physical,
			Access:           leafPerm,
			LeafPermission:   seg.permission,
			TenantPermission: seg.tenantPermission,
			Path:             leafPath,
		})
	default:
		return fmt.Errorf("%w: segment %d has unknown kind", ErrInvalid, seg.index)
	}
}

func appendStep(in []Step, s Step) []Step {
	out := make([]Step, len(in)+1)
	copy(out, in)
	out[len(in)] = s
	return out
}

func contiguousClass(start, length uint64) Class {
	return Class{Start: start, Run: length, Stride: length, Count: 1}
}

func stripeMasks(seg compiledSegment, side StripeSide) []Class {
	if seg.length == 0 {
		return nil
	}
	w := seg.mapping.StripeWidth
	stripes := seg.length / w
	if seg.length%w != 0 {
		stripes++
	}
	leftCount := (stripes + 1) / 2
	rightCount := stripes / 2
	lastLen := seg.length - (stripes-1)*w

	count := leftCount
	start := seg.virtualStart
	if side == StripeRight {
		count = rightCount
		start = seg.virtualStart + w
	}
	if count == 0 {
		return nil
	}
	lastIsSide := (side == StripeLeft && stripes%2 == 1) ||
		(side == StripeRight && stripes%2 == 0)
	hasTail := lastIsSide && lastLen < w
	full := count
	if hasTail {
		full--
	}
	out := make([]Class, 0, 2)
	if full > 0 {
		out = append(out, Class{
			Start:  start,
			Run:    w,
			Stride: 2 * w,
			Count:  full,
		})
	}
	if hasTail {
		tailStart := seg.virtualStart + (stripes-1)*w
		out = append(out, contiguousClass(tailStart, lastLen))
	}
	return out
}

func shiftClass(c Class, delta int64) Class {
	c.Start = uint64(int64(c.Start) + delta)
	return c
}

// stripeChildClass compresses a virtual class that is known to belong to one
// side of a stripe segment. Consecutive same-side virtual runs are 2w apart,
// while their child images are only w apart.
func stripeChildClass(c Class, seg compiledSegment, side StripeSide) (Class, error) {
	w := seg.mapping.StripeWidth
	if c.Start < seg.virtualStart {
		return Class{}, fmt.Errorf("%w: stripe class starts before segment", ErrInvalid)
	}
	local := c.Start - seg.virtualStart
	period := 2 * w
	pair := local / period
	within := local % period
	switch side {
	case StripeLeft:
		if within >= w {
			return Class{}, fmt.Errorf("%w: class is not in the left stripe", ErrInvalid)
		}
		c.Start = seg.mapping.LeftOffset + pair*w + within
	case StripeRight:
		if within < w {
			return Class{}, fmt.Errorf("%w: class is not in the right stripe", ErrInvalid)
		}
		c.Start = seg.mapping.RightOffset + pair*w + within - w
	default:
		return Class{}, fmt.Errorf("%w: unknown stripe side", ErrInvalid)
	}
	if c.Count > 1 {
		if c.Stride%2 != 0 {
			return Class{}, fmt.Errorf("%w: odd stripe stride", ErrInvalid)
		}
		c.Stride /= 2
	}
	for i := range c.Repeats {
		if c.Repeats[i].Stride%2 != 0 {
			return Class{}, fmt.Errorf("%w: odd stripe repeat stride", ErrInvalid)
		}
		c.Repeats[i].Stride /= 2
	}
	return c, nil
}

func stripeParentClass(c Class, seg compiledSegment, side StripeSide) (Class, error) {
	parts, err := flattenClass(c, maxFlatten)
	if err != nil {
		return Class{}, err
	}
	inverted := make([]Class, 0, len(parts))
	for _, part := range parts {
		parent, err := stripeParentSimple(part, seg, side)
		if err != nil {
			return Class{}, err
		}
		inverted = append(inverted, parent)
	}
	if len(inverted) == 1 {
		return inverted[0], nil
	}
	merged, err := mergeSimpleClasses(inverted, maxFlatten)
	if err != nil {
		return Class{}, err
	}
	if len(merged) != 1 {
		return Class{}, ErrProofTooComplex
	}
	return merged[0], nil
}

func stripeParentSimple(c Class, seg compiledSegment, side StripeSide) (Class, error) {
	w := seg.mapping.StripeWidth
	var offset uint64
	switch side {
	case StripeLeft:
		if c.Start < seg.mapping.LeftOffset {
			return Class{}, fmt.Errorf("%w: left child class underflow", ErrInvalid)
		}
		offset = c.Start - seg.mapping.LeftOffset
	case StripeRight:
		if c.Start < seg.mapping.RightOffset {
			return Class{}, fmt.Errorf("%w: right child class underflow", ErrInvalid)
		}
		offset = c.Start - seg.mapping.RightOffset
	default:
		return Class{}, fmt.Errorf("%w: unknown stripe side", ErrInvalid)
	}
	pair := offset / w
	within := offset % w
	if side == StripeLeft {
		c.Start = seg.virtualStart + pair*(2*w) + within
	} else {
		c.Start = seg.virtualStart + pair*(2*w) + w + within
	}
	if c.Count > 1 {
		s := c.Stride
		if c.Run > w {
			return Class{}, fmt.Errorf("%w: child run exceeds parent stripe width", ErrInvalid)
		}
		if s >= w {
			// Powers of two make every run land in a later parent stripe pair.
			c.Stride = 2 * s
		} else {
			group := ceilDiv(w-within, s)
			if group == 0 {
				group = 1
			}
			if c.Count <= group {
				// All child runs stay in this parent stripe block.
				c.Stride = s
			} else if within == 0 && w%s == 0 {
				count := c.Count
				if count%group != 0 {
					return Class{}, ErrProofTooComplex
				}
				c.Stride = s
				c.Count = group
				c.Repeats = append(c.Repeats, Repeat{
					Stride: 2 * w,
					Count:  count / group,
				})
			} else {
				return Class{}, ErrProofTooComplex
			}
		}
	}
	return c, nil
}

func mergeSimpleClasses(in []Class, limit int) ([]Class, error) {
	flat := make([]Class, 0, len(in))
	for _, c := range in {
		parts, err := flattenClass(c, limit)
		if err != nil {
			return nil, err
		}
		flat = append(flat, parts...)
	}
	sortClasses(flat)
	out := make([]Class, 0, len(flat))
	for _, c := range flat {
		if len(out) == 0 {
			out = append(out, c)
			continue
		}
		last := &out[len(out)-1]
		steps, ok := mulOK(last.Count, last.Stride)
		var next uint64
		if ok {
			next, ok = addOK(last.Start, steps)
		}
		if ok && last.Run == c.Run && last.Stride == c.Stride && next == c.Start {
			last.Count += c.Count
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

func sortClasses(in []Class) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j].Start < in[j-1].Start; j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
}

// intersectClasses intersects two exact classes. Simple classes are handled
// directly. Repeats are only flattened when necessary; the cap is on class
// combinations, never on sectors.
func intersectClasses(a, b Class, limit int) ([]Class, error) {
	if a.Count == 0 || b.Count == 0 {
		return nil, nil
	}
	if len(a.Repeats) == 0 && len(b.Repeats) == 0 {
		return intersectSimple(a, b), nil
	}
	fa, err := flattenClass(a, limit)
	if err != nil {
		return nil, err
	}
	fb, err := flattenClass(b, limit)
	if err != nil {
		return nil, err
	}
	if len(fa) != 0 && len(fb) != 0 && len(fa) > limit/len(fb) {
		return nil, ErrProofTooComplex
	}
	out := make([]Class, 0)
	for _, x := range fa {
		for _, y := range fb {
			parts := intersectSimple(x, y)
			if len(out)+len(parts) > limit {
				return nil, ErrProofTooComplex
			}
			out = append(out, parts...)
		}
	}
	return out, nil
}

func flattenClass(c Class, limit int) ([]Class, error) {
	if len(c.Repeats) == 0 {
		return []Class{c}, nil
	}
	total := 1
	for _, r := range c.Repeats {
		if r.Count > uint64(limit/total) {
			return nil, ErrProofTooComplex
		}
		total *= int(r.Count)
	}
	out := make([]Class, 0, total)
	var walk func(int, uint64)
	walk = func(i int, off uint64) {
		if i == len(c.Repeats) {
			x := c
			x.Start += off
			x.Repeats = nil
			out = append(out, x)
			return
		}
		r := c.Repeats[i]
		for k := uint64(0); k < r.Count; k++ {
			walk(i+1, off+k*r.Stride)
		}
	}
	walk(0, 0)
	return out, nil
}

// intersectSimple expects classes without Repeats. Its result can use one
// outer Repeat dimension to represent a periodic intersection compactly.
func intersectSimple(a, b Class) []Class {
	if a.Count == 0 || b.Count == 0 {
		return nil
	}
	// A contiguous class is a single interval. This also handles masks such as
	// a segment boundary whose stride is not a power of two.
	if a.Count == 1 {
		end, ok := addOK(a.Start, a.Run)
		if !ok {
			return nil
		}
		return intersectRunRange(b, a.Start, end)
	}
	if b.Count == 1 {
		end, ok := addOK(b.Start, b.Run)
		if !ok {
			return nil
		}
		return intersectRunRange(a, b.Start, end)
	}
	if a.Stride > b.Stride {
		a, b = b, a
	}
	pA, pB := a.Stride, b.Stride
	aEnd, okA := a.innerEnd()
	bEnd, okB := b.innerEnd()
	if !okA || !okB || pA == 0 || pB == 0 {
		return nil
	}
	if pB%pA != 0 {
		// Stripe-generated strides are powers of two. Keep this fallback
		// conservative rather than inventing a non-exact result.
		return nil
	}

	first := uint64(0)
	bFirstEnd, ok := addOK(b.Start, b.Run)
	if !ok {
		return nil
	}
	if bFirstEnd <= a.Start {
		first = ceilDiv(a.Start-bFirstEnd+1, pB)
	}
	last := uint64(math.MaxUint64)
	if aEnd <= b.Start {
		return nil
	}
	last = (aEnd - 1 - b.Start) / pB
	if last >= b.Count {
		last = b.Count - 1
	}
	if first > last || first >= b.Count {
		return nil
	}
	out := make([]Class, 0, 6)
	out = append(out, intersectRunRange(a, b.Start+first*pB, minU64(b.Start+first*pB+b.Run, bEnd))...)
	if first == last {
		return out
	}
	lastStart := b.Start + last*pB
	out = append(out, intersectRunRange(a, lastStart, minU64(lastStart+b.Run, bEnd))...)

	middle := last - first - 1
	if middle > 0 {
		middleStart := b.Start + (first+1)*pB
		parts := intersectRunRange(a, middleStart, minU64(middleStart+b.Run, bEnd))
		for _, p := range parts {
			p.Repeats = append(p.Repeats, Repeat{Stride: pB, Count: middle})
			out = append(out, p)
		}
	}
	return out
}

func intersectRunRange(a Class, lo, hi uint64) []Class {
	if lo >= hi || a.Count == 0 {
		return nil
	}
	aEnd, ok := a.innerEnd()
	if !ok || hi <= a.Start || lo >= aEnd {
		return nil
	}
	if lo < a.Start {
		lo = a.Start
	}
	if hi > aEnd {
		hi = aEnd
	}
	firstEnd, ok := addOK(a.Start, a.Run)
	if !ok {
		return nil
	}
	var iLo uint64
	if firstEnd <= lo {
		iLo = ceilDiv(lo-firstEnd+1, a.Stride)
	}
	if iLo >= a.Count {
		return nil
	}
	var iHi uint64
	if a.Start >= hi {
		return nil
	}
	iHi = (hi - 1 - a.Start) / a.Stride
	if iHi >= a.Count {
		iHi = a.Count - 1
	}
	if iLo > iHi {
		return nil
	}

	firstStart := a.Start + iLo*a.Stride
	firstInterStart := maxU64(firstStart, lo)
	firstInterEnd := minU64(firstStart+a.Run, hi)
	if firstInterStart >= firstInterEnd {
		return nil
	}
	if iLo == iHi {
		return []Class{contiguousClass(firstInterStart, firstInterEnd-firstInterStart)}
	}

	out := make([]Class, 0, 3)
	fullLo := iLo
	if firstInterStart != firstStart || firstInterEnd != firstStart+a.Run {
		out = append(out, contiguousClass(firstInterStart, firstInterEnd-firstInterStart))
		fullLo++
	}

	lastStart := a.Start + iHi*a.Stride
	lastInterStart := maxU64(lastStart, lo)
	lastInterEnd := minU64(lastStart+a.Run, hi)
	if lastInterStart >= lastInterEnd {
		return out
	}
	fullHi := iHi
	if lastInterStart != lastStart || lastInterEnd != lastStart+a.Run {
		fullHi--
	}
	if fullLo <= fullHi {
		out = append(out, Class{
			Start:  a.Start + fullLo*a.Stride,
			Run:    a.Run,
			Stride: a.Stride,
			Count:  fullHi - fullLo + 1,
		})
	}
	if fullHi != iHi || lastInterStart != lastStart || lastInterEnd != lastStart+a.Run {
		out = append(out, contiguousClass(lastInterStart, lastInterEnd-lastInterStart))
	}
	return out
}

func minU64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

func maxU64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

func (cg *compiledGraph) checkIsolations() error {
	coverage := make(map[string][]Class)
	for _, pair := range cg.isolations {
		if pair.A == pair.B {
			return fmt.Errorf("%w: volume %q isolated from itself", ErrIsolation, pair.A)
		}
		ca, ok := coverage[pair.A]
		if !ok {
			var err error
			ca, err = cg.writableCoverage(pair.A)
			if err != nil {
				return err
			}
			coverage[pair.A] = ca
		}
		cb, ok := coverage[pair.B]
		if !ok {
			var err error
			cb, err = cg.writableCoverage(pair.B)
			if err != nil {
				return err
			}
			coverage[pair.B] = cb
		}
		overlap, err := classesOverlap(ca, cb)
		if err != nil {
			return err
		}
		if overlap {
			return fmt.Errorf("%w: %q and %q", ErrIsolation, pair.A, pair.B)
		}
	}
	return nil
}

func (cg *compiledGraph) writableCoverage(name string) ([]Class, error) {
	cv, err := cg.volume(name)
	if err != nil {
		return nil, err
	}
	b := &proofBuilder{graph: cg}
	for _, seg := range cv.segments {
		if seg.length == 0 {
			continue
		}
		if err := b.walkSegment(cv, seg, contiguousClass(seg.virtualStart, seg.length),
			identityClass, nil, PermReadWrite); err != nil {
			return nil, err
		}
	}
	out := make([]Class, 0, len(b.claims))
	for _, c := range b.claims {
		if c.Access.AllowsWrite() {
			out = append(out, c.Physical)
		}
	}
	return out, nil
}

func classesOverlap(a, b []Class) (bool, error) {
	for _, x := range a {
		fx, err := flattenClass(x, maxFlatten)
		if err != nil {
			return false, err
		}
		for _, y := range b {
			fy, err := flattenClass(y, maxFlatten)
			if err != nil {
				return false, err
			}
			for _, p := range fx {
				for _, q := range fy {
					if len(intersectSimple(p, q)) != 0 {
						return true, nil
					}
				}
			}
		}
	}
	return false, nil
}

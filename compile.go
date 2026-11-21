package admission

import (
	"fmt"
	"sort"
)

type compiledGraph struct {
	volumes    map[string]*compiledVolume
	physical   []PhysicalRange
	isolations []IsolationPair
	roots      []string
}

type compiledVolume struct {
	name           string
	length         uint64
	segments       []compiledSegment
	isolationGroup string
}

type compiledSegment struct {
	index            int
	virtualStart     uint64
	length           uint64
	permission       Permission
	mapping          Mapping
	tenantPermission Permission
}

func (s compiledSegment) end() (uint64, bool) {
	return addOK(s.virtualStart, s.length)
}

func compileGraph(g Graph) (*compiledGraph, error) {
	physical, err := validatePhysical(g.Physical)
	if err != nil {
		return nil, err
	}

	raw := make(map[string]*Volume, len(g.Volumes))
	for i := range g.Volumes {
		v := &g.Volumes[i]
		if v.Name == "" {
			return nil, fmt.Errorf("%w: empty volume name", ErrInvalid)
		}
		if _, exists := raw[v.Name]; exists {
			return nil, fmt.Errorf("%w: duplicate volume %q", ErrInvalid, v.Name)
		}
		if v.Length > MaxSectors {
			return nil, fmt.Errorf("%w: volume %q length %d exceeds 2^48", ErrInvalid, v.Name, v.Length)
		}
		raw[v.Name] = v
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: graph has no volumes", ErrInvalid)
	}

	roots := append([]string(nil), g.Roots...)
	if len(roots) == 0 {
		roots = make([]string, 0, len(g.Volumes))
		for _, v := range g.Volumes {
			roots = append(roots, v.Name)
		}
	}
	rootSeen := make(map[string]bool, len(roots))
	uniqueRoots := roots[:0]
	for _, name := range roots {
		if !rootSeen[name] {
			rootSeen[name] = true
			uniqueRoots = append(uniqueRoots, name)
		}
	}
	roots = uniqueRoots
	if len(roots) == 0 {
		return nil, fmt.Errorf("%w: graph has no roots", ErrInvalid)
	}

	cg := &compiledGraph{
		volumes:  make(map[string]*compiledVolume, len(raw)),
		physical: physical,
		roots:    roots,
	}
	state := make(map[string]uint8, len(raw))
	for _, root := range roots {
		if err := cg.visit(root, raw, state, 1); err != nil {
			return nil, err
		}
	}

	cg.isolations = append(cg.isolations, g.Isolations...)
	byGroup := make(map[string][]string)
	for _, v := range cg.volumes {
		if v.isolationGroup != "" {
			byGroup[v.isolationGroup] = append(byGroup[v.isolationGroup], v.name)
		}
	}
	for _, names := range byGroup {
		sort.Strings(names)
		for i := 0; i < len(names); i++ {
			for j := i + 1; j < len(names); j++ {
				cg.isolations = append(cg.isolations, IsolationPair{A: names[i], B: names[j]})
			}
		}
	}
	cg.isolations = uniqueIsolations(cg.isolations)

	if err := cg.checkIsolations(); err != nil {
		return nil, err
	}
	return cg, nil
}

func uniqueIsolations(in []IsolationPair) []IsolationPair {
	seen := make(map[IsolationPair]bool, len(in))
	out := make([]IsolationPair, 0, len(in))
	for _, p := range in {
		if p.A == "" || p.B == "" || p.A == p.B {
			continue
		}
		if p.A > p.B {
			p.A, p.B = p.B, p.A
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

func (cg *compiledGraph) visit(name string, raw map[string]*Volume, state map[string]uint8, depth int) error {
	if depth > MaxLayers {
		return fmt.Errorf("%w: volume %q at depth %d", ErrTooDeep, name, depth)
	}
	switch state[name] {
	case 1:
		return fmt.Errorf("%w: at volume %q", ErrCycle, name)
	case 2:
		// A DAG node may be reached again through a different path. Recheck
		// depth on every route even though it was validated once.
		if cv := cg.volumes[name]; cv != nil {
			for _, s := range cv.segments {
				for _, child := range segmentChildren(s.mapping) {
					if err := cg.visit(child, raw, state, depth+1); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}

	rv, ok := raw[name]
	if !ok {
		return fmt.Errorf("%w: child volume %q", ErrNotFound, name)
	}
	state[name] = 1
	cv, err := cg.compileVolume(rv, raw)
	if err != nil {
		return err
	}
	cg.volumes[name] = cv
	for _, s := range cv.segments {
		for _, child := range segmentChildren(s.mapping) {
			if err := cg.visit(child, raw, state, depth+1); err != nil {
				return err
			}
		}
	}
	state[name] = 2
	return nil
}

func segmentChildren(m Mapping) []string {
	switch m.Kind {
	case MapLinear:
		return []string{m.Child}
	case MapStripe:
		if m.Left == m.Right {
			return []string{m.Left}
		}
		return []string{m.Left, m.Right}
	default:
		return nil
	}
}

func (cg *compiledGraph) compileVolume(v *Volume, raw map[string]*Volume) (*compiledVolume, error) {
	segments := append([]Segment(nil), v.Segments...)
	sort.Slice(segments, func(i, j int) bool {
		if segments[i].VirtualStart == segments[j].VirtualStart {
			return segments[i].Length < segments[j].Length
		}
		return segments[i].VirtualStart < segments[j].VirtualStart
	})

	compiled := &compiledVolume{
		name:           v.Name,
		length:         v.Length,
		isolationGroup: v.IsolationGroup,
		segments:       make([]compiledSegment, 0, len(segments)),
	}
	var previousEnd uint64
	for i, s := range segments {
		if !s.Permission.valid() {
			return nil, fmt.Errorf("%w: volume %q segment %d has invalid permission %d",
				ErrInvalid, v.Name, i, s.Permission)
		}
		end, ok := addOK(s.VirtualStart, s.Length)
		if !ok {
			return nil, fmt.Errorf("%w: volume %q segment %d logical end", ErrOverflow, v.Name, i)
		}
		if end > v.Length || v.Length > MaxSectors || end > MaxSectors {
			return nil, fmt.Errorf("%w: volume %q segment %d exceeds volume length", ErrInvalid, v.Name, i)
		}
		if i > 0 && s.VirtualStart < previousEnd {
			return nil, fmt.Errorf("%w: volume %q at segment %d", ErrOverlap, v.Name, i)
		}
		previousEnd = end

		cs := compiledSegment{
			index:        i,
			virtualStart: s.VirtualStart,
			length:       s.Length,
			permission:   s.Permission,
			mapping:      s.Mapping,
			// A non-physical segment ultimately inherits the permission of its
			// physical descendants. This value is not used for those segments.
			tenantPermission: PermReadWrite,
		}
		tenantPerm, err := cg.validateMapping(v.Name, i, cs, raw)
		if err != nil {
			return nil, err
		}
		cs.tenantPermission = tenantPerm
		compiled.segments = append(compiled.segments, cs)
	}
	return compiled, nil
}

func (cg *compiledGraph) validateMapping(volume string, index int, s compiledSegment, raw map[string]*Volume) (Permission, error) {
	m := s.mapping
	where := fmt.Sprintf("volume %q segment %d", volume, index)
	switch m.Kind {
	case MapLinear:
		if m.Child == "" {
			return PermNone, fmt.Errorf("%w: %s has no child", ErrInvalid, where)
		}
		child, ok := raw[m.Child]
		if !ok {
			return PermNone, fmt.Errorf("%w: %s child %q", ErrNotFound, where, m.Child)
		}
		end, ok := addOK(m.ChildOffset, s.length)
		if !ok || end > child.Length {
			return PermNone, fmt.Errorf("%w: %s child offset", ErrOverflow, where)
		}
	case MapStripe:
		if m.Left == "" || m.Right == "" {
			return PermNone, fmt.Errorf("%w: %s stripe has an empty child", ErrInvalid, where)
		}
		if m.StripeWidth == 0 || m.StripeWidth&(m.StripeWidth-1) != 0 {
			return PermNone, fmt.Errorf("%w: %s stripe width %d is not a power of two", ErrInvalid, where, m.StripeWidth)
		}
		left, ok := raw[m.Left]
		if !ok {
			return PermNone, fmt.Errorf("%w: %s left child %q", ErrNotFound, where, m.Left)
		}
		right, ok := raw[m.Right]
		if !ok {
			return PermNone, fmt.Errorf("%w: %s right child %q", ErrNotFound, where, m.Right)
		}
		if err := stripeFits(s.length, m.StripeWidth,
			m.LeftOffset, left.Length, m.RightOffset, right.Length); err != nil {
			return PermNone, fmt.Errorf("%w: %s", err, where)
		}
	case MapPhysical:
		_, ok := addOK(m.PhysicalStart, s.length)
		if !ok || m.PhysicalStart > MaxSectors || s.length > MaxSectors-m.PhysicalStart {
			return PermNone, fmt.Errorf("%w: %s physical range", ErrOverflow, where)
		}
		perm, err := cg.tenantPermissionForSpan(m.PhysicalStart, s.length)
		if err != nil {
			return PermNone, fmt.Errorf("%s: %w", where, err)
		}
		return perm, nil
	default:
		return PermNone, fmt.Errorf("%w: %s has unknown mapping kind %d", ErrInvalid, where, m.Kind)
	}
	return PermReadWrite, nil
}

func stripeFits(length, width, leftOffset, leftLength, rightOffset, rightLength uint64) error {
	if length == 0 {
		if leftOffset > leftLength || rightOffset > rightLength {
			return fmt.Errorf("%w: stripe offset", ErrOverflow)
		}
		return nil
	}
	stripes := length / width
	if length%width != 0 {
		stripes++
	}
	leftCount := (stripes + 1) / 2
	rightCount := stripes / 2
	lastLen := length - (stripes-1)*width

	if leftCount == 0 {
		if leftOffset > leftLength {
			return fmt.Errorf("%w: left stripe offset", ErrOverflow)
		}
	} else {
		lastLeftLen := width
		if stripes%2 == 1 {
			lastLeftLen = lastLen
		}
		if err := branchFits(leftOffset, leftCount, width, lastLeftLen, leftLength); err != nil {
			return fmt.Errorf("left %w", err)
		}
	}
	if rightCount == 0 {
		if rightOffset > rightLength {
			return fmt.Errorf("%w: right stripe offset", ErrOverflow)
		}
	} else {
		lastRightLen := width
		if stripes%2 == 0 {
			lastRightLen = lastLen
		}
		if err := branchFits(rightOffset, rightCount, width, lastRightLen, rightLength); err != nil {
			return fmt.Errorf("right %w", err)
		}
	}
	return nil
}

func branchFits(offset, count, width, lastLen, childLength uint64) error {
	if count == 0 {
		if offset > childLength {
			return fmt.Errorf("%w: branch offset", ErrOverflow)
		}
		return nil
	}
	steps := count - 1
	span, ok := mulOK(steps, width)
	if !ok {
		return fmt.Errorf("%w: branch span", ErrOverflow)
	}
	span, ok = addOK(span, lastLen)
	if !ok {
		return fmt.Errorf("%w: branch span", ErrOverflow)
	}
	end, ok := addOK(offset, span)
	if !ok {
		return fmt.Errorf("%w: branch end", ErrOverflow)
	}
	if end > childLength || end > MaxSectors {
		return fmt.Errorf("%w: branch exceeds child volume", ErrOverflow)
	}
	return nil
}

func validatePhysical(in []PhysicalRange) ([]PhysicalRange, error) {
	out := append([]PhysicalRange(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Start == out[j].Start {
			return out[i].Length < out[j].Length
		}
		return out[i].Start < out[j].Start
	})
	var previousEnd uint64
	for i, r := range out {
		if r.Length == 0 {
			return nil, fmt.Errorf("%w: physical range %d is empty", ErrInvalid, i)
		}
		if !r.Permission.valid() || r.Permission == PermNone {
			return nil, fmt.Errorf("%w: physical range %d has no usable permission", ErrInvalid, i)
		}
		if r.Start > MaxSectors || r.Length > MaxSectors-r.Start {
			return nil, fmt.Errorf("%w: physical range %d", ErrOverflow, i)
		}
		if i > 0 && r.Start < previousEnd {
			return nil, fmt.Errorf("%w: physical ranges %d and %d", ErrOverlap, i-1, i)
		}
		previousEnd = r.Start + r.Length
	}
	return out, nil
}

// tenantPermissionForSpan requires complete ownership and returns the
// conservative intersection of all physical-range permissions covering it.
func (cg *compiledGraph) tenantPermissionForSpan(start, length uint64) (Permission, error) {
	if length == 0 {
		return PermReadWrite, nil
	}
	end, ok := addOK(start, length)
	if !ok {
		return PermNone, fmt.Errorf("%w: physical span end", ErrOverflow)
	}
	cursor := start
	perm := PermReadWrite
	for _, r := range cg.physical {
		rend := r.Start + r.Length
		if rend <= cursor {
			continue
		}
		if r.Start > cursor {
			return PermNone, fmt.Errorf("%w: gap at sector %d", ErrNotOwned, cursor)
		}
		pieceEnd := rend
		if pieceEnd > end {
			pieceEnd = end
		}
		perm &= r.Permission
		cursor = pieceEnd
		if cursor == end {
			return perm, nil
		}
	}
	return PermNone, fmt.Errorf("%w: gap at sector %d", ErrNotOwned, cursor)
}

func (cg *compiledGraph) volume(name string) (*compiledVolume, error) {
	v := cg.volumes[name]
	if v == nil {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	return v, nil
}

package readproof

import (
	"errors"
	"fmt"
)

// Checker issues and accepts certificates against one node source and one root
// signature policy.
type Checker struct {
	Nodes      NodeSource
	VerifyRoot VerifyRootFunc
}

// NewChecker constructs a certificate checker.
func NewChecker(nodes NodeSource, verifyRoot VerifyRootFunc) *Checker {
	return &Checker{Nodes: nodes, VerifyRoot: verifyRoot}
}

// Build is a convenience wrapper around Checker.Issue.
func Build(nodes NodeSource, verifyRoot VerifyRootFunc, req Request) (*Certificate, error) {
	return NewChecker(nodes, verifyRoot).Issue(req)
}

// Accept is a convenience wrapper around Checker.Accept.
func Accept(nodes NodeSource, verifyRoot VerifyRootFunc, cert *Certificate, req Request) error {
	return NewChecker(nodes, verifyRoot).Accept(cert, req)
}

type prepared struct {
	graph       *graph
	rootInfo    *nodeInfo
	active      *activeSet
	validations *validationIndex
}

func (c *Checker) prepare(req Request) (*prepared, error) {
	if c == nil || c.Nodes == nil {
		return nil, fmt.Errorf("%w: node source is nil", ErrMissingNode)
	}
	if c.VerifyRoot == nil {
		return nil, ErrNoVerifier
	}
	if err := c.VerifyRoot(req.Root.Digest, req.Root.Signature); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSignature, err)
	}
	if req.View.Root != req.Root.Digest {
		return nil, fmt.Errorf("%w: signed root %s, view root %s",
			ErrRootMismatch, req.Root.Digest, req.View.Root)
	}
	if !req.Range.Valid() {
		return nil, fmt.Errorf("%w: [%d,%d)", ErrInvalidRange, req.Range.Start, req.Range.End)
	}

	g := newGraph(c.Nodes)
	rootInfo, err := g.get(req.Root.Digest)
	if err != nil {
		return nil, err
	}
	active, err := buildActiveSet(req.View, req.Root.Digest, req.View.Tenant)
	if err != nil {
		return nil, err
	}
	validations, err := newValidationIndex(req.Validated)
	if err != nil {
		return nil, err
	}
	return &prepared{
		graph:       g,
		rootInfo:    rootInfo,
		active:      active,
		validations: validations,
	}, nil
}

// Issue returns an exact-cover certificate for req.Range.
//
// It minimizes segment count by taking, at each uncovered root position, the
// candidate with the farthest endpoint.  Ties are resolved by ascending
// credential ID.  A candidate is a leaf-local logical interval, so a segment
// never crosses a shared-node boundary.
func (c *Checker) Issue(req Request) (*Certificate, error) {
	p, err := c.prepare(req)
	if err != nil {
		if errors.Is(err, ErrMissingNode) {
			return nil, &GapError{
				Offset: req.Range.Start, Range: req.Range, Reason: ReasonMissingManifest,
			}
		}
		return nil, err
	}
	if err := checkWithinRoot(req.Range, p.rootInfo.size); err != nil {
		return nil, err
	}

	cert := &Certificate{
		Root:   req.Root.Digest,
		Tenant: req.View.Tenant,
		Range:  req.Range,
	}
	if req.Range.Length() == 0 {
		return cert, nil
	}

	cursor := req.Range.Start
	for cursor < req.Range.End {
		if req.MaxSegments > 0 && len(cert.Segments) >= req.MaxSegments {
			return nil, &SegmentLimitError{Limit: req.MaxSegments}
		}
		loc, err := locateLeaf(p.graph, req.Root.Digest, cursor)
		if err != nil {
			if errors.Is(err, ErrMissingNode) {
				return nil, &GapError{Offset: cursor, Range: req.Range, Reason: ReasonMissingManifest}
			}
			return nil, err
		}
		seg, reason, err := chooseSegment(p, loc, cursor, req.Range.End)
		if err != nil {
			return nil, err
		}
		if seg == nil {
			return nil, &GapError{Offset: cursor, Range: req.Range, Reason: reason}
		}
		cert.Segments = append(cert.Segments, *seg)
		cursor = seg.Range.End
	}
	return cert, nil
}

func checkWithinRoot(r ByteRange, rootSize uint64) error {
	if r.Start > rootSize {
		return &GapError{Offset: r.Start, Range: r, Reason: ReasonOutsideRoot}
	}
	if r.End > rootSize {
		offset := r.Start
		if offset < rootSize {
			offset = rootSize
		}
		return &GapError{Offset: offset, Range: r, Reason: ReasonOutsideRoot}
	}
	return nil
}

type leafLocation struct {
	leaf  Leaf
	local uint64
	path  []PathRef
}

func locateLeaf(g *graph, root Digest, pos uint64) (*leafLocation, error) {
	id := root
	local := pos
	path := make([]PathRef, 1, 8)
	path[0] = PathRef{Digest: id, Offset: local}
	seen := make(map[Digest]struct{}, 8)

	for {
		if _, ok := seen[id]; ok {
			return nil, fmt.Errorf("%w: at node %s", ErrCycle, id)
		}
		seen[id] = struct{}{}
		info, err := g.infoFor(id)
		if err != nil {
			return nil, err
		}
		if local >= info.size {
			return nil, fmt.Errorf("%w: offset %d outside node %s of size %d",
				ErrInvalidNode, local, id, info.size)
		}
		if info.node.Kind == LeafNode {
			if local >= info.node.Leaf.Length {
				return nil, fmt.Errorf("%w: leaf offset %d exceeds length %d",
					ErrInvalidNode, local, info.node.Leaf.Length)
			}
			return &leafLocation{
				leaf:  info.node.Leaf,
				local: local,
				path:  path,
			}, nil
		}
		child, childStart, _, ok := info.childAt(local)
		if !ok {
			return nil, fmt.Errorf("%w: no child covers offset %d in %s",
				ErrInvalidNode, local, id)
		}
		childInfo, err := g.infoFor(child.Digest)
		if err != nil {
			return nil, err
		}
		if err := checkChildBounds(child, childInfo); err != nil {
			return nil, err
		}
		local = child.Offset + (local - childStart)
		id = child.Digest
		path = append(path, PathRef{Digest: id, Offset: local})
	}
}

func chooseSegment(p *prepared, loc *leafLocation, cursor, requestEnd uint64) (*Segment, string, error) {
	var haveAuthorization bool
	for _, cred := range p.active.ordered {
		if cred.Range.Start <= cursor && cursor < cred.Range.End {
			haveAuthorization = true
			break
		}
	}
	if !haveAuthorization {
		reason := ReasonNoAuthorization
		if p.active.anyScopedAt(cursor) {
			reason = ReasonRevoked
		}
		return nil, reason, nil
	}

	validated, ok := p.validations.containingPoint(loc.leaf.Block, loc.local)
	if !ok {
		return nil, ReasonNoValidatedData, nil
	}
	validatedEnd, _ := validated.End()
	leafRemaining := loc.leaf.Length - loc.local
	validatedRemaining := validatedEnd - loc.local
	if validatedRemaining > leafRemaining {
		validatedRemaining = leafRemaining
	}

	var chosen *Credential
	var chosenLength uint64
	for i := range p.active.ordered {
		cred := &p.active.ordered[i]
		if cred.Range.Start > cursor || cursor >= cred.Range.End {
			continue
		}
		length := cred.Range.End - cursor
		if validatedRemaining < length {
			length = validatedRemaining
		}
		if requestEnd-cursor < length {
			length = requestEnd - cursor
		}
		if length == 0 {
			continue
		}
		if chosen == nil || length > chosenLength ||
			(length == chosenLength && cred.ID < chosen.ID) {
			chosen = cred
			chosenLength = length
		}
	}
	if chosen == nil {
		return nil, ReasonNoAuthorization, nil
	}

	length := chosenLength
	path := make([]PathRef, len(loc.path))
	for i, ref := range loc.path {
		ref.Length = length
		path[i] = ref
	}
	seg := &Segment{
		Path:         path,
		Range:        ByteRange{Start: cursor, End: cursor + length},
		Physical:     PhysicalRange{Block: loc.leaf.Block, Offset: loc.local, Length: length},
		CredentialID: chosen.ID,
		Generation:   chosen.Generation,
		Validated:    validated,
	}
	return seg, "", nil
}

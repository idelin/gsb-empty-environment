package readproof

import "fmt"

// Verify is an alias for Accept.
func (c *Checker) Verify(cert *Certificate, req Request) error {
	return c.Accept(cert, req)
}

// Accept independently accepts or rejects a certificate before the host reads
// any bytes.
//
// It re-verifies the root signature, every manifest identity and path position,
// the current authorization snapshot (including revocations), the exact
// physical mapping, validated byte intervals, and gap-free exact coverage.
func (c *Checker) Accept(cert *Certificate, req Request) error {
	if cert == nil {
		return fmt.Errorf("%w: nil certificate", ErrInvalidCertificate)
	}
	p, err := c.prepare(req)
	if err != nil {
		return err
	}
	if err := checkWithinRoot(req.Range, p.rootInfo.size); err != nil {
		return err
	}
	if cert.Root != req.Root.Digest {
		return fmt.Errorf("%w: certificate root %s, trusted root %s",
			ErrRootMismatch, cert.Root, req.Root.Digest)
	}
	if cert.Tenant != req.View.Tenant {
		return fmt.Errorf("%w: certificate tenant %q, view tenant %q",
			ErrTenantMismatch, cert.Tenant, req.View.Tenant)
	}
	if cert.Range != req.Range {
		return fmt.Errorf("%w: certificate range [%d,%d), requested [%d,%d)",
			ErrInvalidCertificate, cert.Range.Start, cert.Range.End,
			req.Range.Start, req.Range.End)
	}
	if req.MaxSegments > 0 && len(cert.Segments) > req.MaxSegments {
		return &SegmentLimitError{Limit: req.MaxSegments}
	}
	if req.Range.Length() == 0 {
		if len(cert.Segments) != 0 {
			return fmt.Errorf("%w: empty read has %d segments", ErrInvalidCertificate, len(cert.Segments))
		}
		return nil
	}
	if len(cert.Segments) == 0 {
		return &GapError{Offset: req.Range.Start, Range: req.Range, Reason: ReasonNoAuthorization}
	}

	cursor := req.Range.Start
	for i := range cert.Segments {
		seg := &cert.Segments[i]
		if !seg.Range.Valid() || seg.Range.Start != cursor ||
			seg.Range.End <= seg.Range.Start || seg.Range.End > req.Range.End {
			return fmt.Errorf("%w: segment %d does not continue exact coverage at %d",
				ErrInvalidCertificate, i, cursor)
		}

		cred, ok := p.active.get(seg.CredentialID)
		if !ok || cred.Generation != seg.Generation {
			return fmt.Errorf("%w: segment %d credential %q generation %d",
				ErrUnauthorized, i, seg.CredentialID, seg.Generation)
		}
		if !cred.Range.Contains(seg.Range.Start, seg.Range.End) {
			return fmt.Errorf("%w: segment %d range [%d,%d) is outside credential range [%d,%d)",
				ErrUnauthorized, i, seg.Range.Start, seg.Range.End,
				cred.Range.Start, cred.Range.End)
		}

		mapped, err := resolveSegment(p.graph, req.Root.Digest, seg)
		if err != nil {
			return err
		}
		if mapped != seg.Physical {
			return fmt.Errorf("%w: segment %d physical range %+v, manifest maps %+v",
				ErrInvalidCertificate, i, seg.Physical, mapped)
		}
		if !p.validations.containsRange(seg.Validated) {
			return fmt.Errorf("%w: segment %d", ErrUnvalidated, i)
		}
		if !physicalContains(seg.Validated, seg.Physical) {
			return fmt.Errorf("%w: segment %d validated interval does not cover physical range",
				ErrInvalidCertificate, i)
		}
		cursor = seg.Range.End
	}
	if cursor != req.Range.End {
		return fmt.Errorf("%w: coverage stops at %d, request ends at %d",
			ErrInvalidCertificate, cursor, req.Range.End)
	}
	return nil
}

func resolveSegment(g *graph, root Digest, seg *Segment) (PhysicalRange, error) {
	length := seg.Range.Length()
	if len(seg.Path) == 0 || length == 0 {
		return PhysicalRange{}, fmt.Errorf("%w: empty path or segment", ErrInvalidCertificate)
	}
	cursor := seg.Range.Start
	expectedDigest := root
	seen := make(map[Digest]struct{}, len(seg.Path))
	for i, ref := range seg.Path {
		if _, ok := seen[ref.Digest]; ok {
			return PhysicalRange{}, fmt.Errorf("%w: path revisits node %s", ErrCycle, ref.Digest)
		}
		seen[ref.Digest] = struct{}{}
		if ref.Digest != expectedDigest {
			return PhysicalRange{}, fmt.Errorf("%w: path step %d digest %s, expected %s",
				ErrInvalidCertificate, i, ref.Digest, expectedDigest)
		}
		if ref.Offset != cursor || ref.Length != length {
			return PhysicalRange{}, fmt.Errorf("%w: path step %d interval [%d,%d), expected [%d,%d)",
				ErrInvalidCertificate, i, ref.Offset, ref.Offset+ref.Length,
				cursor, cursor+length)
		}
		end := cursor + length
		if end < cursor {
			return PhysicalRange{}, fmt.Errorf("%w: path offset overflow", ErrLengthOverflow)
		}

		info, err := g.infoFor(ref.Digest)
		if err != nil {
			return PhysicalRange{}, err
		}
		if info.node.Kind == LeafNode {
			if i != len(seg.Path)-1 {
				return PhysicalRange{}, fmt.Errorf("%w: path continues below leaf", ErrInvalidCertificate)
			}
			if end > info.node.Leaf.Length {
				return PhysicalRange{}, fmt.Errorf("%w: leaf range [%d,%d) exceeds length %d",
					ErrInvalidCertificate, cursor, end, info.node.Leaf.Length)
			}
			return PhysicalRange{
				Block:  info.node.Leaf.Block,
				Offset: cursor,
				Length: length,
			}, nil
		}
		if i == len(seg.Path)-1 {
			return PhysicalRange{}, fmt.Errorf("%w: path ends at an internal node", ErrInvalidCertificate)
		}
		child, childStart, childEnd, ok := info.childAt(cursor)
		if !ok {
			return PhysicalRange{}, fmt.Errorf("%w: no child at offset %d", ErrInvalidCertificate, cursor)
		}
		if end > childEnd {
			return PhysicalRange{}, fmt.Errorf("%w: segment crosses child boundary at %d", ErrInvalidCertificate, childEnd)
		}
		childInfo, err := g.infoFor(child.Digest)
		if err != nil {
			return PhysicalRange{}, err
		}
		if err := checkChildBounds(child, childInfo); err != nil {
			return PhysicalRange{}, err
		}
		next := child.Offset + (cursor - childStart)
		if next < child.Offset {
			return PhysicalRange{}, fmt.Errorf("%w: child offset overflow", ErrLengthOverflow)
		}
		cursor = next
		expectedDigest = child.Digest
	}
	return PhysicalRange{}, fmt.Errorf("%w: path did not reach a leaf", ErrInvalidCertificate)
}

func physicalContains(outer, inner PhysicalRange) bool {
	outerEnd, ok := outer.End()
	if !ok {
		return false
	}
	innerEnd, ok := inner.End()
	if !ok {
		return false
	}
	return outer.Block == inner.Block &&
		outer.Offset <= inner.Offset &&
		outerEnd >= innerEnd
}

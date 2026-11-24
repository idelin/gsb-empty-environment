package readproof

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
)

func acceptAny(Digest, []byte) error { return nil }

func mustAdd(t *testing.T, c *Catalog, n Node) Digest {
	t.Helper()
	id, err := c.Add(n)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	return id
}

func signed(d Digest) SignedRoot {
	return SignedRoot{Digest: d, Signature: []byte("test-signature")}
}

func viewFor(root Digest, creds []Credential, revs []Revocation) AuthorizationView {
	return AuthorizationView{
		Tenant:      "tenant-a",
		Root:        root,
		Credentials: creds,
		Revocations: revs,
	}
}

func TestCanonicalIdentity(t *testing.T) {
	a := NewLeaf(1, 4)
	b := NewLeaf(2, 4)
	aID := a.MustDigest()
	if aID != a.MustDigest() {
		t.Fatal("leaf identity is not deterministic")
	}
	if aID == b.MustDigest() {
		t.Fatal("different leaf content produced the same identity")
	}

	ab := NewInternal(FullRef(aID, 4), FullRef(b.MustDigest(), 4))
	ba := NewInternal(FullRef(b.MustDigest(), 4), FullRef(aID, 4))
	if ab.MustDigest() == ba.MustDigest() {
		t.Fatal("child order must be authenticated")
	}

	parsed, err := ParseDigest(aID.String())
	if err != nil {
		t.Fatalf("ParseDigest: %v", err)
	}
	if parsed != aID {
		t.Fatal("ParseDigest round trip failed")
	}
	if _, err := ParseDigest(strings.Repeat("x", 64)); err == nil {
		t.Fatal("invalid hexadecimal digest accepted")
	}
}

func TestCatalogAcceptsOutOfOrderNodes(t *testing.T) {
	c := NewCatalog()
	leaf := NewLeaf(10, 4)
	leafID := leaf.MustDigest()
	root := NewInternal(FullRef(leafID, 4))
	rootID := root.MustDigest()

	if err := c.Put(rootID, root); err != nil {
		t.Fatalf("Put root: %v", err)
	}
	if err := c.Put(leafID, leaf); err != nil {
		t.Fatalf("Put leaf: %v", err)
	}
	req := Request{
		Root: signed(rootID),
		View: viewFor(rootID, []Credential{{
			ID: "c1", Tenant: "tenant-a", Root: rootID,
			Range: NewRange(0, 4), Generation: 1,
		}}, nil),
		Range:     NewRange(0, 4),
		Validated: []PhysicalRange{{Block: 10, Length: 4}},
	}
	cert, err := Build(c, acceptAny, req)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(cert.Segments) != 1 {
		t.Fatalf("got %d segments, want 1", len(cert.Segments))
	}
	if err := Accept(c, acceptAny, cert, req); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	wrong := rootID
	wrong[0] ^= 0xff
	if err := c.Put(wrong, root); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("Put with wrong identity: got %v, want ErrIdentityMismatch", err)
	}
}

func TestPartialManifestOnlyNeedsPathsForRequestedBytes(t *testing.T) {
	c := NewCatalog()
	left := NewLeaf(80, 2)
	right := NewLeaf(81, 2)
	leftID := left.MustDigest()
	rightID := right.MustDigest()
	rootID := mustAdd(t, c, NewInternal(FullRef(leftID, 2), FullRef(rightID, 2)))
	if _, err := c.Add(left); err != nil {
		t.Fatalf("Add left: %v", err)
	}

	view := viewFor(rootID, []Credential{{
		ID: "left", Tenant: "tenant-a", Root: rootID,
		Range: NewRange(0, 4), Generation: 1,
	}}, nil)
	leftReq := Request{
		Root: signed(rootID), View: view, Range: NewRange(0, 2),
		Validated: []PhysicalRange{{Block: 80, Length: 2}},
	}
	cert, err := Build(c, acceptAny, leftReq)
	if err != nil {
		t.Fatalf("left branch should not need the missing right node: %v", err)
	}
	if err := Accept(c, acceptAny, cert, leftReq); err != nil {
		t.Fatalf("Accept left branch: %v", err)
	}

	rightReq := leftReq
	rightReq.Range = NewRange(2, 4)
	rightReq.Validated = []PhysicalRange{{Block: 81, Length: 2}}
	_, err = Build(c, acceptAny, rightReq)
	var gap *GapError
	if !errors.As(err, &gap) || gap.Offset != 2 || gap.Reason != ReasonMissingManifest {
		t.Fatalf("missing right node: got %v, want gap at 2 with %q", err, ReasonMissingManifest)
	}
}

func TestChildIntervalOffsetsMapRootToBlock(t *testing.T) {
	c := NewCatalog()
	leafID := mustAdd(t, c, NewLeaf(77, 8))
	rootID := mustAdd(t, c, NewInternal(IntervalRef(leafID, 2, 4)))
	req := Request{
		Root: signed(rootID),
		View: viewFor(rootID, []Credential{{
			ID: "slot", Tenant: "tenant-a", Root: rootID,
			Range: NewRange(0, 4), Generation: 7,
		}}, nil),
		Range:     NewRange(0, 4),
		Validated: []PhysicalRange{{Block: 77, Offset: 2, Length: 4}},
	}
	cert, err := Build(c, acceptAny, req)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(cert.Segments) != 1 {
		t.Fatalf("got %d segments, want 1", len(cert.Segments))
	}
	seg := cert.Segments[0]
	wantPhysical := PhysicalRange{Block: 77, Offset: 2, Length: 4}
	if seg.Physical != wantPhysical {
		t.Fatalf("physical = %+v, want %+v", seg.Physical, wantPhysical)
	}
	if got := seg.Path[len(seg.Path)-1]; got.Offset != 2 || got.Length != 4 {
		t.Fatalf("leaf path = %+v, want offset 2 length 4", got)
	}
	if err := Accept(c, acceptAny, cert, req); err != nil {
		t.Fatalf("Accept: %v", err)
	}
}

func chunkCovered(start, end uint64, creds []Credential, validated []PhysicalRange, block uint64) bool {
	authorized := false
	for _, cred := range creds {
		if cred.Range.Contains(start, end) {
			authorized = true
			break
		}
	}
	if !authorized {
		return false
	}
	for _, r := range validated {
		if r.Block != block {
			continue
		}
		rEnd, ok := r.End()
		if ok && r.Offset <= start && rEnd >= end {
			return true
		}
	}
	return false
}

// brutePartitions enumerates every cut pattern and returns the minimum number
// of coverable chunks.  It is deliberately independent of the planner's greedy
// choices and is only used on tiny trees.
func brutePartitions(start, end uint64, creds []Credential, validated []PhysicalRange, block uint64) (int, bool) {
	if start == end {
		return 0, true
	}
	interior := int(end - start - 1)
	best := math.MaxInt
	for mask := 0; mask < 1<<interior; mask++ {
		cuts := []uint64{start}
		for bit := 0; bit < interior; bit++ {
			if mask&(1<<bit) != 0 {
				cuts = append(cuts, start+1+uint64(bit))
			}
		}
		cuts = append(cuts, end)
		count := len(cuts) - 1
		if count >= best {
			continue
		}
		ok := true
		for i := 0; i+1 < len(cuts); i++ {
			if !chunkCovered(cuts[i], cuts[i+1], creds, validated, block) {
				ok = false
				break
			}
		}
		if ok {
			best = count
		}
	}
	if best == math.MaxInt {
		return 0, false
	}
	return best, true
}

func TestExhaustiveSmallPartitionsMatchOptimal(t *testing.T) {
	const block = uint64(9)
	c := NewCatalog()
	rootID := mustAdd(t, c, NewLeaf(block, 6))
	creds := []Credential{
		{ID: "a", Tenant: "tenant-a", Root: rootID, Range: NewRange(0, 2), Generation: 1},
		{ID: "b", Tenant: "tenant-a", Root: rootID, Range: NewRange(0, 4), Generation: 1},
		{ID: "c", Tenant: "tenant-a", Root: rootID, Range: NewRange(2, 6), Generation: 1},
		{ID: "d", Tenant: "tenant-a", Root: rootID, Range: NewRange(4, 6), Generation: 1},
	}
	validated := []PhysicalRange{{Block: block, Length: 6}}
	view := viewFor(rootID, creds, nil)

	ranges := []ByteRange{
		NewRange(0, 6), NewRange(1, 6), NewRange(0, 5),
		NewRange(1, 5), NewRange(2, 5), NewRange(3, 6),
	}
	for _, r := range ranges {
		req := Request{
			Root: signed(rootID), View: view, Range: r, Validated: validated,
		}
		want, possible := brutePartitions(r.Start, r.End, creds, validated, block)
		if !possible {
			t.Fatalf("test setup: range %v is not coverable", r)
		}
		cert, err := Build(c, acceptAny, req)
		if err != nil {
			t.Fatalf("range %v: Build: %v", r, err)
		}
		if len(cert.Segments) != want {
			t.Fatalf("range %v: got %d segments, exhaustive optimum is %d",
				r, len(cert.Segments), want)
		}
		if err := Accept(c, acceptAny, cert, req); err != nil {
			t.Fatalf("range %v: Accept: %v", r, err)
		}
	}

	req := Request{
		Root: signed(rootID), View: view, Range: NewRange(0, 6), Validated: validated,
	}
	cert, err := Build(c, acceptAny, req)
	if err != nil {
		t.Fatalf("Build tie case: %v", err)
	}
	if len(cert.Segments) != 2 {
		t.Fatalf("tie case: got %d segments, want 2", len(cert.Segments))
	}
	if cert.Segments[0].Range.End != 4 || cert.Segments[0].CredentialID != "b" {
		t.Fatalf("first tie choice = end %d cred %q, want end 4 cred b",
			cert.Segments[0].Range.End, cert.Segments[0].CredentialID)
	}
	if cert.Segments[1].Range.End != 6 || cert.Segments[1].CredentialID != "c" {
		t.Fatalf("second tie choice = end %d cred %q, want end 6 cred c",
			cert.Segments[1].Range.End, cert.Segments[1].CredentialID)
	}
}

type oracleLeaf struct {
	start uint64
	end   uint64
	block uint64
}

func treeChunkCovered(start, end uint64, leaves []oracleLeaf, creds []Credential, validated []PhysicalRange) bool {
	var leaf *oracleLeaf
	for i := range leaves {
		if start >= leaves[i].start && end <= leaves[i].end {
			leaf = &leaves[i]
			break
		}
	}
	if leaf == nil {
		return false
	}
	authorized := false
	for _, cred := range creds {
		if cred.Range.Contains(start, end) {
			authorized = true
			break
		}
	}
	if !authorized {
		return false
	}
	physical := PhysicalRange{
		Block:  leaf.block,
		Offset: start - leaf.start,
		Length: end - start,
	}
	physicalEnd, ok := physical.End()
	if !ok {
		return false
	}
	for _, r := range validated {
		if r.Block != physical.Block {
			continue
		}
		rEnd, ok := r.End()
		if ok && r.Offset <= physical.Offset && rEnd >= physicalEnd {
			return true
		}
	}
	return false
}

func bruteTreePartitions(start, end uint64, leaves []oracleLeaf, creds []Credential, validated []PhysicalRange) (int, bool) {
	if start == end {
		return 0, true
	}
	interior := int(end - start - 1)
	best := math.MaxInt
	for mask := 0; mask < 1<<interior; mask++ {
		cuts := []uint64{start}
		for bit := 0; bit < interior; bit++ {
			if mask&(1<<bit) != 0 {
				cuts = append(cuts, start+1+uint64(bit))
			}
		}
		cuts = append(cuts, end)
		count := len(cuts) - 1
		if count >= best {
			continue
		}
		ok := true
		for i := 0; i+1 < len(cuts); i++ {
			if !treeChunkCovered(cuts[i], cuts[i+1], leaves, creds, validated) {
				ok = false
				break
			}
		}
		if ok {
			best = count
		}
	}
	if best == math.MaxInt {
		return 0, false
	}
	return best, true
}

func TestExhaustiveThreeLeafTreeOptimality(t *testing.T) {
	c := NewCatalog()
	ids := make([]Digest, 3)
	leaves := []oracleLeaf{
		{start: 0, end: 2, block: 100},
		{start: 2, end: 5, block: 101},
		{start: 5, end: 7, block: 102},
	}
	for i, leaf := range leaves {
		ids[i] = mustAdd(t, c, NewLeaf(leaf.block, leaf.end-leaf.start))
	}
	rootID := mustAdd(t, c, NewInternal(
		FullRef(ids[0], 2),
		FullRef(ids[1], 3),
		FullRef(ids[2], 2),
	))
	creds := []Credential{
		{ID: "a", Tenant: "tenant-a", Root: rootID, Range: NewRange(0, 3), Generation: 1},
		{ID: "b", Tenant: "tenant-a", Root: rootID, Range: NewRange(1, 5), Generation: 1},
		{ID: "c", Tenant: "tenant-a", Root: rootID, Range: NewRange(3, 7), Generation: 1},
		{ID: "d", Tenant: "tenant-a", Root: rootID, Range: NewRange(4, 7), Generation: 1},
		{ID: "e", Tenant: "tenant-a", Root: rootID, Range: NewRange(0, 1), Generation: 1},
		{ID: "f", Tenant: "tenant-a", Root: rootID, Range: NewRange(5, 6), Generation: 1},
	}
	validated := []PhysicalRange{
		{Block: 100, Length: 2},
		{Block: 101, Length: 3},
		{Block: 102, Length: 2},
	}
	view := viewFor(rootID, creds, nil)

	for start := uint64(0); start < 7; start++ {
		for end := start + 1; end <= 7; end++ {
			want, possible := bruteTreePartitions(start, end, leaves, creds, validated)
			req := Request{
				Root: signed(rootID), View: view,
				Range: NewRange(start, end), Validated: validated,
			}
			cert, err := Build(c, acceptAny, req)
			if !possible {
				if !errors.Is(err, ErrGap) {
					t.Fatalf("range [%d,%d): expected an evidence gap, got %v", start, end, err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("range [%d,%d): Build: %v", start, end, err)
			}
			if len(cert.Segments) != want {
				t.Fatalf("range [%d,%d): got %d segments, exhaustive optimum is %d",
					start, end, len(cert.Segments), want)
			}
			if err := Accept(c, acceptAny, cert, req); err != nil {
				t.Fatalf("range [%d,%d): Accept: %v", start, end, err)
			}
		}
	}
}

func TestSameBlockAtTwoPositionsDoesNotShareAuthorization(t *testing.T) {
	const block = uint64(7)
	c := NewCatalog()
	leafID := mustAdd(t, c, NewLeaf(block, 4))
	rootID := mustAdd(t, c, NewInternal(
		FullRef(leafID, 4),
		FullRef(leafID, 4),
	))
	validated := []PhysicalRange{{Block: block, Length: 4}}

	firstOnly := Request{
		Root: signed(rootID),
		View: viewFor(rootID, []Credential{{
			ID: "first", Tenant: "tenant-a", Root: rootID,
			Range: NewRange(0, 4), Generation: 1,
		}}, nil),
		Range:     NewRange(4, 8),
		Validated: validated,
	}
	if _, err := Build(c, acceptAny, firstOnly); !errors.Is(err, ErrGap) {
		t.Fatalf("authorization at first position used for second: got %v, want gap", err)
	}

	second := Request{
		Root: signed(rootID),
		View: viewFor(rootID, []Credential{{
			ID: "second", Tenant: "tenant-a", Root: rootID,
			Range: NewRange(4, 8), Generation: 3,
		}}, nil),
		Range:     NewRange(4, 8),
		Validated: validated,
	}
	cert, err := Build(c, acceptAny, second)
	if err != nil {
		t.Fatalf("Build second position: %v", err)
	}
	if cert.Segments[0].Physical != (PhysicalRange{Block: block, Length: 4}) {
		t.Fatalf("unexpected physical mapping: %+v", cert.Segments[0].Physical)
	}
	if err := Accept(c, acceptAny, cert, second); err != nil {
		t.Fatalf("Accept second position: %v", err)
	}

	tampered := *cert
	tampered.Segments = append([]Segment(nil), cert.Segments...)
	tampered.Segments[0].Physical.Offset = 1
	if err := Accept(c, acceptAny, &tampered, second); !errors.Is(err, ErrInvalidCertificate) {
		t.Fatalf("tampered physical range: got %v, want ErrInvalidCertificate", err)
	}
}

func TestUnalignedSlices(t *testing.T) {
	c := NewCatalog()
	leftID := mustAdd(t, c, NewLeaf(10, 4))
	rightID := mustAdd(t, c, NewLeaf(11, 4))
	rootID := mustAdd(t, c, NewInternal(
		FullRef(leftID, 4),
		FullRef(rightID, 4),
	))
	validated := []PhysicalRange{
		{Block: 10, Offset: 1, Length: 3},
		{Block: 11, Offset: 0, Length: 4},
	}
	creds := []Credential{
		{ID: "left", Tenant: "tenant-a", Root: rootID, Range: NewRange(0, 2), Generation: 1},
		{ID: "mid", Tenant: "tenant-a", Root: rootID, Range: NewRange(1, 5), Generation: 1},
		{ID: "tail", Tenant: "tenant-a", Root: rootID, Range: NewRange(5, 8), Generation: 1},
	}

	req := Request{
		Root: signed(rootID), View: viewFor(rootID, creds, nil),
		Range: NewRange(1, 5), Validated: validated,
	}
	cert, err := Build(c, acceptAny, req)
	if err != nil {
		t.Fatalf("Build [1,5): %v", err)
	}
	if len(cert.Segments) != 2 {
		t.Fatalf("got %d segments, want 2", len(cert.Segments))
	}
	if cert.Segments[0].Physical != (PhysicalRange{Block: 10, Offset: 1, Length: 3}) {
		t.Fatalf("first physical = %+v", cert.Segments[0].Physical)
	}
	if cert.Segments[1].Physical != (PhysicalRange{Block: 11, Offset: 0, Length: 1}) {
		t.Fatalf("second physical = %+v", cert.Segments[1].Physical)
	}
	if err := Accept(c, acceptAny, cert, req); err != nil {
		t.Fatalf("Accept [1,5): %v", err)
	}

	req = Request{
		Root: signed(rootID), View: viewFor(rootID, creds, nil),
		Range: NewRange(2, 7), Validated: validated,
	}
	cert, err = Build(c, acceptAny, req)
	if err != nil {
		t.Fatalf("Build [2,7): %v", err)
	}
	if len(cert.Segments) != 3 {
		t.Fatalf("got %d segments, want 3", len(cert.Segments))
	}
	gotIDs := []string{
		cert.Segments[0].CredentialID,
		cert.Segments[1].CredentialID,
		cert.Segments[2].CredentialID,
	}
	if fmt.Sprint(gotIDs) != "[mid mid tail]" {
		t.Fatalf("credential sequence = %v, want [mid mid tail]", gotIDs)
	}
	if err := Accept(c, acceptAny, cert, req); err != nil {
		t.Fatalf("Accept [2,7): %v", err)
	}
}

func TestRevocationAndLateOldCredential(t *testing.T) {
	const block = uint64(21)
	c := NewCatalog()
	rootID := mustAdd(t, c, NewLeaf(block, 4))
	validated := []PhysicalRange{{Block: block, Length: 4}}
	old := Credential{
		ID: "lease", Tenant: "tenant-a", Root: rootID,
		Range: NewRange(0, 4), Generation: 1,
	}
	newer := old
	newer.Generation = 2
	revoked := []Revocation{{CredentialID: "lease", Generation: 1}}

	oldOnlyLate := Request{
		Root: signed(rootID), View: viewFor(rootID, []Credential{old}, revoked),
		Range: NewRange(0, 4), Validated: validated,
	}
	_, err := Build(c, acceptAny, oldOnlyLate)
	var gap *GapError
	if !errors.As(err, &gap) || gap.Offset != 0 || gap.Reason != ReasonRevoked {
		t.Fatalf("revoked old credential: got %v, want first gap reason %q", err, ReasonRevoked)
	}

	withNewer := oldOnlyLate
	withNewer.View = viewFor(rootID, []Credential{old, newer}, revoked)
	cert, err := Build(c, acceptAny, withNewer)
	if err != nil {
		t.Fatalf("new generation should be usable: %v", err)
	}
	if cert.Segments[0].Generation != 2 {
		t.Fatalf("used generation %d, want 2", cert.Segments[0].Generation)
	}
	if err := Accept(c, acceptAny, cert, withNewer); err != nil {
		t.Fatalf("Accept newer generation: %v", err)
	}

	beforeRevocation := withNewer
	beforeRevocation.View = viewFor(rootID, []Credential{old}, nil)
	cert, err = Build(c, acceptAny, beforeRevocation)
	if err != nil {
		t.Fatalf("Build before revocation: %v", err)
	}
	afterRevocation := beforeRevocation
	afterRevocation.View = viewFor(rootID, []Credential{old}, revoked)
	if err := Accept(c, acceptAny, cert, afterRevocation); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("certificate survived revocation: got %v, want ErrUnauthorized", err)
	}
}

func TestDeepRepeatedTreeTailRead(t *testing.T) {
	c := NewCatalog()
	id := mustAdd(t, c, NewLeaf(5, 1))
	size := uint64(1)
	const levels = 50
	for i := 0; i < levels; i++ {
		id = mustAdd(t, c, NewInternal(
			FullRef(id, size),
			FullRef(id, size),
		))
		size *= 2
	}
	if size != 1<<50 {
		t.Fatalf("size = %d, want 2^50", size)
	}
	if c.Len() != levels+1 {
		t.Fatalf("catalog has %d nodes, want %d", c.Len(), levels+1)
	}

	req := Request{
		Root: signed(id),
		View: viewFor(id, []Credential{{
			ID: "tail", Tenant: "tenant-a", Root: id,
			Range: NewRange(size-1, size), Generation: 1,
		}}, nil),
		Range:     NewRange(size-1, size),
		Validated: []PhysicalRange{{Block: 5, Length: 1}},
	}
	cert, err := Build(c, acceptAny, req)
	if err != nil {
		t.Fatalf("Build tail: %v", err)
	}
	if len(cert.Segments) != 1 {
		t.Fatalf("got %d segments, want 1", len(cert.Segments))
	}
	if len(cert.Segments[0].Path) != levels+1 {
		t.Fatalf("path depth = %d, want %d", len(cert.Segments[0].Path), levels+1)
	}
	if cert.Segments[0].Physical != (PhysicalRange{Block: 5, Length: 1}) {
		t.Fatalf("physical = %+v", cert.Segments[0].Physical)
	}
	if err := Accept(c, acceptAny, cert, req); err != nil {
		t.Fatalf("Accept tail: %v", err)
	}
}

func TestSegmentLimitReturnsExplicitError(t *testing.T) {
	const block = uint64(31)
	c := NewCatalog()
	rootID := mustAdd(t, c, NewLeaf(block, 6))
	view := viewFor(rootID, []Credential{
		{ID: "a", Tenant: "tenant-a", Root: rootID, Range: NewRange(0, 4), Generation: 1},
		{ID: "b", Tenant: "tenant-a", Root: rootID, Range: NewRange(4, 6), Generation: 1},
	}, nil)
	req := Request{
		Root: signed(rootID), View: view, Range: NewRange(0, 6),
		Validated:   []PhysicalRange{{Block: block, Length: 6}},
		MaxSegments: 1,
	}
	cert, err := Build(c, acceptAny, req)
	if cert != nil {
		t.Fatal("segment limit returned a partial certificate")
	}
	var limit *SegmentLimitError
	if !errors.As(err, &limit) || limit.Limit != 1 {
		t.Fatalf("got %v, want SegmentLimitError with limit 1", err)
	}
	if !errors.Is(err, ErrSegmentLimit) {
		t.Fatalf("errors.Is(%v, ErrSegmentLimit) = false", err)
	}
	req.MaxSegments = 2
	if _, err := Build(c, acceptAny, req); err != nil {
		t.Fatalf("two segments should fit: %v", err)
	}
}

func TestGapReportsFirstMissingEvidence(t *testing.T) {
	const block = uint64(41)
	c := NewCatalog()
	rootID := mustAdd(t, c, NewLeaf(block, 6))
	view := viewFor(rootID, []Credential{
		{ID: "a", Tenant: "tenant-a", Root: rootID, Range: NewRange(0, 2), Generation: 1},
		{ID: "b", Tenant: "tenant-a", Root: rootID, Range: NewRange(3, 6), Generation: 1},
	}, nil)
	req := Request{
		Root: signed(rootID), View: view, Range: NewRange(0, 6),
		Validated: []PhysicalRange{{Block: block, Length: 6}},
	}
	_, err := Build(c, acceptAny, req)
	var gap *GapError
	if !errors.As(err, &gap) || gap.Offset != 2 || gap.Reason != ReasonNoAuthorization {
		t.Fatalf("got %v, want gap at 2 with %q", err, ReasonNoAuthorization)
	}

	req.View = viewFor(rootID, []Credential{{
		ID: "all", Tenant: "tenant-a", Root: rootID,
		Range: NewRange(0, 6), Generation: 1,
	}}, nil)
	req.Validated = []PhysicalRange{{Block: block, Length: 2}}
	_, err = Build(c, acceptAny, req)
	if !errors.As(err, &gap) || gap.Offset != 2 || gap.Reason != ReasonNoValidatedData {
		t.Fatalf("got %v, want gap at 2 with %q", err, ReasonNoValidatedData)
	}
}

func TestFailuresNeverYieldUsableSegments(t *testing.T) {
	const block = uint64(51)
	c := NewCatalog()
	rootID := mustAdd(t, c, NewLeaf(block, 4))
	goodView := viewFor(rootID, []Credential{{
		ID: "c", Tenant: "tenant-a", Root: rootID,
		Range: NewRange(0, 4), Generation: 1,
	}}, nil)
	req := Request{
		Root: signed(rootID), View: goodView, Range: NewRange(0, 4),
		Validated: []PhysicalRange{{Block: block, Length: 4}},
	}

	badSig := req
	badSig.Root.Signature = []byte("bad")
	reject := func(Digest, []byte) error { return errors.New("signature mismatch") }
	if cert, err := Build(c, reject, badSig); cert != nil || !errors.Is(err, ErrSignature) {
		t.Fatalf("bad signature yielded cert=%v err=%v", cert, err)
	}
	goodCert, err := Build(c, acceptAny, req)
	if err != nil {
		t.Fatalf("Build good cert: %v", err)
	}
	if err := Accept(c, reject, goodCert, badSig); !errors.Is(err, ErrSignature) {
		t.Fatalf("accept bad signature: got %v, want ErrSignature", err)
	}
	if cert, err := Build(c, nil, req); cert != nil || !errors.Is(err, ErrNoVerifier) {
		t.Fatalf("nil verifier yielded cert=%v err=%v", cert, err)
	}

	mismatch := req
	mismatch.View.Root = Digest{0xff}
	if cert, err := Build(c, acceptAny, mismatch); cert != nil || !errors.Is(err, ErrRootMismatch) {
		t.Fatalf("root mismatch yielded cert=%v err=%v", cert, err)
	}

	overflowLeaves := NewCatalog()
	wideID := mustAdd(t, overflowLeaves, NewLeaf(60, math.MaxUint64))
	overflowID := mustAdd(t, overflowLeaves, NewInternal(
		FullRef(wideID, math.MaxUint64),
		FullRef(wideID, math.MaxUint64),
	))
	overflowReq := Request{
		Root:  signed(overflowID),
		View:  viewFor(overflowID, nil, nil),
		Range: NewRange(0, 1),
	}
	if cert, err := Build(overflowLeaves, acceptAny, overflowReq); cert != nil ||
		!errors.Is(err, ErrLengthOverflow) {
		t.Fatalf("length overflow yielded cert=%v err=%v", cert, err)
	}

	nestedLeaves := NewCatalog()
	nestedLeafID := mustAdd(t, nestedLeaves, NewLeaf(61, math.MaxUint64))
	nestedInnerID := mustAdd(t, nestedLeaves, NewInternal(
		FullRef(nestedLeafID, math.MaxUint64),
		FullRef(nestedLeafID, math.MaxUint64),
	))
	nestedRootID := mustAdd(t, nestedLeaves, NewInternal(FullRef(nestedInnerID, 1)))
	nestedReq := Request{
		Root:  signed(nestedRootID),
		View:  viewFor(nestedRootID, nil, nil),
		Range: NewRange(0, 1),
	}
	if cert, err := Build(nestedLeaves, acceptAny, nestedReq); cert != nil ||
		!errors.Is(err, ErrLengthOverflow) {
		t.Fatalf("nested length overflow yielded cert=%v err=%v", cert, err)
	}

	cycle := NewCatalog()
	cycleID := Digest{0xaa}
	cycle.nodes[cycleID] = NewInternal(FullRef(cycleID, 1))
	cycleReq := Request{
		Root:  signed(cycleID),
		View:  viewFor(cycleID, nil, nil),
		Range: NewRange(0, 1),
	}
	if cert, err := Build(cycle, acceptAny, cycleReq); cert != nil ||
		!errors.Is(err, ErrCycle) {
		t.Fatalf("cycle yielded cert=%v err=%v", cert, err)
	}

	identity := NewCatalog()
	identityID := Digest{0xbb}
	identity.nodes[identityID] = NewLeaf(1, 1)
	identityReq := Request{
		Root:  signed(identityID),
		View:  viewFor(identityID, nil, nil),
		Range: NewRange(0, 1),
	}
	if cert, err := Build(identity, acceptAny, identityReq); cert != nil ||
		!errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("identity mismatch yielded cert=%v err=%v", cert, err)
	}
}

func TestAcceptRejectsTamperingAndCoverageGaps(t *testing.T) {
	const block = uint64(61)
	c := NewCatalog()
	rootID := mustAdd(t, c, NewLeaf(block, 6))
	view := viewFor(rootID, []Credential{
		{ID: "a", Tenant: "tenant-a", Root: rootID, Range: NewRange(0, 4), Generation: 1},
		{ID: "b", Tenant: "tenant-a", Root: rootID, Range: NewRange(4, 6), Generation: 1},
	}, nil)
	req := Request{
		Root: signed(rootID), View: view, Range: NewRange(0, 6),
		Validated: []PhysicalRange{{Block: block, Length: 6}},
	}
	cert, err := Build(c, acceptAny, req)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := Accept(c, acceptAny, cert, req); err != nil {
		t.Fatalf("Accept valid: %v", err)
	}

	missing := *cert
	missing.Segments = append([]Segment(nil), cert.Segments[:1]...)
	if err := Accept(c, acceptAny, &missing, req); !errors.Is(err, ErrInvalidCertificate) {
		t.Fatalf("missing tail: got %v, want ErrInvalidCertificate", err)
	}

	wrongCred := *cert
	wrongCred.Segments = append([]Segment(nil), cert.Segments...)
	wrongCred.Segments[0].CredentialID = "b"
	if err := Accept(c, acceptAny, &wrongCred, req); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong credential: got %v, want ErrUnauthorized", err)
	}

	wrongPath := *cert
	wrongPath.Segments = append([]Segment(nil), cert.Segments...)
	wrongPath.Segments[0].Path = append([]PathRef(nil), cert.Segments[0].Path...)
	wrongPath.Segments[0].Path[0].Offset++
	if err := Accept(c, acceptAny, &wrongPath, req); !errors.Is(err, ErrInvalidCertificate) {
		t.Fatalf("wrong path: got %v, want ErrInvalidCertificate", err)
	}

	short := req
	short.Range = NewRange(0, 5)
	short.View = view
	if err := Accept(c, acceptAny, cert, short); !errors.Is(err, ErrInvalidCertificate) {
		t.Fatalf("mismatched request range: got %v, want ErrInvalidCertificate", err)
	}
}

func TestEmptyAndOutsideRanges(t *testing.T) {
	const block = uint64(71)
	c := NewCatalog()
	rootID := mustAdd(t, c, NewLeaf(block, 4))
	view := viewFor(rootID, nil, nil)

	empty := Request{
		Root: signed(rootID), View: view, Range: NewRange(4, 4),
	}
	cert, err := Build(c, acceptAny, empty)
	if err != nil {
		t.Fatalf("Build empty: %v", err)
	}
	if len(cert.Segments) != 0 {
		t.Fatalf("empty read returned %d segments", len(cert.Segments))
	}
	if err := Accept(c, acceptAny, cert, empty); err != nil {
		t.Fatalf("Accept empty: %v", err)
	}

	outside := empty
	outside.Range = NewRange(5, 6)
	_, err = Build(c, acceptAny, outside)
	var gap *GapError
	if !errors.As(err, &gap) || gap.Offset != 5 || gap.Reason != ReasonOutsideRoot {
		t.Fatalf("outside range: got %v, want gap at 5", err)
	}
}

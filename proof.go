package replayhandoff

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
)

// Barrier is a logical evidence barrier. A proof taken at barrier b describes
// the authenticated node state through that barrier. A closed lease may require
// a proof at or after its closure barrier.
type Barrier uint64

// RangeProof is an authenticated claim for one node, one partition and one
// half-open range. Seen is clipped to Range. Sequence numbers outside Range are
// unknown, never implicitly unseen.
type RangeProof struct {
	Node         NodeID
	Partition    Partition
	Barrier      Barrier
	Generation   uint64
	PreviousRoot Root
	Root         Root
	Range        Interval
	Seen         IntervalSet
	Signature    []byte
}

func encodeProofContent(proof RangeProof) []byte {
	out := appendLP(nil, []byte("replayhandoff/range-proof-content/v1"))
	out = appendLP(out, []byte(proof.Node))
	out = appendPartition(out, proof.Partition)
	out = appendDigest(out, ContextDigestFor(proof.Partition.Context))
	out = appendU64(out, uint64(proof.Barrier))
	out = appendU64(out, proof.Generation)
	out = appendRoot(out, proof.PreviousRoot)
	out = appendRoot(out, proof.Root)
	out = appendInterval(out, proof.Range)
	out = appendIntervalSet(out, proof.Seen)
	return out
}

// ProofContentDigest identifies the signed claim but deliberately excludes the
// signature bytes. It is used for duplicate detection.
func ProofContentDigest(proof RangeProof) Root {
	content := encodeProofContent(proof)
	return hashParts([]byte("replayhandoff/proof-digest/v1"), content)
}

// ProofSigningMessage is the domain-separated payload signed by the node.
func ProofSigningMessage(proof RangeProof) []byte {
	content := encodeProofContent(proof)
	return SigningMessage("range-proof", content)
}

// Validate checks structural invariants without checking the signature.
func (p RangeProof) Validate() error {
	if p.Node == "" {
		return errors.New("replayhandoff: proof node must not be empty")
	}
	if err := p.Partition.Validate(); err != nil {
		return err
	}
	if err := p.Range.Validate(); err != nil {
		return fmt.Errorf("replayhandoff: proof range: %w", err)
	}
	if uint64(p.Barrier) > p.Generation {
		return errors.New("replayhandoff: proof barrier is after its generation")
	}
	if err := ValidateSet(p.Seen); err != nil {
		return fmt.Errorf("replayhandoff: proof seen set: %w", err)
	}
	for _, iv := range p.Seen.Intervals() {
		if iv.Lo < p.Range.Lo || iv.Hi > p.Range.Hi {
			return errors.New("replayhandoff: proof seen interval escapes proof range")
		}
	}
	return nil
}

// Verify validates structure and the node's authenticated signature over Root.
func (p RangeProof) Verify(keys KeyRing) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if err := keys.Verify(p.Node, ProofSigningMessage(p), p.Signature); err != nil {
		return err
	}
	return nil
}

// Proof creates a fresh proof through the current generation.
func (l *NodeLog) Proof(partition Partition, bounds Interval, signer *NodeSigner) (RangeProof, error) {
	if err := partition.Validate(); err != nil {
		return RangeProof{}, err
	}
	if partition.Context != l.context {
		return RangeProof{}, fmt.Errorf("replayhandoff: proof context %q does not match log context %q", partition.Context, l.context)
	}
	if err := bounds.Validate(); err != nil {
		return RangeProof{}, err
	}
	if signer == nil || signer.Node != l.node {
		return RangeProof{}, fmt.Errorf("replayhandoff: proof requires signer for node %q", l.node)
	}
	l.mu.RLock()
	defer l.mu.RUnlock()

	seen := l.state[partition].Intersect(MustIntervalSet(bounds))
	previous := ZeroRoot
	if len(l.batches) >= 2 {
		previous = l.batches[len(l.batches)-2].Root
	}
	proof := RangeProof{
		Node:         l.node,
		Partition:    partition,
		Barrier:      Barrier(l.generation),
		Generation:   l.generation,
		PreviousRoot: previous,
		Root:         l.root,
		Range:        bounds,
		Seen:         seen,
	}
	proof.Signature = signer.Sign(ProofSigningMessage(proof))
	return proof, nil
}

// VerifyAgainstLog verifies both the signature and exact agreement with the
// historical state at the proof's generation.
func (p RangeProof) VerifyAgainstLog(log *NodeLog, keys KeyRing) error {
	if err := p.Verify(keys); err != nil {
		return err
	}
	if p.Node != log.node {
		return errors.New("replayhandoff: proof node does not match log")
	}
	if p.Partition.Context != log.context {
		return errors.New("replayhandoff: proof context does not match log")
	}
	log.mu.RLock()
	defer log.mu.RUnlock()
	if p.Generation > log.generation {
		return errors.New("replayhandoff: proof generation is in the future")
	}
	state := IntervalSet{}
	var root Root
	var previous Root
	for i := uint64(0); i < p.Generation; i++ {
		batch := log.batches[i]
		for _, entry := range batch.Entries {
			if entry.Partition == p.Partition {
				state = state.Union(entry.Delivered)
			}
		}
		previous = root
		root = batch.Root
	}
	if p.Generation == 0 {
		previous = ZeroRoot
		root = ZeroRoot
	}
	if p.Root != root || p.PreviousRoot != previous {
		return errors.New("replayhandoff: proof root does not match log barrier")
	}
	if !p.Seen.Equal(state.Intersect(MustIntervalSet(p.Range))) {
		return errors.New("replayhandoff: proof seen set does not match log state")
	}
	return nil
}

// ProofPage is a candidate page. One cost buys all proofs on the page, which
// may cover multiple nodes, ranges and partitions.
type ProofPage struct {
	ID     string
	Cost   int64
	Proofs []RangeProof
}

// Validate checks page-level invariants and every proof's signature.
func (p ProofPage) Validate(keys KeyRing) error {
	if p.ID == "" {
		return errors.New("replayhandoff: proof page id must not be empty")
	}
	if p.Cost < 0 {
		return errors.New("replayhandoff: proof page cost must be non-negative")
	}
	for i, proof := range p.Proofs {
		if err := proof.Verify(keys); err != nil {
			return fmt.Errorf("replayhandoff: page %q proof %d: %w", p.ID, i, err)
		}
	}
	return nil
}

// PayloadDigest identifies semantic page content independent of its id and
// price. Duplicate pages can therefore be collapsed safely.
func (p ProofPage) PayloadDigest() Root {
	proofs := normalizedProofCopy(p.Proofs)
	out := appendLP(nil, []byte("replayhandoff/page-payload/v1"))
	out = appendU64(out, uint64(len(proofs)))
	for _, proof := range proofs {
		out = appendRoot(out, ProofContentDigest(proof))
	}
	return hashParts([]byte("replayhandoff/page-digest/v1"), out)
}

func normalizedProofCopy(proofs []RangeProof) []RangeProof {
	byContent := make(map[Root]RangeProof, len(proofs))
	for _, proof := range proofs {
		key := ProofContentDigest(proof)
		if _, exists := byContent[key]; !exists {
			byContent[key] = cloneProof(proof)
		}
	}
	out := make([]RangeProof, 0, len(byContent))
	for _, proof := range byContent {
		out = append(out, proof)
	}
	sortProofs(out)
	return out
}

func cloneProof(proof RangeProof) RangeProof {
	out := proof
	out.Signature = append([]byte(nil), proof.Signature...)
	return out
}

func sortProofs(proofs []RangeProof) {
	sort.Slice(proofs, func(i, j int) bool {
		a, b := proofs[i], proofs[j]
		if a.Partition.Context != b.Partition.Context {
			return a.Partition.Context < b.Partition.Context
		}
		if a.Partition.Sender != b.Partition.Sender {
			return a.Partition.Sender < b.Partition.Sender
		}
		if a.Node != b.Node {
			return a.Node < b.Node
		}
		if a.Barrier != b.Barrier {
			return a.Barrier < b.Barrier
		}
		if a.Range.Lo != b.Range.Lo {
			return a.Range.Lo < b.Range.Lo
		}
		if a.Range.Hi != b.Range.Hi {
			return a.Range.Hi < b.Range.Hi
		}
		if c := bytes.Compare(a.Root[:], b.Root[:]); c != 0 {
			return c < 0
		}
		return bytes.Compare(a.Signature, b.Signature) < 0
	})
}

// RootConflict records the same node/partition/barrier exhibiting more than
// one authenticated root. The proofs are isolated, never merged.
type RootConflict struct {
	Node      NodeID
	Partition Partition
	Barrier   Barrier
	Roots     []Root
}

// NormalizationReport describes evidence canonicalization and duplicate
// removal.
type NormalizationReport struct {
	KnownProofsInput      int
	KnownProofsUnique     int
	KnownProofsDuplicates int
	PagesInput            int
	PagesUnique           int
	PageDuplicates        int
	ProofDuplicates       int
	Conflicts             []RootConflict
}

// NormalizedEvidence is canonical evidence suitable for decision making.
type NormalizedEvidence struct {
	Known  []RangeProof
	Pages  []ProofPage
	Report NormalizationReport
}

// NormalizeEvidence validates evidence, removes exact duplicates, sorts
// out-of-order input, and records same-barrier/different-root conflicts for
// later isolation. It enforces at most MaxCandidatePages distinct pages.
func NormalizeEvidence(known []RangeProof, pages []ProofPage, keys KeyRing) (NormalizedEvidence, error) {
	if keys == nil {
		keys = KeyRing{}
	}
	report := NormalizationReport{
		KnownProofsInput: len(known),
		PagesInput:       len(pages),
	}

	knownMap := make(map[Root]RangeProof, len(known))
	for i, proof := range known {
		if err := proof.Verify(keys); err != nil {
			return NormalizedEvidence{}, fmt.Errorf("replayhandoff: known proof %d: %w", i, err)
		}
		key := ProofContentDigest(proof)
		if _, exists := knownMap[key]; exists {
			report.ProofDuplicates++
			continue
		}
		knownMap[key] = cloneProof(proof)
	}
	knownUnique := make([]RangeProof, 0, len(knownMap))
	for _, proof := range knownMap {
		knownUnique = append(knownUnique, proof)
	}
	sortProofs(knownUnique)
	report.KnownProofsUnique = len(knownUnique)
	report.KnownProofsDuplicates = report.KnownProofsInput - len(knownUnique)

	type pageEntry struct {
		page    ProofPage
		payload Root
	}
	entries := make([]pageEntry, 0, len(pages))
	idToPayload := make(map[string]Root)
	for i, page := range pages {
		if page.ID == "" {
			return NormalizedEvidence{}, fmt.Errorf("replayhandoff: page %d has empty id", i)
		}
		if page.Cost < 0 {
			return NormalizedEvidence{}, fmt.Errorf("replayhandoff: page %q has negative cost", page.ID)
		}
		for j, proof := range page.Proofs {
			if err := proof.Verify(keys); err != nil {
				return NormalizedEvidence{}, fmt.Errorf("replayhandoff: page %q proof %d: %w", page.ID, j, err)
			}
		}
		uniqueProofs := normalizedProofCopy(page.Proofs)
		report.ProofDuplicates += len(page.Proofs) - len(uniqueProofs)
		page.Proofs = uniqueProofs
		payload := page.PayloadDigest()
		if prior, exists := idToPayload[page.ID]; exists && prior != payload {
			return NormalizedEvidence{}, fmt.Errorf("replayhandoff: page id %q has conflicting payloads", page.ID)
		}
		idToPayload[page.ID] = payload
		entries = append(entries, pageEntry{page: page, payload: payload})
	}

	byPayload := make(map[Root]pageEntry)
	for _, entry := range entries {
		prior, exists := byPayload[entry.payload]
		if !exists {
			byPayload[entry.payload] = entry
			continue
		}
		report.PageDuplicates++
		if betterPage(entry.page, prior.page) {
			byPayload[entry.payload] = entry
		}
	}
	uniquePages := make([]ProofPage, 0, len(byPayload))
	for _, entry := range byPayload {
		uniquePages = append(uniquePages, entry.page)
	}
	sort.Slice(uniquePages, func(i, j int) bool {
		return uniquePages[i].ID < uniquePages[j].ID
	})
	report.PagesUnique = len(uniquePages)
	if report.PagesUnique > MaxCandidatePages {
		return NormalizedEvidence{}, fmt.Errorf("replayhandoff: %d distinct candidate pages exceeds maximum %d", report.PagesUnique, MaxCandidatePages)
	}
	const maxInt64 = int64(^uint64(0) >> 1)
	var totalCost int64
	for _, page := range uniquePages {
		if page.Cost > maxInt64-totalCost {
			return NormalizedEvidence{}, errors.New("replayhandoff: total candidate page cost overflows int64")
		}
		totalCost += page.Cost
	}

	conflicts := detectRootConflicts(knownUnique, uniquePages)
	report.Conflicts = conflicts
	return NormalizedEvidence{Known: knownUnique, Pages: uniquePages, Report: report}, nil
}

func betterPage(candidate, current ProofPage) bool {
	if candidate.Cost != current.Cost {
		return candidate.Cost < current.Cost
	}
	if len(candidate.Proofs) != len(current.Proofs) {
		return len(candidate.Proofs) < len(current.Proofs)
	}
	return candidate.ID < current.ID
}

func detectRootConflicts(known []RangeProof, pages []ProofPage) []RootConflict {
	type conflictKey struct {
		node      NodeID
		partition Partition
		barrier   Barrier
	}
	roots := make(map[conflictKey]map[Root]struct{})
	add := func(proof RangeProof) {
		key := conflictKey{node: proof.Node, partition: proof.Partition, barrier: proof.Barrier}
		if roots[key] == nil {
			roots[key] = make(map[Root]struct{})
		}
		roots[key][proof.Root] = struct{}{}
	}
	for _, proof := range known {
		add(proof)
	}
	for _, page := range pages {
		for _, proof := range page.Proofs {
			add(proof)
		}
	}
	out := make([]RootConflict, 0)
	for key, rootSet := range roots {
		if len(rootSet) <= 1 {
			continue
		}
		conflict := RootConflict{Node: key.node, Partition: key.partition, Barrier: key.barrier}
		for root := range rootSet {
			conflict.Roots = append(conflict.Roots, root)
		}
		sort.Slice(conflict.Roots, func(i, j int) bool {
			return bytes.Compare(conflict.Roots[i][:], conflict.Roots[j][:]) < 0
		})
		out = append(out, conflict)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Partition.Context != out[j].Partition.Context {
			return out[i].Partition.Context < out[j].Partition.Context
		}
		if out[i].Partition.Sender != out[j].Partition.Sender {
			return out[i].Partition.Sender < out[j].Partition.Sender
		}
		if out[i].Node != out[j].Node {
			return out[i].Node < out[j].Node
		}
		return out[i].Barrier < out[j].Barrier
	})
	return out
}

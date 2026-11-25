package replayhandoff

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
)

// CertificateInput is the self-contained finite protocol input used to build a
// certificate. Public keys are supplied separately by the verifier.
type CertificateInput struct {
	Nodes            []NodeID
	NodesByPartition map[Partition][]NodeID
	NewNode          NodeID
	Leases           []Lease
	LocalSeen        map[Partition]IntervalSet
	KnownProofs      []RangeProof
	CandidatePages   []ProofPage
	Queries          []QuerySegment
}

// CertificateSegment is the certified piecewise result for one input query.
type CertificateSegment struct {
	Index     int
	Partition Partition
	Range     Interval
	Decision  Decision
	Parts     []PartDecision
}

// CoverageCertificate is independently verifiable: it carries the finite input,
// selected page ids, piecewise decisions and witness digests. It is bound by a
// digest and may additionally be signed by the migrating receiver.
type CoverageCertificate struct {
	Version         uint64
	Input           CertificateInput
	SelectedPages   []string
	SupplementCost  int64
	Feasible        bool
	FullyDetermined bool
	LeaseBlocked    bool
	Normalization   NormalizationReport
	Conflicts       []RootConflict
	Segments        []CertificateSegment
	Issuer          NodeID
	Digest          Root
	Signature       []byte
}

// CertificateSigningMessage is the domain-separated certificate signature
// message.
func CertificateSigningMessage(digest Root) []byte {
	payload := appendRoot(nil, digest)
	return SigningMessage("coverage-certificate", payload)
}

func encodeWitness(dst []byte, witness Witness) []byte {
	dst = appendLP(dst, []byte(witness.Node))
	dst = appendU64(dst, boolU64(witness.Local))
	dst = appendU64(dst, boolU64(witness.Seen))
	dst = appendRoot(dst, witness.Root)
	dst = appendRoot(dst, witness.ProofDigest)
	dst = appendU64(dst, uint64(witness.Barrier))
	dst = appendU64(dst, witness.Generation)
	dst = appendInterval(dst, witness.Range)
	return dst
}

func encodePartDecision(dst []byte, part PartDecision) []byte {
	dst = appendInterval(dst, part.Interval)
	dst = appendU64(dst, uint64(part.Decision))
	dst = appendLP(dst, []byte(part.Reason))
	dst = appendU64(dst, uint64(len(part.MissingNodes)))
	for _, node := range part.MissingNodes {
		dst = appendLP(dst, []byte(node))
	}
	dst = appendU64(dst, uint64(len(part.SeenBy)))
	for _, node := range part.SeenBy {
		dst = appendLP(dst, []byte(node))
	}
	dst = appendU64(dst, uint64(len(part.OpenLeases)))
	for _, node := range part.OpenLeases {
		dst = appendLP(dst, []byte(node))
	}
	dst = appendU64(dst, uint64(len(part.Witnesses)))
	for _, witness := range part.Witnesses {
		dst = encodeWitness(dst, witness)
	}
	return dst
}

func encodeNormalization(dst []byte, report NormalizationReport) []byte {
	dst = appendU64(dst, uint64(report.KnownProofsInput))
	dst = appendU64(dst, uint64(report.KnownProofsUnique))
	dst = appendU64(dst, uint64(report.KnownProofsDuplicates))
	dst = appendU64(dst, uint64(report.PagesInput))
	dst = appendU64(dst, uint64(report.PagesUnique))
	dst = appendU64(dst, uint64(report.PageDuplicates))
	dst = appendU64(dst, uint64(report.ProofDuplicates))
	dst = appendU64(dst, uint64(len(report.Conflicts)))
	for _, conflict := range report.Conflicts {
		dst = encodeRootConflict(dst, conflict)
	}
	return dst
}

func encodeRootConflict(dst []byte, conflict RootConflict) []byte {
	dst = appendLP(dst, []byte(conflict.Node))
	dst = appendPartition(dst, conflict.Partition)
	dst = appendU64(dst, uint64(conflict.Barrier))
	dst = appendU64(dst, uint64(len(conflict.Roots)))
	for _, root := range conflict.Roots {
		dst = appendRoot(dst, root)
	}
	return dst
}

func encodeProof(dst []byte, proof RangeProof) []byte {
	dst = appendLP(dst, []byte(proof.Node))
	dst = appendPartition(dst, proof.Partition)
	dst = appendU64(dst, uint64(proof.Barrier))
	dst = appendU64(dst, proof.Generation)
	dst = appendRoot(dst, proof.PreviousRoot)
	dst = appendRoot(dst, proof.Root)
	dst = appendInterval(dst, proof.Range)
	dst = appendIntervalSet(dst, proof.Seen)
	dst = appendLP(dst, proof.Signature)
	return dst
}

func encodePage(dst []byte, page ProofPage) []byte {
	dst = appendLP(dst, []byte(page.ID))
	dst = appendU64(dst, uint64(page.Cost))
	dst = appendU64(dst, uint64(len(page.Proofs)))
	for _, proof := range page.Proofs {
		dst = encodeProof(dst, proof)
	}
	return dst
}

func encodeCertificateBody(cert CoverageCertificate) []byte {
	out := appendLP(nil, []byte("replayhandoff/coverage-certificate/v1"))
	out = appendU64(out, cert.Version)
	out = appendU64(out, uint64(len(cert.Input.Nodes)))
	for _, node := range cert.Input.Nodes {
		out = appendLP(out, []byte(node))
	}
	partitionKeys := make([]Partition, 0, len(cert.Input.NodesByPartition))
	for partition := range cert.Input.NodesByPartition {
		partitionKeys = append(partitionKeys, partition)
	}
	sort.Slice(partitionKeys, func(i, j int) bool { return partitionKeys[i].String() < partitionKeys[j].String() })
	out = appendU64(out, uint64(len(partitionKeys)))
	for _, partition := range partitionKeys {
		out = appendPartition(out, partition)
		nodes := cert.Input.NodesByPartition[partition]
		out = appendU64(out, uint64(len(nodes)))
		for _, node := range nodes {
			out = appendLP(out, []byte(node))
		}
	}
	out = appendLP(out, []byte(cert.Input.NewNode))
	out = appendU64(out, uint64(len(cert.Input.Leases)))
	leases := append([]Lease(nil), cert.Input.Leases...)
	sort.Slice(leases, func(i, j int) bool { return leases[i].Node < leases[j].Node })
	for _, lease := range leases {
		out = appendLP(out, []byte(lease.Node))
		out = appendU64(out, boolU64(lease.Closed))
		out = appendU64(out, uint64(lease.Barrier))
	}
	localKeys := make([]Partition, 0, len(cert.Input.LocalSeen))
	for partition := range cert.Input.LocalSeen {
		localKeys = append(localKeys, partition)
	}
	sort.Slice(localKeys, func(i, j int) bool { return localKeys[i].String() < localKeys[j].String() })
	out = appendU64(out, uint64(len(localKeys)))
	for _, partition := range localKeys {
		out = appendPartition(out, partition)
		out = appendIntervalSet(out, cert.Input.LocalSeen[partition])
	}
	out = appendU64(out, uint64(len(cert.Input.KnownProofs)))
	proofs := append([]RangeProof(nil), cert.Input.KnownProofs...)
	sortProofs(proofs)
	for _, proof := range proofs {
		out = encodeProof(out, proof)
	}
	out = appendU64(out, uint64(len(cert.Input.CandidatePages)))
	pages := append([]ProofPage(nil), cert.Input.CandidatePages...)
	sort.Slice(pages, func(i, j int) bool { return pages[i].ID < pages[j].ID })
	for _, page := range pages {
		out = encodePage(out, page)
	}
	out = appendU64(out, uint64(len(cert.Input.Queries)))
	for _, query := range cert.Input.Queries {
		out = appendPartition(out, query.Partition)
		out = appendInterval(out, query.Range)
	}
	out = appendU64(out, uint64(len(cert.SelectedPages)))
	for _, id := range cert.SelectedPages {
		out = appendLP(out, []byte(id))
	}
	out = appendU64(out, uint64(cert.SupplementCost))
	out = appendU64(out, boolU64(cert.Feasible))
	out = appendU64(out, boolU64(cert.FullyDetermined))
	out = appendU64(out, boolU64(cert.LeaseBlocked))
	out = encodeNormalization(out, cert.Normalization)
	out = appendU64(out, uint64(len(cert.Conflicts)))
	for _, conflict := range cert.Conflicts {
		out = encodeRootConflict(out, conflict)
	}
	out = appendU64(out, uint64(len(cert.Segments)))
	for _, segment := range cert.Segments {
		out = appendU64(out, uint64(segment.Index))
		out = appendPartition(out, segment.Partition)
		out = appendInterval(out, segment.Range)
		out = appendU64(out, uint64(segment.Decision))
		out = appendU64(out, uint64(len(segment.Parts)))
		for _, part := range segment.Parts {
			out = encodePartDecision(out, part)
		}
	}
	out = appendLP(out, []byte(cert.Issuer))
	return out
}

// ComputeCertificateDigest hashes every certificate field except Digest and
// Signature.
func ComputeCertificateDigest(cert CoverageCertificate) Root {
	body := encodeCertificateBody(cert)
	return hashParts([]byte("replayhandoff/certificate-digest/v1"), body)
}

// Seal computes the digest and signs it as the issuer.
func (cert *CoverageCertificate) Seal(signer *NodeSigner) error {
	if cert == nil || signer == nil {
		return errors.New("replayhandoff: certificate signer is required")
	}
	cert.Issuer = signer.Node
	cert.Digest = ComputeCertificateDigest(*cert)
	cert.Signature = signer.Sign(CertificateSigningMessage(cert.Digest))
	return nil
}

func (h *Handoff) newCoverageCertificate(queries []QuerySegment, selected []PageSelection, segments []SegmentReport, report *BatchReport) CoverageCertificate {
	input := CertificateInput{
		Nodes:            append([]NodeID(nil), h.config.Nodes...),
		NodesByPartition: clonePartitionNodes(h.config.NodesByPartition),
		NewNode:          h.config.NewNode,
		Leases:           append([]Lease(nil), h.config.Leases...),
		LocalSeen:        cloneLocalSeen(h.localSeen),
		KnownProofs:      make([]RangeProof, 0, len(h.known)),
		CandidatePages:   make([]ProofPage, 0, len(h.pages)),
		Queries:          append([]QuerySegment(nil), queries...),
	}
	for _, proof := range h.known {
		input.KnownProofs = append(input.KnownProofs, cloneProof(proof))
	}
	for _, page := range h.pages {
		input.CandidatePages = append(input.CandidatePages, clonePage(page))
	}
	cert := CoverageCertificate{
		Version:         1,
		Input:           input,
		SelectedPages:   pageIDs(selected),
		SupplementCost:  report.SupplementCost,
		Feasible:        report.Feasible,
		FullyDetermined: report.FullyDetermined,
		LeaseBlocked:    report.LeaseBlocked,
		Normalization:   cloneNormalization(h.report),
		Conflicts:       cloneNormalization(h.report).Conflicts,
		Segments:        make([]CertificateSegment, 0, len(segments)),
	}
	for _, segment := range segments {
		cert.Segments = append(cert.Segments, CertificateSegment{
			Index:     segment.Index,
			Partition: segment.Segment.Partition,
			Range:     segment.Segment.Range,
			Decision:  segment.ProjectedDecision,
			Parts:     append([]PartDecision(nil), segment.ProjectedParts...),
		})
	}
	if h.config.CertificateSigner != nil {
		_ = cert.Seal(h.config.CertificateSigner)
	} else {
		cert.Digest = ComputeCertificateDigest(cert)
	}
	return cert
}

func pageIDs(selected []PageSelection) []string {
	out := make([]string, 0, len(selected))
	for _, page := range selected {
		out = append(out, page.ID)
	}
	sort.Strings(out)
	return out
}

func clonePartitionNodes(src map[Partition][]NodeID) map[Partition][]NodeID {
	if src == nil {
		return nil
	}
	out := make(map[Partition][]NodeID, len(src))
	for partition, nodes := range src {
		out[partition] = append([]NodeID(nil), nodes...)
	}
	return out
}

func cloneLocalSeen(src map[Partition]IntervalSet) map[Partition]IntervalSet {
	if src == nil {
		return nil
	}
	out := make(map[Partition]IntervalSet, len(src))
	for partition, seen := range src {
		out[partition] = seen
	}
	return out
}

// VerifyCertificate recomputes the entire finite analysis from the embedded
// input and checks the digest and all selected pages. It does not require a
// signature, but does require every embedded root proof to verify.
func VerifyCertificate(cert *CoverageCertificate, keys KeyRing) error {
	if cert == nil {
		return errors.New("replayhandoff: nil certificate")
	}
	if cert.Version != 1 {
		return fmt.Errorf("replayhandoff: unsupported certificate version %d", cert.Version)
	}
	if ComputeCertificateDigest(*cert) != cert.Digest {
		return errors.New("replayhandoff: certificate digest mismatch")
	}
	if len(cert.Signature) > 0 {
		if cert.Issuer == "" {
			return errors.New("replayhandoff: signed certificate has no issuer")
		}
		if err := keys.Verify(cert.Issuer, CertificateSigningMessage(cert.Digest), cert.Signature); err != nil {
			return fmt.Errorf("replayhandoff: certificate signature: %w", err)
		}
	}
	return verifyCertificateAnalysis(cert, keys)
}

// VerifyCertificateAuthenticated requires both the analysis and an issuer
// signature.
func VerifyCertificateAuthenticated(cert *CoverageCertificate, keys KeyRing) error {
	if cert == nil || cert.Issuer == "" || len(cert.Signature) == 0 {
		return errors.New("replayhandoff: certificate is not signed")
	}
	return VerifyCertificate(cert, keys)
}

func verifyCertificateAnalysis(cert *CoverageCertificate, keys KeyRing) error {
	h, err := NewHandoff(HandoffConfig{
		Nodes:            cert.Input.Nodes,
		NodesByPartition: clonePartitionNodes(cert.Input.NodesByPartition),
		NewNode:          cert.Input.NewNode,
		Leases:           cert.Input.Leases,
		LocalSeen:        cloneLocalSeen(cert.Input.LocalSeen),
		KnownProofs:      cert.Input.KnownProofs,
		CandidatePages:   cert.Input.CandidatePages,
		Keys:             keys,
	})
	if err != nil {
		return fmt.Errorf("replayhandoff: certificate input: %w", err)
	}
	report, err := h.analyzeBatch(cert.Input.Queries, false)
	if err != nil {
		return fmt.Errorf("replayhandoff: certificate recomputation: %w", err)
	}
	if !report.Feasible {
		return errors.New("replayhandoff: certificate claims infeasible page selection")
	}
	if !reflect.DeepEqual(cert.SelectedPages, pageIDs(report.Supplement)) {
		return errors.New("replayhandoff: selected page set is not the recomputed minimum")
	}
	if cert.SupplementCost != report.SupplementCost {
		return errors.New("replayhandoff: supplement cost mismatch")
	}
	if cert.FullyDetermined != report.FullyDetermined || cert.LeaseBlocked != report.LeaseBlocked {
		return errors.New("replayhandoff: certificate completeness flags mismatch")
	}
	if !reflect.DeepEqual(cert.Conflicts, h.Normalization().Conflicts) {
		return errors.New("replayhandoff: certificate root-conflict report mismatch")
	}
	if len(cert.Segments) != len(report.Segments) {
		return errors.New("replayhandoff: certificate segment count mismatch")
	}
	for i, segment := range cert.Segments {
		computed := report.Segments[i]
		if segment.Index != i || segment.Partition != computed.Segment.Partition || segment.Range != computed.Segment.Range {
			return fmt.Errorf("replayhandoff: certificate segment %d input mismatch", i)
		}
		if segment.Decision != computed.ProjectedDecision {
			return fmt.Errorf("replayhandoff: certificate segment %d decision mismatch", i)
		}
		if !reflect.DeepEqual(segment.Parts, computed.ProjectedParts) {
			return fmt.Errorf("replayhandoff: certificate segment %d parts mismatch", i)
		}
	}
	if !reflect.DeepEqual(cert.Digest, ComputeCertificateDigest(*cert)) {
		return errors.New("replayhandoff: certificate digest changed")
	}
	if err := verifyCertificateWitnesses(cert, h, keys); err != nil {
		return err
	}
	return nil
}

func boolU64(v bool) uint64 {
	if v {
		return 1
	}
	return 0
}

func verifyCertificateWitnesses(cert *CoverageCertificate, h *Handoff, keys KeyRing) error {
	selected := make(map[string]struct{}, len(cert.SelectedPages))
	for _, id := range cert.SelectedPages {
		selected[id] = struct{}{}
	}
	evidence := make(map[Root]RangeProof, len(cert.Input.KnownProofs))
	evidenceProofs := append([]RangeProof(nil), cert.Input.KnownProofs...)
	for _, page := range cert.Input.CandidatePages {
		if _, ok := selected[page.ID]; ok {
			evidenceProofs = append(evidenceProofs, page.Proofs...)
		}
	}
	for _, proof := range evidenceProofs {
		if err := proof.Verify(keys); err != nil {
			return fmt.Errorf("replayhandoff: certificate witness proof: %w", err)
		}
		evidence[ProofContentDigest(proof)] = proof
	}
	for segmentIndex, segment := range cert.Segments {
		for partIndex, part := range segment.Parts {
			if part.Decision == DecisionWait {
				continue
			}
			required := h.NodesFor(segment.Partition)
			requiredSet := make(map[NodeID]struct{}, len(required))
			for _, node := range required {
				requiredSet[node] = struct{}{}
			}
			localSeen := h.localSet(segment.Partition)
			accepted := make(map[NodeID]bool)
			rejected := false
			for _, witness := range part.Witnesses {
				if _, ok := requiredSet[witness.Node]; !ok {
					return fmt.Errorf("replayhandoff: certificate segment %d part %d has non-required witness %q", segmentIndex, partIndex, witness.Node)
				}
				if witness.Local {
					if witness.Node != cert.Input.NewNode {
						return fmt.Errorf("replayhandoff: certificate local witness is not NewNode")
					}
					seen := !localSeen.Intersect(MustIntervalSet(part.Interval)).Empty()
					if witness.Seen != seen {
						return fmt.Errorf("replayhandoff: certificate local witness seen flag mismatch")
					}
					if seen {
						rejected = true
					} else {
						accepted[witness.Node] = true
					}
					continue
				}
				proof, ok := evidence[witness.ProofDigest]
				if !ok {
					return fmt.Errorf("replayhandoff: certificate witness proof is absent from selected evidence")
				}
				if proof.Node != witness.Node ||
					proof.Root != witness.Root ||
					proof.Barrier != witness.Barrier ||
					proof.Generation != witness.Generation ||
					proof.Range != witness.Range {
					return fmt.Errorf("replayhandoff: certificate witness does not match its proof")
				}
				if proof.Partition != segment.Partition || !proofCoversAtom(proof, part.Interval) {
					return fmt.Errorf("replayhandoff: certificate witness proof does not cover the part")
				}
				seen := !proof.Seen.Intersect(MustIntervalSet(part.Interval)).Empty()
				if witness.Seen != seen {
					return fmt.Errorf("replayhandoff: certificate proof witness seen flag mismatch")
				}
				if seen {
					rejected = true
				} else {
					if proof.Barrier < h.leaseBarrier(proof.Node) {
						return fmt.Errorf("replayhandoff: certificate unseen witness is older than lease closure")
					}
					accepted[proof.Node] = true
				}
			}
			switch part.Decision {
			case DecisionAccept:
				for _, node := range required {
					if !accepted[node] {
						return fmt.Errorf("replayhandoff: certificate segment %d part %d lacks unseen witness for %q", segmentIndex, partIndex, node)
					}
				}
			case DecisionReject:
				if !rejected {
					return fmt.Errorf("replayhandoff: certificate segment %d part %d lacks a seen witness", segmentIndex, partIndex)
				}
			}
		}
	}
	return nil
}

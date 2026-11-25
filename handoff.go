package replayhandoff

import (
	"errors"
	"fmt"
	"sort"
)

// Decision is the result of a replay-state handoff check.
type Decision uint8

const (
	// DecisionWait means evidence or lease closure is incomplete.
	DecisionWait Decision = iota
	// DecisionAccept means every possible node proved the sequence unseen.
	DecisionAccept
	// DecisionReject means at least one node proved the sequence delivered.
	DecisionReject
	// DecisionMixed means an interval has both accepted and rejected parts but
	// no waiting part.
	DecisionMixed
)

// Convenience aliases used in predicates and reports.
const (
	Wait   = DecisionWait
	Accept = DecisionAccept
	Reject = DecisionReject
	Mixed  = DecisionMixed
)

// String returns a stable decision name.
func (d Decision) String() string {
	switch d {
	case DecisionWait:
		return "wait"
	case DecisionAccept:
		return "accept"
	case DecisionReject:
		return "reject"
	case DecisionMixed:
		return "mixed"
	default:
		return fmt.Sprintf("decision(%d)", d)
	}
}

// ReasonCode explains a part or aggregate decision.
type ReasonCode string

const (
	ReasonNone           ReasonCode = ""
	ReasonAllUnseen      ReasonCode = "all_possible_nodes_proved_unseen"
	ReasonSeen           ReasonCode = "delivered_by_a_possible_node"
	ReasonLeaseOpen      ReasonCode = "old_endpoint_lease_open"
	ReasonMissingProof   ReasonCode = "uncovered_range_is_unknown"
	ReasonNoNodes        ReasonCode = "no_possible_node_for_partition"
	ReasonEmpty          ReasonCode = "empty_half_open_range"
	ReasonRootConflict   ReasonCode = "same_barrier_root_conflict"
	ReasonPagesNeeded    ReasonCode = "supplementary_pages_required"
	ReasonProvedUnseen   ReasonCode = "witness_is_unseen"
	ReasonProvedSeen     ReasonCode = "witness_is_seen"
	ReasonNoProofPageSet ReasonCode = "no_page_subset_can_decide"
)

// Lease describes an old receiver's endpoint lease. A missing lease record is
// treated as not closed. Barrier is the closure barrier; unseen proofs at a
// lower barrier are stale and do not establish non-delivery.
type Lease struct {
	Node    NodeID
	Closed  bool
	Barrier Barrier
}

// HandoffConfig is the complete, finite, offline input to a handoff engine.
type HandoffConfig struct {
	// Nodes is the default set of nodes that could have served a request.
	Nodes []NodeID
	// NodesByPartition optionally overrides Nodes for a sender partition.
	NodesByPartition map[Partition][]NodeID
	// NewNode is the replacing receiver. Its local state is trusted evidence;
	// it is implicitly added to every partition's possible-node set.
	NewNode NodeID
	// Leases describes old endpoints. Old possible nodes without a closed lease
	// are considered still live.
	Leases []Lease
	// LocalSeen are deliveries already made by NewNode. A missing partition key
	// means the fresh node has seen nothing.
	LocalSeen map[Partition]IntervalSet
	// KnownProofs are already-obtained authenticated range proofs.
	KnownProofs []RangeProof
	// CandidatePages are not fetched yet. They are used only to compute the
	// minimum supplementary page set.
	CandidatePages []ProofPage
	// Keys is the trusted node public-key ring.
	Keys KeyRing
	// CertificateSigner optionally signs emitted coverage certificates.
	CertificateSigner *NodeSigner
}

// QuerySegment is an arbitrary half-open batch query for one partition.
type QuerySegment struct {
	Partition Partition
	Range     Interval
}

// PageSelection names a candidate proof page and the one-time cost paid for
// fetching it.
type PageSelection struct {
	ID   string
	Cost int64
}

// Witness is independently checkable proof material for a decision part.
type Witness struct {
	Node        NodeID
	Local       bool
	Seen        bool
	Root        Root
	ProofDigest Root
	Barrier     Barrier
	Generation  uint64
	Range       Interval
}

// PartDecision is the uniform decision over one half-open atomic piece of a
// query.
type PartDecision struct {
	Interval     Interval
	Decision     Decision
	Reason       ReasonCode
	MissingNodes []NodeID
	SeenBy       []NodeID
	OpenLeases   []NodeID
	Witnesses    []Witness
}

// AggregateParts reduces piecewise decisions. Any waiting piece makes the
// aggregate wait; otherwise all-accept or all-reject stay pure and a mixture
// becomes DecisionMixed.
func AggregateParts(parts []PartDecision) Decision {
	if len(parts) == 0 {
		return DecisionAccept
	}
	seenAccept, seenReject := false, false
	for _, part := range parts {
		switch part.Decision {
		case DecisionWait:
			return DecisionWait
		case DecisionAccept:
			seenAccept = true
		case DecisionReject:
			seenReject = true
		}
	}
	if seenAccept && seenReject {
		return DecisionMixed
	}
	if seenReject {
		return DecisionReject
	}
	return DecisionAccept
}

// Verdict is the decision for one sequence number, including the minimum
// supplementary page set that would make the evidence decisive.
type Verdict struct {
	Partition    Partition
	Seq          Seq
	Decision     Decision
	Reason       ReasonCode
	MissingNodes []NodeID
	SeenBy       []NodeID
	OpenLeases   []NodeID
	Witnesses    []Witness
	NeededPages  []PageSelection
	NeededCost   int64
	// Resolvable means a supplementary page subset can decide the evidence.
	Resolvable bool
	// LeaseBlocked means acceptance is impossible until old leases close.
	LeaseBlocked bool
	Conflicts    []RootConflict
}

// IntervalReport is a known-evidence-only piecewise interval analysis.
type IntervalReport struct {
	Partition Partition
	Range     Interval
	Decision  Decision
	Parts     []PartDecision
}

// Handoff is an offline replay-state migration engine. It makes no network
// calls and does not enumerate the 2^40 sequence space.
type Handoff struct {
	config    HandoffConfig
	leases    map[NodeID]Lease
	known     []RangeProof
	pages     []ProofPage
	report    NormalizationReport
	localSeen map[Partition]IntervalSet
}

// NewHandoff validates and normalizes the finite protocol input.
func NewHandoff(config HandoffConfig) (*Handoff, error) {
	h := &Handoff{
		config:    config,
		leases:    make(map[NodeID]Lease),
		localSeen: make(map[Partition]IntervalSet),
	}
	h.config.Nodes = dedupeNodes(config.Nodes)
	h.config.NodesByPartition = clonePartitionNodes(config.NodesByPartition)
	for partition, nodes := range h.config.NodesByPartition {
		if err := partition.Validate(); err != nil {
			return nil, fmt.Errorf("replayhandoff: routing partition: %w", err)
		}
		h.config.NodesByPartition[partition] = dedupeNodes(nodes)
	}
	h.config.Leases = append([]Lease(nil), config.Leases...)
	h.config.Keys = config.Keys.Clone()
	if h.config.CertificateSigner != nil {
		public, ok := h.config.Keys[h.config.CertificateSigner.Node]
		if !ok {
			return nil, fmt.Errorf("replayhandoff: certificate signer %q is absent from key ring", h.config.CertificateSigner.Node)
		}
		if string(public) != string(h.config.CertificateSigner.Public) {
			return nil, errors.New("replayhandoff: certificate signer public key does not match key ring")
		}
	}
	if len(config.Nodes) == 0 && len(config.NodesByPartition) == 0 && config.NewNode == "" {
		return nil, errors.New("replayhandoff: at least one possible node or NewNode is required")
	}
	for _, lease := range config.Leases {
		if lease.Node == "" {
			return nil, errors.New("replayhandoff: lease node must not be empty")
		}
		if _, exists := h.leases[lease.Node]; exists {
			return nil, fmt.Errorf("replayhandoff: duplicate lease for node %q", lease.Node)
		}
		h.leases[lease.Node] = lease
	}
	for partition, seen := range config.LocalSeen {
		if err := partition.Validate(); err != nil {
			return nil, err
		}
		if err := ValidateSet(seen); err != nil {
			return nil, fmt.Errorf("replayhandoff: local seen set for %s: %w", partition, err)
		}
		h.localSeen[partition] = seen
	}
	normalized, err := NormalizeEvidence(config.KnownProofs, config.CandidatePages, h.config.Keys)
	if err != nil {
		return nil, err
	}
	h.known = normalized.Known
	h.pages = normalized.Pages
	h.report = normalized.Report
	return h, nil
}

// Normalization exposes duplicate and root-conflict diagnostics.
func (h *Handoff) Normalization() NormalizationReport {
	return cloneNormalization(h.report)
}

// KnownProofs returns the normalized already-obtained evidence.
func (h *Handoff) KnownProofs() []RangeProof {
	out := make([]RangeProof, len(h.known))
	for i, proof := range h.known {
		out[i] = cloneProof(proof)
	}
	return out
}

// CandidatePages returns normalized candidate pages.
func (h *Handoff) CandidatePages() []ProofPage {
	out := make([]ProofPage, len(h.pages))
	for i, page := range h.pages {
		out[i] = clonePage(page)
	}
	return out
}

func clonePage(page ProofPage) ProofPage {
	out := ProofPage{ID: page.ID, Cost: page.Cost}
	out.Proofs = make([]RangeProof, 0, len(page.Proofs))
	for _, proof := range page.Proofs {
		out.Proofs = append(out.Proofs, cloneProof(proof))
	}
	return out
}

func cloneNormalization(report NormalizationReport) NormalizationReport {
	out := report
	out.Conflicts = append([]RootConflict(nil), report.Conflicts...)
	for i := range out.Conflicts {
		out.Conflicts[i].Roots = append([]Root(nil), report.Conflicts[i].Roots...)
	}
	return out
}

// NodesFor returns the possible serving nodes for a partition, including the
// new receiver when configured.
func (h *Handoff) NodesFor(partition Partition) []NodeID {
	nodes := h.config.Nodes
	if override, ok := h.config.NodesByPartition[partition]; ok {
		nodes = override
	}
	nodes = dedupeNodes(nodes)
	if h.config.NewNode != "" {
		found := false
		for _, node := range nodes {
			if node == h.config.NewNode {
				found = true
				break
			}
		}
		if !found {
			nodes = append(nodes, h.config.NewNode)
		}
	}
	return nodes
}

func dedupeNodes(nodes []NodeID) []NodeID {
	out := make([]NodeID, 0, len(nodes))
	seen := make(map[NodeID]struct{}, len(nodes))
	for _, node := range nodes {
		if node == "" {
			continue
		}
		if _, exists := seen[node]; exists {
			continue
		}
		seen[node] = struct{}{}
		out = append(out, node)
	}
	return out
}

func (h *Handoff) localSet(partition Partition) IntervalSet {
	return h.localSeen[partition]
}

func (h *Handoff) leaseBarrier(node NodeID) Barrier {
	if lease, ok := h.leases[node]; ok {
		return lease.Barrier
	}
	return 0
}

func (h *Handoff) leaseOpen(node NodeID) bool {
	if node == h.config.NewNode {
		return false
	}
	lease, ok := h.leases[node]
	return !ok || !lease.Closed
}

func (h *Handoff) proofsFor(partition Partition, proofs []RangeProof) []RangeProof {
	out := make([]RangeProof, 0, len(proofs))
	for _, proof := range proofs {
		if proof.Partition == partition {
			out = append(out, proof)
		}
	}
	sortProofs(out)
	return out
}

func proofCoversAtom(proof RangeProof, atom Interval) bool {
	return proof.Range.Lo <= atom.Lo && proof.Range.Hi >= atom.Hi
}

func proofSeesAtom(proof RangeProof, atom Interval) bool {
	return proof.Seen.Intersect(MustIntervalSet(atom)).Empty() == false
}

func (h *Handoff) evaluateAtom(partition Partition, atom Interval, proofs []RangeProof) PartDecision {
	part := PartDecision{Interval: atom, Decision: DecisionWait, Reason: ReasonMissingProof}
	if atom.Empty() {
		part.Decision = DecisionAccept
		part.Reason = ReasonEmpty
		return part
	}
	nodes := h.NodesFor(partition)
	if len(nodes) == 0 {
		part.Reason = ReasonNoNodes
		return part
	}

	seenBy := make(map[NodeID]Witness)
	seenNodes := make([]NodeID, 0)
	cover := make(map[NodeID]Witness)
	missing := make([]NodeID, 0)
	open := make([]NodeID, 0)
	local := h.localSet(partition)
	if local.Intersect(MustIntervalSet(atom)).Empty() == false && h.config.NewNode != "" {
		seenBy[h.config.NewNode] = Witness{
			Node:  h.config.NewNode,
			Local: true,
			Seen:  true,
			Range: atom,
		}
		seenNodes = append(seenNodes, h.config.NewNode)
	}
	for _, node := range nodes {
		if h.leaseOpen(node) {
			open = append(open, node)
		}
		if h.config.NewNode != "" && node == h.config.NewNode {
			if witness, seen := seenBy[node]; seen {
				_ = witness
				continue
			}
			cover[node] = Witness{Node: node, Local: true, Seen: false, Range: atom}
		}
	}
	for _, proof := range proofs {
		if !proofCoversAtom(proof, atom) {
			continue
		}
		required := false
		for _, node := range nodes {
			if node == proof.Node {
				required = true
				break
			}
		}
		if !required {
			continue
		}
		seen := proofSeesAtom(proof, atom)
		if seen {
			if _, exists := seenBy[proof.Node]; !exists {
				seenBy[proof.Node] = Witness{
					Node:        proof.Node,
					Seen:        true,
					Root:        proof.Root,
					ProofDigest: ProofContentDigest(proof),
					Barrier:     proof.Barrier,
					Generation:  proof.Generation,
					Range:       proof.Range,
				}
				seenNodes = append(seenNodes, proof.Node)
			}
			continue
		}
		if proof.Barrier < h.leaseBarrier(proof.Node) {
			continue
		}
		if _, exists := cover[proof.Node]; !exists {
			cover[proof.Node] = Witness{
				Node:        proof.Node,
				Seen:        false,
				Root:        proof.Root,
				ProofDigest: ProofContentDigest(proof),
				Barrier:     proof.Barrier,
				Generation:  proof.Generation,
				Range:       proof.Range,
			}
		}
	}
	if len(seenNodes) > 0 {
		part.Decision = DecisionReject
		part.Reason = ReasonSeen
		part.SeenBy = append([]NodeID(nil), seenNodes...)
		for _, node := range seenNodes {
			part.Witnesses = append(part.Witnesses, seenBy[node])
		}
		return part
	}
	for _, node := range nodes {
		if _, ok := cover[node]; !ok {
			missing = append(missing, node)
		}
	}
	if len(open) > 0 {
		part.Decision = DecisionWait
		part.Reason = ReasonLeaseOpen
		part.MissingNodes = missing
		part.OpenLeases = open
		return part
	}
	if len(missing) > 0 {
		part.Decision = DecisionWait
		part.Reason = ReasonMissingProof
		part.MissingNodes = missing
		return part
	}
	part.Decision = DecisionAccept
	part.Reason = ReasonAllUnseen
	for _, node := range nodes {
		part.Witnesses = append(part.Witnesses, cover[node])
	}
	return part
}

func (h *Handoff) atomBoundaries(partition Partition, query Interval, includeCandidates bool) []Interval {
	if query.Empty() {
		return nil
	}
	boundaries := map[Seq]struct{}{
		query.Lo: {},
		query.Hi: {},
	}
	addProof := func(proof RangeProof) {
		if proof.Partition != partition || !proof.Range.Intersects(query) {
			return
		}
		if proof.Range.Lo >= query.Lo && proof.Range.Lo < query.Hi {
			boundaries[proof.Range.Lo] = struct{}{}
		}
		if proof.Range.Hi > query.Lo && proof.Range.Hi <= query.Hi {
			boundaries[proof.Range.Hi] = struct{}{}
		}
		for _, seen := range proof.Seen.Intervals() {
			if seen.Lo >= query.Lo && seen.Lo < query.Hi {
				boundaries[seen.Lo] = struct{}{}
			}
			if seen.Hi > query.Lo && seen.Hi <= query.Hi {
				boundaries[seen.Hi] = struct{}{}
			}
		}
	}
	for _, proof := range h.known {
		addProof(proof)
	}
	if includeCandidates {
		for _, page := range h.pages {
			for _, proof := range page.Proofs {
				addProof(proof)
			}
		}
	}
	for _, seen := range h.localSet(partition).Intervals() {
		if seen.Lo >= query.Lo && seen.Lo < query.Hi {
			boundaries[seen.Lo] = struct{}{}
		}
		if seen.Hi > query.Lo && seen.Hi <= query.Hi {
			boundaries[seen.Hi] = struct{}{}
		}
	}
	values := make([]Seq, 0, len(boundaries))
	for value := range boundaries {
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	out := make([]Interval, 0, len(values)-1)
	for i := 0; i+1 < len(values); i++ {
		if values[i] < values[i+1] {
			out = append(out, Interval{Lo: values[i], Hi: values[i+1]})
		}
	}
	return out
}

func (h *Handoff) analyzeParts(partition Partition, query Interval, proofs []RangeProof, includeCandidates bool) []PartDecision {
	atoms := h.atomBoundaries(partition, query, includeCandidates)
	parts := make([]PartDecision, 0, len(atoms))
	for _, atom := range atoms {
		parts = append(parts, h.evaluateAtom(partition, atom, proofs))
	}
	return parts
}

// EvaluateSequence decides exactly one 40-bit sequence number. If evidence is
// incomplete it computes the minimum-cost candidate page subset that would
// decide it.
func (h *Handoff) EvaluateSequence(partition Partition, seq Seq) (Verdict, error) {
	if err := partition.Validate(); err != nil {
		return Verdict{}, err
	}
	if !seq.Valid() {
		return Verdict{}, ErrOutsideSequenceSpace
	}
	proofs := h.proofsFor(partition, h.known)
	part := h.evaluateAtom(partition, Interval{Lo: seq, Hi: seq + 1}, proofs)
	verdict := Verdict{
		Partition:    partition,
		Seq:          seq,
		Decision:     part.Decision,
		Reason:       part.Reason,
		MissingNodes: part.MissingNodes,
		SeenBy:       part.SeenBy,
		OpenLeases:   part.OpenLeases,
		Witnesses:    part.Witnesses,
		LeaseBlocked: len(part.OpenLeases) > 0,
		Conflicts:    conflictsFor(h.report.Conflicts, partition),
	}
	if part.Decision != DecisionWait || len(part.MissingNodes) == 0 {
		if part.Decision == DecisionWait {
			verdict.Resolvable = part.Reason != ReasonNoNodes
		}
		return verdict, nil
	}
	req := h.requirementFor(partition, part.Interval, part.MissingNodes)
	selected, cost, feasible := h.minimumPages([]requirement{req})
	verdict.Resolvable = feasible
	verdict.NeededPages = selected
	verdict.NeededCost = cost
	if !feasible {
		verdict.Reason = ReasonNoProofPageSet
	}
	return verdict, nil
}

// EvaluateInterval returns a piecewise decision from currently known proofs.
func (h *Handoff) EvaluateInterval(partition Partition, bounds Interval) (IntervalReport, error) {
	if err := partition.Validate(); err != nil {
		return IntervalReport{}, err
	}
	if err := bounds.Validate(); err != nil {
		return IntervalReport{}, err
	}
	parts := h.analyzeParts(partition, bounds, h.proofsFor(partition, h.known), false)
	return IntervalReport{
		Partition: partition,
		Range:     bounds,
		Decision:  AggregateParts(parts),
		Parts:     parts,
	}, nil
}

func conflictsFor(conflicts []RootConflict, partition Partition) []RootConflict {
	out := make([]RootConflict, 0)
	for _, conflict := range conflicts {
		if conflict.Partition != partition {
			continue
		}
		clone := conflict
		clone.Roots = append([]Root(nil), conflict.Roots...)
		out = append(out, clone)
	}
	return out
}

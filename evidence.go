package admission

import (
	"bytes"
	"fmt"
	"sort"
)

// Status is the admission result for a publication.
type Status string

const (
	// StatusAdmitted means the target extends every committed checkpoint, has
	// a valid module inclusion proof, and satisfies the witness policy.
	StatusAdmitted Status = "admitted"
	// StatusUnknown means evidence is missing. It does not mean the target is
	// safe or unsafe; callers must wait for more evidence or acquire a plan.
	StatusUnknown Status = "unknown"
	// StatusFork means two different roots were observed at the same size.
	StatusFork Status = "observed_fork"
	// StatusRejected means supplied evidence was structurally invalid and the
	// target cannot be admitted.
	StatusRejected Status = "rejected"
)

// Short aliases used by some callers.
const (
	Admitted     = StatusAdmitted
	Unknown      = StatusUnknown
	ObservedFork = StatusFork
	Rejected     = StatusRejected
)

// Fork records distinct roots observed at one tree size.
type Fork struct {
	Size        uint64       `json:"size"`
	Roots       [][]byte     `json:"roots"`
	Checkpoints []Checkpoint `json:"checkpoints"`
}

// EvidenceEdge is a signed consistency proof from one checkpoint to a larger
// checkpoint. The proof and signatures are checked independently; an edge is
// usable only when both are valid and the signature set meets its threshold.
type EvidenceEdge struct {
	From             Checkpoint         `json:"from"`
	To               Checkpoint         `json:"to"`
	Proof            [][]byte           `json:"proof"`
	Signatures       []WitnessSignature `json:"signatures"`
	Threshold        int                `json:"threshold,omitempty"`
	ConsistencyProof [][]byte           `json:"consistency_proof,omitempty"`
	Witnesses        []WitnessSignature `json:"witnesses,omitempty"`
}

// Evidence and ConsistencyEvidence are aliases for EvidenceEdge.
type Evidence = EvidenceEdge
type ConsistencyEvidence = EvidenceEdge

// NewEvidenceEdge constructs a consistency edge.
func NewEvidenceEdge(from, to Checkpoint, proof [][]byte, signatures []WitnessSignature) EvidenceEdge {
	return EvidenceEdge{
		From:       from,
		To:         to,
		Proof:      cloneHashes(proof),
		Signatures: signatures,
	}
}

// NewConsistencyEvidence is an alias for NewEvidenceEdge.
func NewConsistencyEvidence(from, to Checkpoint, proof [][]byte, signatures []WitnessSignature) EvidenceEdge {
	return NewEvidenceEdge(from, to, proof, signatures)
}

func (e EvidenceEdge) proof() [][]byte {
	if e.Proof != nil {
		return e.Proof
	}
	return e.ConsistencyProof
}

func (e EvidenceEdge) signatures() []WitnessSignature {
	if e.Signatures != nil && e.Witnesses != nil {
		out := make([]WitnessSignature, 0, len(e.Signatures)+len(e.Witnesses))
		out = append(out, e.Signatures...)
		out = append(out, e.Witnesses...)
		return out
	}
	if e.Signatures != nil {
		return e.Signatures
	}
	return e.Witnesses
}

// Result is the admission decision and, when still unknown, the optional
// minimum-cost acquisition plan for missing evidence.
type Result struct {
	Status       Status       `json:"status"`
	Module       Module       `json:"module"`
	Target       Checkpoint   `json:"target"`
	Missing      []Checkpoint `json:"missing_anchors,omitempty"`
	Fork         *Fork        `json:"fork,omitempty"`
	Plan         *Plan        `json:"plan,omitempty"`
	Reason       string       `json:"reason,omitempty"`
	ForkObserved bool         `json:"fork_observed,omitempty"`
}

// Evaluator checks publications against a set of already-committed
// checkpoints. It is stateless and safe to reuse.
type Evaluator struct {
	Anchors []Checkpoint
	Policy  Policy
}

// NewEvaluator accepts either a Policy or an integer threshold as the optional
// second argument.
func NewEvaluator(anchors []Checkpoint, policyOrThreshold ...any) *Evaluator {
	policy := Policy{Threshold: 1}
	switch len(policyOrThreshold) {
	case 0:
	case 1:
		switch v := policyOrThreshold[0].(type) {
		case Policy:
			policy = v
		case int:
			policy.Threshold = v
		case int64:
			policy.Threshold = int(v)
		}
	default:
		policy = parsePolicyArgs(policyOrThreshold...)
	}
	anchorsCopy := make([]Checkpoint, len(anchors))
	for i := range anchors {
		anchorsCopy[i] = anchors[i].normalized()
	}
	return &Evaluator{Anchors: anchorsCopy, Policy: policy}
}

// Evaluate checks one publication. Additional arguments may be a
// []ProofOffer, a PlanLimits value, or both, in any order.
func (e *Evaluator) Evaluate(publication Publication, edges []EvidenceEdge, args ...any) Result {
	offers, limits := parsePlanArgs(args...)
	return e.evaluate([]Publication{publication}, edges, offers, limits, false)[0]
}

// EvaluateAll checks several publications together. Separate targets at the
// same size with different roots are an observed fork. Additional arguments
// are parsed as in Evaluate.
func (e *Evaluator) EvaluateAll(publications []Publication, edges []EvidenceEdge, args ...any) []Result {
	offers, limits := parsePlanArgs(args...)
	return e.evaluate(publications, edges, offers, limits, true)
}

// EvaluateWithOffers is a typed convenience wrapper.
func (e *Evaluator) EvaluateWithOffers(publication Publication, edges []EvidenceEdge, offers []ProofOffer, limits PlanLimits) Result {
	return e.evaluate([]Publication{publication}, edges, offers, limits, false)[0]
}

func parsePolicyArgs(args ...any) Policy {
	policy := Policy{Threshold: 1}
	for _, arg := range args {
		switch v := arg.(type) {
		case Policy:
			policy = v
		case int:
			policy.Threshold = v
		case SignatureVerifier:
			policy.SignatureVerifier = v
		}
	}
	return policy
}

type verifiedEdge struct {
	from Checkpoint
	to   Checkpoint
}

type evidenceGraph struct {
	nodes map[string]Checkpoint
	edges map[string]verifiedEdge
	adj   map[string][]string
}

func newEvidenceGraph() *evidenceGraph {
	return &evidenceGraph{
		nodes: make(map[string]Checkpoint),
		edges: make(map[string]verifiedEdge),
		adj:   make(map[string][]string),
	}
}

func (g *evidenceGraph) addNode(cp Checkpoint) {
	if !cp.Valid() {
		return
	}
	key := cp.Key()
	if _, ok := g.nodes[key]; !ok {
		g.nodes[key] = cp.normalized()
	}
}

func (g *evidenceGraph) addEdge(from, to Checkpoint) {
	g.addNode(from)
	g.addNode(to)
	fromKey, toKey := from.Key(), to.Key()
	edgeKey := fromKey + ">" + toKey
	if _, ok := g.edges[edgeKey]; ok {
		return
	}
	g.edges[edgeKey] = verifiedEdge{from: from.normalized(), to: to.normalized()}
	g.adj[fromKey] = append(g.adj[fromKey], toKey)
	sort.Strings(g.adj[fromKey])
}

func (g *evidenceGraph) hasEdge(from, to Checkpoint) bool {
	_, ok := g.edges[from.Key()+">"+to.Key()]
	return ok
}

func (g *evidenceGraph) reachable(from, to Checkpoint) bool {
	if from.Equal(to) {
		return true
	}
	if !from.Valid() || !to.Valid() || from.Size > to.Size {
		return false
	}
	start, goal := from.Key(), to.Key()
	if _, ok := g.nodes[start]; !ok {
		return false
	}
	if _, ok := g.nodes[goal]; !ok {
		return false
	}
	seen := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) != 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == goal {
			return true
		}
		for _, next := range g.adj[cur] {
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return false
}

func (e *Evaluator) evaluate(publications []Publication, edges []EvidenceEdge, offers []ProofOffer, limits PlanLimits, all bool) []Result {
	g := newEvidenceGraph()
	for _, anchor := range e.Anchors {
		if anchor.Valid() {
			g.addNode(anchor)
		}
	}

	// Add every structurally well-formed checkpoint to the observed set before
	// deciding which edges are usable. This makes same-size/different-root
	// forks visible even when their proof or signatures are invalid.
	observed := make(map[uint64][]Checkpoint)
	for _, cp := range e.Anchors {
		if cp.Valid() {
			observed[cp.Size] = append(observed[cp.Size], cp.normalized())
		}
	}
	for _, publication := range publications {
		cp := publication.checkpoint()
		if cp.Valid() {
			observed[cp.Size] = append(observed[cp.Size], cp.normalized())
		}
	}

	for _, edge := range edges {
		from, to := edge.From.normalized(), edge.To.normalized()
		if from.Valid() {
			observed[from.Size] = append(observed[from.Size], from)
			g.addNode(from)
		}
		if to.Valid() {
			observed[to.Size] = append(observed[to.Size], to)
			g.addNode(to)
		}
	}

	forks := detectForks(observed)

	for _, edge := range edges {
		e.addVerifiedEdge(g, edge)
	}

	results := make([]Result, len(publications))
	for i, publication := range publications {
		results[i] = e.evaluateOne(g, publication, forks, offers, limits)
	}
	if all {
		globalFork := firstFork(observed)
		if globalFork != nil {
			for i := range results {
				if results[i].Status != StatusFork {
					results[i].Status = StatusFork
					results[i].Fork = globalFork
					results[i].ForkObserved = true
					results[i].Plan = nil
					results[i].Missing = nil
					if results[i].Reason == "" {
						results[i].Reason = fmt.Sprintf("observed fork at size %d", globalFork.Size)
					}
				}
			}
		}
	}
	return results
}

func (e *Evaluator) addVerifiedEdge(g *evidenceGraph, edge EvidenceEdge) {
	from, to := edge.From.normalized(), edge.To.normalized()
	if !from.Valid() || !to.Valid() || from.Size > to.Size {
		return
	}
	if from.Size == to.Size {
		// Same-size edges only carry identity. They cannot establish a new
		// root and a differing root is already an observed fork.
		return
	}
	if g.hasEdge(from, to) {
		return
	}
	if !VerifyConsistencyCheckpoints(from, to, edge.proof()) {
		return
	}
	threshold := edge.Threshold
	if threshold <= 0 {
		threshold = e.Policy.Threshold
	}
	if !MeetsWitnessThreshold(edge.signatures(), to, threshold, e.Policy.SignatureVerifier) {
		return
	}
	g.addEdge(from, to)
}

func (e *Evaluator) evaluateOne(g *evidenceGraph, publication Publication, forks []Fork, offers []ProofOffer, limits PlanLimits) Result {
	cp := publication.checkpoint().normalized()
	result := Result{
		Status: StatusUnknown,
		Module: publication.Module,
		Target: cp,
	}

	fork := forkForCheckpoint(forks, cp)
	if fork != nil {
		result.Status = StatusFork
		result.Fork = fork
		result.ForkObserved = true
		result.Reason = fmt.Sprintf("observed fork at size %d", fork.Size)
		return result
	}
	if !cp.Valid() {
		result.Status = StatusRejected
		result.Reason = "target checkpoint must contain a 32-byte root"
		return result
	}
	if cp.Size == 0 {
		result.Status = StatusRejected
		result.Reason = "an empty tree cannot contain a module leaf"
		return result
	}

	leaf, err := publication.leafData()
	if err != nil {
		result.Status = StatusRejected
		result.Reason = err.Error()
		return result
	}
	proof := publication.inclusionProof()
	if len(proof) == 0 && cp.Size > 1 {
		result.Reason = "missing module inclusion proof"
	} else if !VerifyInclusion(leaf, publication.leafIndex(), cp.Size, proof, cp.Root) {
		result.Status = StatusRejected
		result.Reason = "module inclusion proof does not verify against target root"
		return result
	}

	if !e.targetHasWitnesses(g, publication, cp) {
		result.Reason = "target checkpoint does not meet the witness threshold"
		// A plan may obtain an edge whose signatures establish the target.
		if plan := e.planFor(g, cp, offers, limits, false); plan != nil && len(plan.Offers) > 0 {
			result.Plan = plan
		}
		return result
	}

	if len(proof) == 0 && cp.Size > 1 {
		if plan := e.planFor(g, cp, offers, limits, false); plan != nil && len(plan.Offers) > 0 {
			result.Plan = plan
		}
		return result
	}

	var missing []Checkpoint
	for _, anchor := range e.Anchors {
		if !anchor.Valid() || !g.reachable(anchor, cp) {
			missing = append(missing, anchor.normalized())
		}
	}
	if len(missing) == 0 {
		result.Status = StatusAdmitted
		result.Reason = ""
		return result
	}

	result.Missing = missing
	if len(missing) == 1 {
		result.Reason = "no verified consistency chain from committed checkpoint to target"
	} else {
		result.Reason = "no verified consistency chain from all committed checkpoints to target"
	}
	// The plan is only a request to acquire evidence. Its signatures and
	// consistency proofs have not been obtained yet and are not assumed valid.
	if plan := e.planFor(g, cp, offers, limits, true); plan != nil {
		result.Plan = plan
	}
	return result
}

func (e *Evaluator) targetHasWitnesses(g *evidenceGraph, publication Publication, cp Checkpoint) bool {
	if e.Policy.Threshold <= 0 {
		return true
	}
	// A checkpoint already in the committed set is trusted as-is. Re-admitting
	// a module at that same checkpoint does not require a fresh attestation.
	for _, anchor := range e.Anchors {
		if anchor.Equal(cp) {
			return true
		}
	}
	if MeetsWitnessThreshold(publication.Signatures, cp, e.Policy.Threshold, e.Policy.SignatureVerifier) {
		return true
	}
	if e.Policy.RequireTargetSignature {
		return false
	}
	// A valid incoming edge carries a threshold attestation for its target
	// checkpoint, so it can authenticate the target as well.
	for _, edge := range g.edges {
		if edge.to.Equal(cp) && edge.from.Size < edge.to.Size {
			return true
		}
	}
	return false
}

func detectForks(observed map[uint64][]Checkpoint) []Fork {
	var forks []Fork
	for size, cps := range observed {
		rootSet := make(map[string][]byte)
		for _, cp := range cps {
			if !cp.Valid() {
				continue
			}
			rootSet[string(cp.Root)] = cp.Root
		}
		if len(rootSet) < 2 {
			continue
		}
		fork := Fork{Size: size}
		keys := make([]string, 0, len(rootSet))
		for key := range rootSet {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fork.Roots = append(fork.Roots, cloneBytes(rootSet[key]))
			fork.Checkpoints = append(fork.Checkpoints, NewCheckpoint(size, rootSet[key]))
		}
		forks = append(forks, fork)
	}
	sort.Slice(forks, func(i, j int) bool { return forks[i].Size < forks[j].Size })
	return forks
}

func firstFork(observed map[uint64][]Checkpoint) *Fork {
	forks := detectForks(observed)
	if len(forks) == 0 {
		return nil
	}
	f := forks[0]
	return &f
}

func forkForCheckpoint(forks []Fork, cp Checkpoint) *Fork {
	for i := range forks {
		if forks[i].Size != cp.Size {
			continue
		}
		hasRoot := false
		for _, root := range forks[i].Roots {
			if bytes.Equal(root, cp.Root) {
				hasRoot = true
				break
			}
		}
		if hasRoot {
			return &forks[i]
		}
	}
	return nil
}

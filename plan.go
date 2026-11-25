package admission

import (
	"sort"
	"strconv"
)

// EdgeRef identifies one consistency edge that an offer can cover.
type EdgeRef struct {
	From Checkpoint `json:"from"`
	To   Checkpoint `json:"to"`
}

// Key is the canonical edge identity.
func (r EdgeRef) Key() string { return r.From.Key() + ">" + r.To.Key() }

// ProofOffer is one host-supplied acquisition option. It covers one or more
// consistency edges at a fixed cost from a provider. An offer is only a
// request candidate; none of its not-yet-obtained proofs or signatures are
// considered valid during admission.
type ProofOffer struct {
	ID       string    `json:"id"`
	Provider string    `json:"provider"`
	Cost     int64     `json:"cost"`
	Price    int64     `json:"price,omitempty"`
	Covers   []EdgeRef `json:"covers"`

	// Edges is accepted as an alias for Covers by some adapters.
	Edges    []EdgeRef `json:"edges,omitempty"`
	EdgeRefs []EdgeRef `json:"edge_refs,omitempty"`
}

// Offer and SupplementalOffer are aliases for ProofOffer.
type Offer = ProofOffer
type SupplementalOffer = ProofOffer
type Candidate = ProofOffer
type OfferRequest = ProofOffer

func (o ProofOffer) edges() []EdgeRef {
	edges := make([]EdgeRef, 0, len(o.Covers)+len(o.Edges)+len(o.EdgeRefs))
	edges = append(edges, o.Covers...)
	edges = append(edges, o.Edges...)
	edges = append(edges, o.EdgeRefs...)
	return edges
}

func (o ProofOffer) cost() int64 {
	if o.Cost != 0 || o.Price == 0 {
		return o.Cost
	}
	return o.Price
}

// PlanLimits caps the number of acquisition requests per provider in one plan.
type PlanLimits struct {
	MaxAcquisitionsPerProvider map[string]int `json:"max_acquisitions_per_provider,omitempty"`
	// ProviderCaps is an accepted alias for MaxAcquisitionsPerProvider.
	ProviderCaps map[string]int `json:"provider_caps,omitempty"`
	// MaxPerProvider is another accepted alias.
	MaxPerProvider map[string]int `json:"max_per_provider,omitempty"`
}

func (l PlanLimits) limit(provider string) (int, bool) {
	if l.MaxAcquisitionsPerProvider != nil {
		if v, ok := l.MaxAcquisitionsPerProvider[provider]; ok {
			return v, true
		}
	}
	if l.ProviderCaps != nil {
		if v, ok := l.ProviderCaps[provider]; ok {
			return v, true
		}
	}
	if l.MaxPerProvider != nil {
		if v, ok := l.MaxPerProvider[provider]; ok {
			return v, true
		}
	}
	return 0, false
}

// Plan is a minimum-cost set of acquisition requests. It is conditional: the
// caller must still obtain and verify every edge before they can be used for
// an admission decision.
type Plan struct {
	Offers         []ProofOffer   `json:"offers"`
	OfferIDs       []string       `json:"offer_ids"`
	Cost           int64          `json:"cost"`
	ProviderCounts map[string]int `json:"provider_counts"`
	CoveredEdges   []EdgeRef      `json:"covered_edges"`
}

func parsePlanArgs(args ...any) ([]ProofOffer, PlanLimits) {
	var offers []ProofOffer
	var limits PlanLimits
	for _, arg := range args {
		switch v := arg.(type) {
		case []ProofOffer:
			offers = append(offers, v...)
		case ProofOffer:
			offers = append(offers, v)
		case PlanLimits:
			limits = v
		case *PlanLimits:
			if v != nil {
				limits = *v
			}
		case map[string]int:
			limits = PlanLimits{MaxAcquisitionsPerProvider: v}
		}
	}
	return offers, limits
}

// SupplementPlan computes a minimum-cost plan that would connect every
// committed checkpoint to target if all selected offers were successfully
// acquired. The second return value is false when no offered set can make the
// target reachable or when the target is already reachable.
func (e *Evaluator) SupplementPlan(target Checkpoint, edges []EvidenceEdge, offers []ProofOffer, limits PlanLimits) (*Plan, bool) {
	g := newEvidenceGraph()
	for _, anchor := range e.Anchors {
		if anchor.Valid() {
			g.addNode(anchor)
		}
	}
	for _, edge := range edges {
		e.addVerifiedEdge(g, edge)
	}
	if connectsAll(g, e.Anchors, target) {
		return nil, false
	}
	return minimumPlan(g, e.Anchors, target, offers, limits)
}

// MinimumSupplementPlan is a package-level convenience wrapper.
func MinimumSupplementPlan(anchors []Checkpoint, target Checkpoint, edges []EvidenceEdge, offers []ProofOffer, limits PlanLimits, policy ...any) (*Plan, bool) {
	e := NewEvaluator(anchors, policy...)
	return e.SupplementPlan(target, edges, offers, limits)
}

func (e *Evaluator) planFor(base *evidenceGraph, target Checkpoint, offers []ProofOffer, limits PlanLimits, requireAll bool) *Plan {
	if len(offers) == 0 {
		return nil
	}
	plan, ok := minimumPlan(base, e.Anchors, target, offers, limits)
	if !ok {
		return nil
	}
	if requireAll && len(plan.Offers) == 0 {
		return nil
	}
	return plan
}

type normalizedOffer struct {
	offer    ProofOffer
	provider string
	cost     int64
	edges    []EdgeRef
}

func minimumPlan(base *evidenceGraph, anchors []Checkpoint, target Checkpoint, offers []ProofOffer, limits PlanLimits) (*Plan, bool) {
	if !target.Valid() {
		return nil, false
	}
	normalized := normalizeOffers(offers)
	if len(normalized) == 0 {
		return nil, false
	}

	// First make sure the union of all offers is capable of connecting every
	// anchor. This quickly rules out impossible targets and gives the search a
	// useful pruning upper bound.
	allGraph := cloneGraph(base)
	for _, offer := range normalized {
		for _, ref := range offer.edges {
			allGraph.addEdge(ref.From, ref.To)
		}
	}
	for _, anchor := range anchors {
		if !allGraph.reachable(anchor, target) {
			return nil, false
		}
	}

	var best *Plan
	var selected []normalizedOffer
	providerCounts := make(map[string]int)

	var search func(index int, cost int64)
	search = func(index int, cost int64) {
		if best != nil && cost >= best.Cost {
			// Equal-cost plans are ordered by offer IDs, so a later branch
			// cannot improve on an already deterministic equal-cost result.
			if cost > best.Cost {
				return
			}
		}
		if index == len(normalized) {
			candidateGraph := cloneGraph(base)
			for _, offer := range selected {
				for _, ref := range offer.edges {
					candidateGraph.addEdge(ref.From, ref.To)
				}
			}
			if !connectsAll(candidateGraph, anchors, target) {
				return
			}
			candidate := makePlan(selected)
			if best == nil || betterPlan(candidate, best) {
				best = candidate
			}
			return
		}

		// Prune if neither taking nor skipping the remaining offers can
		// connect all anchors.
		pruneGraph := cloneGraph(base)
		for _, offer := range selected {
			for _, ref := range offer.edges {
				pruneGraph.addEdge(ref.From, ref.To)
			}
		}
		for i := index; i < len(normalized); i++ {
			for _, ref := range normalized[i].edges {
				pruneGraph.addEdge(ref.From, ref.To)
			}
		}
		if !connectsAll(pruneGraph, anchors, target) {
			return
		}

		offer := normalized[index]
		max, capped := limits.limit(offer.provider)
		if !capped || providerCounts[offer.provider] < max {
			providerCounts[offer.provider]++
			selected = append(selected, offer)
			search(index+1, cost+offer.cost)
			selected = selected[:len(selected)-1]
			providerCounts[offer.provider]--
			if providerCounts[offer.provider] == 0 {
				delete(providerCounts, offer.provider)
			}
		}
		search(index+1, cost)
	}
	search(0, 0)
	if best == nil {
		return nil, false
	}
	return best, true
}

func normalizeOffers(offers []ProofOffer) []normalizedOffer {
	out := make([]normalizedOffer, 0, len(offers))
	seenIDs := make(map[string]bool)
	for i, offer := range offers {
		if offer.cost() < 0 {
			continue
		}
		if offer.ID == "" {
			offer.ID = "offer-" + strconv.Itoa(i)
		}
		if seenIDs[offer.ID] {
			continue
		}
		var edges []EdgeRef
		seenEdges := make(map[string]bool)
		for _, ref := range offer.edges() {
			from, to := ref.From.normalized(), ref.To.normalized()
			if !from.Valid() || !to.Valid() || from.Size >= to.Size {
				continue
			}
			key := from.Key() + ">" + to.Key()
			if seenEdges[key] {
				continue
			}
			seenEdges[key] = true
			edges = append(edges, EdgeRef{From: from, To: to})
		}
		if len(edges) == 0 {
			continue
		}
		seenIDs[offer.ID] = true
		out = append(out, normalizedOffer{
			offer:    offer,
			provider: offer.Provider,
			cost:     offer.cost(),
			edges:    edges,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].cost != out[j].cost {
			return out[i].cost < out[j].cost
		}
		return out[i].offer.ID < out[j].offer.ID
	})
	return out
}

func cloneGraph(base *evidenceGraph) *evidenceGraph {
	g := newEvidenceGraph()
	for key, node := range base.nodes {
		g.nodes[key] = node.normalized()
	}
	for key, edge := range base.edges {
		g.edges[key] = edge
	}
	for key, next := range base.adj {
		g.adj[key] = append([]string(nil), next...)
	}
	return g
}

func connectsAll(g *evidenceGraph, anchors []Checkpoint, target Checkpoint) bool {
	for _, anchor := range anchors {
		if !anchor.Valid() || !g.reachable(anchor, target) {
			return false
		}
	}
	return true
}

func makePlan(selected []normalizedOffer) *Plan {
	plan := &Plan{
		ProviderCounts: make(map[string]int),
	}
	edgeSet := make(map[string]EdgeRef)
	for _, offer := range selected {
		plan.Offers = append(plan.Offers, offer.offer)
		plan.OfferIDs = append(plan.OfferIDs, offer.offer.ID)
		plan.Cost += offer.cost
		plan.ProviderCounts[offer.provider]++
		for _, ref := range offer.edges {
			edgeSet[ref.Key()] = ref
		}
	}
	sort.Strings(plan.OfferIDs)
	plan.Offers = append([]ProofOffer(nil), plan.Offers...)
	sort.Slice(plan.Offers, func(i, j int) bool { return plan.Offers[i].ID < plan.Offers[j].ID })
	keys := make([]string, 0, len(edgeSet))
	for key := range edgeSet {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		plan.CoveredEdges = append(plan.CoveredEdges, edgeSet[key])
	}
	return plan
}

func betterPlan(candidate, best *Plan) bool {
	if candidate.Cost < best.Cost {
		return true
	}
	if candidate.Cost > best.Cost {
		return false
	}
	if len(candidate.OfferIDs) != len(best.OfferIDs) {
		return len(candidate.OfferIDs) < len(best.OfferIDs)
	}
	for i := 0; i < len(candidate.OfferIDs) && i < len(best.OfferIDs); i++ {
		if candidate.OfferIDs[i] < best.OfferIDs[i] {
			return true
		}
		if candidate.OfferIDs[i] > best.OfferIDs[i] {
			return false
		}
	}
	return len(candidate.OfferIDs) < len(best.OfferIDs)
}

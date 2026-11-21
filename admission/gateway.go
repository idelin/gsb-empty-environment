package admission

import "fmt"

// Gateway accumulates released tables. A new table is admitted only when one
// and the same feasible-pairing semantics still holds for the union of every
// already-published table: the merged problem must remain feasible and keep
// every protected batch above the threshold. A rejected request never mutates
// the committed state, so prior commitments are preserved.
type Gateway struct {
	opts Options

	batches      []Batch
	edges        []Edge
	groups       []DecisionGroup
	quota        Quota
	protected    []string
	threshold    int
	thresholdSet bool

	committed bool
	last      Report
}

// NewGateway creates a gateway with optional solver options.
func NewGateway(opts ...Options) *Gateway {
	o := DefaultOptions()
	if len(opts) > 0 {
		o = opts[0]
	}
	return &Gateway{opts: o}
}

// Publish evaluates a new table against the union of all committed tables.
// The returned report describes the merged state. On rejection (infeasible or
// below threshold) or malformed input the gateway keeps its prior state.
func (g *Gateway) Publish(req PublishRequest) (Report, error) {
	merged, err := g.mergedRequest(req)
	if err != nil {
		return Report{}, err
	}

	report, err := Admit(merged, g.opts)
	if err != nil {
		return Report{}, err
	}
	g.last = report
	if report.Decision != DecisionPublishable {
		return report, nil
	}

	g.committed = true
	g.batches = merged.Batches
	g.edges = merged.Edges
	g.groups = merged.Groups
	g.quota = merged.Quota
	g.protected = merged.ProtectedBatches
	if req.PrivacyThreshold > 0 {
		g.threshold = req.PrivacyThreshold
		g.thresholdSet = true
	}
	return report, nil
}

// Committed reports whether at least one table has been published.
func (g *Gateway) Committed() bool {
	return g.committed
}

// LastReport returns the most recent evaluation report.
func (g *Gateway) LastReport() Report {
	return g.last
}

// Snapshot returns a deep-copied PublishRequest describing every committed
// table. It contains only synthetic categories, quotas and anonymous IDs.
func (g *Gateway) Snapshot() PublishRequest {
	batches := append([]Batch(nil), g.batches...)
	edges := append([]Edge(nil), g.edges...)
	groups := make([]DecisionGroup, len(g.groups))
	for i, grp := range g.groups {
		groups[i] = DecisionGroup{
			ID:          grp.ID,
			MemberEdges: append([]string(nil), grp.MemberEdges...),
		}
	}
	quota := Quota{Total: g.quota.Total, MaxPerCategory: map[string]int{}}
	for k, v := range g.quota.MaxPerCategory {
		quota.MaxPerCategory[k] = v
	}
	protected := append([]string(nil), g.protected...)
	return PublishRequest{
		Batches:          batches,
		Edges:            edges,
		Groups:           groups,
		Quota:            quota,
		ProtectedBatches: protected,
		PrivacyThreshold: g.threshold,
	}
}

func (g *Gateway) mergedRequest(req PublishRequest) (PublishRequest, error) {
	merged := g.Snapshot()

	knownBatch := make(map[string]Batch, len(merged.Batches))
	for _, b := range merged.Batches {
		knownBatch[b.ID] = b
	}
	for _, b := range req.Batches {
		if prior, ok := knownBatch[b.ID]; ok {
			if prior.Side != b.Side || prior.Category != b.Category {
				return PublishRequest{}, fmt.Errorf("admission: batch %q conflicts with an already published batch", b.ID)
			}
			continue
		}
		knownBatch[b.ID] = b
		merged.Batches = append(merged.Batches, b)
	}

	knownEdge := make(map[string]bool, len(merged.Edges))
	for _, e := range merged.Edges {
		knownEdge[edgeKey(e)] = true
	}
	for _, e := range req.Edges {
		key := edgeKey(e)
		if knownEdge[key] {
			return PublishRequest{}, fmt.Errorf("admission: edge %q is already published", key)
		}
		knownEdge[key] = true
		merged.Edges = append(merged.Edges, e)
	}

	knownGroup := make(map[string]bool, len(merged.Groups))
	for _, grp := range merged.Groups {
		knownGroup[grp.ID] = true
	}
	for _, grp := range req.Groups {
		if knownGroup[grp.ID] {
			return PublishRequest{}, fmt.Errorf("admission: decision group %q is already published", grp.ID)
		}
		knownGroup[grp.ID] = true
		merged.Groups = append(merged.Groups, DecisionGroup{
			ID:          grp.ID,
			MemberEdges: append([]string(nil), grp.MemberEdges...),
		})
	}

	if merged.Quota.MaxPerCategory == nil {
		merged.Quota.MaxPerCategory = map[string]int{}
	}
	for cat, limit := range req.Quota.MaxPerCategory {
		if prior, ok := merged.Quota.MaxPerCategory[cat]; !ok || limit < prior {
			merged.Quota.MaxPerCategory[cat] = limit
		}
	}
	if req.Quota.Total > 0 && (merged.Quota.Total == 0 || req.Quota.Total < merged.Quota.Total) {
		merged.Quota.Total = req.Quota.Total
	}

	protectedSeen := make(map[string]bool, len(merged.ProtectedBatches))
	for _, id := range merged.ProtectedBatches {
		protectedSeen[id] = true
	}
	for _, id := range req.ProtectedBatches {
		if !protectedSeen[id] {
			protectedSeen[id] = true
			merged.ProtectedBatches = append(merged.ProtectedBatches, id)
		}
	}

	if req.PrivacyThreshold > 0 {
		merged.PrivacyThreshold = req.PrivacyThreshold
	}
	// A request carries its own decomposition; the merged problem is always
	// re-validated from scratch against it.
	merged.Decomposition = req.Decomposition
	return merged, nil
}

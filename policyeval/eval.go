package policyeval

import (
	"errors"
	"fmt"
	"sort"
)

// state is one reverse-evaluation frontier entry: a concrete policy OID at
// one layer plus the path prefix that derived it from the leaf usage.
type state struct {
	layer  int
	policy OID
	prefix []PathStep
}

// validate checks the structural well-formedness of the inputs. Structural
// problems are caller errors and are returned as Go errors; policy failures
// (empty admissible sets) are normal evaluation outcomes and are reported
// through rejected results instead.
func validate(chain *Chain, leafUsage []OID) error {
	if chain == nil {
		return errors.New("policyeval: candidate chain is nil")
	}
	if len(chain.Layers) == 0 {
		return fmt.Errorf("policyeval: chain %q has no layers", chain.ID)
	}
	if len(leafUsage) == 0 {
		return fmt.Errorf("policyeval: chain %q received an empty leaf usage set", chain.ID)
	}

	for li, layer := range chain.Layers {
		if len(layer.AllowedPolicies) == 0 {
			return fmt.Errorf("policyeval: chain %q layer %d (%q) declares no allowed policies",
				chain.ID, li, layer.Name)
		}
		for _, policy := range layer.AllowedPolicies {
			if policy == "" {
				return fmt.Errorf("policyeval: chain %q layer %d (%q) contains an empty policy OID",
					chain.ID, li, layer.Name)
			}
		}
		for _, mapping := range layer.Mappings {
			if mapping.IssuerDomain == "" || mapping.SubjectDomain == "" {
				return fmt.Errorf("policyeval: chain %q layer %d (%q) has a mapping with an empty OID",
					chain.ID, li, layer.Name)
			}
		}
	}

	root := chain.Layers[0]
	if len(root.Mappings) != 0 {
		return fmt.Errorf("policyeval: chain %q trust-anchor layer %q must not declare inbound mappings",
			chain.ID, root.Name)
	}

	for _, usage := range leafUsage {
		if usage == "" {
			return fmt.Errorf("policyeval: chain %q leaf usage contains an empty OID", chain.ID)
		}
	}
	return nil
}

// uniqueOIDs returns the sorted, de-duplicated elements of in.
func uniqueOIDs(in []OID) []OID {
	if len(in) == 0 {
		return nil
	}
	set := make(map[OID]struct{}, len(in))
	for _, policy := range in {
		set[policy] = struct{}{}
	}
	out := make([]OID, 0, len(set))
	for policy := range set {
		out = append(out, policy)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// allowedAt reports whether policy is admissible at layer li. An explicitly
// listed anyPolicy makes every concrete policy admissible.
func allowedAt(layer Layer, policy OID) bool {
	for _, candidate := range layer.AllowedPolicies {
		if candidate == policy || candidate == AnyPolicy {
			return true
		}
	}
	return false
}

// predecessor describes one issuer-side policy that can derive the policy
// held on the subject side across a boundary, together with the mapping that
// justifies the step (nil for the identity relation).
type predecessor struct {
	issuer OID
	mapped *Mapping
}

// predecessors computes every admissible issuer-side policy at layer li that
// can derive the subject-side policy held at layer li+1 across their shared
// boundary.
//
// The boundary is governed by the subject-side layer li+1, which carries the
// policyMappings extension and the inhibit-mapping marker:
//
//   - Identity: the held OID crosses unchanged when both layers admit it
//     (an explicit OID or anyPolicy on each side).
//   - Mapping reverse lookup: every entry IssuerDomain -> SubjectDomain whose
//     SubjectDomain equals the held OID contributes IssuerDomain, provided
//     the issuer layer admits IssuerDomain. Multi-hop translation emerges by
//     applying each boundary in turn as the walk moves up.
//
// Mappings are ignored entirely when the subject layer inhibits mapping.
// Mappings only ever pair these two adjacent layers, so they cannot cross a
// non-declared boundary or escape the finite policy universe.
func predecessors(chain *Chain, li int, subjectPolicy OID) []predecessor {
	upper := chain.Layers[li]
	lower := chain.Layers[li+1]

	var out []predecessor
	if allowedAt(upper, subjectPolicy) && allowedAt(lower, subjectPolicy) {
		out = append(out, predecessor{issuer: subjectPolicy})
	}
	if lower.InhibitMapping {
		return dedupPredecessors(out)
	}
	for mi := range lower.Mappings {
		mapping := &lower.Mappings[mi]
		if mapping.SubjectDomain != subjectPolicy {
			continue
		}
		if !allowedAt(upper, mapping.IssuerDomain) {
			continue
		}
		out = append(out, predecessor{issuer: mapping.IssuerDomain, mapped: mapping})
	}
	return dedupPredecessors(out)
}

// dedupPredecessors de-duplicates on the issuer OID, preferring the identity
// entry (nil mapping) on ties, and returns a deterministically ordered slice.
func dedupPredecessors(out []predecessor) []predecessor {
	seen := map[OID]int{}
	deduped := make([]predecessor, 0, len(out))
	for _, pred := range out {
		if pos, ok := seen[pred.issuer]; ok {
			if deduped[pos].mapped == nil {
				continue
			}
			if pred.mapped == nil {
				deduped[pos] = pred
			}
			continue
		}
		seen[pred.issuer] = len(deduped)
		deduped = append(deduped, pred)
	}
	sort.SliceStable(deduped, func(i, j int) bool {
		if deduped[i].mapped == nil || deduped[j].mapped == nil {
			return deduped[i].mapped == nil
		}
		return deduped[i].issuer < deduped[j].issuer
	})
	return deduped
}

// EvaluateChain performs the reverse policy derivation for one candidate
// chain. Starting from the leaf's required usage policies it enumerates every
// admissible derivation across the declared adjacent-layer boundaries until
// the trust anchor is reached.
//
// The result always lists the complete set of mapping paths. An empty
// acceptable root-policy set yields DecisionRejected; every layer whose
// admissible policy set empties during derivation likewise rejects.
func EvaluateChain(chain *Chain, leafUsage []OID) (*Result, error) {
	if err := validate(chain, leafUsage); err != nil {
		return nil, err
	}

	result := &Result{ChainID: chain.ID}
	leafIndex := len(chain.Layers) - 1

	frontier := make([]state, 0, len(uniqueOIDs(leafUsage)))
	for _, usage := range uniqueOIDs(leafUsage) {
		if !allowedAt(chain.Layers[leafIndex], usage) {
			continue
		}
		frontier = append(frontier, state{
			layer:  leafIndex,
			policy: usage,
			prefix: []PathStep{{
				Layer:     leafIndex,
				LayerName: chain.Layers[leafIndex].Name,
				Policy:    usage,
			}},
		})
	}

	if len(frontier) == 0 {
		result.Decision = DecisionRejected
		result.Reason = "no requested leaf usage policy is allowed at the leaf layer"
		return result, nil
	}

	// key guards against the same (layer, policy) being expanded twice along
	// different prefixes; each distinct prefix is still retained, so mapping
	// rings that merge back onto an already-seen OID cannot loop forever.
	keyOf := func(layer int, policy OID, prefix []PathStep) string {
		key := fmt.Sprintf("%d|%s|", layer, policy)
		for _, step := range prefix {
			if step.AppliedMapping != nil {
				key += string(step.AppliedMapping.IssuerDomain) + ">" +
					string(step.AppliedMapping.SubjectDomain) + ";"
			} else {
				key += "id;"
			}
		}
		return key
	}
	visited := map[string]struct{}{}

	var completed []Path
	rootSet := map[OID]struct{}{}

	for len(frontier) > 0 {
		current := frontier[0]
		frontier = frontier[1:]

		key := keyOf(current.layer, current.policy, current.prefix)
		if _, ok := visited[key]; ok {
			continue
		}
		visited[key] = struct{}{}

		if current.layer == 0 {
			if !allowedAt(chain.Layers[0], current.policy) {
				continue
			}
			steps := reverseSteps(current.prefix)
			completed = append(completed, Path{
				Steps:      steps,
				RootPolicy: current.policy,
				LeafPolicy: current.prefix[0].Policy,
			})
			rootSet[current.policy] = struct{}{}
			continue
		}

		lower := chain.Layers[current.layer]
		for _, pred := range predecessors(chain, current.layer-1, current.policy) {
			// The mapping is declared by the subject-side (current) layer,
			// so it annotates that layer's existing path step rather than
			// the newly created issuer-side step.
			prefix := make([]PathStep, 0, len(current.prefix)+1)
			prefix = append(prefix, current.prefix...)
			prefix[len(prefix)-1].Inhibited = lower.InhibitMapping
			if pred.mapped != nil {
				prefix[len(prefix)-1].AppliedMapping = pred.mapped
			}
			step := PathStep{
				Layer:     current.layer - 1,
				LayerName: chain.Layers[current.layer-1].Name,
				Policy:    pred.issuer,
			}
			prefix = append(prefix, step)
			frontier = append(frontier, state{
				layer:  current.layer - 1,
				policy: pred.issuer,
				prefix: prefix,
			})
		}
	}

	if len(completed) == 0 {
		result.Decision = DecisionRejected
		result.Reason = "no trust-anchor policy can derive any requested leaf usage under the declared mappings"
		return result, nil
	}

	for root := range rootSet {
		result.RootPolicies = append(result.RootPolicies, root)
	}
	sort.Slice(result.RootPolicies, func(i, j int) bool {
		return result.RootPolicies[i] < result.RootPolicies[j]
	})

	sort.Slice(completed, func(i, j int) bool {
		if completed[i].RootPolicy != completed[j].RootPolicy {
			return completed[i].RootPolicy < completed[j].RootPolicy
		}
		return completed[i].LeafPolicy < completed[j].LeafPolicy
	})
	result.Paths = completed
	result.Decision = DecisionAccepted
	return result, nil
}

// reverseSteps flips a leaf-first prefix into the root-first ordering promised
// by Path.
func reverseSteps(prefix []PathStep) []PathStep {
	steps := make([]PathStep, len(prefix))
	for i, step := range prefix {
		steps[len(prefix)-1-i] = step
	}
	return steps
}

// EvaluateChains evaluates every candidate chain independently and returns
// their results keyed by chain ID. Candidate chains never interact: a policy
// that is undeclared in one chain cannot be borrowed from another, which is
// enforced structurally because each chain only receives its own layers.
func EvaluateChains(chains []*Chain, leafUsage []OID) (map[string]*Result, error) {
	out := make(map[string]*Result, len(chains))
	for _, chain := range chains {
		result, err := EvaluateChain(chain, leafUsage)
		if err != nil {
			return nil, err
		}
		out[chain.ID] = result
	}
	return out, nil
}

// Authorize reports whether at least one candidate chain accepts the leaf
// usage, alongside the accepting results. It returns the accepted results so
// callers can inspect the justifying mapping paths.
func Authorize(chains []*Chain, leafUsage []OID) (bool, []*Result, error) {
	results, err := EvaluateChains(chains, leafUsage)
	if err != nil {
		return false, nil, err
	}
	var accepted []*Result
	for _, result := range results {
		if result.Decision == DecisionAccepted {
			accepted = append(accepted, result)
		}
	}
	sort.Slice(accepted, func(i, j int) bool {
		return accepted[i].ChainID < accepted[j].ChainID
	})
	return len(accepted) > 0, accepted, nil
}

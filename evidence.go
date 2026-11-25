package closure

import "sort"

// Choice is one replayable dependency decision.
type Choice struct {
	Owner           Identity  `json:"owner"`
	DependencyIndex int       `json:"dependency_index"`
	DependencyName  string    `json:"dependency_name,omitempty"`
	Active          bool      `json:"active"`
	Condition       Condition `json:"condition,omitempty"`
	Selected        Identity  `json:"selected,omitempty"`
}

// ChainLink is one edge in a root-to-unproven-package reference chain.
type ChainLink struct {
	From            Identity  `json:"from"`
	DependencyIndex int       `json:"dependency_index"`
	DependencyName  string    `json:"dependency_name,omitempty"`
	Condition       Condition `json:"condition,omitempty"`
	To              Identity  `json:"to"`
}

// Evidence reproduces one legal resolution in which Unproven has no applicable
// attestation under Config. VerifyEvidence recomputes all checks from Request.
type Evidence struct {
	// Config is the concrete flag assignment reproduced by the witness.
	Config Assignment `json:"config"`

	// Root is the root identity used by this resolution.
	Root Identity `json:"root"`

	// Unproven is the exact identity lacking applicable provenance.
	Unproven Identity `json:"unproven"`

	// ReasonCode is no_attestation, generation_mismatch, or condition_false.
	ReasonCode string `json:"reason_code"`
	Reason     string `json:"reason,omitempty"`

	PolicyGeneration uint64 `json:"policy_generation"`

	// Selection maps package name to its one globally chosen exact identity.
	Selection map[string]Identity `json:"selection"`

	// Choices contains active/inactive decisions for every dependency of every
	// selected package.
	Choices []Choice `json:"choices"`

	// IdentityChain is root ... unproven.
	IdentityChain []Identity `json:"identity_chain"`

	// Chain contains edge details for IdentityChain.
	Chain []ChainLink `json:"chain"`

	// Support is the shared-graph region in which this exact witness is bad.
	Support NodeID `json:"support_node"`
}

func (a *analysis) makeEvidence(state *resolveState, unproven Identity, support NodeID, config Assignment) Evidence {
	selection := make(map[string]Identity, len(state.bound))
	for name, id := range state.bound {
		selection[name] = id
	}

	choices := make([]Choice, 0)
	for _, owner := range state.order {
		pkg := a.pkgs[owner]
		if pkg == nil {
			continue
		}
		for index, dep := range pkg.canonicalDeps() {
			ref := depRef{owner: owner, index: index}
			condition := mergedCondition(dep.conditions())
			choice := Choice{
				Owner:           owner,
				DependencyIndex: index,
				DependencyName:  dependencyName(dep, index),
				Condition:       condition,
			}
			if selected, ok := state.chosen[ref]; ok {
				choice.Active = true
				choice.Selected = selected
			} else {
				choice.Active = false
			}
			choices = append(choices, choice)
		}
	}
	sort.Slice(choices, func(i, j int) bool {
		if choices[i].Owner.String() != choices[j].Owner.String() {
			return choices[i].Owner.String() < choices[j].Owner.String()
		}
		return choices[i].DependencyIndex < choices[j].DependencyIndex
	})

	identityChain, chain := a.buildChain(state, unproven)
	code, reason := proofFailureReason(a.req, unproven, config)
	return Evidence{
		Config:           config,
		Root:             state.root,
		Unproven:         unproven,
		ReasonCode:       code,
		Reason:           reason,
		PolicyGeneration: a.req.PolicyGeneration,
		Selection:        selection,
		Choices:          choices,
		IdentityChain:    identityChain,
		Chain:            chain,
		Support:          support,
	}
}

func dependencyName(dep Dependency, index int) string {
	if dep.Name != "" {
		return dep.Name
	}
	if alts := dep.canonicalAlternatives(); len(alts) > 0 {
		return alts[0].Name
	}
	return "dependency-" + itoa(index)
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var buf [20]byte
	pos := len(buf)
	for value > 0 {
		pos--
		buf[pos] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

func mergedCondition(conditions []Condition) Condition {
	if len(conditions) == 0 {
		return nil
	}
	out := Condition{}
	for _, cond := range conditions {
		for flag, value := range cond {
			out[flag] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (a *analysis) buildChain(state *resolveState, target Identity) ([]Identity, []ChainLink) {
	type parentInfo struct {
		parent Identity
		ref    depRef
	}
	parents := map[Identity]parentInfo{}
	visited := map[Identity]bool{state.root: true}
	queue := []Identity{state.root}
	for len(queue) > 0 {
		owner := queue[0]
		queue = queue[1:]
		pkg := a.pkgs[owner]
		if pkg == nil {
			continue
		}
		for index := range pkg.canonicalDeps() {
			ref := depRef{owner: owner, index: index}
			next, ok := state.chosen[ref]
			if !ok {
				continue
			}
			if !visited[next] {
				visited[next] = true
				parents[next] = parentInfo{parent: owner, ref: ref}
				queue = append(queue, next)
			}
		}
	}

	if !visited[target] {
		return nil, nil
	}
	reversed := []Identity{target}
	linksReversed := []ChainLink{}
	current := target
	for current != state.root {
		info, ok := parents[current]
		if !ok {
			break
		}
		pkg := a.pkgs[info.parent]
		dep := pkg.canonicalDeps()[info.ref.index]
		link := ChainLink{
			From:            info.parent,
			DependencyIndex: info.ref.index,
			DependencyName:  dependencyName(dep, info.ref.index),
			Condition:       mergedCondition(dep.conditions()),
			To:              current,
		}
		linksReversed = append(linksReversed, link)
		reversed = append(reversed, info.parent)
		current = info.parent
	}

	identityChain := make([]Identity, len(reversed))
	for i, id := range reversed {
		identityChain[len(reversed)-1-i] = id
	}
	chain := make([]ChainLink, len(linksReversed))
	for i, link := range linksReversed {
		chain[len(linksReversed)-1-i] = link
	}
	return identityChain, chain
}

func proofFailureReason(req Request, id Identity, config map[string]bool) (string, string) {
	var exactGeneration, otherGeneration int
	for _, att := range req.Attestations {
		attID := att.canonicalID()
		if attID != id {
			continue
		}
		if att.generation() == req.PolicyGeneration {
			exactGeneration++
			if conditionSatisfied(mergedCondition(att.conditions()), config) {
				// This should not happen when symbolic selection chose unproven.
				return "proven", "an applicable attestation exists"
			}
		} else {
			otherGeneration++
		}
	}
	switch {
	case exactGeneration > 0:
		return "condition_false", "an exact identity/generation attestation exists, but its flag condition is false"
	case otherGeneration > 0:
		return "generation_mismatch", "an exact identity attestation exists, but not for policy generation " + itoa(int(req.PolicyGeneration))
	default:
		return "no_attestation", "no attestation binds this exact identity at the required policy generation"
	}
}

package fedpolicy

import "sort"

// productConfig is one state of the transducer x old-policy x labeler
// product. The policy verdict and labels depend only on the bounded tail
// (maxDepth segments) and on the labeler's finite key, so this configuration
// takes finitely many values even though paths are unbounded.
type productConfig struct {
	q          int
	matched    int
	excess     string
	tail       string
	tailLen    int
	labelState any
}

type productNode struct {
	config  productConfig
	parent  *productNode
	arc     Arc
	oldPath []string
}

// analyzeCandidate adjudicates a single (prefix, label) combination.
func analyzeCandidate(c Candidate, mapper *Mapper, policy OldPolicy, labeler FinitaryLabeler, public labelSet) *Decision {
	decision := &Decision{Candidate: c}

	for _, l := range c.Require {
		if !public.contains(l) {
			decision.Safe = false
			decision.Reason = RejectNonPublicLabel
			return decision
		}
	}

	channels := mapper.ChannelsForNew(c.Prefix.Header())
	if len(channels) == 0 {
		decision.Safe = false
		decision.Reason = RejectNoPreimage
		decision.Witnesses = append(decision.Witnesses,
			syntheticGapWitness(c))
		return decision
	}

	required := newLabelSet(c.Require)
	maxDepth := policy.MaxPrefixLen()
	if look := labeler.MaxLookahead(); look > maxDepth {
		maxDepth = look
	}

	denySeen := map[string]bool{}
	for _, ch := range channels {
		runChannel(c, ch, policy, labeler, required, maxDepth, decision, denySeen)
	}

	// A candidate that admits any target identity without a qualifying old
	// preimage would mint new permission; locate the shortest such identity.
	if gap, boundary, boundaryOld, ok := findGap(c.Prefix, mapper); ok {
		decision.Safe = false
		if decision.Reason == "" {
			decision.Reason = RejectNoPreimage
		}
		decision.Witnesses = append(decision.Witnesses, Witness{
			Old:         boundaryOld,
			Target:      Name{Issuer: c.Prefix.Issuer, Tenant: c.Prefix.Tenant, Path: gap},
			Trace:       boundary,
			MatchedRule: -1,
			Effect:      Deny,
		})
	}

	if decision.Reason == "" {
		decision.Safe = true
	}
	return decision
}

// runChannel explores every old path of one channel whose transduction stays
// under the candidate prefix. BFS over the finite product state space both
// terminates on cyclic transducers and yields shortest witnesses.
func runChannel(c Candidate, ch *Channel, policy OldPolicy, labeler FinitaryLabeler,
	required labelSet, maxDepth int, decision *Decision, denySeen map[string]bool) {

	startName := Name{Issuer: ch.Old.Issuer, Tenant: ch.Old.Tenant}
	start := productConfig{
		q:          ch.Transducer.Start,
		matched:    0,
		excess:     "",
		labelState: labeler.StateKey(startName),
	}
	visited := map[productConfig]bool{start: true}
	root := &productNode{config: start}
	frontier := []*productNode{root}

	for len(frontier) > 0 {
		node := frontier[0]
		frontier = frontier[1:]
		cfg := node.config

		if ch.Transducer.Final[cfg.q] && cfg.matched == len(c.Prefix.Path) {
			examinePreimage(node, ch, c, policy, labeler, required, decision, denySeen)
		}

		for _, arc := range ch.Transducer.arcsByState[cfg.q] {
			nextMatched := cfg.matched
			nextExcess := cfg.excess
			if arc.Out != "" {
				if cfg.matched < len(c.Prefix.Path) {
					if arc.Out != c.Prefix.Path[cfg.matched] {
						// The output has diverged from the required prefix and
						// a prefix language can never re-converge.
						continue
					}
					nextMatched++
				} else {
					// Prefix already matched. Keep only the bounded output
					// suffix; the full output is reconstructed from the BFS
					// parent pointers for witnesses.
					nextExcess = pushString(cfg.excess, arc.Out, maxDepth)
				}
			}

			nextTail, nextLen := pushTail(cfg.tail, cfg.tailLen, arc.In, maxDepth)
			nextName := Name{
				Issuer: ch.Old.Issuer,
				Tenant: ch.Old.Tenant,
				Path:   tailPath(nextTail, nextLen),
			}
			next := productConfig{
				q:          arc.To,
				matched:    nextMatched,
				excess:     nextExcess,
				tail:       nextTail,
				tailLen:    nextLen,
				labelState: labeler.StateKey(nextName),
			}
			if visited[next] {
				continue
			}
			visited[next] = true
			childPath := append(append([]string{}, node.oldPath...), arc.In)
			frontier = append(frontier, &productNode{
				config: next, parent: node, arc: arc, oldPath: childPath,
			})
		}
	}
}

// examinePreimage evaluates one accepting product node: an accepting
// transducer state at full prefix match is a concrete old preimage of some
// target identity under the candidate. It records either coverage or a
// deny/missing-label witness.
func examinePreimage(node *productNode, ch *Channel, c Candidate, policy OldPolicy,
	labeler FinitaryLabeler, required labelSet, decision *Decision, denySeen map[string]bool) {

	oldPath := append([]string(nil), node.oldPath...)
	trace := reconstructTrace(node)
	old := Name{
		Issuer: ch.Old.Issuer,
		Tenant: ch.Old.Tenant,
		Path:   oldPath,
	}
	old.Labels = labeler.Labels(old)
	target := Name{
		Issuer: ch.New.Issuer,
		Tenant: ch.New.Tenant,
		Path:   traceOutput(trace),
		Labels: old.Labels,
	}
	decision.Preimages++

	have := newLabelSet(old.Labels)
	var missing []Label
	for l := range required {
		if !have.contains(l) {
			missing = append(missing, l)
		}
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i] < missing[j] })

	ruleIdx, effect := policy.MatchedRule(old)
	if effect == Deny || len(missing) > 0 {
		key := ch.Old.Issuer + "\x00" + ch.Old.Tenant + "\x00" + joinPath(oldPath)
		if denySeen[key] {
			return
		}
		denySeen[key] = true
		if decision.Reason == "" {
			decision.Reason = RejectOldDeny
		}
		decision.Witnesses = append(decision.Witnesses, Witness{
			Old:           old,
			Target:        target,
			Trace:         append(Trace(nil), trace...),
			MatchedRule:   ruleIdx,
			Effect:        effect,
			MissingLabels: missing,
		})
		return
	}
	decision.Covered++
}

package migration

import (
	"sort"

	"prescreen.local/ddd-10554-v3/spec"
)

// BuildCertificate 导出包含全部枚举证据的核验证书。
func (p *Planner) BuildCertificate() (*spec.Certificate, error) {
	specFP, err := spec.Fingerprint(p.model.chip)
	if err != nil {
		return nil, err
	}

	cert := &spec.Certificate{
		SchemaVersion:   1,
		Chip:            p.model.chip,
		RootBeliefKey:   p.rootKey,
		Beliefs:         map[string]*spec.BeliefNode{},
		Actions:         map[string]*spec.Action{},
		Observations:    map[string]*spec.Observation{},
		States:          map[string]*spec.State{},
		SpecFingerprint: specFP,
		WorstCaseBurns:  p.WorstCaseBurns(),
	}

	nodeKeys := make([]string, 0, len(p.nodes))
	for k := range p.nodes {
		nodeKeys = append(nodeKeys, k)
	}
	sort.Strings(nodeKeys)

	for _, k := range nodeKeys {
		n := p.nodes[k]
		stateKeys := make([]string, 0, len(n.states))
		for sk := range n.states {
			stateKeys = append(stateKeys, sk)
		}
		sort.Strings(stateKeys)
		cert.Beliefs[k] = &spec.BeliefNode{States: stateKeys, ObsKey: n.obsKey}
	}

	for sk, s := range p.stateOf {
		cert.States[sk] = s
	}

	// 信念级转移证据，并收集全部出现过的动作与观测。
	for _, k := range nodeKeys {
		n := p.nodes[k]
		actionKeys := make([]string, 0, len(n.edges))
		for ak := range n.edges {
			actionKeys = append(actionKeys, ak)
		}
		sort.Strings(actionKeys)
		for _, ak := range actionKeys {
			entry := &spec.TransitionEntry{
				BeliefKey: k,
				ActionKey: ak,
				Outcomes:  map[string]string{},
			}
			obsKeys := make([]string, 0, len(n.edges[ak]))
			for ok := range n.edges[ak] {
				obsKeys = append(obsKeys, ok)
			}
			sort.Strings(obsKeys)
			for _, ok := range obsKeys {
				entry.Outcomes[ok] = n.edges[ak][ok]
			}
			cert.Transitions = append(cert.Transitions, entry)
		}
	}

	// 策略表（覆盖每个信念的每个预算格点）。
	B := p.model.chip.MaxBurns
	for _, k := range nodeKeys {
		n := p.nodes[k]
		if p.allTerminal(n) {
			continue
		}
		for b := 0; b <= B; b++ {
			choice, ok := p.choice[k][b]
			if !ok {
				continue
			}
			ak := actionFingerprint(choice.action)
			cert.Policy = append(cert.Policy, &spec.PolicyEntry{
				BeliefKey:      k,
				BudgetLeft:     b,
				ActionKey:      ak,
				WorstCaseBurns: choice.value,
			})
		}
	}

	// 汇总动作与观测对象，使证书自包含。
	for _, k := range nodeKeys {
		n := p.nodes[k]
		if p.allTerminal(n) {
			continue
		}
		for b := 0; b <= B; b++ {
			if choice, ok := p.choice[k][b]; ok {
				ak := actionFingerprint(choice.action)
				cert.Actions[ak] = choice.action
			}
		}
		for _, actions := range [][]*spec.Action{p.legalAtAllBudgets(n)} {
			for _, a := range actions {
				ak := actionFingerprint(a)
				cert.Actions[ak] = a
			}
		}
		for _, outcomes := range n.edges {
			for ok := range outcomes {
				cert.Observations[ok] = p.obsOf[ok]
			}
		}
	}

	// ack 观测不挂在 boot 边上，单独登记以保证证书自包含。
	ackKey := observationFingerprint(&spec.Observation{Kind: spec.ObsAck})
	cert.Observations[ackKey] = &spec.Observation{Kind: spec.ObsAck}
	return cert, nil
}

func (p *Planner) legalAtAllBudgets(n *beliefNode) []*spec.Action {
	seen := map[string]*spec.Action{}
	B := p.model.chip.MaxBurns
	for b := 0; b <= B; b++ {
		for _, a := range p.legal[b][n.key] {
			seen[actionFingerprint(a)] = a
		}
	}
	var out []*spec.Action
	for _, a := range seen {
		out = append(out, a)
	}
	return out
}

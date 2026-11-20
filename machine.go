package projection

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

type machineEvent struct {
	to       int
	weight   *big.Rat
	label    Label
	category Category
	release  bool
}

type initialSecret struct {
	id    SecretID
	state int
}

type machine struct {
	labels      []Label
	labelIndex  map[Label]int
	states      []StateID
	stateIndex  map[StateID]int
	initials    []initialSecret
	out         [][]machineEvent
	termination []*big.Rat
	verifier    func(Release) bool
}

type compiledEvent struct {
	to       int
	weight   *big.Rat
	label    Label
	category Category
	release  bool
}

func compileProtocol(p Protocol) (*machine, error) {
	stateSet := make(map[StateID]struct{})
	for _, state := range p.States {
		if state == "" {
			return nil, fmt.Errorf("projection: empty state id")
		}
		stateSet[state] = struct{}{}
	}
	for secret, state := range p.Initial {
		if secret == "" {
			return nil, fmt.Errorf("projection: empty secret id")
		}
		if state == "" {
			return nil, fmt.Errorf("projection: secret %q has empty initial state", secret)
		}
		stateSet[state] = struct{}{}
	}
	for i, transition := range p.Transitions {
		if transition.From == "" || transition.To == "" {
			return nil, fmt.Errorf("projection: transition %d has an empty endpoint", i)
		}
		if transition.Label == "" {
			return nil, fmt.Errorf("projection: transition %d has an empty label", i)
		}
		stateSet[transition.From] = struct{}{}
		stateSet[transition.To] = struct{}{}
	}

	states := make([]StateID, 0, len(stateSet))
	for state := range stateSet {
		states = append(states, state)
	}
	sort.Slice(states, func(i, j int) bool { return states[i] < states[j] })
	stateIndex := make(map[StateID]int, len(states))
	for i, state := range states {
		stateIndex[state] = i
	}

	labelSet := make(map[Label]struct{})
	for _, transition := range p.Transitions {
		labelSet[transition.Label] = struct{}{}
	}
	labels := sortedLabelsFromSet(labelSet)
	labelIndex := make(map[Label]int, len(labels))
	for i, label := range labels {
		labelIndex[label] = i
	}

	m := &machine{
		labels:      labels,
		labelIndex:  labelIndex,
		states:      states,
		stateIndex:  stateIndex,
		out:         make([][]machineEvent, len(states)),
		termination: make([]*big.Rat, len(states)),
		verifier:    p.VerifyRelease,
	}

	total := make([]*big.Rat, len(states))
	for i := range total {
		total[i] = new(big.Rat)
	}
	for i, transition := range p.Transitions {
		weight := transition.Weight
		if weight == nil {
			parsed, err := ParseProbability(transition.Probability)
			if err != nil {
				return nil, fmt.Errorf("projection: transition %d: %w", i, err)
			}
			weight = parsed
		} else {
			weight = new(big.Rat).Set(weight)
		}
		if weight.Sign() <= 0 {
			return nil, fmt.Errorf("projection: transition %d has non-positive weight", i)
		}
		if weight.Cmp(big.NewRat(1, 1)) > 0 {
			return nil, fmt.Errorf("projection: transition %d has weight greater than one", i)
		}

		from := stateIndex[transition.From]
		to := stateIndex[transition.To]
		event := machineEvent{
			to:     to,
			weight: weight,
			label:  transition.Label,
		}
		if transition.Release != nil && m.acceptRelease(*transition.Release) {
			if transition.Release.Category == "" {
				return nil, fmt.Errorf("projection: transition %d has an empty signed release category", i)
			}
			event.release = true
			event.category = transition.Release.Category
		}
		m.out[from] = append(m.out[from], event)
		total[from] = addRat(total[from], weight)
	}

	for state := range m.out {
		if total[state].Cmp(big.NewRat(1, 1)) > 0 {
			return nil, fmt.Errorf("projection: outgoing probability from state %q exceeds one (%s)", m.states[state], RatString(total[state]))
		}
		m.termination[state] = new(big.Rat).Sub(big.NewRat(1, 1), total[state])
		sort.Slice(m.out[state], func(i, j int) bool {
			a, b := m.out[state][i], m.out[state][j]
			if a.label != b.label {
				return a.label < b.label
			}
			if a.release != b.release {
				return !a.release
			}
			if a.category != b.category {
				return a.category < b.category
			}
			if a.to != b.to {
				return m.states[a.to] < m.states[b.to]
			}
			return a.weight.Cmp(b.weight) < 0
		})
	}

	initials := make([]initialSecret, 0, len(p.Initial))
	for secret, state := range p.Initial {
		initials = append(initials, initialSecret{id: secret, state: stateIndex[state]})
	}
	sort.Slice(initials, func(i, j int) bool { return initials[i].id < initials[j].id })
	m.initials = initials

	return m, nil
}

func (m *machine) acceptRelease(release Release) bool {
	if m.verifier == nil {
		return release.Signed || strings.TrimSpace(release.Signature) != ""
	}
	return m.verifier(release)
}

func (m *machine) projectedEvents(state int, projection Projection) []compiledEvent {
	events := make([]compiledEvent, 0, len(m.out[state]))
	for _, event := range m.out[state] {
		events = append(events, compiledEvent{
			to:       event.to,
			weight:   event.weight,
			label:    projection.Public(event.label),
			category: event.category,
			release:  event.release,
		})
	}
	return events
}

func (m *machine) validateProjection(projection Projection) error {
	for _, label := range m.labels {
		if out, ok := projection.Mapping[label]; ok && out == "" {
			return fmt.Errorf("projection: label %q maps to an empty public label", label)
		}
	}
	return nil
}

package projection

import (
	"fmt"
	"math/big"
	"sort"
)

type pairAnalysis struct {
	failure  *SafetyFailure
	ordinary []OrdinaryProof
	releases []ReleaseProof
}

func ordinaryGroups(events []compiledEvent) map[Label][]probabilityAtom {
	groups := make(map[Label][]probabilityAtom)
	for _, event := range events {
		if event.release {
			continue
		}
		groups[event.label] = append(groups[event.label], probabilityAtom{
			label: event.label,
			state: event.to,
			mass:  new(big.Rat).Set(event.weight),
		})
	}
	return groups
}

func releaseGroups(events []compiledEvent) map[Category][]probabilityAtom {
	groups := make(map[Category][]probabilityAtom)
	for _, event := range events {
		if !event.release {
			continue
		}
		groups[event.category] = append(groups[event.category], probabilityAtom{
			label: event.label,
			state: event.to,
			mass:  new(big.Rat).Set(event.weight),
		})
	}
	return groups
}

func atomMass(atoms []probabilityAtom) *big.Rat {
	total := new(big.Rat)
	for _, atom := range atoms {
		total.Add(total, atom.mass)
	}
	return total
}

func normalizeAtoms(atoms []probabilityAtom, total *big.Rat) []probabilityAtom {
	out := make([]probabilityAtom, 0, len(atoms))
	for _, atom := range atoms {
		out = append(out, probabilityAtom{
			label: atom.label,
			state: atom.state,
			mass:  new(big.Rat).Quo(atom.mass, total),
		})
	}
	return out
}

func analyzePair(m *machine, projection Projection, left, right int, relation [][]bool, withProof bool) pairAnalysis {
	stateA, stateB := m.states[left], m.states[right]
	if m.termination[left].Cmp(m.termination[right]) != 0 {
		return pairAnalysis{failure: &SafetyFailure{
			Reason:       "termination",
			StateA:       stateA,
			StateB:       stateB,
			ProbabilityA: RatString(m.termination[left]),
			ProbabilityB: RatString(m.termination[right]),
			Detail:       "early termination probabilities differ",
		}}
	}

	eventsA := m.projectedEvents(left, projection)
	eventsB := m.projectedEvents(right, projection)
	ordinaryA := ordinaryGroups(eventsA)
	ordinaryB := ordinaryGroups(eventsB)

	labelSet := make(map[Label]struct{}, len(ordinaryA)+len(ordinaryB))
	for label := range ordinaryA {
		labelSet[label] = struct{}{}
	}
	for label := range ordinaryB {
		labelSet[label] = struct{}{}
	}
	labels := make([]Label, 0, len(labelSet))
	for label := range labelSet {
		labels = append(labels, label)
	}
	sort.Slice(labels, func(i, j int) bool { return labels[i] < labels[j] })

	result := pairAnalysis{}
	for _, label := range labels {
		massA := atomMass(ordinaryA[label])
		massB := atomMass(ordinaryB[label])
		if massA.Cmp(massB) != 0 {
			return pairAnalysis{failure: &SafetyFailure{
				Reason:       "ordinary_marginal",
				StateA:       stateA,
				StateB:       stateB,
				Label:        label,
				ProbabilityA: RatString(massA),
				ProbabilityB: RatString(massB),
				Detail:       "ordinary public-label probabilities differ",
			}}
		}
		if massA.Sign() == 0 {
			continue
		}
		cells, ok := coupleAtoms(ordinaryA[label], ordinaryB[label], relation, m.states, false)
		if !ok {
			return pairAnalysis{failure: &SafetyFailure{
				Reason:       "ordinary_coupling",
				StateA:       stateA,
				StateB:       stateB,
				Label:        label,
				ProbabilityA: RatString(massA),
				ProbabilityB: RatString(massB),
				Detail:       "no rational coupling exists inside the current bisimulation relation",
			}}
		}
		if withProof {
			result.ordinary = append(result.ordinary, OrdinaryProof{
				Label:        label,
				ProbabilityA: RatString(massA),
				ProbabilityB: RatString(massB),
				Coupling:     cells,
			})
		}
	}

	releasesA := releaseGroups(eventsA)
	releasesB := releaseGroups(eventsB)
	categorySet := make(map[Category]struct{}, len(releasesA)+len(releasesB))
	for category := range releasesA {
		categorySet[category] = struct{}{}
	}
	for category := range releasesB {
		categorySet[category] = struct{}{}
	}
	categories := make([]Category, 0, len(categorySet))
	for category := range categorySet {
		categories = append(categories, category)
	}
	sort.Slice(categories, func(i, j int) bool { return categories[i] < categories[j] })

	for _, category := range categories {
		massA := atomMass(releasesA[category])
		massB := atomMass(releasesB[category])
		if massA.Sign() == 0 || massB.Sign() == 0 {
			// The two hypotheses have published different categories.  A signed
			// release is precisely the permitted point at which they diverge.
			continue
		}
		leftAtoms := normalizeAtoms(releasesA[category], massA)
		rightAtoms := normalizeAtoms(releasesB[category], massB)
		cells, ok := coupleAtoms(leftAtoms, rightAtoms, relation, m.states, true)
		if !ok {
			return pairAnalysis{failure: &SafetyFailure{
				Reason:       "release_continuation",
				StateA:       stateA,
				StateB:       stateB,
				Category:     category,
				ProbabilityA: RatString(massA),
				ProbabilityB: RatString(massB),
				Detail:       "conditional successor distributions after the same signed release differ",
			}}
		}
		if withProof {
			result.releases = append(result.releases, ReleaseProof{
				Category:     category,
				ProbabilityA: RatString(massA),
				ProbabilityB: RatString(massB),
				Coupling:     cells,
			})
		}
	}

	return result
}

func evaluateSafety(m *machine, projection Projection, withProof bool, labels []Label) (*SafetyCertificate, *SafetyFailure, error) {
	if err := m.validateProjection(projection); err != nil {
		return nil, nil, err
	}
	n := len(m.states)
	relation := make([][]bool, n)
	for i := range relation {
		relation[i] = make([]bool, n)
		for j := range relation[i] {
			relation[i][j] = true
		}
	}

	changed := true
	for changed {
		changed = false
		for left := 0; left < n; left++ {
			for right := 0; right < n; right++ {
				if !relation[left][right] {
					continue
				}
				analysis := analyzePair(m, projection, left, right, relation, false)
				if analysis.failure != nil {
					relation[left][right] = false
					relation[right][left] = false
					changed = true
				}
			}
		}
	}

	for i := 0; i < len(m.initials); i++ {
		for j := i + 1; j < len(m.initials); j++ {
			left, right := m.initials[i], m.initials[j]
			if !relation[left.state][right.state] {
				return nil, buildFailure(m, projection, relation, left, right), nil
			}
		}
	}

	certificate := &SafetyCertificate{
		Encoding:      encodeProjection(projection, labels),
		CategoryCount: projection.CategoryCount(labels),
		Labels:        append([]Label(nil), labels...),
	}
	if !withProof {
		return certificate, nil, nil
	}

	for left := 0; left < n; left++ {
		for right := 0; right < n; right++ {
			if !relation[left][right] {
				continue
			}
			analysis := analyzePair(m, projection, left, right, relation, true)
			if analysis.failure != nil {
				return nil, analysis.failure, fmt.Errorf("projection: internal bisimulation instability")
			}
			certificate.Relation = append(certificate.Relation, StatePair{
				A: m.states[left],
				B: m.states[right],
			})
			certificate.Pairs = append(certificate.Pairs, PairProof{
				A:            m.states[left],
				B:            m.states[right],
				TerminationA: RatString(m.termination[left]),
				TerminationB: RatString(m.termination[right]),
				Ordinary:     analysis.ordinary,
				Releases:     analysis.releases,
			})
		}
	}
	return certificate, nil, nil
}

type witnessNode struct {
	left, right int
	trace       []Observation
}

func buildFailure(m *machine, projection Projection, relation [][]bool, initialA, initialB initialSecret) *SafetyFailure {
	n := len(m.states)
	visited := make([][]bool, n)
	for i := range visited {
		visited[i] = make([]bool, n)
	}
	queue := []witnessNode{{left: initialA.state, right: initialB.state}}
	visited[initialA.state][initialB.state] = true

	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		analysis := analyzePair(m, projection, node.left, node.right, relation, false)
		if analysis.failure != nil {
			deeper := false
			if analysis.failure.Reason == "ordinary_coupling" || analysis.failure.Reason == "release_continuation" {
				eventsA := m.projectedEvents(node.left, projection)
				eventsB := m.projectedEvents(node.right, projection)
				for _, a := range eventsA {
					for _, b := range eventsB {
						if a.release != b.release || a.label != b.label {
							continue
						}
						if a.release && a.category != b.category {
							continue
						}
						if relation[a.to][b.to] {
							continue
						}
						if visited[a.to][b.to] {
							continue
						}
						visited[a.to][b.to] = true
						observation := Observation{Kind: "ordinary", Label: a.label}
						if a.release {
							observation.Kind = "release"
							observation.Category = a.category
						}
						trace := append(append([]Observation(nil), node.trace...), observation)
						queue = append(queue, witnessNode{left: a.to, right: b.to, trace: trace})
						deeper = true
					}
				}
			}
			if !deeper {
				failure := analysis.failure
				failure.SecretA = initialA.id
				failure.SecretB = initialB.id
				failure.Trace = append([]Observation(nil), node.trace...)
				return failure
			}
			continue
		}

		eventsA := m.projectedEvents(node.left, projection)
		eventsB := m.projectedEvents(node.right, projection)
		for _, a := range eventsA {
			for _, b := range eventsB {
				if a.release != b.release || a.label != b.label {
					continue
				}
				if a.release && a.category != b.category {
					continue
				}
				if relation[a.to][b.to] || visited[a.to][b.to] {
					continue
				}
				visited[a.to][b.to] = true
				observation := Observation{Kind: "ordinary", Label: a.label}
				if a.release {
					observation.Kind = "release"
					observation.Category = a.category
				}
				trace := append(append([]Observation(nil), node.trace...), observation)
				queue = append(queue, witnessNode{left: a.to, right: b.to, trace: trace})
			}
		}
	}

	return &SafetyFailure{
		Reason:  "bisimulation",
		SecretA: initialA.id,
		SecretB: initialB.id,
		StateA:  m.states[initialA.state],
		StateB:  m.states[initialB.state],
		Detail:  "initial secret states are not probabilistically bisimilar",
	}
}

// CheckSafety proves or refutes one projection.  A nil failure with a non-nil
// certificate means the projection is safe.
func CheckSafety(protocol Protocol, projection Projection) (*SafetyCertificate, *SafetyFailure, error) {
	m, err := compileProtocol(protocol)
	if err != nil {
		return nil, nil, err
	}
	if len(m.labels) > 12 {
		return nil, nil, fmt.Errorf("projection: %d original labels exceed the limit of 12", len(m.labels))
	}
	return evaluateSafety(m, projection, true, m.labels)
}

// IsSafe is a convenience wrapper around CheckSafety.
func IsSafe(protocol Protocol, projection Projection) (bool, *SafetyFailure, error) {
	certificate, failure, err := CheckSafety(protocol, projection)
	return certificate != nil && failure == nil && err == nil, failure, err
}

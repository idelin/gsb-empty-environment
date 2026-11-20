package projection

import (
	"fmt"
	"math/big"
	"sort"
)

func equalProjection(a, b Projection) bool {
	if len(a.Mapping) != len(b.Mapping) {
		return false
	}
	for label, value := range a.Mapping {
		if other, ok := b.Mapping[label]; !ok || other != value {
			return false
		}
	}
	return true
}

func parseRatEqual(value string, expected *big.Rat) bool {
	parsed, err := ParseProbability(value)
	return err == nil && parsed.Cmp(expected) == 0
}

func pairKey(a, b StateID) string {
	return string(a) + "\x00" + string(b)
}

func verifyPairProof(m *machine, projection Projection, certPairs map[string]PairProof, relation map[string]bool, left, right int) error {
	aID, bID := m.states[left], m.states[right]
	key := pairKey(aID, bID)
	proof, ok := certPairs[key]
	if !ok {
		return fmt.Errorf("projection: missing pair proof for %q,%q", aID, bID)
	}
	if proof.A != aID || proof.B != bID {
		return fmt.Errorf("projection: malformed pair proof for %q,%q", aID, bID)
	}
	if !parseRatEqual(proof.TerminationA, m.termination[left]) {
		return fmt.Errorf("projection: wrong termination probability for %q", aID)
	}
	if !parseRatEqual(proof.TerminationB, m.termination[right]) {
		return fmt.Errorf("projection: wrong termination probability for %q", bID)
	}
	if m.termination[left].Cmp(m.termination[right]) != 0 {
		return fmt.Errorf("projection: termination probabilities differ for %q,%q", aID, bID)
	}

	eventsA := m.projectedEvents(left, projection)
	eventsB := m.projectedEvents(right, projection)
	ordinaryA := ordinaryGroups(eventsA)
	ordinaryB := ordinaryGroups(eventsB)
	ordinaryProof := make(map[Label]OrdinaryProof, len(proof.Ordinary))
	for _, item := range proof.Ordinary {
		if _, exists := ordinaryProof[item.Label]; exists {
			return fmt.Errorf("projection: duplicate ordinary proof for label %q", item.Label)
		}
		ordinaryProof[item.Label] = item
	}
	labelSet := make(map[Label]struct{}, len(ordinaryA)+len(ordinaryB))
	for label := range ordinaryA {
		labelSet[label] = struct{}{}
	}
	for label := range ordinaryB {
		labelSet[label] = struct{}{}
	}
	for label := range labelSet {
		massA := atomMass(ordinaryA[label])
		massB := atomMass(ordinaryB[label])
		if massA.Sign() == 0 && massB.Sign() == 0 {
			continue
		}
		item, exists := ordinaryProof[label]
		if !exists {
			return fmt.Errorf("projection: missing ordinary proof for label %q", label)
		}
		if massA.Cmp(massB) != 0 {
			return fmt.Errorf("projection: ordinary masses differ for label %q", label)
		}
		if !parseRatEqual(item.ProbabilityA, massA) || !parseRatEqual(item.ProbabilityB, massB) {
			return fmt.Errorf("projection: wrong ordinary mass for label %q", label)
		}
		fromByState := make(map[int]*big.Rat)
		toByState := make(map[int]*big.Rat)
		for _, atom := range ordinaryA[label] {
			fromByState[atom.state] = addRat(fromByState[atom.state], atom.mass)
		}
		for _, atom := range ordinaryB[label] {
			toByState[atom.state] = addRat(toByState[atom.state], atom.mass)
		}
		cellFrom := make(map[int]*big.Rat)
		cellTo := make(map[int]*big.Rat)
		for _, cell := range item.Coupling {
			from, okFrom := m.stateIndex[cell.From]
			to, okTo := m.stateIndex[cell.To]
			if !okFrom || !okTo {
				return fmt.Errorf("projection: coupling references an unknown state")
			}
			if cell.FromLabel != label || cell.ToLabel != label {
				return fmt.Errorf("projection: ordinary coupling has the wrong label")
			}
			mass, err := ParseProbability(cell.Probability)
			if err != nil || mass.Sign() <= 0 {
				return fmt.Errorf("projection: invalid coupling mass %q", cell.Probability)
			}
			if !relation[pairKey(m.states[from], m.states[to])] {
				return fmt.Errorf("projection: coupling leaves the candidate bisimulation relation")
			}
			cellFrom[from] = addRat(cellFrom[from], mass)
			cellTo[to] = addRat(cellTo[to], mass)
		}
		for state, mass := range fromByState {
			if cmpRat(cellFrom[state], mass) != 0 {
				return fmt.Errorf("projection: coupling does not match the left marginal")
			}
		}
		for state, mass := range toByState {
			if cmpRat(cellTo[state], mass) != 0 {
				return fmt.Errorf("projection: coupling does not match the right marginal")
			}
		}
	}
	if len(ordinaryProof) != len(labelSet) {
		for label := range ordinaryProof {
			if _, expected := labelSet[label]; !expected {
				return fmt.Errorf("projection: unexpected ordinary proof for label %q", label)
			}
		}
	}

	releasesA := releaseGroups(eventsA)
	releasesB := releaseGroups(eventsB)
	releaseProof := make(map[Category]ReleaseProof, len(proof.Releases))
	for _, item := range proof.Releases {
		if _, exists := releaseProof[item.Category]; exists {
			return fmt.Errorf("projection: duplicate release proof for category %q", item.Category)
		}
		releaseProof[item.Category] = item
	}
	categorySet := make(map[Category]struct{}, len(releasesA)+len(releasesB))
	for category := range releasesA {
		categorySet[category] = struct{}{}
	}
	for category := range releasesB {
		categorySet[category] = struct{}{}
	}
	for category := range categorySet {
		massA := atomMass(releasesA[category])
		massB := atomMass(releasesB[category])
		if massA.Sign() == 0 || massB.Sign() == 0 {
			continue
		}
		item, exists := releaseProof[category]
		if !exists {
			return fmt.Errorf("projection: missing release proof for category %q", category)
		}
		if !parseRatEqual(item.ProbabilityA, massA) || !parseRatEqual(item.ProbabilityB, massB) {
			return fmt.Errorf("projection: wrong release mass for category %q", category)
		}
		fromAtoms := make(map[string]*big.Rat)
		toAtoms := make(map[string]*big.Rat)
		for _, atom := range releasesA[category] {
			key := string(atom.label) + "\x00" + string(m.states[atom.state])
			fromAtoms[key] = addRat(fromAtoms[key], new(big.Rat).Quo(atom.mass, massA))
		}
		for _, atom := range releasesB[category] {
			key := string(atom.label) + "\x00" + string(m.states[atom.state])
			toAtoms[key] = addRat(toAtoms[key], new(big.Rat).Quo(atom.mass, massB))
		}
		fromCell := make(map[string]*big.Rat)
		toCell := make(map[string]*big.Rat)
		for _, cell := range item.Coupling {
			if cell.FromLabel != cell.ToLabel {
				return fmt.Errorf("projection: release coupling changes the projected label")
			}
			from, okFrom := m.stateIndex[cell.From]
			to, okTo := m.stateIndex[cell.To]
			if !okFrom || !okTo {
				return fmt.Errorf("projection: release coupling references an unknown state")
			}
			if !relation[pairKey(m.states[from], m.states[to])] {
				return fmt.Errorf("projection: release coupling leaves the candidate bisimulation relation")
			}
			mass, err := ParseProbability(cell.Probability)
			if err != nil || mass.Sign() <= 0 {
				return fmt.Errorf("projection: invalid release coupling mass %q", cell.Probability)
			}
			fromKey := string(cell.FromLabel) + "\x00" + string(cell.From)
			toKey := string(cell.ToLabel) + "\x00" + string(cell.To)
			fromCell[fromKey] = addRat(fromCell[fromKey], mass)
			toCell[toKey] = addRat(toCell[toKey], mass)
		}
		for key, mass := range fromAtoms {
			if cmpRat(fromCell[key], mass) != 0 {
				return fmt.Errorf("projection: release coupling does not match the left marginal")
			}
		}
		for key, mass := range toAtoms {
			if cmpRat(toCell[key], mass) != 0 {
				return fmt.Errorf("projection: release coupling does not match the right marginal")
			}
		}
	}
	if len(releaseProof) != len(categorySet) {
		for category := range releaseProof {
			if _, expected := categorySet[category]; !expected {
				return fmt.Errorf("projection: unexpected release proof for category %q", category)
			}
		}
	}
	return nil
}

func verifySafetyCertificate(m *machine, projection Projection, labels []Label, certificate SafetyCertificate) error {
	if err := m.validateProjection(projection); err != nil {
		return err
	}
	if certificate.CategoryCount != projection.CategoryCount(labels) {
		return fmt.Errorf("projection: safety certificate category count mismatch")
	}
	if certificate.Encoding != encodeProjection(projection, labels) {
		return fmt.Errorf("projection: safety certificate encoding mismatch")
	}

	relation := make(map[string]bool, len(certificate.Relation))
	for _, pair := range certificate.Relation {
		key := pairKey(pair.A, pair.B)
		if relation[key] {
			return fmt.Errorf("projection: duplicate relation pair %q,%q", pair.A, pair.B)
		}
		if _, okA := m.stateIndex[pair.A]; !okA {
			return fmt.Errorf("projection: relation references unknown state %q", pair.A)
		}
		if _, okB := m.stateIndex[pair.B]; !okB {
			return fmt.Errorf("projection: relation references unknown state %q", pair.B)
		}
		relation[key] = true
	}
	for key := range relation {
		var a, b StateID
		separator := -1
		for i := 0; i < len(key); i++ {
			if key[i] == 0 {
				separator = i
				break
			}
		}
		if separator < 0 {
			return fmt.Errorf("projection: malformed relation key")
		}
		a, b = StateID(key[:separator]), StateID(key[separator+1:])
		if !relation[pairKey(b, a)] {
			return fmt.Errorf("projection: relation is not symmetric at %q,%q", a, b)
		}
	}
	for i := 0; i < len(m.initials); i++ {
		for j := i + 1; j < len(m.initials); j++ {
			a, b := m.states[m.initials[i].state], m.states[m.initials[j].state]
			if !relation[pairKey(a, b)] {
				return fmt.Errorf("projection: relation omits initial secret pair %q,%q", a, b)
			}
		}
	}

	certPairs := make(map[string]PairProof, len(certificate.Pairs))
	for _, pair := range certificate.Pairs {
		key := pairKey(pair.A, pair.B)
		if _, exists := certPairs[key]; exists {
			return fmt.Errorf("projection: duplicate pair proof %q,%q", pair.A, pair.B)
		}
		certPairs[key] = pair
	}
	for _, pair := range certificate.Relation {
		left, _ := m.stateIndex[pair.A]
		right, _ := m.stateIndex[pair.B]
		if err := verifyPairProof(m, projection, certPairs, relation, left, right); err != nil {
			return err
		}
	}
	if len(certPairs) != len(certificate.Relation) {
		return fmt.Errorf("projection: pair proofs and relation have different sizes")
	}
	return nil
}

// VerifySafetyCertificate checks the self-contained bisimulation proof without
// rerunning the fixed-point search.  Labels not present in the protocol cannot
// appear in this standalone form; VerifyCertificate handles configured labels.
func VerifySafetyCertificate(protocol Protocol, projection Projection, certificate SafetyCertificate) error {
	m, err := compileProtocol(protocol)
	if err != nil {
		return err
	}
	labels := append([]Label(nil), m.labels...)
	if len(certificate.Labels) > 0 {
		if len(certificate.Labels) > 12 {
			return fmt.Errorf("projection: certificate has more than 12 labels")
		}
		seen := make(map[Label]struct{}, len(certificate.Labels))
		for _, label := range certificate.Labels {
			if _, exists := seen[label]; exists {
				return fmt.Errorf("projection: duplicate certificate label %q", label)
			}
			seen[label] = struct{}{}
		}
		for _, label := range m.labels {
			if _, exists := seen[label]; !exists {
				return fmt.Errorf("projection: certificate omits protocol label %q", label)
			}
		}
		labels = append([]Label(nil), certificate.Labels...)
	}
	return verifySafetyCertificate(m, projection, labels, certificate)
}

// VerifyCertificate independently replays the safety proof, the optimum, and
// every submitted refutation witness.
func VerifyCertificate(protocol Protocol, config Config, certificate Certificate) error {
	fresh, err := Synthesize(protocol, config)
	if err != nil {
		return err
	}
	if !equalProjection(certificate.Projection, fresh.Projection) ||
		certificate.CategoryCount != fresh.CategoryCount ||
		certificate.Encoding != fresh.Certificate.Encoding {
		return fmt.Errorf("projection: certificate does not match the computed optimum")
	}

	m, err := compileProtocol(protocol)
	if err != nil {
		return err
	}
	constraints, err := buildConstraints(m, config)
	if err != nil {
		return err
	}
	if err := verifySafetyCertificate(m, certificate.Projection, constraints.labels, certificate.Safety); err != nil {
		return err
	}
	if len(certificate.Safety.Labels) > 0 {
		if len(certificate.Safety.Labels) != len(constraints.labels) {
			return fmt.Errorf("projection: safety certificate label universe mismatch")
		}
		expected := append([]Label(nil), constraints.labels...)
		actual := append([]Label(nil), certificate.Safety.Labels...)
		if len(expected) > 1 {
			sort.Slice(expected, func(i, j int) bool { return expected[i] < expected[j] })
			sort.Slice(actual, func(i, j int) bool { return actual[i] < actual[j] })
		}
		for i := range expected {
			if expected[i] != actual[i] {
				return fmt.Errorf("projection: safety certificate label universe mismatch")
			}
		}
	}
	if certificate.Optimality.CategoryCount != fresh.Certificate.Optimality.CategoryCount ||
		certificate.Optimality.CanonicalEncoding != fresh.Certificate.Optimality.CanonicalEncoding ||
		certificate.Optimality.AboveBest != fresh.Certificate.Optimality.AboveBest ||
		certificate.Optimality.Refuted != fresh.Certificate.Optimality.Refuted ||
		certificate.Optimality.RefutationDigest != fresh.Certificate.Optimality.RefutationDigest ||
		certificate.Optimality.Exhaustive != fresh.Certificate.Optimality.Exhaustive {
		return fmt.Errorf("projection: optimality certificate does not match exhaustive replay")
	}

	for _, refutation := range certificate.Optimality.Refutations {
		if refutation.CategoryCount <= certificate.CategoryCount {
			return fmt.Errorf("projection: refutation does not exceed the optimum")
		}
		if refutation.Encoding != encodeProjection(refutation.Projection, constraints.labels) {
			return fmt.Errorf("projection: refutation encoding mismatch")
		}
		safe, _, err := IsSafe(protocol, refutation.Projection)
		if err != nil {
			return err
		}
		if safe {
			return fmt.Errorf("projection: submitted refutation is actually safe")
		}
	}
	return VerifyRefutationDigest(protocol, config, certificate.Optimality)
}

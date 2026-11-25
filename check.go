package closure

import (
	"errors"
	"fmt"
)

// RegionDisallowed is the fourth truth-table outcome: an assignment excluded
// by a global clause. The requested three regions never include it.
const RegionDisallowed RegionName = "disallowed"

func conditionSatisfied(cond Condition, config map[string]bool) bool {
	for flag, required := range cond {
		if config[flag] != required {
			return false
		}
	}
	return true
}

func literalSatisfied(lit Literal, config map[string]bool) bool {
	value := config[lit.flagName()]
	if lit.negated() {
		return !value
	}
	return value
}

func clausesSatisfied(req Request, config map[string]bool) bool {
	for _, clause := range req.Clauses {
		satisfied := false
		count := 0
		for _, lit := range clause {
			if lit.flagName() == "" {
				continue
			}
			count++
			if literalSatisfied(lit, config) {
				satisfied = true
			}
		}
		if count > 0 && !satisfied {
			return false
		}
	}
	return true
}

func hasAlternative(dep Dependency, id Identity) bool {
	for _, alternative := range dep.canonicalAlternatives() {
		if alternative == id {
			return true
		}
	}
	return false
}

// VerifyEvidence independently replays evidence using only req and the
// concrete witness. It does not trust BDD node IDs or analysis internals.
func VerifyEvidence(input any, evidence Evidence) error {
	req, err := normalizeInput(input)
	if err != nil {
		return err
	}
	if evidence.Config == nil {
		return fmt.Errorf("closure: evidence has no configuration")
	}
	if !clausesSatisfied(req, evidence.Config) {
		return fmt.Errorf("closure: evidence configuration violates a global clause")
	}

	packages := map[Identity]*Package{}
	for i := range req.Packages {
		pkg := &req.Packages[i]
		packages[pkg.ID] = pkg
	}
	roots := resolveRoots(req, packages)
	rootOK := false
	for _, root := range roots {
		if root == evidence.Root {
			rootOK = true
		}
	}
	if !rootOK {
		return fmt.Errorf("closure: evidence root %s is not a target root in the candidate table", evidence.Root)
	}
	if _, ok := packages[evidence.Root]; !ok {
		return fmt.Errorf("closure: evidence root %s is absent from candidate table", evidence.Root)
	}

	for name, id := range evidence.Selection {
		if id.Name != name {
			return fmt.Errorf("closure: selection key %q does not match identity name %q", name, id.Name)
		}
		if _, ok := packages[id]; !ok {
			return fmt.Errorf("closure: selected identity %s is absent from candidate table", id)
		}
	}
	if evidence.Selection[evidence.Root.Name] != evidence.Root {
		return fmt.Errorf("closure: root name %q is not globally bound to evidence root %s", evidence.Root.Name, evidence.Root)
	}

	type choiceRecord struct {
		choice Choice
		dep    Dependency
	}
	choices := map[depRef]choiceRecord{}
	for _, choice := range evidence.Choices {
		if _, ok := packages[choice.Owner]; !ok {
			return fmt.Errorf("closure: choice owner %s is absent", choice.Owner)
		}
		if evidence.Selection[choice.Owner.Name] != choice.Owner {
			return fmt.Errorf("closure: choice owner %s is not in the global selection", choice.Owner)
		}
		pkg := packages[choice.Owner]
		deps := pkg.canonicalDeps()
		if choice.DependencyIndex < 0 || choice.DependencyIndex >= len(deps) {
			return fmt.Errorf("closure: choice for %s has invalid dependency index %d", choice.Owner, choice.DependencyIndex)
		}
		ref := depRef{owner: choice.Owner, index: choice.DependencyIndex}
		if _, duplicate := choices[ref]; duplicate {
			return fmt.Errorf("closure: duplicate choice for %s dependency %d", choice.Owner, choice.DependencyIndex)
		}
		dep := deps[choice.DependencyIndex]
		condition := mergedCondition(dep.conditions())
		conditionValue := conditionSatisfied(condition, evidence.Config)
		if choice.Active != conditionValue {
			return fmt.Errorf("closure: choice active=%v but dependency condition evaluates %v for %s#%d", choice.Active, conditionValue, choice.Owner, choice.DependencyIndex)
		}
		if choice.Active {
			if choice.Selected.empty() {
				return fmt.Errorf("closure: active choice %s#%d has no selected identity", choice.Owner, choice.DependencyIndex)
			}
			if !hasAlternative(dep, choice.Selected) {
				return fmt.Errorf("closure: selected %s is not an exact alternative of %s#%d", choice.Selected, choice.Owner, choice.DependencyIndex)
			}
			if _, ok := packages[choice.Selected]; !ok {
				return fmt.Errorf("closure: selected identity %s is absent from candidate table", choice.Selected)
			}
			if got := evidence.Selection[choice.Selected.Name]; got != choice.Selected {
				return fmt.Errorf("closure: name %q is bound to %s, not chosen %s", choice.Selected.Name, got, choice.Selected)
			}
		} else if !choice.Selected.empty() {
			return fmt.Errorf("closure: inactive choice %s#%d unexpectedly selects %s", choice.Owner, choice.DependencyIndex, choice.Selected)
		}
		choices[ref] = choiceRecord{choice: choice, dep: dep}
	}

	visited := map[Identity]bool{evidence.Root: true}
	queue := []Identity{evidence.Root}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if evidence.Selection[id.Name] != id {
			return fmt.Errorf("closure: reachable %s is not the global selection for name %q", id, id.Name)
		}
		pkg := packages[id]
		if pkg == nil {
			return fmt.Errorf("closure: reachable identity %s is absent", id)
		}
		for index, dep := range pkg.canonicalDeps() {
			ref := depRef{owner: id, index: index}
			record, ok := choices[ref]
			if !ok {
				return fmt.Errorf("closure: reachable package %s dependency %d has no evidence choice", id, index)
			}
			condition := mergedCondition(dep.conditions())
			conditionValue := conditionSatisfied(condition, evidence.Config)
			if record.choice.Active != conditionValue {
				return fmt.Errorf("closure: evidence choice for %s#%d does not match condition", id, index)
			}
			if !conditionValue {
				continue
			}
			selected := record.choice.Selected
			if !hasAlternative(dep, selected) {
				return fmt.Errorf("closure: active dependency %s#%d selects invalid alternative %s", id, index, selected)
			}
			if _, ok := packages[selected]; !ok {
				return fmt.Errorf("closure: selected alternative %s is absent", selected)
			}
			if evidence.Selection[selected.Name] != selected {
				return fmt.Errorf("closure: global name %q conflicts for selected %s", selected.Name, selected)
			}
			if !visited[selected] {
				visited[selected] = true
				queue = append(queue, selected)
			}
		}
	}

	if len(evidence.IdentityChain) == 0 {
		return fmt.Errorf("closure: evidence has no root reference chain")
	}
	if evidence.IdentityChain[0] != evidence.Root {
		return fmt.Errorf("closure: chain starts at %s, want root %s", evidence.IdentityChain[0], evidence.Root)
	}
	if evidence.IdentityChain[len(evidence.IdentityChain)-1] != evidence.Unproven {
		return fmt.Errorf("closure: chain ends at %s, want unproven %s", evidence.IdentityChain[len(evidence.IdentityChain)-1], evidence.Unproven)
	}
	if len(evidence.Chain)+1 != len(evidence.IdentityChain) {
		return fmt.Errorf("closure: chain edge/identity length mismatch")
	}
	current := evidence.Root
	for i, link := range evidence.Chain {
		if link.From != current {
			return fmt.Errorf("closure: chain link %d starts at %s, expected %s", i, link.From, current)
		}
		pkg := packages[current]
		if pkg == nil {
			return fmt.Errorf("closure: chain owner %s absent", current)
		}
		deps := pkg.canonicalDeps()
		if link.DependencyIndex < 0 || link.DependencyIndex >= len(deps) {
			return fmt.Errorf("closure: chain link %d has invalid dependency index", i)
		}
		dep := deps[link.DependencyIndex]
		if !conditionSatisfied(mergedCondition(dep.conditions()), evidence.Config) {
			return fmt.Errorf("closure: chain link %d condition is false under evidence config", i)
		}
		ref := depRef{owner: current, index: link.DependencyIndex}
		record, ok := choices[ref]
		if !ok || !record.choice.Active || record.choice.Selected != link.To {
			return fmt.Errorf("closure: chain link %d is not reproduced by an active concrete choice", i)
		}
		if !hasAlternative(dep, link.To) {
			return fmt.Errorf("closure: chain link %d uses %s not listed as alternative", i, link.To)
		}
		current = link.To
		if i < len(evidence.IdentityChain)-1 && evidence.IdentityChain[i+1] != current {
			return fmt.Errorf("closure: identity chain and edge chain disagree at %d", i)
		}
	}
	if current != evidence.Unproven || !visited[evidence.Unproven] {
		return fmt.Errorf("closure: unproven identity %s is not reachable through reproduced choices", evidence.Unproven)
	}
	if len(evidence.Selection) != len(visited) {
		return fmt.Errorf("closure: selection has %d identities, replayed closure has %d", len(evidence.Selection), len(visited))
	}
	for _, id := range evidence.Selection {
		if !visited[id] {
			return fmt.Errorf("closure: selected identity %s is not root-reachable", id)
		}
	}
	expectedChoices := 0
	for id := range visited {
		expectedChoices += len(packages[id].canonicalDeps())
	}
	if len(choices) != expectedChoices {
		return fmt.Errorf("closure: evidence has %d choices, replayed closure requires %d", len(choices), expectedChoices)
	}

	if req.TrustRoot && evidence.Unproven == evidence.Root {
		return fmt.Errorf("closure: evidence root is explicitly trusted")
	}
	for _, att := range req.Attestations {
		if att.canonicalID() != evidence.Unproven {
			continue
		}
		if att.generation() != req.PolicyGeneration {
			continue
		}
		if conditionSatisfied(mergedCondition(att.conditions()), evidence.Config) {
			return fmt.Errorf("closure: %s is actually proven by an applicable attestation", evidence.Unproven)
		}
	}
	return nil
}

// CheckEvidence is a Boolean wrapper around VerifyEvidence.
func CheckEvidence(input any, evidence Evidence) bool {
	return VerifyEvidence(input, evidence) == nil
}

// Verify is the method form of VerifyEvidence.
func (e Evidence) Verify(input any) error { return VerifyEvidence(input, e) }

// Check is the Boolean method form of VerifyEvidence.
func (e Evidence) Check(input any) bool { return VerifyEvidence(input, e) == nil }

func provenAtConfig(req Request, rootSet map[Identity]bool, id Identity, config Assignment) bool {
	if req.TrustRoot && rootSet[id] {
		return true
	}
	for _, att := range req.Attestations {
		if att.canonicalID() != id || att.generation() != req.PolicyGeneration {
			continue
		}
		if conditionSatisfied(mergedCondition(att.conditions()), config) {
			return true
		}
	}
	return false
}

type fixedState struct {
	root     Identity
	bound    map[string]Identity
	selected map[Identity]bool
	order    []Identity
	chosen   map[depRef]Identity
	inactive map[depRef]bool
}

var errBadResolution = errors.New("closure: bad resolution found")

// ClassifyConfiguration independently classifies one complete flag assignment
// without constructing or evaluating BDDs. It is the <=6-variable oracle.
func ClassifyConfiguration(input any, config Assignment) (RegionName, error) {
	req, err := normalizeInput(input)
	if err != nil {
		return "", err
	}
	if config == nil {
		config = Assignment{}
	}
	if !clausesSatisfied(req, config) {
		return RegionDisallowed, nil
	}
	packages := map[Identity]*Package{}
	for i := range req.Packages {
		pkg := &req.Packages[i]
		packages[pkg.ID] = pkg
	}
	roots := resolveRoots(req, packages)
	if len(roots) == 0 {
		return RegionUnbuildable, nil
	}
	rootSet := map[Identity]bool{}
	for _, root := range roots {
		rootSet[root] = true
	}

	legal := false
	for _, root := range roots {
		state := &fixedState{
			root:     root,
			bound:    map[string]Identity{root.Name: root},
			selected: map[Identity]bool{root: true},
			order:    []Identity{root},
			chosen:   map[depRef]Identity{},
			inactive: map[depRef]bool{},
		}
		err := classifyFixed(req, packages, rootSet, config, state, &legal)
		if errors.Is(err, errBadResolution) {
			return RegionReject, nil
		}
		if err != nil {
			return "", err
		}
	}
	if legal {
		return RegionPass, nil
	}
	return RegionUnbuildable, nil
}

func cloneFixed(s *fixedState) *fixedState {
	bound := map[string]Identity{}
	selected := map[Identity]bool{}
	chosen := map[depRef]Identity{}
	inactive := map[depRef]bool{}
	for k, v := range s.bound {
		bound[k] = v
	}
	for k, v := range s.selected {
		selected[k] = v
	}
	for k, v := range s.chosen {
		chosen[k] = v
	}
	for k, v := range s.inactive {
		inactive[k] = v
	}
	return &fixedState{
		root:     s.root,
		bound:    bound,
		selected: selected,
		order:    append([]Identity(nil), s.order...),
		chosen:   chosen,
		inactive: inactive,
	}
}

func nextFixedDependency(packages map[Identity]*Package, s *fixedState) (depRef, Dependency, bool) {
	for _, owner := range s.order {
		pkg := packages[owner]
		if pkg == nil {
			continue
		}
		for index, dep := range pkg.canonicalDeps() {
			ref := depRef{owner: owner, index: index}
			if _, ok := s.chosen[ref]; ok || s.inactive[ref] {
				continue
			}
			return ref, dep, true
		}
	}
	return depRef{}, Dependency{}, false
}

func classifyFixed(req Request, packages map[Identity]*Package, rootSet map[Identity]bool, config Assignment, state *fixedState, legal *bool) error {
	ref, dep, found := nextFixedDependency(packages, state)
	if !found {
		*legal = true
		for _, id := range state.order {
			if !provenAtConfig(req, rootSet, id, config) {
				return errBadResolution
			}
		}
		return nil
	}

	condition := mergedCondition(dep.conditions())
	if !conditionSatisfied(condition, config) {
		child := cloneFixed(state)
		child.inactive[ref] = true
		return classifyFixed(req, packages, rootSet, config, child, legal)
	}

	for _, alternative := range dep.canonicalAlternatives() {
		if _, ok := packages[alternative]; !ok {
			continue
		}
		if bound, ok := state.bound[alternative.Name]; ok && bound != alternative {
			continue
		}
		child := cloneFixed(state)
		child.chosen[ref] = alternative
		child.bound[alternative.Name] = alternative
		if !child.selected[alternative] {
			child.selected[alternative] = true
			child.order = append(child.order, alternative)
		}
		if err := classifyFixed(req, packages, rootSet, config, child, legal); err != nil {
			if errors.Is(err, errBadResolution) {
				return err
			}
			return err
		}
	}
	return nil
}

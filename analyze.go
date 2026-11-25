package closure

import (
	"errors"
	"fmt"
	"sort"
)

type depRef struct {
	owner Identity
	index int
}

type analysis struct {
	req   Request
	g     *Graph
	pkgs  map[Identity]*Package
	roots []Identity

	conditionCache map[string]NodeID
	depCondition   map[depRef]NodeID
	proof          map[Identity]NodeID
	rootSet        map[Identity]bool

	allowed   NodeID
	buildable NodeID
	reject    NodeID

	evidence     []Evidence
	evidenceSeen map[string]bool
}

type resolveState struct {
	root     Identity
	bound    map[string]Identity
	selected map[Identity]bool
	order    []Identity
	chosen   map[depRef]Identity
	inactive map[depRef]bool
	support  NodeID
}

// Analyze performs closure acceptance. Budget exhaustion is returned as a
// decided=false Report with StatusUndecided and a wrapping Cause, not as a
// partially valid diagram set. Input/usage errors return a nil report.
func Analyze(input any) (*Report, error) {
	req, err := normalizeInput(input)
	if err != nil {
		return nil, err
	}
	g, err := NewGraph(req.VariableOrder, req.NodeBudget)
	if err != nil {
		return nil, err
	}

	packages := make(map[Identity]*Package, len(req.Packages))
	for i := range req.Packages {
		pkg := &req.Packages[i]
		packages[pkg.ID] = pkg
	}
	roots := resolveRoots(req, packages)
	a := &analysis{
		req:            req,
		g:              g,
		pkgs:           packages,
		roots:          roots,
		conditionCache: map[string]NodeID{},
		depCondition:   map[depRef]NodeID{},
		proof:          map[Identity]NodeID{},
		rootSet:        map[Identity]bool{},
		evidenceSeen:   map[string]bool{},
		buildable:      False,
		reject:         False,
	}
	for _, root := range roots {
		a.rootSet[root] = true
	}

	if err := a.buildDependencyConditions(); err != nil {
		return undecidedOrError(g, err)
	}
	if err := a.buildProofBDDs(); err != nil {
		return undecidedOrError(g, err)
	}
	allowed, err := a.buildAllowed()
	if err != nil {
		return undecidedOrError(g, err)
	}
	a.allowed = allowed

	for _, root := range roots {
		state := &resolveState{
			root:     root,
			bound:    map[string]Identity{root.Name: root},
			selected: map[Identity]bool{root: true},
			order:    []Identity{root},
			chosen:   map[depRef]Identity{},
			inactive: map[depRef]bool{},
			support:  allowed,
		}
		if err := a.search(state); err != nil {
			return undecidedOrError(g, err)
		}
	}

	notBuildable, err := g.Not(a.buildable)
	if err != nil {
		return undecidedOrError(g, err)
	}
	unbuildable, err := g.And(a.allowed, notBuildable)
	if err != nil {
		return undecidedOrError(g, err)
	}
	notReject, err := g.Not(a.reject)
	if err != nil {
		return undecidedOrError(g, err)
	}
	pass, err := g.AndMany(a.allowed, a.buildable, notReject)
	if err != nil {
		return undecidedOrError(g, err)
	}

	return &Report{
		Status:      StatusDecided,
		Graph:       g,
		Pass:        pass,
		Reject:      a.reject,
		Unbuildable: unbuildable,
		Allowed:     a.allowed,
		NodeCount:   g.NodeCount(),
		Budget:      g.Budget(),
		Evidence:    a.evidence,
	}, nil
}

// AnalyzeRequest is a strongly typed convenience wrapper.
func AnalyzeRequest(req Request) (*Report, error) { return Analyze(req) }

// Accept is a host-friendly alias for Analyze.
func Accept(input any) (*Report, error) { return Analyze(input) }

// Evaluate is another host-friendly alias.
func Evaluate(input any) (*Report, error) { return Analyze(input) }

func undecidedOrError(g *Graph, err error) (*Report, error) {
	var budgetErr *BudgetError
	if errors.As(err, &budgetErr) || errors.Is(err, ErrBudgetExceeded) {
		return &Report{
			Status:         StatusUnknown,
			Undecided:      true,
			Unknown:        true,
			BudgetExceeded: true,
			Cause:          err,
			Graph:          g,
			Budget:         g.Budget(),
			NodeCount:      g.NodeCount(),
		}, nil
	}
	return nil, err
}

func (a *analysis) conditionNode(cond Condition) (NodeID, error) {
	key := canonicalConditionKey(cond)
	if node, ok := a.conditionCache[key]; ok {
		return node, nil
	}
	flags := make([]string, 0, len(cond))
	for flag := range cond {
		flags = append(flags, flag)
	}
	sort.Strings(flags)
	roots := make([]NodeID, 0, len(flags))
	for _, flag := range flags {
		node, err := a.g.Literal(Literal{Flag: flag, Negated: !cond[flag]})
		if err != nil {
			return 0, err
		}
		roots = append(roots, node)
	}
	node, err := a.g.AndMany(roots...)
	if err != nil {
		return 0, err
	}
	a.conditionCache[key] = node
	return node, nil
}

func canonicalConditionKey(cond Condition) string {
	flags := make([]string, 0, len(cond))
	for flag := range cond {
		flags = append(flags, flag)
	}
	sort.Strings(flags)
	out := ""
	for _, flag := range flags {
		value := 0
		if cond[flag] {
			value = 1
		}
		out += fmt.Sprintf("%s=%d;", flag, value)
	}
	return out
}

func (a *analysis) buildDependencyConditions() error {
	for id, pkg := range a.pkgs {
		for index, dep := range pkg.canonicalDeps() {
			node, err := a.conditionNode(mergedCondition(dep.conditions()))
			if err != nil {
				return err
			}
			a.depCondition[depRef{owner: id, index: index}] = node
		}
	}
	return nil
}

func (a *analysis) buildProofBDDs() error {
	for id := range a.pkgs {
		if a.req.TrustRoot && a.rootSet[id] {
			a.proof[id] = True
			continue
		}
		conditions := make([]NodeID, 0)
		for _, att := range a.req.Attestations {
			if att.canonicalID() != id {
				continue
			}
			if att.generation() != a.req.PolicyGeneration {
				continue
			}
			node, err := a.conditionNode(mergedCondition(att.conditions()))
			if err != nil {
				return err
			}
			conditions = append(conditions, node)
		}
		node, err := a.g.OrMany(conditions...)
		if err != nil {
			return err
		}
		a.proof[id] = node
	}
	return nil
}

func (a *analysis) buildAllowed() (NodeID, error) {
	allowed := True
	for clauseIndex, clause := range a.req.Clauses {
		var literals []NodeID
		for _, literal := range clause {
			name := literal.flagName()
			if name == "" {
				continue
			}
			node, err := a.g.Literal(literal)
			if err != nil {
				return 0, err
			}
			literals = append(literals, node)
		}
		if len(literals) == 0 {
			return 0, fmt.Errorf("closure: global clause %d is empty; binary clauses need at least one literal", clauseIndex)
		}
		clauseNode, err := a.g.OrMany(literals...)
		if err != nil {
			return 0, err
		}
		allowed, err = a.g.And(allowed, clauseNode)
		if err != nil {
			return 0, err
		}
	}
	return allowed, nil
}

func (s *resolveState) clone() *resolveState {
	bound := make(map[string]Identity, len(s.bound)+1)
	for k, v := range s.bound {
		bound[k] = v
	}
	selected := make(map[Identity]bool, len(s.selected)+1)
	for k, v := range s.selected {
		selected[k] = v
	}
	chosen := make(map[depRef]Identity, len(s.chosen)+1)
	for k, v := range s.chosen {
		chosen[k] = v
	}
	inactive := make(map[depRef]bool, len(s.inactive)+1)
	for k, v := range s.inactive {
		inactive[k] = v
	}
	return &resolveState{
		root:     s.root,
		bound:    bound,
		selected: selected,
		order:    append([]Identity(nil), s.order...),
		chosen:   chosen,
		inactive: inactive,
		support:  s.support,
	}
}

func (a *analysis) nextDependency(s *resolveState) (depRef, Dependency, bool) {
	for _, owner := range s.order {
		pkg := a.pkgs[owner]
		if pkg == nil {
			continue
		}
		for index, dep := range pkg.canonicalDeps() {
			ref := depRef{owner: owner, index: index}
			if _, ok := s.chosen[ref]; ok {
				continue
			}
			if s.inactive[ref] {
				continue
			}
			return ref, dep, true
		}
	}
	return depRef{}, Dependency{}, false
}

func (a *analysis) search(s *resolveState) error {
	ref, dep, found := a.nextDependency(s)
	if !found {
		return a.complete(s)
	}

	condition := a.depCondition[ref]
	notCondition, err := a.g.Not(condition)
	if err != nil {
		return err
	}
	inactiveSupport, err := a.g.And(s.support, notCondition)
	if err != nil {
		return err
	}
	if inactiveSupport != False {
		child := s.clone()
		child.support = inactiveSupport
		child.inactive[ref] = true
		if err := a.search(child); err != nil {
			return err
		}
	}

	activeSupport, err := a.g.And(s.support, condition)
	if err != nil {
		return err
	}
	if activeSupport == False {
		return nil
	}

	for _, alternative := range dep.canonicalAlternatives() {
		pkg, exists := a.pkgs[alternative]
		if !exists || pkg == nil {
			continue
		}
		if bound, ok := s.bound[alternative.Name]; ok && bound != alternative {
			continue
		}

		child := s.clone()
		child.support = activeSupport
		child.chosen[ref] = alternative
		child.bound[alternative.Name] = alternative
		if !child.selected[alternative] {
			child.selected[alternative] = true
			child.order = append(child.order, alternative)
		}
		if err := a.search(child); err != nil {
			return err
		}
	}
	return nil
}

func (a *analysis) complete(s *resolveState) error {
	nextBuildable, err := a.g.Or(a.buildable, s.support)
	if err != nil {
		return err
	}
	a.buildable = nextBuildable

	for _, id := range s.order {
		proof := a.proof[id]
		unproven, err := a.g.Not(proof)
		if err != nil {
			return err
		}
		bad, err := a.g.And(s.support, unproven)
		if err != nil {
			return err
		}
		if bad == False {
			continue
		}
		region, err := a.g.And(a.allowed, bad)
		if err != nil {
			return err
		}
		if region == False {
			continue
		}
		config, ok := a.g.OneAssignment(region)
		if !ok {
			continue
		}

		evidence := a.makeEvidence(s, id, region, config)
		key := evidenceKey(evidence)
		if !a.evidenceSeen[key] {
			a.evidenceSeen[key] = true
			a.evidence = append(a.evidence, evidence)
		}
		a.reject, err = a.g.Or(a.reject, region)
		if err != nil {
			return err
		}
	}
	return nil
}

func evidenceKey(ev Evidence) string {
	key := ev.Root.String() + "|" + ev.Unproven.String()
	for _, choice := range ev.Choices {
		status := "0"
		if choice.Active {
			status = "1:" + choice.Selected.String()
		}
		key += "|" + choice.Owner.String() + "#" + itoa(choice.DependencyIndex) + "=" + status
	}
	flags := make([]string, 0, len(ev.Config))
	for flag := range ev.Config {
		flags = append(flags, flag)
	}
	sort.Strings(flags)
	for _, flag := range flags {
		value := "0"
		if ev.Config[flag] {
			value = "1"
		}
		key += "|" + flag + "=" + value
	}
	return key
}

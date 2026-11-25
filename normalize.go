package closure

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

func firstNonEmptyPackages(groups ...[]Package) []Package {
	for _, group := range groups {
		if len(group) > 0 {
			return group
		}
	}
	return nil
}

func firstNonEmptyAttestations(groups ...[]Attestation) []Attestation {
	for _, group := range groups {
		if len(group) > 0 {
			return group
		}
	}
	return nil
}

func firstNonEmptyStrings(groups ...[]string) []string {
	for _, group := range groups {
		if len(group) > 0 {
			return group
		}
	}
	return nil
}

func firstNonZeroInt(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func (p Package) canonicalID() Identity {
	if !p.ID.empty() {
		return p.ID
	}
	if !p.Identity.empty() {
		return p.Identity
	}
	return Identity{Name: p.Name, Version: p.Version, Source: p.Source, Digest: p.Digest}
}

func (p Package) canonicalDeps() []Dependency {
	if len(p.Deps) > 0 {
		return p.Deps
	}
	return p.Dependencies
}

func (d Dependency) canonicalAlternatives() []Identity {
	for _, group := range [][]Identity{d.Alternatives, d.Alts, d.Options} {
		if len(group) > 0 {
			return group
		}
	}
	return nil
}

func (d Dependency) conditions() []Condition {
	var out []Condition
	if len(d.When) > 0 {
		out = append(out, d.When)
	}
	if len(d.Condition) > 0 {
		out = append(out, d.Condition)
	}
	if d.Flag != "" {
		out = append(out, Condition{d.Flag: !d.Negated})
	}
	return out
}

func (a Attestation) canonicalID() Identity {
	for _, id := range []Identity{a.ID, a.Identity, a.Subject, a.Package} {
		if !id.empty() {
			return id
		}
	}
	return Identity{}
}

func (a Attestation) conditions() []Condition {
	var out []Condition
	if len(a.Condition) > 0 {
		out = append(out, a.Condition)
	}
	if len(a.When) > 0 {
		out = append(out, a.When)
	}
	return out
}

func (a Attestation) generation() uint64 {
	if a.PolicyGeneration != 0 {
		return a.PolicyGeneration
	}
	if a.Generation != 0 {
		return a.Generation
	}
	return a.Gen
}

func normalizeInput(input any) (Request, error) {
	switch value := input.(type) {
	case nil:
		return Request{}, fmt.Errorf("closure: nil analysis request")
	case Request:
		return normalizeRequest(value)
	case *Request:
		if value == nil {
			return Request{}, fmt.Errorf("closure: nil analysis request")
		}
		return normalizeRequest(*value)
	case []byte:
		var req Request
		if err := json.Unmarshal(value, &req); err != nil {
			return Request{}, fmt.Errorf("closure: decode JSON request: %w", err)
		}
		return normalizeRequest(req)
	case json.RawMessage:
		return normalizeInput([]byte(value))
	default:
		return Request{}, fmt.Errorf("closure: unsupported input type %T; want Request, *Request, or JSON", input)
	}
}

func normalizeRequest(req Request) (Request, error) {
	req.VariableOrder = firstNonEmptyStrings(req.VariableOrder, req.Order, req.Flags, req.BuildFlags)
	req.Packages = firstNonEmptyPackages(req.Packages, req.Candidates, req.Table)
	req.Candidates = req.Packages
	req.Attestations = firstNonEmptyAttestations(req.Attestations, req.Provenances, req.Proofs)
	req.Provenances = req.Attestations
	if len(req.Clauses) == 0 && len(req.Constraints) > 0 {
		req.Clauses = req.Constraints
	}
	req.Constraints = req.Clauses

	if req.PolicyGeneration == 0 {
		req.PolicyGeneration = req.Generation
	}
	req.Generation = req.PolicyGeneration

	req.NodeBudget = firstNonZeroInt(req.NodeBudget, req.Budget, req.MaxNodes)
	req.Budget = req.NodeBudget
	req.MaxNodes = req.NodeBudget

	requireRootProof := req.RequireRootProof || req.RequireRootAttestation || req.DoNotTrustRoot
	req.TrustRoot = !requireRootProof || req.TrustRoot || req.RootTrusted || req.SkipRootProof

	if req.Root.empty() {
		for _, candidate := range []Identity{req.RootID, req.Target, req.RootIdentity} {
			if !candidate.empty() {
				req.Root = candidate
				break
			}
		}
	}
	if req.RootName == "" {
		req.RootName = req.RootPackage
	}
	if req.RootDigest == "" {
		req.RootDigest = req.TargetDigest
	}
	if req.RootDigest == "" {
		req.RootDigest = req.Digest
	}
	if req.RootName == "" {
		req.RootName = req.Root.Name
	}
	if req.RootDigest == "" {
		req.RootDigest = req.Root.Digest
	}

	if req.RootName == "" && req.RootDigest == "" {
		return Request{}, fmt.Errorf("closure: root identity or target digest is required")
	}

	flags, err := collectFlags(req)
	if err != nil {
		return Request{}, err
	}
	if len(req.VariableOrder) == 0 {
		sort.Strings(flags)
		req.VariableOrder = flags
	}
	if len(req.VariableOrder) > 24 {
		return Request{}, fmt.Errorf("closure: at most 24 build flags are allowed, got %d", len(req.VariableOrder))
	}
	seen := map[string]bool{}
	for _, name := range req.VariableOrder {
		if strings.TrimSpace(name) == "" {
			return Request{}, fmt.Errorf("closure: variable order contains an empty flag name")
		}
		if seen[name] {
			return Request{}, fmt.Errorf("closure: duplicate flag %q in variable order", name)
		}
		seen[name] = true
	}
	for _, name := range flags {
		if !seen[name] {
			return Request{}, fmt.Errorf("closure: flag %q is used but absent from variable order %v", name, req.VariableOrder)
		}
	}

	ids := map[Identity]bool{}
	for i := range req.Packages {
		pkg := &req.Packages[i]
		id := pkg.canonicalID()
		if id.Name == "" {
			return Request{}, fmt.Errorf("closure: package at index %d has an empty name", i)
		}
		if id.Digest == "" {
			return Request{}, fmt.Errorf("closure: package %q v%d source=%q has an empty digest", id.Name, id.Version, id.Source)
		}
		pkg.ID = id
		if ids[id] {
			return Request{}, fmt.Errorf("closure: duplicate exact package identity %s", id)
		}
		ids[id] = true
		deps := pkg.canonicalDeps()
		for j, dep := range deps {
			if len(dep.canonicalAlternatives()) == 0 && dep.Name == "" {
				return Request{}, fmt.Errorf("closure: dependency %d of %s has neither name nor alternatives", j, id)
			}
		}
	}
	return req, nil
}

func collectFlags(req Request) ([]string, error) {
	set := map[string]bool{}
	add := func(name string) error {
		if name == "" {
			return fmt.Errorf("closure: empty build flag name in condition or clause")
		}
		set[name] = true
		return nil
	}
	addCondition := func(cond Condition) error {
		for name := range cond {
			if err := add(name); err != nil {
				return err
			}
		}
		return nil
	}

	for _, name := range firstNonEmptyStrings(req.VariableOrder, req.Order, req.Flags, req.BuildFlags) {
		if err := add(name); err != nil {
			return nil, err
		}
	}
	for _, pkg := range req.Packages {
		id := pkg.canonicalID()
		for _, dep := range pkg.canonicalDeps() {
			if dep.Name == "" && len(dep.canonicalAlternatives()) > 0 {
				dep.Name = dep.canonicalAlternatives()[0].Name
			}
			for _, cond := range dep.conditions() {
				if err := addCondition(cond); err != nil {
					return nil, err
				}
			}
			if dep.Flag != "" {
				if err := add(dep.Flag); err != nil {
					return nil, err
				}
			}
			_ = id
		}
	}
	for _, att := range req.Attestations {
		for _, cond := range att.conditions() {
			if err := addCondition(cond); err != nil {
				return nil, err
			}
		}
	}
	for _, clause := range req.Clauses {
		for _, lit := range clause {
			name := lit.flagName()
			if name == "" {
				continue
			}
			if err := add(name); err != nil {
				return nil, err
			}
		}
	}
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

func resolveRoots(req Request, packages map[Identity]*Package) []Identity {
	var roots []Identity
	seen := map[Identity]bool{}
	add := func(id Identity) {
		if id.Name == "" || id.Digest == "" {
			return
		}
		if _, ok := packages[id]; !ok {
			return
		}
		if !seen[id] {
			seen[id] = true
			roots = append(roots, id)
		}
	}

	selector := req.Root
	if selector.empty() {
		selector = req.RootID
	}
	if selector.empty() {
		selector = req.Target
	}
	if selector.empty() {
		selector = req.RootIdentity
	}

	if !selector.empty() {
		if _, exact := packages[selector]; exact && selector.Name != "" && selector.Digest != "" {
			add(selector)
		}
		for id := range packages {
			if selector.Name != "" && id.Name != selector.Name {
				continue
			}
			if selector.Digest != "" && id.Digest != selector.Digest {
				continue
			}
			if selector.Source != "" && id.Source != selector.Source {
				continue
			}
			if selector.Version != 0 && id.Version != selector.Version {
				continue
			}
			add(id)
		}
	}

	name := req.RootName
	digest := req.RootDigest
	if name != "" || digest != "" {
		for id := range packages {
			if name != "" && id.Name != name {
				continue
			}
			if digest != "" && id.Digest != digest {
				continue
			}
			add(id)
		}
	}

	sort.Slice(roots, func(i, j int) bool { return roots[i].String() < roots[j].String() })
	return roots
}

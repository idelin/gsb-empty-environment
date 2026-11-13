package netsec

import (
	"fmt"
	"sort"
)

// HijackStep is one hypothetical attacker object creation that would shadow a
// frozen dependency if the dependency were resolved by name again.
type HijackStep struct {
	FunctionID         FunctionID
	Path               string
	Ref                FrozenRef
	CandidateSchemaID  SchemaID
	CandidateSearchIdx int
	Attacker           PrincipalID
	ObjectID           ObjectID
	Version            int64
	Kind               ObjectKind
	Name               string
	Signature          string
}

// HijackEvidence is the shortest exploit chain found. Hops names the chain of
// SECURITY DEFINER functions from the entry point to the function containing
// the hijackable reference. Steps contains the terminal malicious insertion.
type HijackEvidence struct {
	Attacker PrincipalID
	Hops     []FunctionID
	CallPath []FrozenRef
	Steps    []HijackStep
}

// Empty reports whether no evidence was found.
func (h HijackEvidence) Empty() bool { return len(h.Steps) == 0 }

// ShortestHijackEvidence searches this function for the shortest shadowing
// proof. A direct dependency yields one hop. Registered nested definer
// functions are followed by Engine.ShortestHijackEvidence.
func (p *PublishedFunction) ShortestHijackEvidence(c *Catalog, attacker PrincipalID) (HijackEvidence, bool) {
	if p == nil {
		return HijackEvidence{}, false
	}
	steps := directHijackSteps(p, c, attacker)
	if len(steps) == 0 {
		return HijackEvidence{}, false
	}
	return HijackEvidence{
		Attacker: attacker,
		Hops:     []FunctionID{p.ID},
		Steps:    steps[:1],
	}, true
}

// DirectHijackCandidates returns every earlier attacker-writable namespace
// in which the attacker can create a same-name/same-signature object. They
// are sorted by search-path index.
func (p *PublishedFunction) DirectHijackCandidates(c *Catalog, attacker PrincipalID) []HijackStep {
	return directHijackSteps(p, c, attacker)
}

// FindShortestHijack audits a definition without applying the secure
// publication policy. It is useful for threat modeling a catalog in which an
// attacker already owns a schema: the normal Publish path would reject such a
// search path, but the shortest shadowing proof can still be reported.
//
// The returned proof is one step: either an attacker-writable schema resolves
// the reference itself, or an earlier attacker-writable schema can be seeded
// with a same-name/same-signature function or operator.
func FindShortestHijack(c *Catalog, def FunctionDefinition, attacker PrincipalID) (HijackEvidence, bool, error) {
	if c == nil {
		return HijackEvidence{}, false, fmt.Errorf("%w: nil catalog", ErrInvalidCatalog)
	}
	if def.DefinerID == "" {
		return HijackEvidence{}, false, fmt.Errorf("%w: empty definer ID", ErrInvalidDefinition)
	}
	if attacker == "" || attacker == def.DefinerID {
		return HijackEvidence{}, false, nil
	}
	if len(def.SearchPath) == 0 {
		return HijackEvidence{}, false, fmt.Errorf("%w: empty search path", ErrInvalidSearchPath)
	}
	seen := make(map[SchemaID]bool, len(def.SearchPath))
	for i, id := range def.SearchPath {
		if id == "" || seen[id] {
			return HijackEvidence{}, false, fmt.Errorf("%w: bad schema ID at position %d", ErrInvalidSearchPath, i)
		}
		if id == CallerSearchPath {
			return HijackEvidence{}, false, fmt.Errorf("%w: caller search path marker", ErrTemporarySchema)
		}
		schema, ok := c.Schema(id)
		if !ok {
			return HijackEvidence{}, false, fmt.Errorf("%w: %q is not a schema ID", ErrInvalidSearchPath, id)
		}
		if schema.isTemporary() {
			return HijackEvidence{}, false, fmt.Errorf("%w: schema %q", ErrTemporarySchema, id)
		}
		seen[id] = true
	}
	if def.ID == "" {
		def.ID = FunctionID(string(def.SchemaID) + "." + def.Name)
	}

	audit := &PublishedFunction{
		ID:         def.ID,
		SearchPath: append([]SchemaID(nil), def.SearchPath...),
		frozen:     make(map[string]FrozenRef),
		dynamic:    make(map[string]DynamicSite),
		definer:    def.DefinerID,
		searchPath: append([]SchemaID(nil), def.SearchPath...),
	}
	if err := c.freezeAST(audit, def.AST, "body"); err != nil {
		return HijackEvidence{}, false, err
	}
	audit.deps = append([]Dependency(nil), audit.Dependencies...)
	for i := range audit.deps {
		audit.deps[i].Candidates = append([]ResolutionCandidate(nil), audit.Dependencies[i].Candidates...)
	}

	steps := directHijackSteps(audit, c, attacker)
	for _, dep := range audit.Dependencies {
		ref := dep.Ref
		// A resolved object in an attacker-writable schema is already a
		// hijack even when the attacker did not get a chance to shadow it
		// with an earlier candidate.
		if ref.SearchIndex < 0 || ref.SearchIndex >= len(def.SearchPath) {
			continue
		}
		schema, ok := c.Schema(def.SearchPath[ref.SearchIndex])
		if !ok || schema.isTemporary() || !schema.CanWrite(attacker) {
			continue
		}
		steps = append(steps, newHijackStep(audit, ref, schema, ref.SearchIndex, attacker))
	}
	if len(steps) == 0 {
		return HijackEvidence{}, false, nil
	}
	sort.Slice(steps, func(i, j int) bool {
		if steps[i].CandidateSearchIdx != steps[j].CandidateSearchIdx {
			return steps[i].CandidateSearchIdx < steps[j].CandidateSearchIdx
		}
		return steps[i].Path < steps[j].Path
	})
	return HijackEvidence{
		Attacker: attacker,
		Hops:     []FunctionID{audit.ID},
		Steps:    steps[:1],
	}, true, nil
}

func directHijackSteps(p *PublishedFunction, c *Catalog, attacker PrincipalID) []HijackStep {
	if p == nil || c == nil || attacker == "" || attacker == p.definer {
		return nil
	}
	var out []HijackStep
	for _, dep := range p.deps {
		ref := dep.Ref
		// Qualification pins the reference to one schema; earlier
		// namespaces cannot shadow it.
		if ref.Qualified || ref.SearchIndex <= 0 {
			continue
		}
		for i := 0; i < ref.SearchIndex && i < len(p.searchPath); i++ {
			schema, ok := c.Schema(p.searchPath[i])
			if !ok || schema.isTemporary() || !schema.CanWrite(attacker) {
				continue
			}
			out = append(out, newHijackStep(p, ref, schema, i, attacker))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CandidateSearchIdx != out[j].CandidateSearchIdx {
			return out[i].CandidateSearchIdx < out[j].CandidateSearchIdx
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].CandidateSchemaID < out[j].CandidateSchemaID
	})
	return out
}

func newHijackStep(p *PublishedFunction, ref FrozenRef, schema Schema, index int, attacker PrincipalID) HijackStep {
	// This ID represents the object the attacker would create. It is marked
	// with the schema so evidence remains unambiguous even for overloads.
	fakeID := ObjectID(fmt.Sprintf("hijack:%s:%s:%s:%s", schema.ID, ref.Kind, ref.Name, ref.Signature))
	return HijackStep{
		FunctionID:         p.ID,
		Path:               ref.Path,
		Ref:                ref,
		CandidateSchemaID:  schema.ID,
		CandidateSearchIdx: index,
		Attacker:           attacker,
		ObjectID:           fakeID,
		Version:            1,
		Kind:               ref.Kind,
		Name:               ref.Name,
		Signature:          ref.Signature,
	}
}

// ShortestHijackEvidence searches every registered function and follows
// nested definer calls. BFS minimizes the number of definer-function hops;
// ties prefer the earliest attacker-writable search-path position.
func (e *Engine) ShortestHijackEvidence(attacker PrincipalID) (HijackEvidence, bool) {
	if e == nil || e.Catalog == nil {
		return HijackEvidence{}, false
	}
	starts := make([]*PublishedFunction, 0, len(e.functionsByID))
	for _, pub := range e.functionsByID {
		starts = append(starts, pub)
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i].ID < starts[j].ID })
	return e.ShortestHijackEvidenceFrom(starts, attacker)
}

// ShortestHijackEvidenceFrom runs the shortest-proof search from an explicit
// set of entry points.
func (e *Engine) ShortestHijackEvidenceFrom(starts []*PublishedFunction, attacker PrincipalID) (HijackEvidence, bool) {
	if e == nil || e.Catalog == nil || attacker == "" {
		return HijackEvidence{}, false
	}

	type queueItem struct {
		pub      *PublishedFunction
		hops     []FunctionID
		callPath []FrozenRef
		visited  map[FunctionID]bool
	}
	queue := make([]queueItem, 0, len(starts))
	for _, pub := range starts {
		if pub == nil {
			continue
		}
		queue = append(queue, queueItem{
			pub:     pub,
			hops:    []FunctionID{pub.ID},
			visited: map[FunctionID]bool{pub.ID: true},
		})
	}

	best := HijackEvidence{}
	found := false
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]

		// A longer chain can never beat the best evidence found at an
		// earlier BFS level.
		if found && len(item.hops) > len(best.Hops) {
			continue
		}

		steps := directHijackSteps(item.pub, e.Catalog, attacker)
		if len(steps) > 0 {
			candidate := HijackEvidence{
				Attacker: attacker,
				Hops:     append([]FunctionID(nil), item.hops...),
				CallPath: append([]FrozenRef(nil), item.callPath...),
				Steps:    steps[:1],
			}
			if !found || shorterEvidence(candidate, best) {
				best = candidate
				found = true
			}
		}

		for _, dep := range item.pub.deps {
			if dep.Ref.Kind != KindFunction {
				continue
			}
			nested := e.functionsByObject[dep.Ref.ObjectID]
			if nested == nil || item.visited[nested.ID] {
				continue
			}
			visited := make(map[FunctionID]bool, len(item.visited)+1)
			for id := range item.visited {
				visited[id] = true
			}
			visited[nested.ID] = true
			queue = append(queue, queueItem{
				pub:      nested,
				hops:     append(append([]FunctionID(nil), item.hops...), nested.ID),
				callPath: append(append([]FrozenRef(nil), item.callPath...), dep.Ref),
				visited:  visited,
			})
		}
	}
	return best, found
}

func shorterEvidence(a, b HijackEvidence) bool {
	if len(a.Hops) != len(b.Hops) {
		return len(a.Hops) < len(b.Hops)
	}
	if a.Steps[0].CandidateSearchIdx != b.Steps[0].CandidateSearchIdx {
		return a.Steps[0].CandidateSearchIdx < b.Steps[0].CandidateSearchIdx
	}
	if a.Steps[0].Path != b.Steps[0].Path {
		return a.Steps[0].Path < b.Steps[0].Path
	}
	if len(a.Hops) > 0 && len(b.Hops) > 0 {
		for i := range a.Hops {
			if a.Hops[i] != b.Hops[i] {
				return a.Hops[i] < b.Hops[i]
			}
		}
	}
	return a.Steps[0].CandidateSchemaID < b.Steps[0].CandidateSchemaID
}

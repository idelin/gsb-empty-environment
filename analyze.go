package mvrewrite

import (
	"errors"
	"fmt"
	"sort"
)

// AnalyzeOptions controls deterministic finite validation attached to an
// analysis decision.
type AnalyzeOptions struct {
	Search SearchOptions
	// DifferentialModels bounds semantic enumeration for each candidate.
	DifferentialModels int
	// SkipDifferential disables bounded semantic cross-checking.  Structural
	// compensation synthesis still performs output and nullability checks.
	SkipDifferential bool
}

// DefaultAnalyzeOptions returns a small-model search suitable for unit and
// acceptance workloads.
func DefaultAnalyzeOptions() AnalyzeOptions {
	return AnalyzeOptions{Search: DefaultSearchOptions(), DifferentialModels: 50000}
}

// Analyze decides every view and returns decisions in view order.
func Analyze(catalog *Catalog, query *Query, views []*View) (*Report, error) {
	return AnalyzeWithOptions(catalog, query, views, DefaultAnalyzeOptions())
}

// Decide is a concise wrapper that takes a catalog-bearing query and variadic
// views.  It is intended for optimizer callers that construct specs in code.
func Decide(query *Query, views ...*View) []Decision {
	if query == nil {
		return []Decision{{Rewritable: false, Code: ReasonInvalidSpec, Reason: "nil query"}}
	}
	rep, err := AnalyzeWithOptions(query.Schema, query, views, DefaultAnalyzeOptions())
	if err != nil {
		return []Decision{{Rewritable: false, Code: ReasonInvalidSpec, Reason: err.Error()}}
	}
	return rep.Decisions
}

// AnalyzeWithOptions is Analyze with caller-controlled search bounds.
func AnalyzeWithOptions(catalog *Catalog, query *Query, views []*View, opts AnalyzeOptions) (*Report, error) {
	if query == nil || query.Root == nil {
		return nil, fmt.Errorf("mvrewrite: query and query.root are required")
	}
	if opts.DifferentialModels <= 0 {
		opts.DifferentialModels = 50000
	}
	normalizeSearch(&opts.Search)
	catalog = bindCatalogAliases(catalog, query, views)

	rep := &Report{QueryID: query.ID, Decisions: make([]Decision, 0, len(views))}
	for _, view := range views {
		rep.Decisions = append(rep.Decisions, analyzeOne(catalog, query, view, opts))
	}
	return rep, nil
}

func bindCatalogAliases(catalog *Catalog, q *Query, views []*View) *Catalog {
	if catalog == nil {
		return nil
	}
	aliases := map[string]string{}
	for k, v := range catalog.Aliases {
		aliases[k] = v
	}
	for _, s := range collectAllScans(q, views) {
		aliases[s.Alias] = s.Physical
	}
	return &Catalog{Tables: catalog.Tables, Aliases: aliases}
}

func analyzeOne(catalog *Catalog, q *Query, v *View, opts AnalyzeOptions) Decision {
	if v == nil || v.Root == nil {
		return Decision{ViewID: viewID(v), Rewritable: false, Code: ReasonInvalidSpec, Reason: "nil view or view.root"}
	}
	d := Decision{ViewID: v.ID}

	if err := validateSpec(catalog, q, v); err != nil {
		d.Code = ReasonInvalidSpec
		d.Reason = err.Error()
		d.Counterexample = buildValidationCounterexample(catalog, q, v, err)
		return d
	}

	missingCols := missingOutputs(q.Select, v.Select)
	if len(missingCols) > 0 {
		d.Code = ReasonMissingOutput
		d.Reason = fmt.Sprintf("view %q does not export required query/discriminator columns: %v", v.ID, refsStrings(missingCols))
		witness, report, err := FindQueryWitness(catalog, q, v, opts.Search)
		if err == nil && witness != nil {
			witness.Kind = ReasonMissingOutput
			witness.Description = d.Reason
			d.Counterexample = witness
			d.ExhaustiveCheck = report
		} else {
			d.Counterexample = buildMissingOutputCounterexample(catalog, q, v, missingCols)
			if report != nil {
				d.ExhaustiveCheck = report
			}
		}
		return d
	}

	candidates, synthErr := buildCompensationCandidates(catalog, q, v)
	if synthErr != nil && len(candidates) == 0 {
		d.Code = ReasonMissingDiscriminator
		d.Reason = synthErr.Error()
		d.Ambiguity = buildAmbiguityProof(catalog, q, v)
		cx, report := findFailureOrWitness(catalog, q, v, Compensation{Filter: NoFilter(), Project: q.Select}, opts)
		if cx != nil {
			cx.Kind = ReasonMissingDiscriminator
			if cx.Description == "" {
				cx.Description = synthErr.Error()
			} else {
				cx.Description = synthErr.Error() + "; concrete mismatch: " + cx.Description
			}
			d.Counterexample = cx
			d.RawCounterexample = cx
		} else {
			d.Counterexample = buildAmbiguityCounterexample(catalog, q, v, synthErr)
		}
		d.ExhaustiveCheck = report
		return d
	}

	var lastCX *Counterexample
	var lastReport *ExhaustiveReport
	var lastComp *Compensation
	var rawCX *Counterexample
	for _, comp := range candidates {
		comp.Project = append([]ColRef(nil), q.Select...)
		if opts.SkipDifferential {
			d.Rewritable = true
			d.Code = ReasonRewritable
			d.Reason = "structural compensation synthesized; differential search skipped"
			d.Compensation = &comp
			return d
		}
		searchOpts := withModelLimit(opts.Search, opts.DifferentialModels)
		cx, report, err := FindRewriteCounterexample(catalog, q, v, comp, searchOpts)
		if err != nil && cx == nil && errors.Is(err, ErrSearchBudget) {
			// The small-model search hit its model bound without finding a
			// concrete mismatch.  The synthesized certificate supplies the
			// universal argument for this supported predicate/join shape.
			lastReport = report
			if report != nil {
				report.Complete = false
			}
			d.Rewritable = true
			d.Code = ReasonRewritable
			d.Reason = "structural compensation synthesized; bounded semantic search did not refute it"
			d.Compensation = &comp
			d.ExhaustiveCheck = report
			return d
		}
		if err != nil && cx == nil {
			lastReport = report
			continue
		}
		lastReport = report
		if cx == nil {
			d.Rewritable = true
			d.Code = ReasonRewritable
			d.Reason = "compensation preserves all query answers and rejects all extra view rows"
			comp.SQL = comp.Filter.SQL()
			d.Compensation = &comp
			d.ExhaustiveCheck = report
			return d
		}
		if comp.Filter.Empty() {
			rawCX = cx
		}
		lastCX = cx
		compCopy := comp
		lastComp = &compCopy
	}

	// A structurally expressible filter was refuted.  First check whether the
	// view itself loses query rows; that has a more precise reason code.
	covCX, covReport, covErr := FindCoverageCounterexample(catalog, q, v, withModelLimit(opts.Search, opts.DifferentialModels))
	if covErr == nil && covCX != nil {
		d.Code = ReasonViewLosesRows
		d.Reason = "view loses a query answer because of an extra inner join, stronger predicate, or incompatible join shape"
		d.Counterexample = covCX
		d.RawCounterexample = rawCX
		d.ExhaustiveCheck = covReport
		return d
	}

	if synthErr != nil {
		d.Code = ReasonMissingDiscriminator
		d.Reason = synthErr.Error()
		d.Ambiguity = buildAmbiguityProof(catalog, q, v)
	} else {
		d.Code = ReasonCompensationMismatch
		d.Reason = "all synthesized compensation filters are refuted by finite set-semantic instances"
	}
	if lastCX != nil {
		d.Counterexample = lastCX
		d.AttemptedCompensation = lastComp
	} else {
		d.Counterexample = buildAmbiguityCounterexample(catalog, q, v, synthErr)
	}
	d.RawCounterexample = rawCX
	if covReport != nil {
		d.ExhaustiveCheck = covReport
	} else {
		d.ExhaustiveCheck = lastReport
	}
	return d
}

func viewID(v *View) string {
	if v == nil {
		return ""
	}
	return v.ID
}

func withModelLimit(opts SearchOptions, limit int) SearchOptions {
	if opts.MaxModels == 0 || opts.MaxModels > limit {
		opts.MaxModels = limit
	}
	return opts
}

func findFailureOrWitness(catalog *Catalog, q *Query, v *View, comp Compensation, opts AnalyzeOptions) (*Counterexample, *ExhaustiveReport) {
	cx, rep, err := FindRewriteCounterexample(catalog, q, v, comp, withModelLimit(opts.Search, opts.DifferentialModels))
	if err != nil && cx == nil {
		return buildAmbiguityCounterexample(catalog, q, v, nil), rep
	}
	return cx, rep
}

func missingOutputs(want, have []ColRef) []ColRef {
	set := map[ColRef]struct{}{}
	for _, c := range have {
		set[c] = struct{}{}
	}
	var missing []ColRef
	for _, c := range want {
		if _, ok := set[c]; !ok {
			missing = append(missing, c)
		}
	}
	return missing
}

func refsStrings(refs []ColRef) []string {
	out := make([]string, len(refs))
	for i, c := range refs {
		out[i] = c.String()
	}
	sort.Strings(out)
	return out
}

type locatedGroup struct {
	owner *Node
	preds []Predicate
	top   bool
}

func collectPredicateGroups(q *Query) []locatedGroup {
	var groups []locatedGroup
	topPreds := effectivePredicates(q.Where, q.NullReject)
	if len(topPreds) > 0 {
		groups = append(groups, locatedGroup{top: true, preds: topPreds})
	}
	var walk func(n *Node, isRoot bool)
	walk = func(n *Node, isRoot bool) {
		if n == nil {
			return
		}
		nodePreds := effectivePredicates(n.Where, n.NullReject)
		if len(nodePreds) > 0 {
			groups = append(groups, locatedGroup{owner: n, top: isRoot, preds: nodePreds})
		}
		walk(n.Left, false)
		walk(n.Right, false)
	}
	walk(q.Root, true)
	return groups
}

func buildCompensationCandidates(catalog *Catalog, q *Query, v *View) ([]Compensation, error) {
	outputSet := map[ColRef]struct{}{}
	for _, c := range v.Select {
		outputSet[c] = struct{}{}
	}
	groups := collectPredicateGroups(q)

	// Base DNF starts as one empty conjunction (TRUE).  Each located group
	// intersects its condition with all current alternatives.
	alts := [][]Predicate{{}}
	var discs []ColRef
	var firstErr error

	for _, g := range groups {
		compiled, groupDiscs, err := compileGroup(catalog, v, outputSet, g)
		discs = append(discs, groupDiscs...)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			// Even if the exact lifted form is unavailable, add simple mapped
			// candidates below for semantic counterexample search.
			continue
		}
		alts = crossDNF(alts, compiled)
	}

	var candidates []Compensation
	// NoFilter is always a legal candidate: projection can hide an internal
	// padding mismatch or a predicate already enforced by the view.
	candidates = append(candidates, Compensation{Filter: NoFilter()})
	if firstErr == nil {
		f := DNF{Alternatives: alts}
		candidates = append(candidates, Compensation{Filter: f, Discriminators: uniqueRefs(discs)})
	}

	// Conservative fallback candidates: all predicates mapped directly to the
	// top-level view row.  These often correctly implement upper WHERE clauses
	// and also provide concrete refutations for misplaced nested predicates.
	simple := []Predicate{}
	for _, g := range groups {
		for _, p := range g.preds {
			if _, ok := outputSet[p.Col]; ok {
				simple = append(simple, p)
				continue
			}
			if sub := substitutePaddingPredicate(catalog, v, p); sub != nil {
				simple = append(simple, *sub)
			} else {
				simple = nil
				goto noSimple
			}
		}
	}
	candidates = append(candidates, Compensation{Filter: All(dedupPreds(simple)...), Discriminators: reliableDiscRefs(catalog, v.Select)})
noSimple:

	// Add naive nullable discriminator forms only as refutable candidates.
	candidates = append(candidates, naiveDiscriminatorCandidates(catalog, q, v, groups, outputSet)...)

	return uniqueCompensations(candidates), firstErr
}

func compileGroup(catalog *Catalog, v *View, out map[ColRef]struct{}, g locatedGroup) (DNF, []ColRef, error) {
	var compiled []Predicate
	var discs []ColRef
	for _, p := range g.preds {
		cp, disc, err := compilePredicate(catalog, v, out, p)
		if err != nil {
			return DNF{}, nil, err
		}
		compiled = append(compiled, cp)
		if disc.Table != "" {
			discs = append(discs, disc)
		}
	}
	if g.top || g.owner == nil {
		return All(compiled...), discs, nil
	}

	disc, err := ownerDiscriminator(catalog, v.Select, g.owner)
	if err != nil {
		return DNF{}, discs, err
	}
	discs = append(discs, disc)
	// If the owner subtree is padding, the internal predicate must not remove
	// the outer preserved row.  Otherwise all owner predicates must hold.
	return AnyOf([]Predicate{IsNull(disc)}, compiled), discs, nil
}

func compilePredicate(catalog *Catalog, v *View, out map[ColRef]struct{}, p Predicate) (Predicate, ColRef, error) {
	if _, ok := out[p.Col]; ok {
		return p, ColRef{}, nil
	}
	if sub := substitutePaddingPredicate(catalog, v, p); sub != nil {
		disc := sub.Col
		return *sub, disc, nil
	}
	return Predicate{}, ColRef{}, fmt.Errorf("compensation needs %s but view %q does not export it or a NOT NULL/PK substitute", p.Col, v.ID)
}

func substitutePaddingPredicate(catalog *Catalog, v *View, p Predicate) *Predicate {
	if catalog == nil {
		return nil
	}
	spec, ok := catalog.column(p.Col.Table, p.Col.Column)
	if !ok || !spec.NotNull {
		return nil
	}
	if p.Op != OpNotNull && p.Op != OpIsNull {
		return nil
	}
	disc := firstReliableDiscForAlias(catalog, v.Select, p.Col.Table)
	if disc.Table == "" {
		return nil
	}
	op := OpNotNull
	if p.Op == OpIsNull {
		op = OpIsNull
	}
	return &Predicate{Col: disc, Op: op}
}

func ownerDiscriminator(catalog *Catalog, exported []ColRef, owner *Node) (ColRef, error) {
	aliases := mandatoryAliases(owner)
	if len(aliases) == 0 {
		return ColRef{}, fmt.Errorf("cannot find owner subtree discriminator")
	}
	if catalog != nil {
		for _, alias := range aliases {
			if d := firstReliableDiscForAlias(catalog, exported, alias); d.Table != "" {
				return d, nil
			}
		}
		return ColRef{}, fmt.Errorf("nested compensation for %q requires an exported NOT NULL/PK column from one of %v to distinguish a padded subtree from a real NULL", nodeLabel(owner), aliases)
	}
	// Without catalog, conservatively use the leftmost exported column.  This
	// is only a best-effort candidate and semantic search can refute it.
	for _, alias := range aliases {
		for _, c := range exported {
			if c.Table == alias {
				return c, nil
			}
		}
	}
	return ColRef{}, fmt.Errorf("nested compensation requires an exported discriminator from %v", aliases)
}

func mandatoryAliases(n *Node) []string {
	if n == nil {
		return nil
	}
	if n.Kind == ScanNode {
		alias := n.Alias
		if alias == "" {
			alias = n.Table
		}
		return []string{alias}
	}
	if n.Kind == InnerJoin {
		return append(mandatoryAliases(n.Left), mandatoryAliases(n.Right)...)
	}
	// A LEFT JOIN preserves only its left child; right-child columns may be
	// padding even when this node emits a row.
	return mandatoryAliases(n.Left)
}

func firstReliableDiscForAlias(catalog *Catalog, exported []ColRef, alias string) ColRef {
	// Prefer primary keys, then any NOT NULL column for deterministic output.
	var notNull ColRef
	for _, c := range exported {
		if c.Table != alias {
			continue
		}
		spec, ok := catalog.column(physicalForAlias(catalog, alias), c.Column)
		if !ok {
			// ColRef.Table is normally alias, while catalog is keyed by
			// physical name.  Try the alias directly for simple specs.
			spec, ok = catalog.column(alias, c.Column)
			if !ok {
				continue
			}
		}
		if spec.PrimaryKey {
			return c
		}
		if spec.NotNull && notNull.Table == "" {
			notNull = c
		}
	}
	return notNull
}

func physicalForAlias(catalog *Catalog, alias string) string {
	if catalog != nil {
		if _, ok := catalog.Tables[alias]; ok {
			return alias
		}
	}
	// Tree-aware callers usually have aliases equal to table names or pass a
	// catalog keyed by the same name.  collectScans could refine this later.
	return alias
}

func reliableDiscRefs(catalog *Catalog, refs []ColRef) []ColRef {
	if catalog == nil {
		return nil
	}
	var out []ColRef
	for _, c := range refs {
		spec, ok := catalog.column(c.Table, c.Column)
		if !ok {
			continue
		}
		if spec.NotNull {
			out = append(out, c)
		}
	}
	return uniqueRefs(out)
}

func naiveDiscriminatorCandidates(catalog *Catalog, q *Query, v *View, groups []locatedGroup, out map[ColRef]struct{}) []Compensation {
	var outCandidates []Compensation
	for _, g := range groups {
		if g.top || g.owner == nil {
			for _, p := range g.preds {
				alias := p.Col.Table
				for _, c := range v.Select {
					if c.Table != alias {
						continue
					}
					if catalog != nil {
						if spec, ok := catalog.column(c.Table, c.Column); ok && spec.NotNull {
							continue
						}
					}
					outCandidates = append(outCandidates, Compensation{Filter: All(NotNull(c))})
				}
			}
			continue
		}
		scan := leftmostScan(g.owner)
		if scan == nil {
			continue
		}
		alias := scan.Alias
		if alias == "" {
			alias = scan.Table
		}
		for _, c := range v.Select {
			if c.Table != alias {
				continue
			}
			// Reliable discriminators are already considered.  Naive nullable
			// columns are included specifically so finite search can construct
			// the true-NULL versus padding ambiguity counterexample.
			if catalog != nil {
				if spec, ok := catalog.column(c.Table, c.Column); ok && spec.NotNull {
					continue
				}
			}
			var preds []Predicate
			for _, p := range g.preds {
				if _, ok := out[p.Col]; ok {
					preds = append(preds, p)
				} else if sub := substitutePaddingPredicate(catalog, v, p); sub != nil {
					preds = append(preds, *sub)
				} else {
					preds = nil
					break
				}
			}
			if preds == nil {
				continue
			}
			f := AnyOf([]Predicate{IsNull(c)}, preds)
			outCandidates = append(outCandidates, Compensation{Filter: f})
			// A common incorrect compensation is top-level NOT NULL on the
			// nullable discriminator.  Include it as a refutable candidate.
			outCandidates = append(outCandidates, Compensation{Filter: All(NotNull(c))})
		}
	}
	return outCandidates
}

func crossDNF(current [][]Predicate, next DNF) [][]Predicate {
	if len(next.Alternatives) == 0 {
		return nil
	}
	var out [][]Predicate
	for _, a := range current {
		for _, b := range next.Alternatives {
			merged := append(append([]Predicate(nil), a...), b...)
			out = append(out, dedupPreds(merged))
		}
	}
	return out
}

func dedupPreds(ps []Predicate) []Predicate {
	seen := map[string]struct{}{}
	out := make([]Predicate, 0, len(ps))
	for _, p := range ps {
		k := p.SQL()
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SQL() < out[j].SQL() })
	return out
}

func uniqueRefs(refs []ColRef) []ColRef {
	seen := map[ColRef]struct{}{}
	out := []ColRef{}
	for _, r := range refs {
		if _, ok := seen[r]; ok {
			continue
		}
		seen[r] = struct{}{}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func uniqueCompensations(cs []Compensation) []Compensation {
	seen := map[string]struct{}{}
	out := []Compensation{}
	for _, c := range cs {
		c.Filter.Alternatives = dedupAlternatives(c.Filter.Alternatives)
		key := c.Filter.SQL()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		c.SQL = key
		out = append(out, c)
	}
	return out
}

func dedupAlternatives(alts [][]Predicate) [][]Predicate {
	seen := map[string]struct{}{}
	out := [][]Predicate{}
	for _, a := range alts {
		p := dedupPreds(a)
		k := All(p...).SQL()
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, p)
	}
	return out
}

func leftmostScan(n *Node) *Node {
	cur := n
	for cur != nil && cur.Kind != ScanNode {
		cur = cur.Left
	}
	return cur
}

func nodeLabel(n *Node) string {
	if n == nil {
		return "<nil>"
	}
	if n.Kind == ScanNode {
		if n.Alias != "" {
			return n.Alias
		}
		return n.Table
	}
	return string(n.Kind) + "-node"
}

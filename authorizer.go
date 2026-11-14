package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

var (
	// ErrUnauthorized is matched by AuthorizationError.
	ErrUnauthorized = errors.New("netsec: field authorization denied")
	// ErrStalePlan is returned when a caller requires a plan that still
	// matches the catalog's current generation.
	ErrStalePlan = errors.New("netsec: stale authorization plan")
)

// Violation describes one unauthorized construct removed from a request (or
// one that caused a strict authorization failure).
type Violation struct {
	Kind    string `json:"kind"`
	Field   string `json:"field,omitempty"`
	Context string `json:"context,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// AuthorizationError is returned by strict authorization when a request
// attempts to use a field outside the effective allow-list.
type AuthorizationError struct {
	Violations []Violation `json:"violations"`
}

func (e *AuthorizationError) Error() string {
	if e == nil || len(e.Violations) == 0 {
		return ErrUnauthorized.Error()
	}
	parts := make([]string, 0, len(e.Violations))
	for _, violation := range e.Violations {
		if violation.Field == "" {
			parts = append(parts, violation.Kind)
			continue
		}
		parts = append(parts, violation.Kind+":"+violation.Field)
	}
	return ErrUnauthorized.Error() + ": " + strings.Join(parts, ", ")
}

// Is lets callers use errors.Is(err, ErrUnauthorized).
func (e *AuthorizationError) Is(target error) bool {
	return target == ErrUnauthorized
}

// PlanTarget is the generation-bound, resolved part of a plan.
type PlanTarget struct {
	Index            string   `json:"index"`
	Aliases          []string `json:"aliases,omitempty"`
	Fields           []string `json:"fields,omitempty"`
	Policy           Policy   `json:"policy"`
	AliasPolicies    []Policy `json:"alias_policies,omitempty"`
	AliasGenerations []uint64 `json:"alias_generations,omitempty"`
}

// AggPlan mirrors the authorized aggregation tree so responses can be filtered
// with the same bounds as the request.
type AggPlan struct {
	Name     string              `json:"name"`
	Children map[string]*AggPlan `json:"children,omitempty"`
}

// Plan is an immutable authorization decision for one alias/index expression
// at one catalog generation. Per-request data (authorized aggregations and
// violations) is attached when the plan is returned by Authorize.
type Plan struct {
	Expression     string              `json:"expression"`
	Generation     uint64              `json:"generation"`
	PolicyRevision string              `json:"policy_revision"`
	Targets        []PlanTarget        `json:"targets"`
	Aggs           map[string]*AggPlan `json:"aggs,omitempty"`
	Violations     []Violation         `json:"violations,omitempty"`
	SortSanitized  bool                `json:"sort_sanitized,omitempty"`

	rules       []*fieldRules
	allowed     []string
	allowedSet  map[string]struct{}
	known       []string
	removedAggs map[string]struct{}
}

// AuthorizedFields returns a sorted copy of the concrete fields known to be
// allowed after intersecting every target policy. Fields granted only by an
// open-ended policy pattern may not be enumerable; FieldAllowed still evaluates
// them against the full policy set.
func (p *Plan) AuthorizedFields() []string {
	return append([]string(nil), p.allowed...)
}

// FieldAllowed reports whether field is allowed by every resolved target and
// every alias-level policy.
func (p *Plan) FieldAllowed(field string) bool {
	if p == nil || field == "" || len(p.rules) == 0 {
		return false
	}
	if _, ok := p.allowedSet[field]; ok {
		return true
	}
	for _, rules := range p.rules {
		if !rules.allows(field) {
			return false
		}
	}
	return true
}

// MayInclude reports whether path itself or some descendant may be included.
// It is used while recursively filtering objects; a granted parent never grants
// its children implicitly.
func (p *Plan) MayInclude(path string) bool {
	if path == "" {
		return true
	}
	if p.FieldAllowed(path) {
		return true
	}
	for _, field := range p.allowed {
		if fieldIsAncestor(path, field) {
			return true
		}
	}
	for _, rules := range p.rules {
		if !rules.couldAllowDescendant(path) {
			return false
		}
	}
	return len(p.rules) > 0
}

// ScriptFieldAllowed reports whether a script may read field. A plain parent
// field can contain restricted children in _source, so scripts may only read a
// container when every known descendant is allowed and the policy grants the
// whole subtree.
func (p *Plan) ScriptFieldAllowed(field string) bool {
	if p == nil || field == "" || len(p.rules) == 0 {
		return false
	}
	hasDescendant := false
	for _, known := range p.known {
		if !fieldIsAncestor(field, known) {
			continue
		}
		hasDescendant = true
		if !p.FieldAllowed(known) {
			return false
		}
	}
	if !hasDescendant {
		return p.FieldAllowed(field)
	}
	for _, rules := range p.rules {
		if !rules.allowsAllDescendants(field) {
			return false
		}
	}
	return true
}

// ValidAgainst reports whether the plan still belongs to the catalog's current
// generation. A gateway should re-authorize rather than reuse a stale plan.
func (p *Plan) ValidAgainst(catalog *Catalog) bool {
	return p != nil && catalog != nil && p.Generation == catalog.Generation()
}

// Config controls an Authorizer.
type Config struct {
	// Strict makes Authorize return AuthorizationError when the request uses
	// an unauthorized field. The returned request is still sanitized.
	Strict bool
	// PlanCacheSize bounds the generation-scoped plan cache. Zero uses 128.
	PlanCacheSize int
}

// Authorizer compiles field authorization plans and sanitizes requests and
// responses for a catalog.
type Authorizer struct {
	catalog *Catalog
	strict  bool
	cache   *planCache
}

// NewAuthorizer returns an Authorizer in permissive mode. Permissive mode
// removes unauthorized constructs and records them in Plan.Violations.
func NewAuthorizer(catalog *Catalog) *Authorizer {
	authorizer, _ := NewAuthorizerWithConfig(catalog, Config{})
	return authorizer
}

// NewFieldAuthorizer is a descriptive alias for NewAuthorizer.
func NewFieldAuthorizer(catalog *Catalog) *Authorizer {
	return NewAuthorizer(catalog)
}

// NewAuthorizerWithConfig returns an Authorizer with explicit configuration.
func NewAuthorizerWithConfig(catalog *Catalog, config Config) (*Authorizer, error) {
	if catalog == nil {
		return nil, errors.New("netsec: nil catalog")
	}
	size := config.PlanCacheSize
	if size == 0 {
		size = 128
	}
	if size < 1 {
		return nil, errors.New("netsec: plan cache size must be positive")
	}
	return &Authorizer{
		catalog: catalog,
		strict:  config.Strict,
		cache:   newPlanCache(size),
	}, nil
}

// Catalog returns the catalog used by this authorizer.
func (a *Authorizer) Catalog() *Catalog {
	return a.catalog
}

// Prepare authorizes a search request against expression. It returns the
// generation-bound plan and a deep-copied, sanitized request. The input map is
// never modified.
func (a *Authorizer) Prepare(expression string, request map[string]any) (*Plan, map[string]any, error) {
	if a == nil || a.catalog == nil {
		return nil, nil, errors.New("netsec: nil authorizer")
	}
	if request == nil {
		request = map[string]any{}
	}

	snapshot := a.catalog.Snapshot()
	base, err := a.basePlan(snapshot, expression)
	if err != nil {
		return nil, nil, err
	}

	plan := base.forRequest()
	sanitized := cloneMap(request)
	validator := &validator{plan: plan, collectAggs: true}
	validator.sanitize(sanitized)
	plan.Violations = validator.violations
	plan.Aggs = validator.aggs
	plan.removedAggs = validator.removedAggs
	plan.SortSanitized = validator.sortSanitized

	if a.strict && len(plan.Violations) > 0 {
		return plan, sanitized, &AuthorizationError{Violations: append([]Violation(nil), plan.Violations...)}
	}
	return plan, sanitized, nil
}

// Authorize is an alias for Prepare.
func (a *Authorizer) Authorize(expression string, request map[string]any) (*Plan, map[string]any, error) {
	return a.Prepare(expression, request)
}

// WriteTarget describes the concrete write index selected for an alias at a
// particular generation.
type WriteTarget struct {
	Alias           string   `json:"alias"`
	Index           string   `json:"index"`
	Generation      uint64   `json:"generation"`
	AliasGeneration uint64   `json:"alias_generation"`
	PolicyRevision  string   `json:"policy_revision"`
	AllowedFields   []string `json:"allowed_fields"`

	plan *Plan
}

// ValidAgainst reports whether the write target still belongs to the catalog's
// current generation.
func (w *WriteTarget) ValidAgainst(catalog *Catalog) bool {
	return w != nil && catalog != nil && w.Generation == catalog.Generation()
}

// ResolveWrite resolves an alias's write target and builds the field decision
// for that concrete index. The result is bound to the alias generation.
func (a *Authorizer) ResolveWrite(alias string) (*WriteTarget, error) {
	if a == nil || a.catalog == nil {
		return nil, errors.New("netsec: nil authorizer")
	}
	snapshot := a.catalog.Snapshot()
	aliasInfo, target, err := snapshot.resolveWrite(alias)
	if err != nil {
		return nil, err
	}
	rules := make([]*fieldRules, 0, 1+len(target.AliasPolicies))
	rules = append(rules, newFieldRules("index:"+target.Index, target.Policy))
	for i, policy := range target.AliasPolicies {
		rules = append(rules, newFieldRules(fmt.Sprintf("alias:%s:%d", aliasInfo.Name, i), policy))
	}
	allowed := enumerateAllowed(target.Fields, rules)
	plan := &Plan{
		Expression:     aliasInfo.Name,
		Generation:     snapshot.Generation,
		PolicyRevision: revisionFor(snapshot.Generation, []ResolvedTarget{target}, rules),
		Targets: []PlanTarget{{
			Index:            target.Index,
			Aliases:          []string{aliasInfo.Name},
			Fields:           append([]string(nil), target.Fields...),
			Policy:           target.Policy.Clone(),
			AliasPolicies:    clonePolicies(target.AliasPolicies),
			AliasGenerations: []uint64{aliasInfo.Generation},
		}},
		rules:      rules,
		allowed:    allowed,
		allowedSet: sliceSet(allowed),
		known:      collectCandidates(target.Fields, rules),
	}
	return &WriteTarget{
		Alias:           aliasInfo.Name,
		Index:           target.Index,
		Generation:      snapshot.Generation,
		AliasGeneration: aliasInfo.Generation,
		PolicyRevision:  plan.PolicyRevision,
		AllowedFields:   append([]string(nil), allowed...),
		plan:            plan,
	}, nil
}

// AuthorizeDocument filters a document being written through an alias. The
// write index and its policy are resolved at one generation.
func (a *Authorizer) AuthorizeDocument(alias string, document map[string]any) (*WriteTarget, map[string]any, error) {
	target, err := a.ResolveWrite(alias)
	if err != nil {
		return nil, nil, err
	}
	filtered := filterObject(target.plan, "", document)
	if a.strict {
		for _, field := range sortedMapKeys(document) {
			path := field
			if _, ok := filtered[field]; !ok && !target.plan.MayInclude(path) {
				return target, filtered, &AuthorizationError{Violations: []Violation{{
					Kind:    "document",
					Field:   field,
					Context: "write:" + target.Alias,
				}}}
			}
		}
	}
	return target, filtered, nil
}

func (a *Authorizer) basePlan(snapshot Snapshot, expression string) (*basePlan, error) {
	expression = strings.TrimSpace(expression)
	return a.cache.getOrBuild(snapshot.Generation, expression, func() (*basePlan, error) {
		resolution, err := snapshot.Resolve(expression)
		if err != nil {
			return nil, err
		}
		return compileBasePlan(resolution)
	})
}

type basePlan struct {
	expression     string
	generation     uint64
	policyRevision string
	targets        []PlanTarget
	rules          []*fieldRules
	allowed        []string
	allowedSet     map[string]struct{}
	known          []string
}

func compileBasePlan(resolution Resolution) (*basePlan, error) {
	if len(resolution.Targets) == 0 {
		return nil, fmt.Errorf("%w: empty resolution", ErrUnknownIndex)
	}

	rules := make([]*fieldRules, 0, len(resolution.Targets)*2)
	targets := make([]PlanTarget, 0, len(resolution.Targets))
	candidates := make(map[string]struct{})

	for _, target := range resolution.Targets {
		rules = append(rules, newFieldRules("index:"+target.Index, target.Policy))
		for _, policy := range target.AliasPolicies {
			rules = append(rules, newFieldRules("alias:"+target.Index, policy))
		}
		for _, field := range target.Fields {
			if isConcreteField(field) {
				candidates[field] = struct{}{}
			}
		}
		targets = append(targets, PlanTarget{
			Index:            target.Index,
			Aliases:          append([]string(nil), target.Aliases...),
			Fields:           append([]string(nil), target.Fields...),
			Policy:           target.Policy.Clone(),
			AliasPolicies:    clonePolicies(target.AliasPolicies),
			AliasGenerations: append([]uint64(nil), target.AliasGenerations...),
		})
	}

	// Concrete allow-list entries are candidates too. This keeps exact dynamic
	// fields usable when a schema does not enumerate them; every candidate is
	// still checked against all policies below.
	for _, policyRules := range rules {
		for _, pattern := range policyRules.allow {
			if isConcreteField(pattern) {
				candidates[pattern] = struct{}{}
			}
		}
		for _, pattern := range policyRules.deny {
			if isConcreteField(pattern) {
				candidates[pattern] = struct{}{}
			}
		}
	}

	candidateList := make([]string, 0, len(candidates))
	for field := range candidates {
		candidateList = append(candidateList, field)
	}
	sort.Strings(candidateList)

	allowed := make([]string, 0, len(candidateList))
	for _, field := range candidateList {
		permitted := true
		for _, policyRules := range rules {
			if !policyRules.allows(field) {
				permitted = false
				break
			}
		}
		if permitted {
			allowed = append(allowed, field)
		}
	}
	sort.Strings(allowed)

	return &basePlan{
		expression:     resolution.Expression,
		generation:     resolution.Generation,
		policyRevision: revisionFor(resolution.Generation, resolution.Targets, rules),
		targets:        targets,
		rules:          rules,
		allowed:        allowed,
		allowedSet:     sliceSet(allowed),
		known:          candidateList,
	}, nil
}

func (b *basePlan) forRequest() *Plan {
	return &Plan{
		Expression:     b.expression,
		Generation:     b.generation,
		PolicyRevision: b.policyRevision,
		Targets:        clonePlanTargets(b.targets),
		rules:          b.rules,
		allowed:        b.allowed,
		allowedSet:     b.allowedSet,
		known:          b.known,
	}
}

func enumerateAllowed(fields []string, rules []*fieldRules) []string {
	out := make([]string, 0)
	for _, field := range collectCandidates(fields, rules) {
		permitted := true
		for _, policyRules := range rules {
			if !policyRules.allows(field) {
				permitted = false
				break
			}
		}
		if permitted {
			out = append(out, field)
		}
	}
	return out
}

func collectCandidates(fields []string, rules []*fieldRules) []string {
	candidates := make(map[string]struct{})
	for _, field := range fields {
		if isConcreteField(field) {
			candidates[field] = struct{}{}
		}
	}
	for _, policyRules := range rules {
		for _, pattern := range policyRules.allow {
			if isConcreteField(pattern) {
				candidates[pattern] = struct{}{}
			}
		}
		for _, pattern := range policyRules.deny {
			if isConcreteField(pattern) {
				candidates[pattern] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(candidates))
	for field := range candidates {
		out = append(out, field)
	}
	sort.Strings(out)
	return out
}

func revisionFor(generation uint64, targets []ResolvedTarget, rules []*fieldRules) string {
	hash := sha256.New()
	fmt.Fprintf(hash, "generation:%d\n", generation)
	sortedTargets := append([]ResolvedTarget(nil), targets...)
	sort.Slice(sortedTargets, func(i, j int) bool { return sortedTargets[i].Index < sortedTargets[j].Index })
	for _, target := range sortedTargets {
		fmt.Fprintf(hash, "index:%s\n", target.Index)
		fields := append([]string(nil), target.Fields...)
		sort.Strings(fields)
		fmt.Fprintf(hash, "fields:%s\n", strings.Join(fields, ","))
		fmt.Fprintf(hash, "policy:%t|%s|%s\n", target.Policy.AllowAll,
			strings.Join(target.Policy.AllowedFields, ","), strings.Join(target.Policy.DeniedFields, ","))
		for _, policy := range target.AliasPolicies {
			fmt.Fprintf(hash, "alias-policy:%t|%s|%s\n", policy.AllowAll,
				strings.Join(policy.AllowedFields, ","), strings.Join(policy.DeniedFields, ","))
		}
	}
	for _, policyRules := range rules {
		fmt.Fprintf(hash, "rules:%s|%t|%s|%s\n", policyRules.source, policyRules.allowAll,
			strings.Join(policyRules.allow, ","), strings.Join(policyRules.deny, ","))
	}
	return hex.EncodeToString(hash.Sum(nil))[:24]
}

func clonePlanTargets(targets []PlanTarget) []PlanTarget {
	out := make([]PlanTarget, len(targets))
	for i, target := range targets {
		out[i] = PlanTarget{
			Index:            target.Index,
			Aliases:          append([]string(nil), target.Aliases...),
			Fields:           append([]string(nil), target.Fields...),
			Policy:           target.Policy.Clone(),
			AliasPolicies:    clonePolicies(target.AliasPolicies),
			AliasGenerations: append([]uint64(nil), target.AliasGenerations...),
		}
	}
	return out
}

func clonePolicies(policies []Policy) []Policy {
	out := make([]Policy, len(policies))
	for i, policy := range policies {
		out[i] = policy.Clone()
	}
	return out
}

func sliceSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

type planCache struct {
	mu         sync.Mutex
	generation uint64
	plans      map[string]*basePlan
	max        int
}

func newPlanCache(max int) *planCache {
	return &planCache{
		plans: make(map[string]*basePlan),
		max:   max,
	}
}

func (c *planCache) getOrBuild(generation uint64, expression string, build func() (*basePlan, error)) (*basePlan, error) {
	c.mu.Lock()
	if c.generation != generation {
		if generation < c.generation {
			// An older in-flight request must not roll the cache back to its
			// snapshot generation.
			c.mu.Unlock()
			return build()
		}
		c.generation = generation
		c.plans = make(map[string]*basePlan)
	}
	if plan, ok := c.plans[expression]; ok {
		c.mu.Unlock()
		return plan, nil
	}
	if len(c.plans) >= c.max {
		for key := range c.plans {
			delete(c.plans, key)
			break
		}
	}
	c.mu.Unlock()

	plan, err := build()
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation != generation {
		// A mutation advanced the catalog while this plan was compiling. Do not
		// publish a stale decision into the new generation's cache.
		return plan, nil
	}
	c.plans[expression] = plan
	return plan, nil
}

// CacheStats reports the generation and number of plans currently cached.
type CacheStats struct {
	Generation uint64
	Entries    int
}

// CacheStats returns a snapshot of plan-cache state. Tests and operators can
// use it to verify that a generation change dropped old conclusions.
func (a *Authorizer) CacheStats() CacheStats {
	if a == nil || a.cache == nil {
		return CacheStats{}
	}
	if a.catalog == nil {
		return CacheStats{}
	}
	current := a.catalog.Generation()
	a.cache.mu.Lock()
	defer a.cache.mu.Unlock()
	if a.cache.generation != current {
		return CacheStats{Generation: current, Entries: 0}
	}
	return CacheStats{Generation: a.cache.generation, Entries: len(a.cache.plans)}
}

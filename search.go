package krb5policy

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// AttackKind classifies a reachable policy bypass.
type AttackKind string

const (
	AttackUnauthorizedTransit AttackKind = "unauthorized-transit"
	AttackNotDelegatedBypass  AttackKind = "not-delegated-bypass"
)

// AttackStepKind classifies one edge in an attack path.
type AttackStepKind string

const (
	StepTrust          AttackStepKind = "trust"
	StepForgedShortcut AttackStepKind = "forged-shortcut"
	StepDelegation     AttackStepKind = "delegation"
)

// AttackStep is one hop in a discovered attack path.
type AttackStep struct {
	From    string
	To      string
	Kind    AttackStepKind
	Service string
	Target  string
	Note    string
}

// AttackPath is the shortest attack found by the bounded search.
type AttackPath struct {
	Kind               AttackKind
	Realms             []string
	Steps              []AttackStep
	CompromisedRealm   string
	Forged             bool
	BypassedProtection bool
	Hops               int
}

// AttackSearchOptions controls the bounded adversarial search.
type AttackSearchOptions struct {
	StartRealm string
	GoalRealm  string
	GoalSPN    string

	User          Principal
	UserProtected bool

	CompromisedRealms    []string
	WeakDelegationChecks bool

	// MaxDepth bounds total attack hops.  Zero selects 8.
	MaxDepth int
	// MaxPaths bounds the number of explored paths/edges.  Zero selects
	// 1,000,000.  Exceeding the bound returns ErrSearchLimit rather than a
	// false negative.
	MaxPaths int
}

// SearchStats describes how much work a search performed.
type SearchStats struct {
	PathsExamined int
	Truncated     bool
}

func (o AttackSearchOptions) normalized() AttackSearchOptions {
	if o.MaxDepth <= 0 {
		o.MaxDepth = 8
	}
	if o.MaxPaths <= 0 {
		o.MaxPaths = 1_000_000
	}
	return o
}

type searchBudget struct {
	paths int
	max   int
}

func (b *searchBudget) spend() error {
	b.paths++
	if b.paths > b.max {
		return ErrSearchLimit
	}
	return nil
}

// FindShortestAttack searches for the shortest transit-forgery or protected
// delegation path.  A nil attack and nil error means no path was found within
// the bounds.
func FindShortestAttack(g *TrustGraph, dir *Directory, opt AttackSearchOptions) (*AttackPath, error) {
	attack, _, err := FindShortestAttackWithStats(g, dir, opt)
	return attack, err
}

// FindShortestAttackWithStats is FindShortestAttack plus path-budget stats.
func FindShortestAttackWithStats(g *TrustGraph, dir *Directory, opt AttackSearchOptions) (*AttackPath, SearchStats, error) {
	return searchAttacks(g, dir, opt, true)
}

// ExhaustiveShortestAttack performs the same bounded search without
// branch-and-bound pruning.  It is intended to cross-check the optimized
// search on small graphs; a mismatch is a verifier bug.
func ExhaustiveShortestAttack(g *TrustGraph, dir *Directory, opt AttackSearchOptions) (*AttackPath, SearchStats, error) {
	return searchAttacks(g, dir, opt, false)
}

func searchAttacks(g *TrustGraph, dir *Directory, rawOpt AttackSearchOptions, prune bool) (*AttackPath, SearchStats, error) {
	opt := rawOpt.normalized()
	if opt.MaxDepth < 1 {
		return nil, SearchStats{}, fmt.Errorf("%w: MaxDepth must be >= 1", ErrInvalidConfig)
	}
	budget := &searchBudget{max: opt.MaxPaths}
	var best *AttackPath

	transit, err := searchTransit(g, opt, budget, prune, best)
	if err != nil {
		return nil, SearchStats{PathsExamined: budget.paths, Truncated: errors.Is(err, ErrSearchLimit)}, err
	}
	best = transit

	delegation, err := searchDelegation(g, dir, opt, budget, prune, best)
	if err != nil {
		return nil, SearchStats{PathsExamined: budget.paths, Truncated: errors.Is(err, ErrSearchLimit)}, err
	}
	best = chooseAttack(best, delegation)
	return copyAttack(best), SearchStats{PathsExamined: budget.paths}, nil
}

func searchTransit(g *TrustGraph, opt AttackSearchOptions, budget *searchBudget, prune bool, incumbent *AttackPath) (*AttackPath, error) {
	if g == nil {
		return nil, nil
	}
	start, startName, ok := g.lookup(opt.StartRealm)
	if !ok {
		return nil, nil
	}
	goal, goalName, ok := g.lookup(opt.GoalRealm)
	if !ok || start == goal {
		return nil, nil
	}
	compromised := make(map[string]struct{}, len(opt.CompromisedRealms))
	for _, r := range opt.CompromisedRealms {
		compromised[canonicalRealm(r)] = struct{}{}
	}
	if _, ok := compromised[start]; !ok && len(compromised) == 0 {
		return nil, nil
	}

	best := copyAttack(incumbent)
	visited := map[string]bool{start: true}
	realmPath := []string{startName}
	var dfs func(cur string, hops int) error
	dfs = func(cur string, hops int) error {
		if err := budget.spend(); err != nil {
			return err
		}
		if hops >= opt.MaxDepth {
			return nil
		}
		if prune && best != nil && hops+1 >= best.Hops {
			// Any attack from here needs at least one more edge.  It cannot
			// beat the incumbent; equal-length ties are not security relevant.
			return nil
		}
		nextKeys := make([]string, 0, len(g.adj[cur]))
		for next := range g.adj[cur] {
			nextKeys = append(nextKeys, next)
		}
		sort.Strings(nextKeys)
		for _, next := range nextKeys {
			if next == goal {
				if _, isCompromised := compromised[cur]; !isCompromised {
					continue
				}
				intermediates := realmPath[1:] // excludes start; cur included
				_, err := g.ValidatePath(opt.StartRealm, opt.GoalRealm, intermediates)
				if err == nil {
					continue
				}
				if !unauthorizedPathError(err) {
					continue
				}
				attack := buildTransitAttack(realmPath, cur, goalName, intermediates, g)
				best = chooseAttack(best, attack)
				continue
			}
			if visited[next] {
				continue
			}
			visited[next] = true
			realmPath = append(realmPath, g.display[next])
			if err := dfs(next, hops+1); err != nil {
				visited[next] = false
				realmPath = realmPath[:len(realmPath)-1]
				return err
			}
			visited[next] = false
			realmPath = realmPath[:len(realmPath)-1]
		}
		return nil
	}
	if err := dfs(start, 0); err != nil {
		return nil, err
	}
	return best, nil
}

func buildTransitAttack(realmPath []string, compromisedKey, goalName string, intermediates []string, g *TrustGraph) *AttackPath {
	realms := append(append([]string(nil), realmPath...), goalName)
	steps := make([]AttackStep, 0, len(realms)-1)
	for i := 0; i+1 < len(realms); i++ {
		kind := StepTrust
		note := ""
		if i == len(realms)-2 {
			kind = StepForgedShortcut
			note = "attacker controls the final-hop realm and relies on transited validation being skipped"
		}
		steps = append(steps, AttackStep{
			From: realms[i],
			To:   realms[i+1],
			Kind: kind,
			Note: note,
		})
	}
	_ = intermediates
	return &AttackPath{
		Kind:             AttackUnauthorizedTransit,
		Realms:           realms,
		Steps:            steps,
		CompromisedRealm: g.display[compromisedKey],
		Forged:           true,
		Hops:             len(realms) - 1,
	}
}

func searchDelegation(g *TrustGraph, dir *Directory, opt AttackSearchOptions, budget *searchBudget, prune bool, incumbent *AttackPath) (*AttackPath, error) {
	if dir == nil || opt.User.IsZero() {
		return nil, nil
	}
	startRealm := opt.StartRealm
	if startRealm == "" {
		startRealm = opt.User.Realm
	}
	if startRealm == "" {
		return nil, nil
	}
	protected := opt.UserProtected
	if user, ok := dir.User(opt.User); ok && user.Flags.Protected() {
		protected = true
	}
	if !protected {
		return nil, nil
	}
	compromised := make(map[string]struct{}, len(opt.CompromisedRealms))
	for _, r := range opt.CompromisedRealms {
		compromised[canonicalRealm(r)] = struct{}{}
	}
	var goalSPN *Principal
	if opt.GoalSPN != "" {
		p, err := ParsePrincipal(opt.GoalSPN)
		if err != nil {
			return nil, fmt.Errorf("%w: goal SPN: %v", ErrInvalidConfig, err)
		}
		goalSPN = &p
	}

	best := copyAttack(incumbent)
	for _, svc := range dir.Services() {
		serviceRealm := svc.Principal.Realm
		if serviceRealm == "" {
			continue
		}
		var targets []Principal
		for _, raw := range svc.AllowedToDelegateTo {
			target, err := ParsePrincipal(raw)
			if err != nil {
				continue
			}
			if target.Realm == "" {
				target.Realm = serviceRealm
			}
			if goalSPN != nil {
				if goalSPN.Realm != "" && !target.Equal(*goalSPN) {
					continue
				}
				if goalSPN.Realm == "" && !principalNamesEqual(target, *goalSPN) {
					continue
				}
			}
			if goalSPN == nil && opt.GoalRealm != "" && !sameRealm(target.Realm, opt.GoalRealm) {
				continue
			}
			targets = append(targets, target)
		}
		if len(targets) == 0 {
			continue
		}

		paths, err := validTrustPaths(g, startRealm, serviceRealm, opt.MaxDepth-1, budget)
		if err != nil {
			return nil, err
		}
		for _, path := range paths {
			bypass := opt.WeakDelegationChecks
			if !bypass {
				for _, realm := range path {
					if _, ok := compromised[canonicalRealm(realm)]; ok {
						bypass = true
						break
					}
				}
			}
			if !bypass {
				continue
			}
			for _, target := range targets {
				if !sameRealm(serviceRealm, target.Realm) {
					if g == nil {
						continue
					}
					if !g.HasDirectTrust(serviceRealm, target.Realm) {
						continue
					}
					if _, err := g.ValidatePath(serviceRealm, target.Realm, nil); err != nil {
						continue
					}
				}
				attack := buildDelegationAttack(path, svc, target, serviceRealm)
				if attack.Hops > opt.MaxDepth {
					continue
				}
				if prune && best != nil && attack.Hops > best.Hops {
					continue
				}
				best = chooseAttack(best, attack)
			}
		}
	}
	return best, nil
}

func principalNamesEqual(a, b Principal) bool {
	if len(a.Name) != len(b.Name) {
		return false
	}
	for i := range a.Name {
		if !strings.EqualFold(a.Name[i], b.Name[i]) {
			return false
		}
	}
	return true
}

// validTrustPaths returns every simple trust path from start to goal within
// maxHops, including the endpoint-only path for same-realm endpoints.
func validTrustPaths(g *TrustGraph, start, goal string, maxHops int, budget *searchBudget) ([][]string, error) {
	if sameRealm(start, goal) {
		return [][]string{{start}}, nil
	}
	if g == nil || maxHops < 1 {
		return nil, nil
	}
	startKey, startName, ok := g.lookup(start)
	if !ok {
		return nil, nil
	}
	goalKey, _, ok := g.lookup(goal)
	if !ok {
		return nil, nil
	}
	var out [][]string
	visited := map[string]bool{startKey: true}
	path := []string{startName}
	var dfs func(cur string, hops int) error
	dfs = func(cur string, hops int) error {
		if err := budget.spend(); err != nil {
			return err
		}
		if cur == goalKey {
			intermediates := path[1 : len(path)-1]
			if _, err := g.ValidatePath(start, goal, intermediates); err != nil {
				return nil
			}
			out = append(out, append([]string(nil), path...))
			return nil
		}
		if hops >= maxHops {
			return nil
		}
		nextKeys := make([]string, 0, len(g.adj[cur]))
		for next := range g.adj[cur] {
			nextKeys = append(nextKeys, next)
		}
		sort.Strings(nextKeys)
		for _, next := range nextKeys {
			if visited[next] {
				continue
			}
			visited[next] = true
			path = append(path, g.display[next])
			if err := dfs(next, hops+1); err != nil {
				visited[next] = false
				path = path[:len(path)-1]
				return err
			}
			visited[next] = false
			path = path[:len(path)-1]
		}
		return nil
	}
	if err := dfs(startKey, 0); err != nil {
		return nil, err
	}
	return out, nil
}

func buildDelegationAttack(trustPath []string, svc ServiceAccount, target Principal, serviceRealm string) *AttackPath {
	realms := append([]string(nil), trustPath...)
	steps := make([]AttackStep, 0, len(trustPath)-1+1)
	for i := 0; i+1 < len(trustPath); i++ {
		steps = append(steps, AttackStep{
			From: trustPath[i],
			To:   trustPath[i+1],
			Kind: StepTrust,
		})
	}
	from := serviceRealm
	if len(realms) > 0 {
		from = realms[len(realms)-1]
	}
	to := target.Realm
	if !sameRealm(from, to) {
		realms = append(realms, to)
	} else {
		to = from
	}
	steps = append(steps, AttackStep{
		From:    from,
		To:      to,
		Kind:    StepDelegation,
		Service: svc.Principal.String(),
		Target:  target.String(),
		Note:    "protected user flags were not enforced before S4U2Proxy",
	})
	return &AttackPath{
		Kind:               AttackNotDelegatedBypass,
		Realms:             realms,
		Steps:              steps,
		BypassedProtection: true,
		Hops:               len(steps),
	}
}

func unauthorizedPathError(err error) bool {
	return errors.Is(err, ErrCapathViolation) ||
		errors.Is(err, ErrTransitShortcut) ||
		errors.Is(err, ErrPathNotAuthorized)
}

func chooseAttack(best, candidate *AttackPath) *AttackPath {
	if candidate == nil {
		return best
	}
	if best == nil {
		return copyAttack(candidate)
	}
	if candidate.Hops < best.Hops {
		return copyAttack(candidate)
	}
	if candidate.Hops > best.Hops {
		return best
	}
	// Prefer transit forgery on a tie, then a stable lexical order.  This
	// makes exhaustive cross-checking deterministic.
	if best.Kind != candidate.Kind {
		if candidate.Kind == AttackUnauthorizedTransit {
			return copyAttack(candidate)
		}
		if best.Kind == AttackUnauthorizedTransit {
			return best
		}
	}
	if strings.Join(candidate.Realms, "\x00") < strings.Join(best.Realms, "\x00") {
		return copyAttack(candidate)
	}
	return best
}

func copyAttack(a *AttackPath) *AttackPath {
	if a == nil {
		return nil
	}
	out := *a
	out.Realms = append([]string(nil), a.Realms...)
	out.Steps = append([]AttackStep(nil), a.Steps...)
	return &out
}

// CrossCheckSearch runs the optimized and exhaustive searchers and verifies
// that they agree.  It is useful as a deployment audit for small graphs.
func CrossCheckSearch(g *TrustGraph, dir *Directory, opt AttackSearchOptions) error {
	fast, _, err := FindShortestAttackWithStats(g, dir, opt)
	if err != nil {
		return fmt.Errorf("optimized search: %w", err)
	}
	brute, _, err := ExhaustiveShortestAttack(g, dir, opt)
	if err != nil {
		return fmt.Errorf("exhaustive search: %w", err)
	}
	switch {
	case fast == nil && brute == nil:
		return nil
	case fast == nil && brute != nil:
		return fmt.Errorf("%w: optimized search missed attack with %d hops", ErrSearchLimit, brute.Hops)
	case fast != nil && brute == nil:
		return fmt.Errorf("%w: optimized search returned attack not found exhaustively", ErrSearchLimit)
	case fast.Hops != brute.Hops:
		return fmt.Errorf("%w: optimized search found %d hops, exhaustive found %d", ErrSearchLimit, fast.Hops, brute.Hops)
	default:
		return nil
	}
}

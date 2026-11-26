package krb5policy

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

func TestUnauthorizedTransitSearch(t *testing.T) {
	g := newTestGraph(t, []TrustEdge{
		{From: "CLIENT", To: "EDGE"},
		{From: "EDGE", To: "CORE"},
		{From: "CLIENT", To: "TRUSTED"},
		{From: "TRUSTED", To: "CORE"},
	}, []CapathRule{{ClientRealm: "CLIENT", ServerRealm: "CORE", Intermediates: []string{"TRUSTED"}}}, nil)

	attack, err := FindShortestAttack(g, nil, AttackSearchOptions{
		StartRealm:        "CLIENT",
		GoalRealm:         "CORE",
		CompromisedRealms: []string{"EDGE"},
		MaxDepth:          4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if attack == nil {
		t.Fatal("expected an unauthorized transit attack")
	}
	if attack.Kind != AttackUnauthorizedTransit || attack.Hops != 2 || attack.CompromisedRealm != "EDGE" {
		t.Fatalf("attack = %+v", attack)
	}
	if len(attack.Realms) != 3 || attack.Realms[0] != "CLIENT" || attack.Realms[2] != "CORE" {
		t.Fatalf("realms = %v", attack.Realms)
	}
	if attack.Steps[len(attack.Steps)-1].Kind != StepForgedShortcut {
		t.Fatalf("steps = %+v", attack.Steps)
	}
}

func TestTransitSearchNegativeCases(t *testing.T) {
	edges := []TrustEdge{
		{From: "CLIENT", To: "EDGE"}, {From: "EDGE", To: "CORE"},
		{From: "CLIENT", To: "TRUSTED"}, {From: "TRUSTED", To: "CORE"},
	}
	if attack, err := FindShortestAttack(newTestGraph(t, edges, nil, nil), nil, AttackSearchOptions{
		StartRealm: "CLIENT", GoalRealm: "CORE", CompromisedRealms: []string{"EDGE"}, MaxDepth: 4,
	}); err != nil || attack != nil {
		t.Fatalf("without capaths attack=%+v err=%v", attack, err)
	}
	if attack, err := FindShortestAttack(newTestGraph(t, edges, []CapathRule{
		{ClientRealm: "CLIENT", ServerRealm: "CORE", Intermediates: []string{"TRUSTED"}},
	}, nil), nil, AttackSearchOptions{
		StartRealm: "CLIENT", GoalRealm: "CORE", MaxDepth: 4,
	}); err != nil || attack != nil {
		t.Fatalf("without compromised realm attack=%+v err=%v", attack, err)
	}
	if attack, err := FindShortestAttack(newTestGraph(t, edges, []CapathRule{
		{ClientRealm: "CLIENT", ServerRealm: "CORE", Intermediates: []string{"EDGE"}},
	}, nil), nil, AttackSearchOptions{
		StartRealm: "CLIENT", GoalRealm: "CORE", CompromisedRealms: []string{"EDGE"}, MaxDepth: 4,
	}); err != nil || attack != nil {
		t.Fatalf("authorized path attack=%+v err=%v", attack, err)
	}
}

func TestNotDelegatedBypassSearch(t *testing.T) {
	user := NewPrincipal("CLIENT", "user")
	svc := NewPrincipal("EDGE", "http", "app")
	target := NewPrincipal("CORE", "http", "db")
	dir, err := NewDirectory([]ServiceAccount{{
		Principal:           svc,
		AllowedToDelegateTo: []string{target.String()},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	g := newTestGraph(t, []TrustEdge{
		{From: "CLIENT", To: "EDGE"},
		{From: "EDGE", To: "CORE"},
	}, nil, nil)
	opt := AttackSearchOptions{
		StartRealm:        "CLIENT",
		GoalSPN:           target.String(),
		User:              user,
		UserProtected:     true,
		CompromisedRealms: []string{"EDGE"},
		MaxDepth:          4,
	}
	attack, err := FindShortestAttack(g, dir, opt)
	if err != nil {
		t.Fatal(err)
	}
	if attack == nil || attack.Kind != AttackNotDelegatedBypass || attack.Hops != 2 {
		t.Fatalf("attack = %+v", attack)
	}
	if !attack.BypassedProtection || len(attack.Steps) == 0 || attack.Steps[len(attack.Steps)-1].Kind != StepDelegation {
		t.Fatalf("steps = %+v", attack.Steps)
	}

	opt.WeakDelegationChecks = true
	opt.CompromisedRealms = nil
	if attack, err := FindShortestAttack(g, dir, opt); err != nil || attack == nil {
		t.Fatalf("weak delegation attack=%+v err=%v", attack, err)
	}

	opt.WeakDelegationChecks = false
	if attack, err := FindShortestAttack(g, dir, opt); err != nil || attack != nil {
		t.Fatalf("secure delegation attack=%+v err=%v", attack, err)
	}

	opt.GoalSPN = "http/other@CORE"
	if attack, err := FindShortestAttack(g, dir, opt); err != nil || attack != nil {
		t.Fatalf("wrong goal attack=%+v err=%v", attack, err)
	}
}

func TestCrossCheckSearchAgrees(t *testing.T) {
	g := newTestGraph(t, []TrustEdge{
		{From: "CLIENT", To: "EDGE"},
		{From: "EDGE", To: "CORE"},
		{From: "CLIENT", To: "TRUSTED"},
		{From: "TRUSTED", To: "CORE"},
	}, []CapathRule{{ClientRealm: "CLIENT", ServerRealm: "CORE", Intermediates: []string{"TRUSTED"}}}, nil)
	if err := CrossCheckSearch(g, nil, AttackSearchOptions{
		StartRealm:        "CLIENT",
		GoalRealm:         "CORE",
		CompromisedRealms: []string{"EDGE"},
		MaxDepth:          4,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSearchBudget(t *testing.T) {
	g := newTestGraph(t, []TrustEdge{
		{From: "A", To: "B"}, {From: "B", To: "C"}, {From: "C", To: "D"},
	}, []CapathRule{{ClientRealm: "A", ServerRealm: "D", Intermediates: []string{"X"}}}, nil)
	_, stats, err := FindShortestAttackWithStats(g, nil, AttackSearchOptions{
		StartRealm:        "A",
		GoalRealm:         "D",
		CompromisedRealms: []string{"C"},
		MaxDepth:          4,
		MaxPaths:          1,
	})
	if !errors.Is(err, ErrSearchLimit) || !stats.Truncated {
		t.Fatalf("budget err=%v stats=%+v", err, stats)
	}
}

func TestCrossCheckSmallGraphsZeroFalseNegatives(t *testing.T) {
	rng := rand.New(rand.NewSource(20261127))
	for iter := 0; iter < 250; iter++ {
		n := 4 + rng.Intn(2)
		names := make([]string, n)
		for i := range names {
			names[i] = fmt.Sprintf("R%d", i)
		}
		var edges []TrustEdge
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				if i == j {
					continue
				}
				switch rng.Intn(10) {
				case 0, 1:
					edges = append(edges, TrustEdge{From: names[i], To: names[j]})
				case 2:
					edges = append(edges, TrustEdge{From: names[i], To: names[j], Bidirectional: true})
				}
			}
		}
		var capaths []CapathRule
		if rng.Intn(2) == 0 {
			var hops []string
			for i := 1; i < n-1; i++ {
				if rng.Intn(2) == 0 {
					hops = append(hops, names[i])
				}
			}
			capaths = append(capaths, CapathRule{
				ClientRealm: names[0], ServerRealm: names[n-1], Intermediates: hops,
			})
		}
		g, err := NewTrustGraph(TrustGraphConfig{Realms: names, Edges: edges, Capaths: capaths})
		if err != nil {
			t.Fatalf("iter %d graph: %v", iter, err)
		}
		var compromised []string
		for i := 1; i < n; i++ {
			if rng.Intn(3) == 0 {
				compromised = append(compromised, names[i])
			}
		}
		opt := AttackSearchOptions{
			StartRealm:        names[0],
			GoalRealm:         names[n-1],
			CompromisedRealms: compromised,
			MaxDepth:          4,
			MaxPaths:          200000,
		}
		brute := bruteTransit(t, g, names[0], names[n-1], compromised, 4)
		fast, _, err := FindShortestAttackWithStats(g, nil, opt)
		if err != nil {
			t.Fatalf("iter %d fast: %v", iter, err)
		}
		bruteOut, _, err := ExhaustiveShortestAttack(g, nil, opt)
		if err != nil {
			t.Fatalf("iter %d exhaustive: %v", iter, err)
		}
		if attackHops(brute) != attackHops(fast) || attackHops(brute) != attackHops(bruteOut) {
			t.Fatalf("iter %d zero-false-negative mismatch: brute=%+v fast=%+v exhaustive=%+v",
				iter, brute, fast, bruteOut)
		}
	}
}

func TestCrossCheckDelegationZeroFalseNegatives(t *testing.T) {
	rng := rand.New(rand.NewSource(8848))
	for iter := 0; iter < 150; iter++ {
		n := 3 + rng.Intn(2)
		names := make([]string, n)
		for i := range names {
			names[i] = fmt.Sprintf("D%d", i)
		}
		var edges []TrustEdge
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				if i == j {
					continue
				}
				if rng.Intn(4) == 0 {
					edges = append(edges, TrustEdge{From: names[i], To: names[j]})
				}
			}
		}
		g, err := NewTrustGraph(TrustGraphConfig{Realms: names, Edges: edges, MaxPathLength: 5})
		if err != nil {
			t.Fatal(err)
		}
		user := NewPrincipal(names[0], "user")
		var services []ServiceAccount
		for i := 0; i < n; i++ {
			for k := 0; k < 2; k++ {
				svc := NewPrincipal(names[i], "http", fmt.Sprintf("svc%d", k))
				tr := i
				if rng.Intn(2) == 0 {
					tr = rng.Intn(n)
				}
				target := NewPrincipal(names[tr], "http", fmt.Sprintf("db%d", k))
				services = append(services, ServiceAccount{
					Principal:           svc,
					AllowedToDelegateTo: []string{target.String()},
				})
			}
		}
		dir, err := NewDirectory(services, []UserAccount{{Principal: user, Flags: AccountNotDelegated}})
		if err != nil {
			t.Fatal(err)
		}
		var compromised []string
		for i := 0; i < n; i++ {
			if rng.Intn(3) == 0 {
				compromised = append(compromised, names[i])
			}
		}
		weak := rng.Intn(4) == 0
		opt := AttackSearchOptions{
			StartRealm:           names[0],
			User:                 user,
			UserProtected:        true,
			CompromisedRealms:    compromised,
			WeakDelegationChecks: weak,
			MaxDepth:             4,
			MaxPaths:             200000,
		}
		brute := bruteDelegation(t, g, dir, opt)
		fast, _, err := FindShortestAttackWithStats(g, dir, opt)
		if err != nil {
			t.Fatalf("iter %d fast: %v", iter, err)
		}
		bruteOut, _, err := ExhaustiveShortestAttack(g, dir, opt)
		if err != nil {
			t.Fatalf("iter %d exhaustive: %v", iter, err)
		}
		if attackHops(brute) != attackHops(fast) || attackHops(brute) != attackHops(bruteOut) {
			t.Fatalf("iter %d zero-false-negative mismatch: brute=%+v fast=%+v exhaustive=%+v",
				iter, brute, fast, bruteOut)
		}
	}
}

func attackHops(a *AttackPath) int {
	if a == nil {
		return 0
	}
	return a.Hops
}

// bruteTransit is an independent small-graph reference enumerator.
func bruteTransit(t *testing.T, g *TrustGraph, start, goal string, compromised []string, maxDepth int) *AttackPath {
	t.Helper()
	startKey, startName, ok := g.lookup(start)
	if !ok {
		return nil
	}
	goalKey, goalName, ok := g.lookup(goal)
	if !ok || startKey == goalKey {
		return nil
	}
	comp := make(map[string]struct{}, len(compromised))
	for _, r := range compromised {
		comp[canonicalRealm(r)] = struct{}{}
	}
	var best *AttackPath
	visited := map[string]bool{startKey: true}
	path := []string{startName}
	var dfs func(cur string, hops int)
	dfs = func(cur string, hops int) {
		if hops >= maxDepth {
			return
		}
		for next := range g.adj[cur] {
			if next == goalKey {
				if _, ok := comp[cur]; !ok {
					continue
				}
				if _, err := g.ValidatePath(start, goal, path[1:]); err != nil && unauthorizedPathError(err) {
					best = chooseAttack(best, buildTransitAttack(path, cur, goalName, path[1:], g))
				}
				continue
			}
			if visited[next] {
				continue
			}
			visited[next] = true
			path = append(path, g.display[next])
			dfs(next, hops+1)
			path = path[:len(path)-1]
			visited[next] = false
		}
	}
	dfs(startKey, 0)
	return best
}

// bruteDelegation independently enumerates trust prefixes and delegation
// edges for the small-graph zero-false-negative check.
func bruteDelegation(t *testing.T, g *TrustGraph, dir *Directory, opt AttackSearchOptions) *AttackPath {
	t.Helper()
	startKey, startName, ok := g.lookup(opt.StartRealm)
	if !ok {
		return nil
	}
	comp := make(map[string]struct{}, len(opt.CompromisedRealms))
	for _, r := range opt.CompromisedRealms {
		comp[canonicalRealm(r)] = struct{}{}
	}
	var goalSPN *Principal
	if opt.GoalSPN != "" {
		p, _ := ParsePrincipal(opt.GoalSPN)
		goalSPN = &p
	}
	var best *AttackPath
	var walk func(cur string, hops int, path []string)
	walk = func(cur string, hops int, path []string) {
		if hops >= opt.MaxDepth {
			return
		}
		for _, svc := range dir.Services() {
			svcKey, ok := g.key(svc.Principal.Realm)
			if !ok || svcKey != cur {
				continue
			}
			bypass := opt.WeakDelegationChecks
			if !bypass {
				for _, r := range path {
					if _, ok := comp[canonicalRealm(r)]; ok {
						bypass = true
						break
					}
				}
			}
			if !bypass {
				continue
			}
			var intermediates []string
			if len(path) > 1 {
				intermediates = path[1 : len(path)-1]
			}
			if _, err := g.ValidatePath(opt.StartRealm, svc.Principal.Realm, intermediates); err != nil {
				continue
			}
			for _, raw := range svc.AllowedToDelegateTo {
				target, err := ParsePrincipal(raw)
				if err != nil {
					continue
				}
				if target.Realm == "" {
					target.Realm = svc.Principal.Realm
				}
				if goalSPN != nil {
					if goalSPN.Realm != "" && !target.Equal(*goalSPN) {
						continue
					}
					if goalSPN.Realm == "" && !principalNamesEqual(target, *goalSPN) {
						continue
					}
				}
				if opt.GoalRealm != "" && !sameRealm(target.Realm, opt.GoalRealm) {
					continue
				}
				if !sameRealm(svc.Principal.Realm, target.Realm) {
					if !g.HasDirectTrust(svc.Principal.Realm, target.Realm) {
						continue
					}
					if _, err := g.ValidatePath(svc.Principal.Realm, target.Realm, nil); err != nil {
						continue
					}
				}
				attack := buildDelegationAttack(path, svc, target, svc.Principal.Realm)
				if attack.Hops <= opt.MaxDepth {
					best = chooseAttack(best, attack)
				}
			}
		}
		if hops == opt.MaxDepth-1 {
			return
		}
		for next := range g.adj[cur] {
			found := false
			nextDisplay := g.display[next]
			for _, r := range path {
				if sameRealm(r, nextDisplay) {
					found = true
					break
				}
			}
			if found {
				continue
			}
			walk(next, hops+1, append(path, nextDisplay))
		}
	}
	walk(startKey, 0, []string{startName})
	return best
}

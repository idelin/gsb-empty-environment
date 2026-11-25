package closure_test

import (
	"fmt"
	"math/rand"
	"testing"

	closure "prescreen.local/ps-c690d01eea"
)

func randomCondition(r *rand.Rand, flags []string) closure.Condition {
	switch r.Intn(5) {
	case 0:
		return nil
	case 1:
		return closure.Condition{flags[r.Intn(len(flags))]: r.Intn(2) == 0}
	case 2:
		cond := closure.Condition{}
		cond[flags[r.Intn(len(flags))]] = r.Intn(2) == 0
		if r.Intn(2) == 0 {
			cond[flags[r.Intn(len(flags))]] = r.Intn(2) == 0
		}
		return cond
	default:
		return nil
	}
}

func TestRandomSmallGraphsMatchOracle(t *testing.T) {
	r := rand.New(rand.NewSource(20261126))
	flags := []string{"f0", "f1", "f2", "f3"}

	for seed := int64(0); seed < 120; seed++ {
		r.Seed(seed + 1)
		names := []string{"root", "a", "b", "c"}
		byName := map[string][]closure.Identity{}
		var packages []closure.Package
		root := closure.Identity{Name: "root", Version: 1, Source: "src-root", Digest: "digest-root"}
		byName["root"] = []closure.Identity{root}
		packages = append(packages, closure.Package{ID: root})

		for _, name := range names[1:] {
			count := 1 + r.Intn(2)
			for version := 1; version <= count; version++ {
				id := closure.Identity{
					Name:    name,
					Version: version,
					Source:  fmt.Sprintf("src-%s-%d", name, version),
					Digest:  fmt.Sprintf("digest-%s-%d", name, version),
				}
				byName[name] = append(byName[name], id)
				packages = append(packages, closure.Package{ID: id})
			}
		}

		for i := range packages {
			id := packages[i].ID
			depCount := r.Intn(3)
			deps := make([]closure.Dependency, 0, depCount)
			for d := 0; d < depCount; d++ {
				name := names[r.Intn(len(names))]
				all := byName[name]
				alternativeCount := 1 + r.Intn(len(all))
				alternatives := append([]closure.Identity(nil), all...)
				r.Shuffle(len(alternatives), func(x, y int) { alternatives[x], alternatives[y] = alternatives[y], alternatives[x] })
				alternatives = alternatives[:alternativeCount]
				deps = append(deps, closure.Dependency{
					Name:         fmt.Sprintf("%s-dep-%d", id.Name, d),
					When:         randomCondition(r, flags),
					Alternatives: alternatives,
				})
			}
			packages[i].Deps = deps
		}

		var attestations []closure.Attestation
		for _, pkg := range packages {
			if r.Intn(100) < 72 {
				generation := uint64(1)
				if r.Intn(10) == 0 {
					generation = 2
				}
				attestations = append(attestations, closure.Attestation{
					ID:               pkg.ID,
					PolicyGeneration: generation,
					Condition:        randomCondition(r, flags),
				})
			}
		}

		var clauses []closure.Clause
		clauseCount := r.Intn(4)
		for i := 0; i < clauseCount; i++ {
			lit := func() closure.Literal {
				return closure.Literal{Flag: flags[r.Intn(len(flags))], Negated: r.Intn(2) == 0}
			}
			clauses = append(clauses, closure.Either(lit(), lit()))
		}

		req := closure.Request{
			Root:             root,
			VariableOrder:    flags,
			PolicyGeneration: 1,
			Packages:         packages,
			Attestations:     attestations,
			Clauses:          clauses,
			NodeBudget:       -1,
		}
		report, err := closure.Analyze(req)
		if err != nil {
			t.Fatalf("seed %d Analyze: %v", seed, err)
		}
		if report.Status != closure.StatusDecided {
			t.Fatalf("seed %d status %s cause %v", seed, report.Status, report.Cause)
		}

		for bits := 0; bits < 16; bits++ {
			config := configFromBits(flags, bits)
			want, err := closure.ClassifyConfiguration(req, config)
			if err != nil {
				t.Fatalf("seed %d classify: %v", seed, err)
			}
			got := bddRegion(report, config)
			if got != want {
				t.Fatalf("seed %d config %v: BDD=%s oracle=%s", seed, config, got, want)
			}
		}
		for i, evidence := range report.Evidence {
			if err := closure.VerifyEvidence(req, evidence); err != nil {
				t.Fatalf("seed %d evidence %d: %v", seed, i, err)
			}
		}
	}
}

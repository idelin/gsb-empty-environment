package artifactflow_test

import (
	"testing"

	"prescreen.local/ddd-10563-v3/artifactflow"
)

// bruteForce independently re-solves a problem by enumerating every subset of
// realizations and checking global constraints directly. It is a slow oracle
// used only to cross-check the backtracking solver on small instances.
func bruteForce(p *artifactflow.Problem) (feasible bool, sensitive map[string][][]string) {
	indexed, err := artifactflow.ExportPrepare(p)
	if err != nil {
		return false, nil
	}
	en, err := artifactflow.ExportEnumerate(indexed)
	if err != nil {
		return false, nil
	}
	// Recursively choose at most one realization per step.
	stepRels := en.ByStep
	chosen := map[int]bool{}
	sensitive = map[string][][]string{}
	var walk func(step int)
	walk = func(step int) {
		if feasible {
			return
		}
		if step == len(stepRels) {
			if ok, attr := checkSelection(en, chosen, p); ok {
				feasible = true
				for k, v := range attr {
					sensitive[k] = append(sensitive[k], v)
				}
			}
			return
		}
		walk(step + 1) // skip step
		for _, ri := range stepRels[step] {
			chosen[ri] = true
			walk(step + 1)
			delete(chosen, ri)
		}
	}
	walk(0)
	return feasible, sensitive
}

func checkSelection(en *artifactflow.ExportEnum, chosen map[int]bool, p *artifactflow.Problem) (bool, map[string][]string) {
	consumedUse := map[int]int{}
	produced := map[int]int{}
	for ri := range chosen {
		r := en.All[ri]
		for _, out := range r.Outputs {
			if prev, ok := produced[out]; ok && prev != ri {
				return false, nil
			}
			produced[out] = ri
		}
		for _, in := range r.Inputs {
			if in.Consumed {
				consumedUse[in.Receipt]++
				if consumedUse[in.Receipt] > 1 {
					return false, nil
				}
			}
		}
	}
	// Every manifest receipt must be produced or a root.
	attr := map[string][]string{}
	for _, e := range p.Manifest {
		hits := en.Indexed.ReceiptsFor(string(e.Path), string(e.Digest))
		matched := false
		for _, recIdx := range hits {
			root := en.Indexed.Receipts[recIdx].Receipt.IsRoot()
			_, isProd := produced[recIdx]
			if root || isProd {
				matched = true
				if e.Sensitive {
					set := map[string]bool{}
					collect(en, chosen, produced, recIdx, map[int]bool{}, set)
					var list []string
					for k := range set {
						list = append(list, k)
					}
					attr[string(e.Path)] = sortedStrings(list)
				}
			}
		}
		if !matched {
			return false, nil
		}
	}
	return true, attr
}

func collect(en *artifactflow.ExportEnum, chosen map[int]bool, produced map[int]int, rec int, seen map[int]bool, set map[string]bool) {
	if seen[rec] {
		return
	}
	seen[rec] = true
	r := en.Indexed.Receipts[rec].Receipt
	if r.IsRoot() {
		set[string(r.RootSource)] = true
	}
	if ri, ok := produced[rec]; ok && chosen[ri] {
		for _, in := range en.All[ri].Inputs {
			collect(en, chosen, produced, in.Receipt, seen, set)
		}
	}
}

func sortedStrings(s []string) []string {
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
	return s
}

func TestDifferentialAgainstBruteForce(t *testing.T) {
	problems := buildDifferentialProblems()
	for i, p := range problems {
		report, err := artifactflow.Verify(p)
		if err != nil {
			t.Fatalf("case %d: verify error %v", i, err)
		}
		okBF, attr := bruteForce(p)
		if okBF != (report.Status != artifactflow.StatusInfeasible) {
			t.Fatalf("case %d: feasibility mismatch solver=%s bf=%v", i, report.Status, okBF)
		}
		if !okBF {
			continue
		}
		// Ambiguity check for sensitive entries.
		for _, e := range p.Manifest {
			if !e.Sensitive {
				continue
			}
			sets := attr[string(e.Path)]
			unique := map[string]bool{}
			for _, s := range sets {
				unique[join(s)] = true
			}
			bfAmbiguous := len(unique) > 1
			solverAmbiguous := report.Status == artifactflow.StatusAmbiguous &&
				string(report.Ambiguity.ManifestPath) == string(e.Path)
			if bfAmbiguous != solverAmbiguous {
				t.Fatalf("case %d entry %s: ambiguity mismatch solver=%v bf=%v", i, e.Path, solverAmbiguous, bfAmbiguous)
			}
		}
	}
}

func join(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ","
		}
		out += v
	}
	return out
}

func buildDifferentialProblems() []*artifactflow.Problem {
	var ps []*artifactflow.Problem
	dg := func(s string) artifactflow.Digest { return digestOf(s) }
	root := func(id, path, content, src string) artifactflow.Receipt {
		return rootReceipt(id, artifactflow.Path(path), content, src, roleVendor)
	}
	out := func(id, path, content, step string) artifactflow.Receipt {
		return outputReceipt(id, artifactflow.Path(path), content, step, roleBuilder)
	}
	// Simple linear chain.
	ps = append(ps, &artifactflow.Problem{
		TenantRoot: "t",
		Receipts: []artifactflow.Receipt{
			root("r1", "t/in/a", "A", "s1"),
			out("m1", "t/mid/a", "M", "build"),
			out("f1", "t/out/a", "F", "pack"),
		},
		Steps: []artifactflow.Step{
			{ID: "build", Candidates: []artifactflow.Candidate{{
				Name:    "c",
				Inputs:  []artifactflow.Slot{{Name: "in", Path: "t/in/a", Digest: dg("A"), AllowedSigners: []artifactflow.SignerRole{roleVendor}}},
				Outputs: []artifactflow.Slot{{Name: "o", Path: "t/mid/a", Digest: dg("M"), AllowedSigners: []artifactflow.SignerRole{roleBuilder}}},
				Rules:   []artifactflow.Rule{{Input: "in", Output: "o"}},
			}}},
			{ID: "pack", Candidates: []artifactflow.Candidate{{
				Name:    "c",
				Inputs:  []artifactflow.Slot{{Name: "in", Path: "t/mid/a", Digest: dg("M"), AllowedSigners: []artifactflow.SignerRole{roleBuilder}}},
				Outputs: []artifactflow.Slot{{Name: "o", Path: "t/out/a", Digest: dg("F"), AllowedSigners: []artifactflow.SignerRole{roleBuilder}}},
				Rules:   []artifactflow.Rule{{Input: "in", Output: "o"}},
			}}},
		},
		Manifest: []artifactflow.ManifestEntry{{Path: "t/out/a", Digest: dg("F"), Sensitive: true}},
	})
	// Forked shared input: two steps consume one shareable root.
	shared := root("sh", "t/in/s", "S", "src")
	ps = append(ps, &artifactflow.Problem{
		TenantRoot: "t",
		Receipts: []artifactflow.Receipt{
			shared,
			out("o1", "t/out/1", "O1", "b1"),
			out("o2", "t/out/2", "O2", "b2"),
		},
		Steps: []artifactflow.Step{
			forkStep("b1", "t/out/1", "O1"),
			forkStep("b2", "t/out/2", "O2"),
		},
		Manifest: []artifactflow.ManifestEntry{
			{Path: "t/out/1", Digest: dg("O1"), Sensitive: true},
			{Path: "t/out/2", Digest: dg("O2"), Sensitive: true},
		},
	})
	// Same digest two roots -> ambiguous.
	rootA := root("ra", "t/in/a", "DUP", "sa")
	rootB := root("rb", "t/in/b", "DUP", "sb")
	ps = append(ps, &artifactflow.Problem{
		TenantRoot: "t",
		Receipts: []artifactflow.Receipt{
			rootA, rootB,
			out("o", "t/out/f", "F", "build"),
		},
		Steps: []artifactflow.Step{{
			ID: "build", Candidates: []artifactflow.Candidate{{
				Name: "c",
				Inputs: []artifactflow.Slot{
					{Name: "i1", Path: "t/in/a", Digest: dg("DUP"), AllowedSigners: []artifactflow.SignerRole{roleVendor}},
					{Name: "i2", Path: "t/in/b", Digest: dg("DUP"), AllowedSigners: []artifactflow.SignerRole{roleVendor}},
				},
				Outputs: []artifactflow.Slot{{Name: "o", Path: "t/out/f", Digest: dg("F"), AllowedSigners: []artifactflow.SignerRole{roleBuilder}}},
				Rules:   []artifactflow.Rule{{Input: "i1", Output: "o"}},
			}},
		}},
		Manifest: []artifactflow.ManifestEntry{{Path: "t/out/f", Digest: dg("F"), Sensitive: true}},
	})
	// Consumed root spent on two steps must be infeasible.
	consumedRoot := root("c", "t/in/c", "C", "src")
	ps = append(ps, &artifactflow.Problem{
		TenantRoot: "t",
		Receipts: []artifactflow.Receipt{
			consumedRoot,
			out("x1", "t/out/x1", "X1", "x1"),
			out("x2", "t/out/x2", "X2", "x2"),
		},
		Steps: []artifactflow.Step{
			consumedForkStep("x1", "t/out/x1", "X1"),
			consumedForkStep("x2", "t/out/x2", "X2"),
		},
		Manifest: []artifactflow.ManifestEntry{
			{Path: "t/out/x1", Digest: dg("X1"), Sensitive: true},
			{Path: "t/out/x2", Digest: dg("X2"), Sensitive: true},
		},
	})
	return ps
}

func consumedForkStep(id, outPath, outContent string) artifactflow.Step {
	st := forkStep(id, outPath, outContent)
	st.Candidates[0].Inputs[0].Consumed = true
	return st
}

func forkStep(id, outPath, outContent string) artifactflow.Step {
	return artifactflow.Step{ID: id, Candidates: []artifactflow.Candidate{{
		Name: "c",
		Inputs: []artifactflow.Slot{{
			Name: "in", Path: "t/in/s", Digest: digestOf("S"),
			AllowedSigners: []artifactflow.SignerRole{roleVendor},
		}},
		Outputs: []artifactflow.Slot{{
			Name: "o", Path: artifactflow.Path(outPath), Digest: digestOf(outContent),
			AllowedSigners: []artifactflow.SignerRole{roleBuilder},
		}},
		Rules: []artifactflow.Rule{{Input: "in", Output: "o"}},
	}}}
}

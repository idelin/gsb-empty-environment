package netsec

import (
	"math"
	"math/bits"
	"strconv"
	"testing"
)

func handAnalysis(t *testing.T) *Analysis {
	t.Helper()
	secrets := []string{"s1", "s2", "s3", "s4"}
	plugins := []Plugin{
		{Name: "region", Labels: []string{"east", "east", "west", "east"}},
		{Name: "device", Labels: []string{"phone", "laptop", "phone", "laptop"}},
		{Name: "tier", Labels: []string{"a", "b", "a", "c"}},
	}
	analysis, err := NewAnalysis(secrets, plugins, nil)
	if err != nil {
		t.Fatalf("NewAnalysis: %v", err)
	}
	return analysis
}

func classSecrets(partition *Partition) [][]string {
	classes := make([][]string, len(partition.Classes))
	for idx, class := range partition.Classes {
		classes[idx] = append(classes[idx], class.Secrets...)
	}
	return classes
}

func TestRefinementHand(t *testing.T) {
	analysis := handAnalysis(t)

	empty := analysis.Refine(0)
	if got := classSecrets(empty); len(got) != 1 || len(got[0]) != 4 {
		t.Fatalf("empty refinement = %v, want one class of four", got)
	}
	if !floatsEqual(empty.Classes[0].Mass, 1.0) {
		t.Fatalf("empty class mass = %v, want 1", empty.Classes[0].Mass)
	}

	region, err := analysis.MaskOf([]string{"region"})
	if err != nil {
		t.Fatal(err)
	}
	one := analysis.Refine(region)
	if got := classSecrets(one); len(got) != 2 {
		t.Fatalf("region refinement = %v, want two classes", got)
	}
	if got := one.Classes[0].Secrets; len(got) != 3 || got[0] != "s1" || got[2] != "s4" {
		t.Fatalf("region first class = %v, want [s1 s2 s4]", got)
	}
	if !floatsEqual(one.Classes[0].Mass, 0.75) || !floatsEqual(one.Classes[1].Mass, 0.25) {
		t.Fatalf("region masses = %v %v, want 0.75 0.25", one.Classes[0].Mass, one.Classes[1].Mass)
	}

	two, err := analysis.RefineNames([]string{"region", "device"})
	if err != nil {
		t.Fatal(err)
	}
	if got := classSecrets(two); len(got) != 3 {
		t.Fatalf("region+device = %v, want three classes", got)
	}
	if !floatsEqual(two.BayesRate, 0.75) {
		t.Fatalf("bayes rate = %v, want 0.75", two.BayesRate)
	}
	if !floatsEqual(two.SingletonRate, 0.5) {
		t.Fatalf("singleton rate = %v, want 0.5", two.SingletonRate)
	}

	full := analysis.Refine(uint32(1)<<uint(analysis.PluginCount()) - 1)
	if len(full.Classes) != 4 {
		t.Fatalf("full refinement has %d classes, want 4 singletons", len(full.Classes))
	}
	for _, class := range full.Classes {
		if len(class.Members) != 1 || !floatsEqual(class.Mass, 0.25) {
			t.Fatalf("full class not singleton: %+v", class)
		}
	}
	if !floatsEqual(full.BayesRate, 1.0) || !floatsEqual(full.SingletonRate, 1.0) {
		t.Fatalf("full rates = %v/%v, want 1/1", full.BayesRate, full.SingletonRate)
	}
}

func TestAbstention(t *testing.T) {
	secrets := []string{"x", "y", "z"}
	plugins := []Plugin{
		{Name: "silent", Labels: []string{"", "", ""}},
		{Name: "partial", Labels: []string{"a", "", "a"}},
	}
	analysis, err := NewAnalysis(secrets, plugins, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A fully silent plugin keeps the whole domain in one class.
	if partition := analysis.Refine(1); len(partition.Classes) != 1 {
		t.Fatalf("silent plugin splits the domain: %v", classSecrets(partition))
	}

	// An abstention is treated as the secret's own label value: x and z share
	// label "a", while y's empty label forms its own cell.
	partition := analysis.Refine(2)
	if len(partition.Classes) != 2 {
		t.Fatalf("partial plugin classes = %v, want two cells", classSecrets(partition))
	}
}

func TestMinimalSets(t *testing.T) {
	analysis := handAnalysis(t)

	fullMasks, err := analysis.MinimalSetIndices(1.0, MetricBayes)
	if err != nil {
		t.Fatal(err)
	}
	if len(fullMasks) != 1 || bits.OnesCount32(fullMasks[0]) != 2 {
		t.Fatalf("bayes 1.0 masks = %b, want one two-plugin set", fullMasks)
	}

	sets, err := analysis.MinimalSets(1.0, MetricSingleton)
	if err != nil {
		t.Fatal(err)
	}
	if len(sets) != 1 || len(sets[0]) != 2 {
		t.Fatalf("singleton 1.0 sets = %v, want one two-plugin set", sets)
	}

	half, err := analysis.MinimalSetIndices(0.75, MetricBayes)
	if err != nil {
		t.Fatal(err)
	}
	for _, mask := range half {
		if count := bits.OnesCount32(mask); count < 1 || count > 2 {
			t.Fatalf("mask %b has %d plugins, want one or two", mask, count)
		}
		if bayes, _ := analysis.rates(mask); !reaches(bayes, 0.75) {
			t.Fatalf("returned mask %b does not reach threshold", mask)
		}
	}
	if len(half) == 0 {
		t.Fatal("expected at least one solution at 0.75")
	}

	if _, err := analysis.MinimalSetIndices(1.01, MetricBayes); err != ErrUnreachable {
		t.Fatalf("unreachable threshold returned err = %v, want ErrUnreachable", err)
	}

	zero, err := analysis.MinimalSetIndices(0, MetricBayes)
	if err != nil || len(zero) != 1 || zero[0] != 0 {
		t.Fatalf("threshold zero = %v, %v; want empty combination", zero, err)
	}
}

func TestMinimalMatchesExhaustive(t *testing.T) {
	analysis := handAnalysis(t)
	results := analysis.Exhaustive()
	if len(results) != 1<<analysis.PluginCount() {
		t.Fatalf("exhaustive returned %d rows, want %d", len(results), 1<<analysis.PluginCount())
	}

	for _, metric := range []Metric{MetricBayes, MetricSingleton} {
		for _, threshold := range []float64{0.25, 0.5, 0.75, 0.8, 1.0} {
			got, err := analysis.MinimalSetIndices(threshold, metric)
			if err != nil {
				t.Fatalf("metric %d threshold %v: %v", metric, threshold, err)
			}

			wantMinimum := math.MaxInt
			feasible := map[uint32]bool{}
			for _, result := range results {
				rate := result.BayesRate
				if metric == MetricSingleton {
					rate = result.SingletonRate
				}
				if reaches(rate, threshold) {
					feasible[result.Mask] = true
					if count := bits.OnesCount32(result.Mask); count < wantMinimum {
						wantMinimum = count
					}
				}
			}

			for _, mask := range got {
				if !feasible[mask] {
					t.Fatalf("metric %d threshold %v: infeasible mask %b returned", metric, threshold, mask)
				}
				if bits.OnesCount32(mask) != wantMinimum {
					t.Fatalf("metric %d threshold %v: mask %b size %d, want %d",
						metric, threshold, mask, bits.OnesCount32(mask), wantMinimum)
				}
			}

			count := 0
			for mask := range feasible {
				if bits.OnesCount32(mask) == wantMinimum {
					count++
				}
			}
			if count != len(got) {
				t.Fatalf("metric %d threshold %v: got %d optimum masks, exhaustive finds %d",
					metric, threshold, len(got), count)
			}
		}
	}
}

func TestVerifyCrossChecks(t *testing.T) {
	analysis := handAnalysis(t)
	report := analysis.Verify()
	if !report.OK() {
		t.Fatalf("cross-check disagreements:\n%s", FormatVerifyReport(report))
	}
	if report.Subsets != 1<<analysis.PluginCount() || report.Secrets != 4 {
		t.Fatalf("report coverage = %+v", report)
	}

	weighted, err := NewAnalysis(
		[]string{"s1", "s2", "s3", "s4"},
		[]Plugin{
			{Name: "region", Labels: []string{"east", "east", "west", "east"}},
			{Name: "device", Labels: []string{"phone", "laptop", "phone", "laptop"}},
		},
		[]float64{4, 2, 3, 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	if report := weighted.Verify(); !report.OK() {
		t.Fatalf("weighted cross-check disagreements:\n%s", FormatVerifyReport(report))
	}
	partition := weighted.Refine(3)
	var total float64
	for _, class := range partition.Classes {
		total += class.Mass
	}
	if !floatsEqual(total, 1.0) {
		t.Fatalf("class masses sum to %v, want 1", total)
	}

	if _, err := weighted.GreedySet(1.0, MetricBayes); err != ErrUnreachable {
		t.Fatalf("unreachable greedy threshold returned err = %v, want ErrUnreachable", err)
	}
	greedy, err := weighted.GreedySet(0.8, MetricBayes)
	if err != nil {
		t.Fatal(err)
	}
	mask, err := weighted.MaskOf(greedy)
	if err != nil {
		t.Fatal(err)
	}
	if bayes, _ := weighted.rates(mask); !reaches(bayes, 0.8) {
		t.Fatalf("greedy set %v does not reach the requested rate", greedy)
	}
}

func TestValidation(t *testing.T) {
	if _, err := NewAnalysis(nil, nil, nil); err == nil {
		t.Fatal("empty domain accepted")
	}
	if _, err := NewAnalysis([]string{"a", "a"}, nil, nil); err == nil {
		t.Fatal("duplicate secrets accepted")
	}
	secrets := []string{"a", "b"}
	if _, err := NewAnalysis(secrets, []Plugin{{Name: "p", Labels: []string{"x"}}}, nil); err == nil {
		t.Fatal("label length mismatch accepted")
	}
	if _, err := NewAnalysis(secrets, nil, []float64{1, -1}); err == nil {
		t.Fatal("negative weight accepted")
	}
	if _, err := NewAnalysis(secrets, nil, []float64{0, 0}); err == nil {
		t.Fatal("zero total weight accepted")
	}

	many := make([]Plugin, MaxPlugins+1)
	for idx := range many {
		many[idx] = Plugin{Name: string(rune('a' + idx)), Labels: []string{"", ""}}
	}
	if _, err := NewAnalysis(secrets, many, nil); err == nil {
		t.Fatal("more than MaxPlugins accepted")
	}

	analysis := handAnalysis(t)
	if _, err := analysis.PluginIndex("missing"); err == nil {
		t.Fatal("unknown plugin name accepted")
	}
}

func TestTwentyPluginsScale(t *testing.T) {
	const n = 20
	secrets := make([]string, n)
	for i := 0; i < n; i++ {
		secrets[i] = "sec" + string(rune('a'+i))
	}
	plugins := make([]Plugin, MaxPlugins)
	for p := 0; p < MaxPlugins; p++ {
		labels := make([]string, MaxPlugins)
		if p < 5 {
			// The first five plugins emit unique bit-pattern labels.
			for s := 0; s < MaxPlugins; s++ {
				if (s>>uint(p))&1 == 1 {
					labels[s] = "b1"
				} else {
					labels[s] = "b0"
				}
			}
		} else {
			// The remaining plugins reveal nothing.
			for s := range labels {
				labels[s] = "public"
			}
		}
		plugins[p] = Plugin{Name: "plugin" + string(rune('a'+p)), Labels: labels}
	}

	analysis, err := NewAnalysis(secrets, plugins, nil)
	if err != nil {
		t.Fatal(err)
	}

	masks, err := analysis.MinimalSetIndices(1.0, MetricBayes)
	if err != nil {
		t.Fatal(err)
	}
	if len(masks) != 1 {
		t.Fatalf("full identification has %d optimum masks, want exactly one", len(masks))
	}
	for _, mask := range masks {
		if bits.OnesCount32(mask) != 5 {
			t.Fatalf("mask %b identifies with %d plugins, want five informative ones",
				mask, bits.OnesCount32(mask))
		}
	}

	// Any minimum solution must never include a non-informative plugin.
	informative := uint32(1<<5 - 1)
	if masks[0] != informative {
		t.Fatalf("optimum mask = %b, want %b", masks[0], informative)
	}

	// All 2^20 combinations stay consistent with the independent enumeration
	// for the rates the DFS actually evaluates; check monotonicity globally.
	if violations := analysis.CheckMonotonic(); len(violations) != 0 {
		t.Fatalf("monotonicity violated: %v", violations)
	}
}

func TestVerifyAtTwelvePlugins(t *testing.T) {
	const n = 12
	secrets := make([]string, 5)
	for i := range secrets {
		secrets[i] = "k" + strconv.Itoa(i)
	}
	plugins := make([]Plugin, n)
	for p := 0; p < n; p++ {
		labels := make([]string, len(secrets))
		for s := range labels {
			labels[s] = "L" + strconv.Itoa((s*(p+3)+p)%11)
		}
		plugins[p] = Plugin{Name: "p" + strconv.Itoa(p), Labels: labels}
	}
	analysis, err := NewAnalysis(secrets, plugins, nil)
	if err != nil {
		t.Fatal(err)
	}
	report := analysis.Verify()
	if !report.OK() {
		t.Fatalf("cross-check disagreements over %d subsets:\n%s",
			report.Subsets, FormatVerifyReport(report))
	}
	if report.Subsets != 1<<n {
		t.Fatalf("verified %d subsets, want %d", report.Subsets, 1<<n)
	}
}

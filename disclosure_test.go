package netsec_test

import (
	"errors"
	"fmt"
	"math"
	"testing"

	netsec "example.org/netsec/q0491"
)

func subjectIDs(prefix string, count int) []string {
	subjects := make([]string, count)
	for i := range subjects {
		subjects[i] = fmt.Sprintf("%s-%03d", prefix, i+1)
	}
	return subjects
}

func summary(key string, subjects []string, count, value float64) netsec.GroupSummary {
	return netsec.GroupSummary{
		Key:      key,
		Subjects: subjects,
		Count:    count,
		Value:    value,
	}
}

func engineeredDataset(t *testing.T, groupSizes map[string]int) *netsec.Dataset {
	t.Helper()
	records := make([]netsec.Record, 0)
	for group, size := range groupSizes {
		for i, subject := range subjectIDs(group, size) {
			records = append(records, netsec.Record{
				SubjectID: subject,
				Group:     group,
				Values: map[string]float64{
					"amount":    float64(i + 1),
					"diagnosis": float64(i % 2),
				},
				Sensitive: true,
			})
		}
	}
	dataset, err := netsec.NewDataset(records)
	if err != nil {
		t.Fatalf("NewDataset: %v", err)
	}
	return dataset
}

func deterministicPolicy() netsec.Policy {
	policy := netsec.DefaultPolicy()
	policy.NoiseMode = netsec.NoiseNone
	policy.RoundingBase = 5
	policy.TotalBudget = 1
	policy.SubjectBudget = 1
	policy.DefaultEpsilon = 0.1
	return policy
}

func TestTinyGroupsAreSuppressedWhileLegalGroupsRelease(t *testing.T) {
	dataset := engineeredDataset(t, map[string]int{"tiny": 2, "large": 10})
	engine, err := netsec.NewEngine(deterministicPolicy(), dataset)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	result, err := engine.ExecuteDataset([]string{"tiny", "large"}, netsec.StatisticCount, "", 0.1)
	if err != nil {
		t.Fatalf("ExecuteDataset: %v", err)
	}
	if len(result.Groups) != 2 {
		t.Fatalf("got %d groups, want 2", len(result.Groups))
	}

	tiny := result.Groups[0]
	if tiny.Released {
		t.Fatal("tiny group was released despite falling below MinGroupSize")
	}
	if !containsReason(tiny.Reasons, netsec.ReasonMinimumGroupSize) {
		t.Fatalf("tiny reasons = %v, want minimum group size", tiny.Reasons)
	}

	large := result.Groups[1]
	if !large.Released || large.Value != 10 {
		t.Fatalf("large group = %+v, want released count 10", large)
	}
	if !result.AnyReleased {
		t.Fatal("AnyReleased = false, want true")
	}
	if status := engine.Budget(); !almostEqual(status.BudgetSpent, 0.1) {
		t.Fatalf("BudgetSpent = %v, want 0.1 even though one cell was suppressed", status.BudgetSpent)
	}
}

func TestSensitiveAttributeThresholdAndSuppressAll(t *testing.T) {
	dataset := engineeredDataset(t, map[string]int{"small-sensitive": 7, "large-sensitive": 12})

	thresholdPolicy := deterministicPolicy()
	thresholdPolicy.SensitiveAttributes = map[string]bool{"diagnosis": true}
	thresholdPolicy.SensitiveMinGroupSize = 10
	thresholdEngine, err := netsec.NewEngine(thresholdPolicy, dataset)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	result, err := thresholdEngine.ExecuteDataset(
		[]string{"small-sensitive", "large-sensitive"}, netsec.StatisticSum, "diagnosis", 0.1)
	if err != nil {
		t.Fatalf("ExecuteDataset: %v", err)
	}
	if result.Groups[0].Released || !containsReason(result.Groups[0].Reasons, netsec.ReasonSensitiveAttribute) {
		t.Fatalf("small sensitive group = %+v, want sensitive suppression", result.Groups[0])
	}
	if !result.Groups[1].Released {
		t.Fatalf("large sensitive group should pass the stricter threshold: %+v", result.Groups[1])
	}

	suppressPolicy := thresholdPolicy
	suppressPolicy.SensitiveMode = netsec.SensitiveSuppressAll
	suppressEngine, err := netsec.NewEngine(suppressPolicy, dataset)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	result, err = suppressEngine.ExecuteDataset(
		[]string{"large-sensitive"}, netsec.StatisticSum, "diagnosis", 0.1)
	if err != nil {
		t.Fatalf("ExecuteDataset: %v", err)
	}
	if result.Groups[0].Released {
		t.Fatal("sensitive aggregate was released in suppress-all mode")
	}
}

func TestSmallSensitiveSubgroupIsSuppressed(t *testing.T) {
	policy := deterministicPolicy()
	policy.SensitiveMinGroupSize = 5
	engine, err := netsec.NewEngine(policy)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	group := summary("cohort", subjectIDs("cohort", 20), 20, 20)
	group.Sensitive = true
	group.SensitiveCount = 2

	result, err := engine.Execute(netsec.Query{
		ID:        "subgroup",
		Statistic: netsec.StatisticCount,
		Groups:    []netsec.GroupSummary{group},
		Epsilon:   0.1,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Groups[0].Released {
		t.Fatal("cell with a small sensitive subgroup was released")
	}
	if !containsReason(result.Groups[0].Reasons, netsec.ReasonSensitiveSubgroup) {
		t.Fatalf("reasons = %v, want sensitive subgroup", result.Groups[0].Reasons)
	}
}

func TestDifferenceQueriesAndDuplicatesAreRejected(t *testing.T) {
	policy := deterministicPolicy()
	policy.MaxQueryOverlap = 0.8
	engine, err := netsec.NewEngine(policy)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	all := subjectIDs("p", 20)
	subset := all[:19]
	first, err := engine.Execute(netsec.Query{
		ID:        "all",
		Statistic: netsec.StatisticCount,
		Groups:    []netsec.GroupSummary{summary("all", all, 20, 20)},
		Epsilon:   0.1,
	})
	if err != nil {
		t.Fatalf("first query: %v", err)
	}
	if !first.Groups[0].Released || first.Groups[0].Value != 20 {
		t.Fatalf("first result = %+v, want count 20", first.Groups[0])
	}

	_, err = engine.Execute(netsec.Query{
		ID:        "subset",
		Statistic: netsec.StatisticCount,
		Groups:    []netsec.GroupSummary{summary("subset", subset, 19, 19)},
		Epsilon:   0.1,
	})
	if !errors.Is(err, netsec.ErrCrossQueryRestricted) {
		t.Fatalf("subset query error = %v, want ErrCrossQueryRestricted", err)
	}

	_, err = engine.Execute(netsec.Query{
		ID:        "all-again",
		Statistic: netsec.StatisticCount,
		Groups:    []netsec.GroupSummary{summary("all", all, 20, 20)},
		Epsilon:   0.1,
	})
	if !errors.Is(err, netsec.ErrDuplicateQuery) {
		t.Fatalf("duplicate query error = %v, want ErrDuplicateQuery", err)
	}
}

func TestIndividualAttributeCannotBeIsolatedEndToEnd(t *testing.T) {
	policy := deterministicPolicy()
	policy.MinGroupSize = 5
	policy.SensitiveMinGroupSize = 10

	t.Run("tiny cell reveals no value", func(t *testing.T) {
		engine, err := netsec.NewEngine(policy)
		if err != nil {
			t.Fatalf("NewEngine: %v", err)
		}
		individual := netsec.GroupSummary{
			Key:            "individual",
			Subjects:       []string{"target"},
			Count:          1,
			Value:          1,
			Sensitive:      true,
			SensitiveCount: 1,
		}
		cell, err := engine.Execute(netsec.Query{
			ID:          "probe-individual",
			Statistic:   netsec.StatisticSum,
			Attribute:   "condition",
			Sensitive:   true,
			Groups:      []netsec.GroupSummary{individual},
			Epsilon:     0.1,
			Sensitivity: 1,
		})
		if err != nil {
			t.Fatalf("tiny query: %v", err)
		}
		if cell.Groups[0].Released || cell.Groups[0].Value != 0 {
			t.Fatalf("individual cell leaked: %+v", cell.Groups[0])
		}
	})

	t.Run("superset subtraction is blocked", func(t *testing.T) {
		engine, err := netsec.NewEngine(policy)
		if err != nil {
			t.Fatalf("NewEngine: %v", err)
		}
		all := subjectIDs("cohort", 20)
		first, err := engine.Execute(netsec.Query{
			ID:        "cohort",
			Statistic: netsec.StatisticCount,
			Groups:    []netsec.GroupSummary{summary("cohort", all, 20, 20)},
			Epsilon:   0.1,
		})
		if err != nil {
			t.Fatalf("cohort query: %v", err)
		}
		if !first.Groups[0].Released || first.Groups[0].Value != 20 {
			t.Fatalf("cohort aggregate = %+v, want rounded count 20", first.Groups[0])
		}
		_, err = engine.Execute(netsec.Query{
			ID:        "cohort-minus-target",
			Statistic: netsec.StatisticCount,
			Groups:    []netsec.GroupSummary{summary("cohort-minus-target", all[1:], 19, 19)},
			Epsilon:   0.1,
		})
		if !errors.Is(err, netsec.ErrCrossQueryRestricted) {
			t.Fatalf("subtraction query error = %v, want ErrCrossQueryRestricted", err)
		}
	})
}

func TestSubjectBudgetAccumulatesAcrossIntersectingQueries(t *testing.T) {
	policy := deterministicPolicy()
	policy.TotalBudget = 10
	policy.SubjectBudget = 0.25
	policy.MaxQueryOverlap = 1.0
	engine, err := netsec.NewEngine(policy)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	queries := []netsec.Query{
		{ID: "q1", Statistic: netsec.StatisticCount, Groups: []netsec.GroupSummary{
			summary("g1", []string{"s1", "s2", "s3", "s4", "s5"}, 5, 5)}},
		{ID: "q2", Statistic: netsec.StatisticCount, Groups: []netsec.GroupSummary{
			summary("g2", []string{"s3", "s4", "s5", "s6", "s7"}, 5, 5)}},
		{ID: "q3", Statistic: netsec.StatisticCount, Groups: []netsec.GroupSummary{
			summary("g3", []string{"s5", "s6", "s7", "s8", "s9"}, 5, 5)}},
	}
	for i, query := range queries {
		query.Epsilon = 0.1
		_, err := engine.Execute(query)
		if i < 2 && err != nil {
			t.Fatalf("%s: %v", query.ID, err)
		}
		if i == 2 && !errors.Is(err, netsec.ErrBudgetExhausted) {
			t.Fatalf("%s error = %v, want ErrBudgetExhausted", query.ID, err)
		}
	}

	spent, count := engine.SubjectStatus("s5")
	if !almostEqual(spent, 0.2) || count != 2 {
		t.Fatalf("s5 status = (%.3f, %d), want (0.200, 2)", spent, count)
	}
	spent, count = engine.SubjectStatus("s9")
	if spent != 0 || count != 0 {
		t.Fatalf("s9 status = (%.3f, %d), want no charge after rejection", spent, count)
	}
}

func TestPerSubjectQueryLimit(t *testing.T) {
	policy := deterministicPolicy()
	policy.TotalBudget = 10
	policy.SubjectBudget = 10
	policy.MaxQueriesPerSubject = 2
	policy.MaxQueryOverlap = 1.0
	engine, err := netsec.NewEngine(policy)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	sets := [][]string{
		{"s1", "s2", "s3", "s4", "s5"},
		{"s1", "s6", "s7", "s8", "s9"},
		{"s1", "s10", "s11", "s12", "s13"},
	}
	for i, subjects := range sets {
		_, err := engine.Execute(netsec.Query{
			ID:        fmt.Sprintf("limit-%d", i),
			Statistic: netsec.StatisticCount,
			Groups:    []netsec.GroupSummary{summary(fmt.Sprintf("set-%d", i), subjects, 5, 5)},
			Epsilon:   0.1,
		})
		if i < 2 && err != nil {
			t.Fatalf("query %d: %v", i, err)
		}
		if i == 2 && !errors.Is(err, netsec.ErrSubjectQueryLimit) {
			t.Fatalf("query %d error = %v, want ErrSubjectQueryLimit", i, err)
		}
	}
}

func TestGlobalBudgetIsEnforced(t *testing.T) {
	policy := deterministicPolicy()
	policy.TotalBudget = 0.25
	policy.SubjectBudget = 10
	policy.MaxQueryOverlap = 1.0
	engine, err := netsec.NewEngine(policy)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	for i := 0; i < 2; i++ {
		_, err := engine.Execute(netsec.Query{
			ID:        fmt.Sprintf("budget-%d", i),
			Statistic: netsec.StatisticCount,
			Groups: []netsec.GroupSummary{
				summary(fmt.Sprintf("budget-group-%d", i), subjectIDs(fmt.Sprintf("b%d", i), 10), 10, 10),
			},
			Epsilon: 0.1,
		})
		if err != nil {
			t.Fatalf("query %d: %v", i, err)
		}
	}
	_, err = engine.Execute(netsec.Query{
		ID:        "over-budget",
		Statistic: netsec.StatisticCount,
		Groups: []netsec.GroupSummary{
			summary("third", subjectIDs("third", 10), 10, 10),
		},
		Epsilon: 0.1,
	})
	if !errors.Is(err, netsec.ErrBudgetExhausted) {
		t.Fatalf("over-budget error = %v, want ErrBudgetExhausted", err)
	}

	status := engine.Budget()
	if !almostEqual(status.BudgetSpent, 0.2) || !almostEqual(status.BudgetRemaining, 0.05) {
		t.Fatalf("budget status = %+v", status)
	}
	if status.QueriesAccepted != 2 {
		t.Fatalf("QueriesAccepted = %d, want 2", status.QueriesAccepted)
	}
}

func TestRoundingAndCalibratedNoiseFollowPolicy(t *testing.T) {
	policy := deterministicPolicy()
	policy.RoundingBase = 5
	policy.NoiseMode = netsec.NoiseLaplace
	seed := int64(1234)
	policy.RandomSeed = &seed
	engine, err := netsec.NewEngine(policy)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	group := summary("noisy", subjectIDs("noise", 40), 40, 40)
	result, err := engine.Execute(netsec.Query{
		ID:          "noisy",
		Statistic:   netsec.StatisticCount,
		Groups:      []netsec.GroupSummary{group},
		Epsilon:     0.1,
		Sensitivity: 1,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := result.Groups[0]
	if !got.Released || !got.NoiseApplied || !got.Rounded {
		t.Fatalf("group = %+v, want released noisy rounded value", got)
	}
	if math.Mod(got.Value, 5) != 0 {
		t.Fatalf("value %v is not a multiple of rounding base 5", got.Value)
	}

	second, err := netsec.NewEngine(policy)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	replayed, err := second.Execute(netsec.Query{
		ID:          "noisy-replay",
		Statistic:   netsec.StatisticCount,
		Groups:      []netsec.GroupSummary{group},
		Epsilon:     0.1,
		Sensitivity: 1,
	})
	if err != nil {
		t.Fatalf("replay Execute: %v", err)
	}
	if replayed.Groups[0].Value != got.Value {
		t.Fatalf("seeded replay = %v, want %v", replayed.Groups[0].Value, got.Value)
	}
}

func TestUnlinkedCellsRequireExplicitOptIn(t *testing.T) {
	policy := deterministicPolicy()
	engine, err := netsec.NewEngine(policy)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	unlinked := netsec.GroupSummary{Key: "warehouse-count", Count: 12, Value: 12}
	query := netsec.Query{
		ID:        "unlinked",
		Statistic: netsec.StatisticCount,
		Groups:    []netsec.GroupSummary{unlinked},
		Epsilon:   0.1,
	}
	if _, err := engine.Execute(query); !errors.Is(err, netsec.ErrUnlinkedCell) {
		t.Fatalf("error = %v, want ErrUnlinkedCell", err)
	}

	policy.AllowUnlinkedCells = true
	engine, err = netsec.NewEngine(policy)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	result, err := engine.Execute(query)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Groups[0].Released || result.Groups[0].Value != 10 {
		t.Fatalf("unlinked result = %+v, want rounded count 10", result.Groups[0])
	}
	if result.UnlinkedGroups != 1 {
		t.Fatalf("UnlinkedGroups = %d, want 1 for an unlinked cell", result.UnlinkedGroups)
	}
}

func TestConcurrentDuplicateExecutionIsSerialized(t *testing.T) {
	policy := deterministicPolicy()
	engine, err := netsec.NewEngine(policy)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	query := netsec.Query{
		ID:        "concurrent",
		Statistic: netsec.StatisticCount,
		Groups:    []netsec.GroupSummary{summary("concurrent", subjectIDs("c", 10), 10, 10)},
		Epsilon:   0.1,
	}

	const workers = 12
	results := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			_, err := engine.Execute(query)
			results <- err
		}()
	}
	accepted, duplicates := 0, 0
	for i := 0; i < workers; i++ {
		err := <-results
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, netsec.ErrDuplicateQuery):
			duplicates++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if accepted != 1 || duplicates != workers-1 {
		t.Fatalf("accepted=%d duplicates=%d, want 1/%d", accepted, duplicates, workers-1)
	}
	if status := engine.Budget(); !almostEqual(status.BudgetSpent, 0.1) {
		t.Fatalf("BudgetSpent = %v, want one query", status.BudgetSpent)
	}
}

func containsReason(reasons []netsec.SuppressionReason, want netsec.SuppressionReason) bool {
	for _, reason := range reasons {
		if reason == want {
			return true
		}
	}
	return false
}

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

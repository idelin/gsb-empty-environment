package netsec

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"
)

func newTestTaxonomy(t *testing.T, name string, leaves []string, levelsPerLeaf [][]string) *Taxonomy {
	t.Helper()
	parent := map[string]string{}
	nodeSet := map[string]bool{}
	root := levelsPerLeaf[0][len(levelsPerLeaf[0])-1]
	for leafIndex, leaf := range leaves {
		chain := append([]string{leaf}, levelsPerLeaf[leafIndex]...)
		for i := 0; i < len(chain)-1; i++ {
			nodeSet[chain[i]] = true
			parent[chain[i]] = chain[i+1]
		}
		nodeSet[chain[len(chain)-1]] = true
	}
	nodes := make([]string, 0, len(nodeSet))
	for node := range nodeSet {
		nodes = append(nodes, node)
	}
	sort.Strings(nodes)
	taxonomy, err := NewTaxonomy(name, nodes, root, parent)
	if err != nil {
		t.Fatalf("NewTaxonomy(%s): %v", name, err)
	}
	return taxonomy
}

func twoColumnTaxonomies(t *testing.T) (*Taxonomy, *Taxonomy) {
	city := newTestTaxonomy(t, "city",
		[]string{"NYC", "LA", "SF"},
		[][]string{
			{"NY", "US"},
			{"CA", "US"},
			{"CA", "US"},
		})
	gender := newTestTaxonomy(t, "gender",
		[]string{"M", "F"},
		[][]string{
			{"ANY"},
			{"ANY"},
		})
	return city, gender
}

func TestAnonymizeHandComputed(t *testing.T) {
	city, gender := twoColumnTaxonomies(t)
	cfg := Config{
		Taxonomies:      []*Taxonomy{city, gender},
		LevelCosts:      [][]int64{{0, 5, 100}, {0, 30}},
		SuppressionCost: 10,
		K:               2,
		MaxSuppressed:   1,
		Records: []Record{
			{ID: "r1", Quasi: []string{"NYC", "M"}},
			{ID: "r2", Quasi: []string{"NYC", "F"}},
			{ID: "r3", Quasi: []string{"LA", "M"}},
			{ID: "r4", Quasi: []string{"SF", "F"}},
		},
	}

	result, err := Anonymize(cfg)
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}
	if !result.Published {
		t.Fatal("expected publication")
	}
	if got, want := result.Level, []int{1, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("level = %v, want %v", got, want)
	}
	if got, want := result.Evidence.LevelCost, int64(35); got != want {
		t.Fatalf("level cost = %d, want %d", got, want)
	}
	if result.Evidence.Suppressed != 0 {
		t.Fatalf("suppressed = %d, want 0", result.Evidence.Suppressed)
	}
	if got, want := result.Evidence.TotalCost, int64(35); got != want {
		t.Fatalf("total cost = %d, want %d", got, want)
	}

	groupByKey := map[string]Group{}
	for _, group := range result.Groups {
		groupByKey[fmt.Sprint(group.Key)] = group
	}
	nyGroup := groupByKey["[NY ANY]"]
	if nyGroup.Suppressed || len(nyGroup.RecordIDs) != 2 {
		t.Fatalf("NY/ANY group wrong: %+v", nyGroup)
	}
	caGroup := groupByKey["[CA ANY]"]
	if caGroup.Suppressed || len(caGroup.RecordIDs) != 2 {
		t.Fatalf("CA/ANY group wrong: %+v", caGroup)
	}
	if len(result.PublishedGroups) != 2 {
		t.Fatalf("published groups = %d, want 2", len(result.PublishedGroups))
	}
	if len(result.SuppressedRecordIDs) != 0 {
		t.Fatalf("suppressed ids = %v, want none", result.SuppressedRecordIDs)
	}
	if !result.Evidence.CrossCheck.Matched || result.Evidence.CrossCheck.Enumerated != 6 {
		t.Fatalf("cross check wrong: %+v", result.Evidence.CrossCheck)
	}
	stats := result.Evidence.Stats
	if stats.LatticePoints != 6 || stats.Evaluated+stats.BoundPruned != 6 {
		t.Fatalf("stats do not cover lattice: %+v", stats)
	}
}

func TestOptimumMaySuppressWholeGroups(t *testing.T) {
	city, gender := twoColumnTaxonomies(t)
	cfg := Config{
		Taxonomies:      []*Taxonomy{city, gender},
		LevelCosts:      [][]int64{{0, 200, 300}, {0, 7}},
		SuppressionCost: 1,
		K:               2,
		MaxSuppressed:   1,
		Records: []Record{
			{ID: "r1", Quasi: []string{"NYC", "M"}},
			{ID: "r2", Quasi: []string{"NYC", "F"}},
			{ID: "r3", Quasi: []string{"LA", "M"}},
			{ID: "r4", Quasi: []string{"LA", "F"}},
			{ID: "r5", Quasi: []string{"SF", "F"}},
		},
	}

	result, err := Anonymize(cfg)
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}
	if got, want := result.Level, []int{0, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("level = %v, want %v", got, want)
	}
	if result.Evidence.Suppressed != 1 {
		t.Fatalf("suppressed = %d, want 1", result.Evidence.Suppressed)
	}
	if got, want := result.Evidence.LevelCost, int64(7); got != want {
		t.Fatalf("level cost = %d, want %d", got, want)
	}
	if got, want := result.Evidence.SuppressionCost, int64(1); got != want {
		t.Fatalf("suppression cost = %d, want %d", got, want)
	}
	if got, want := result.Evidence.TotalCost, int64(8); got != want {
		t.Fatalf("total cost = %d, want %d", got, want)
	}

	var suppressedGroup *Group
	for i := range result.Groups {
		if result.Groups[i].Suppressed {
			suppressedGroup = &result.Groups[i]
		}
	}
	if suppressedGroup == nil || !reflect.DeepEqual(suppressedGroup.RecordIDs, []string{"r5"}) {
		t.Fatalf("expected the SF/ANY singleton to be fully suppressed: %+v", result.Groups)
	}
	if !reflect.DeepEqual(result.SuppressedRecordIDs, []string{"r5"}) {
		t.Fatalf("suppressed ids = %v, want [r5]", result.SuppressedRecordIDs)
	}
	if len(result.PublishedGroups) != 2 {
		t.Fatalf("published groups = %d, want 2", len(result.PublishedGroups))
	}
}

func TestTieBreakLexicographic(t *testing.T) {
	city, gender := twoColumnTaxonomies(t)
	cfg := Config{
		Taxonomies:      []*Taxonomy{city, gender},
		LevelCosts:      [][]int64{{0, 0, 0}, {0, 0}},
		SuppressionCost: 0,
		K:               2,
		MaxSuppressed:   4,
		Records: []Record{
			{ID: "r1", Quasi: []string{"NYC", "M"}},
			{ID: "r2", Quasi: []string{"LA", "F"}},
		},
	}

	result, err := Anonymize(cfg)
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}
	if got, want := result.Level, []int{0, 0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("level = %v, want lexicographic minimum %v", got, want)
	}
	if result.Evidence.TotalCost != 0 || result.Evidence.Suppressed != 2 {
		t.Fatalf("unexpected costs/suppression: %+v", result.Evidence)
	}
}

func TestRefusesWhenNoLevelIsFeasible(t *testing.T) {
	segment := newTestTaxonomy(t, "segment",
		[]string{"a", "b", "c", "d"},
		[][]string{{"G", "ROOT"}, {"G", "ROOT"}, {"H", "ROOT"}, {"H", "ROOT"}})
	flag := newTestTaxonomy(t, "flag", []string{"x", "y"}, [][]string{{"Z"}, {"Z"}})
	cfg := Config{
		Taxonomies:      []*Taxonomy{segment, flag},
		LevelCosts:      [][]int64{{0, 9, 50}, {0, 4}},
		SuppressionCost: 10,
		K:               5,
		MaxSuppressed:   0,
		Records: []Record{
			{ID: "r1", Quasi: []string{"a", "x"}},
			{ID: "r2", Quasi: []string{"b", "y"}},
			{ID: "r3", Quasi: []string{"c", "x"}},
			{ID: "r4", Quasi: []string{"d", "y"}},
		},
	}

	result, err := Anonymize(cfg)
	if !errors.Is(err, ErrNoFeasiblePublication) {
		t.Fatalf("err = %v, want ErrNoFeasiblePublication", err)
	}
	if result == nil || result.Published {
		t.Fatalf("refusal result wrong: %+v", result)
	}
	if len(result.Groups) != 0 || len(result.Level) != 0 {
		t.Fatalf("refusal must carry no groups or level: %+v", result)
	}
	if !result.Evidence.CrossCheck.Matched {
		t.Fatalf("refusal cross check wrong: %+v", result.Evidence.CrossCheck)
	}
	wantPoints := (segment.Height() + 1) * (flag.Height() + 1)
	if result.Evidence.CrossCheck.Enumerated != wantPoints {
		t.Fatalf("enumerated = %d, want %d", result.Evidence.CrossCheck.Enumerated, wantPoints)
	}
	if result.Evidence.Suppressed != 4 {
		t.Fatalf("top-level suppression = %d, want 4", result.Evidence.Suppressed)
	}
}

func TestBoundPruningFires(t *testing.T) {
	city, gender := twoColumnTaxonomies(t)
	cfg := Config{
		Taxonomies:      []*Taxonomy{city, gender},
		LevelCosts:      [][]int64{{0, 100, 100}, {0, 7}},
		SuppressionCost: 1,
		K:               2,
		MaxSuppressed:   4,
		Records: []Record{
			{ID: "r1", Quasi: []string{"NYC", "M"}},
			{ID: "r2", Quasi: []string{"NYC", "M"}},
			{ID: "r3", Quasi: []string{"LA", "M"}},
			{ID: "r4", Quasi: []string{"SF", "F"}},
		},
	}

	result, err := Anonymize(cfg)
	if err != nil {
		t.Fatalf("Anonymize: %v", err)
	}
	if got, want := result.Level, []int{0, 0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("level = %v, want %v", got, want)
	}
	if result.Evidence.Stats.BoundPruned == 0 {
		t.Fatalf("expected bound pruning, stats: %+v", result.Evidence.Stats)
	}
	if result.Evidence.Stats.Evaluated+result.Evidence.Stats.BoundPruned != 6 {
		t.Fatalf("stats do not cover lattice: %+v", result.Evidence.Stats)
	}
}

package netsec

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

func TestInvalidConfigurations(t *testing.T) {
	city, gender := twoColumnTaxonomies(t)
	base := Config{
		Taxonomies:      []*Taxonomy{city, gender},
		LevelCosts:      [][]int64{{0, 5, 20}, {0, 7}},
		SuppressionCost: 1,
		K:               2,
		MaxSuppressed:   1,
		Records:         []Record{{ID: "r1", Quasi: []string{"NYC", "M"}}},
	}

	t.Run("too many columns", func(t *testing.T) {
		cfg := base
		cfg.Taxonomies = make([]*Taxonomy, 7)
		for i := range cfg.Taxonomies {
			cfg.Taxonomies[i] = gender
		}
		cfg.LevelCosts = make([][]int64, 7)
		for i := range cfg.LevelCosts {
			cfg.LevelCosts[i] = []int64{0, 7}
		}
		if _, err := Anonymize(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("bad k", func(t *testing.T) {
		cfg := base
		cfg.K = 1
		if _, err := Anonymize(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("negative suppression budget", func(t *testing.T) {
		cfg := base
		cfg.MaxSuppressed = -1
		if _, err := Anonymize(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("negative cost", func(t *testing.T) {
		cfg := base
		cfg.LevelCosts = [][]int64{{0, -5, 20}, {0, 7}}
		if _, err := Anonymize(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("wrong cost width", func(t *testing.T) {
		cfg := base
		cfg.LevelCosts = [][]int64{{0, 5}, {0, 7}}
		if _, err := Anonymize(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("unknown value", func(t *testing.T) {
		cfg := base
		cfg.Records = []Record{{ID: "r1", Quasi: []string{"BOSTON", "M"}}}
		if _, err := Anonymize(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("duplicate id", func(t *testing.T) {
		cfg := base
		cfg.Records = []Record{
			{ID: "r1", Quasi: []string{"NYC", "M"}},
			{ID: "r1", Quasi: []string{"LA", "F"}},
		}
		if _, err := Anonymize(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("row width mismatch", func(t *testing.T) {
		cfg := base
		cfg.Records = []Record{{ID: "r1", Quasi: []string{"NYC"}}}
		if _, err := Anonymize(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("empty records", func(t *testing.T) {
		cfg := base
		cfg.Records = nil
		if _, err := Anonymize(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("cycle rejected", func(t *testing.T) {
		_, err := NewTaxonomy("bad", []string{"a", "b"}, "a", map[string]string{"a": "b", "b": "a"})
		if err == nil {
			t.Fatal("expected error for cyclic tree")
		}
	})

	t.Run("height rejected", func(t *testing.T) {
		parent := map[string]string{}
		nodes := make([]string, 7)
		for i := range nodes {
			nodes[i] = fmt.Sprintf("n%d", i)
			if i > 0 {
				parent[nodes[i]] = nodes[i-1]
			}
		}
		if _, err := NewTaxonomy("tall", nodes, "n0", parent); err == nil {
			t.Fatal("expected error for over-height tree")
		}
	})
}

func TestRandomizedMatchesIndependentBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(1275))
	for iteration := 0; iteration < 80; iteration++ {
		cols := 1 + rng.Intn(MaxQuasiIdentifiers)
		taxonomies := make([]*Taxonomy, cols)
		leavesPerColumn := make([][]string, cols)
		levelCosts := make([][]int64, cols)
		for col := 0; col < cols; col++ {
			taxonomy, leaves := randomTaxonomy(t, rng, col)
			taxonomies[col] = taxonomy
			leavesPerColumn[col] = leaves
			levelCosts[col] = make([]int64, taxonomy.Height()+1)
			for level := range levelCosts[col] {
				levelCosts[col][level] = int64(rng.Intn(12))
			}
		}

		recordCount := 1 + rng.Intn(30)
		records := make([]Record, recordCount)
		for row := range records {
			values := make([]string, cols)
			for col, leaves := range leavesPerColumn {
				values[col] = leaves[rng.Intn(len(leaves))]
			}
			records[row] = Record{ID: fmt.Sprintf("row-%d-%d", iteration, row), Quasi: values}
		}

		cfg := Config{
			Taxonomies:      taxonomies,
			LevelCosts:      levelCosts,
			SuppressionCost: int64(rng.Intn(8)),
			K:               2 + rng.Intn(4),
			MaxSuppressed:   rng.Intn(recordCount + 1),
			Records:         records,
		}

		result, err := Anonymize(cfg)
		wantLevel, wantCost, wantSuppressed, feasible := bruteForce(t, cfg)
		if !feasible {
			if !errors.Is(err, ErrNoFeasiblePublication) {
				t.Fatalf("iteration %d: err = %v, want refusal", iteration, err)
			}
			if result == nil || result.Published {
				t.Fatalf("iteration %d: refusal result wrong", iteration)
			}
			continue
		}
		if err != nil {
			t.Fatalf("iteration %d: Anonymize: %v", iteration, err)
		}
		if !reflect.DeepEqual(result.Level, wantLevel) {
			t.Fatalf("iteration %d: level = %v, want %v", iteration, result.Level, wantLevel)
		}
		if result.Evidence.TotalCost != wantCost {
			t.Fatalf("iteration %d: cost = %d, want %d", iteration, result.Evidence.TotalCost, wantCost)
		}
		if result.Evidence.Suppressed != wantSuppressed {
			t.Fatalf("iteration %d: suppressed = %d, want %d", iteration, result.Evidence.Suppressed, wantSuppressed)
		}

		totalPoints := 1
		for _, taxonomy := range taxonomies {
			totalPoints *= taxonomy.Height() + 1
		}
		stats := result.Evidence.Stats
		if stats.Evaluated+stats.BoundPruned != totalPoints {
			t.Fatalf("iteration %d: stats do not cover %d points: %+v", iteration, totalPoints, stats)
		}
		if result.Evidence.CrossCheck.Enumerated != totalPoints || !result.Evidence.CrossCheck.Matched {
			t.Fatalf("iteration %d: cross check wrong: %+v", iteration, result.Evidence.CrossCheck)
		}
	}
}

func randomTaxonomy(t *testing.T, rng *rand.Rand, col int) (*Taxonomy, []string) {
	t.Helper()
	depth := 1 + rng.Intn(MaxTreeHeight)
	branching := 1 + rng.Intn(3)
	root := fmt.Sprintf("c%d-root", col)
	nodes := []string{root}
	parent := map[string]string{}
	leaves := []string{}

	frontier := []string{root}
	for level := 1; level <= depth; level++ {
		var next []string
		for _, ancestor := range frontier {
			children := 1 + rng.Intn(branching)
			for child := 0; child < children; child++ {
				node := fmt.Sprintf("c%d-l%d-%s-%d", col, level, ancestor, child)
				nodes = append(nodes, node)
				parent[node] = ancestor
				next = append(next, node)
				if level == depth {
					leaves = append(leaves, node)
				}
			}
		}
		frontier = next
	}

	taxonomy, err := NewTaxonomy(fmt.Sprintf("c%d", col), nodes, root, parent)
	if err != nil {
		t.Fatalf("random taxonomy: %v", err)
	}
	return taxonomy, leaves
}

func bruteForce(t *testing.T, cfg Config) ([]int, int64, int, bool) {
	heights := make([]int, len(cfg.Taxonomies))
	totalPoints := 1
	for col, taxonomy := range cfg.Taxonomies {
		heights[col] = taxonomy.Height()
		totalPoints *= heights[col] + 1
	}

	var bestLevel []int
	var bestCost int64
	bestSuppressed := 0
	found := false
	for index := 0; index < totalPoints; index++ {
		level := decodeLevel(index, heights)
		groups := map[string]int{}
		levelCost := int64(0)
		for col, l := range level {
			levelCost += cfg.LevelCosts[col][l]
		}
		for _, record := range cfg.Records {
			key := ""
			for col, taxonomy := range cfg.Taxonomies {
				node, err := taxonomy.generalizeAt(record.Quasi[col], level[col])
				if err != nil {
					t.Fatalf("brute force: %v", err)
				}
				key += node + "\x00"
			}
			groups[key]++
		}
		suppressed := 0
		for _, size := range groups {
			if size < cfg.K {
				suppressed += size
			}
		}
		if suppressed > cfg.MaxSuppressed {
			continue
		}
		cost := levelCost + int64(suppressed)*cfg.SuppressionCost
		if !found || cost < bestCost || (cost == bestCost && lexCompareInt(level, bestLevel) < 0) {
			found = true
			bestLevel = level
			bestCost = cost
			bestSuppressed = suppressed
		}
	}
	return bestLevel, bestCost, bestSuppressed, found
}

func decodeLevel(index int, heights []int) []int {
	level := make([]int, len(heights))
	cursor := index
	for col := len(heights) - 1; col >= 0; col-- {
		width := heights[col] + 1
		level[col] = cursor % width
		cursor /= width
	}
	return level
}

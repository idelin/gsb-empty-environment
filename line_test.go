package taskpkg

import (
	"math"
	"math/rand"
	"sort"
	"testing"
)

func rectQuad(x, y, w, h float64) Quad {
	return Quad{
		{X: x, Y: y},
		{X: x + w, Y: y},
		{X: x + w, Y: y + h},
		{X: x, Y: y + h},
	}
}

func word(id, text string, x, y, w, h, confidence float64) Word {
	return Word{ID: id, Text: text, Quad: rectQuad(x, y, w, h), Confidence: confidence}
}

func lineIDs(line Line) []string {
	ids := make([]string, len(line.Words))
	for i, w := range line.Words {
		ids[i] = w.ID
	}
	return ids
}

func TestTwoColumnsStaySeparate(t *testing.T) {
	words := []Word{
		word("l1a", "alpha", 0, 0, 20, 10, 0.99),
		word("l1b", "beta", 24, 0, 20, 10, 0.98),
		word("r1a", "gamma", 100, 0, 20, 10, 0.97),
		word("r1b", "delta", 124, 0, 20, 10, 0.96),
		word("l2a", "one", 0, 20, 20, 10, 0.95),
		word("l2b", "two", 24, 20, 20, 10, 0.94),
		word("r2a", "three", 100, 20, 20, 10, 0.93),
		word("r2b", "four", 124, 20, 20, 10, 0.92),
	}
	res, err := BuildLines(words, Options{
		BaselineThreshold: 2,
		LinePenalty:       50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Lines) != 4 {
		t.Fatalf("got %d lines, want 4: %+v", len(res.Lines), lineTexts(res.Lines))
	}
	want := [][]string{
		{"l1a", "l1b"},
		{"r1a", "r1b"},
		{"l2a", "l2b"},
		{"r2a", "r2b"},
	}
	for i := range want {
		got := lineIDs(res.Lines[i])
		if !equalStrings(got, want[i]) {
			t.Fatalf("line %d = %v, want %v (all lines: %v)",
				i, got, want[i], lineTexts(res.Lines))
		}
	}
	if res.Cost != 200 {
		t.Fatalf("cost = %v, want 200", res.Cost)
	}
}

func TestSuperscriptRemainsOnBaseLine(t *testing.T) {
	words := []Word{
		word("x", "x", 0, 0, 20, 20, 0.99),
		word("sup", "2", 21, 0, 8, 10, 0.90),
		word("eq", "=", 30, 0, 20, 20, 0.98),
		word("y", "y", 54, 0, 20, 20, 0.97),
	}
	res, err := BuildLines(words, Options{
		BaselineThreshold: 10,
		LinePenalty:       200,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Lines) != 1 {
		t.Fatalf("got %d lines, want 1: %v", len(res.Lines), lineTexts(res.Lines))
	}
	ids := lineIDs(res.Lines[0])
	want := []string{"x", "sup", "eq", "y"}
	if !equalStrings(ids, want) {
		t.Fatalf("line IDs = %v, want %v", ids, want)
	}
	if res.Lines[0].Text() != "x 2 = y" {
		t.Fatalf("text = %q", res.Lines[0].Text())
	}
	// 200 + 10^2 + 10^2 + 0.
	if res.Cost != 400 {
		t.Fatalf("cost = %v, want 400", res.Cost)
	}
}

func TestDuplicateBoxesKeepBestCandidateAndEvidence(t *testing.T) {
	words := []Word{
		word("low", "helo", 1, 1, 20, 10, 0.71),
		word("high", "hello", 0, 0, 20, 10, 0.96),
		word("tail", "world", 24, 0, 20, 10, 0.90),
	}
	res, err := BuildLines(words, Options{
		BaselineThreshold: 1,
		LinePenalty:       20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Words) != 2 {
		t.Fatalf("got %d selected words, want 2", len(res.Words))
	}
	if len(res.Duplicates) != 1 {
		t.Fatalf("got %d duplicate groups, want 1", len(res.Duplicates))
	}
	group := res.Duplicates[0]
	if group.Selected.ID != "high" || group.Selected.Text != "hello" {
		t.Fatalf("selected = %+v", group.Selected)
	}
	if len(group.Candidates) != 2 {
		t.Fatalf("evidence has %d candidates, want 2", len(group.Candidates))
	}
	if group.Candidates[0].ID != "high" || group.Candidates[1].ID != "low" {
		t.Fatalf("candidate order = %v, %v", group.Candidates[0].ID, group.Candidates[1].ID)
	}
	if len(res.Lines) != 1 || res.Lines[0].Text() != "hello world" {
		t.Fatalf("lines = %v", lineTexts(res.Lines))
	}
}

func TestPageRotationIsRemoved(t *testing.T) {
	words := []Word{
		word("a", "a", 0, 0, 20, 10, 0.99),
		word("b", "b", 24, 0, 20, 10, 0.98),
		word("c", "c", 48, 0, 20, 10, 0.97),
	}
	const degrees = 30.0
	for i := range words {
		words[i].Quad = rotateQuad(words[i].Quad, degrees)
	}
	res, err := BuildLines(words, Options{
		PageRotationDegrees: degrees,
		BaselineThreshold:   1,
		LinePenalty:         40,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Lines) != 1 {
		t.Fatalf("got %d lines after rotation, want 1: %v", len(res.Lines), lineTexts(res.Lines))
	}
	ids := lineIDs(res.Lines[0])
	want := []string{"a", "b", "c"}
	if !equalStrings(ids, want) {
		t.Fatalf("line IDs = %v, want %v", ids, want)
	}
}

func TestExactPartitionCanUseNonContiguousWords(t *testing.T) {
	words := []Word{
		word("left", "left", 0, 0, 20, 10, 0.99),
		word("mid", "2", 20.5, 10, 2, 10, 0.80),
		word("right", "right", 22, 0, 20, 10, 0.98),
	}
	res, err := BuildLines(words, Options{
		BaselineThreshold: 10,
		LinePenalty:       50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Lines) != 2 {
		t.Fatalf("got %d lines, want 2: %v", len(res.Lines), lineTexts(res.Lines))
	}
	var together []string
	for _, line := range res.Lines {
		if len(line.Words) == 2 {
			together = lineIDs(line)
		}
	}
	if !equalStrings(together, []string{"left", "right"}) {
		t.Fatalf("two-word line = %v, want [left right]", together)
	}
	// (50 + 0) for left/right and 50 for the middle word.
	if res.Cost != 100 {
		t.Fatalf("cost = %v, want 100", res.Cost)
	}
}

func TestExactPartitionMatchesFullEnumeration(t *testing.T) {
	// A deliberately awkward four-word set: the first and third words share
	// a baseline, while the second and fourth are offset but still within the
	// threshold of their neighbours.
	words := []Word{
		word("a", "a", 0, 0, 20, 10, 0.99),
		word("b", "b", 24, 4, 20, 10, 0.95),
		word("c", "c", 48, 0, 20, 10, 0.97),
		word("d", "d", 72, 4, 20, 10, 0.90),
	}
	opts := Options{
		BaselineThreshold: 5,
		LinePenalty:       30,
	}
	res, err := BuildLines(words, opts)
	if err != nil {
		t.Fatal(err)
	}
	wantCost, wantBlocks := bruteForcePartition(words, opts)
	if !nearlyEqual(res.Cost, wantCost) {
		t.Fatalf("cost = %v, brute force = %v", res.Cost, wantCost)
	}
	gotBlocks := make([][]string, len(res.Lines))
	for i, line := range res.Lines {
		ids := lineIDs(line)
		sort.Strings(ids)
		gotBlocks[i] = ids
	}
	sortStringSlices(gotBlocks)
	sortStringSlices(wantBlocks)
	if !equalStringSlices(gotBlocks, wantBlocks) {
		t.Fatalf("partition = %v, brute force = %v (cost %v)", gotBlocks, wantBlocks, wantCost)
	}
}

func TestTooManyDistinctWords(t *testing.T) {
	words := make([]Word, 25)
	for i := range words {
		words[i] = word("w", "w", float64(i*30), 0, 20, 10, 0.9)
		words[i].ID = "w" + string(rune('A'+i%26))
	}
	_, err := BuildLines(words, Options{BaselineThreshold: 1})
	if err == nil {
		t.Fatal("expected too-many-words error")
	}
}

func TestTwentyFourWordLine(t *testing.T) {
	words := make([]Word, MaxWords)
	for i := range words {
		words[i] = word(
			"w"+string(rune('A'+i)),
			"w",
			float64(i)*24,
			0,
			20,
			10,
			0.90,
		)
	}
	res, err := BuildLines(words, Options{
		BaselineThreshold: 1,
		LinePenalty:       100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Lines) != 1 || len(res.Lines[0].Words) != MaxWords {
		t.Fatalf("got %d lines, first has %d words",
			len(res.Lines), len(res.Lines[0].Words))
	}
	if res.Cost != 100 {
		t.Fatalf("cost = %v, want 100", res.Cost)
	}
}

func TestRandomSmallSetsMatchFullEnumeration(t *testing.T) {
	rng := rand.New(rand.NewSource(71))
	for trial := 0; trial < 30; trial++ {
		words := make([]Word, 5)
		x := 0.0
		for i := range words {
			w := 12.0 + float64(rng.Intn(20))
			y := float64(rng.Intn(2)) * 3
			words[i] = word(
				string(rune('a'+i)),
				"t",
				x,
				y,
				w,
				10,
				0.9,
			)
			// Overlap some neighbours and leave modest gaps.
			x += w - 3 + float64(rng.Intn(9))
		}
		opts := Options{
			BaselineThreshold: 4,
			LinePenalty:       18,
		}
		res, err := BuildLines(words, opts)
		if err != nil {
			t.Fatal(err)
		}
		wantCost, _ := bruteForcePartition(words, opts)
		if !nearlyEqual(res.Cost, wantCost) {
			t.Fatalf("trial %d: cost = %v, brute force = %v", trial, res.Cost, wantCost)
		}
		sum := 0.0
		for _, line := range res.Lines {
			sum += line.Cost
			for i := 1; i < len(line.Words); i++ {
				if line.Words[i-1].Quad.Bounds().CenterX() > line.Words[i].Quad.Bounds().CenterX() {
					t.Fatalf("trial %d: line is not x-sorted: %v", trial, lineIDs(line))
				}
			}
		}
		if !nearlyEqual(sum, res.Cost) {
			t.Fatalf("trial %d: line costs %v do not sum to %v", trial, sum, res.Cost)
		}
	}
}

func rotateQuad(q Quad, degrees float64) Quad {
	radians := degrees * math.Pi / 180
	sin, cos := math.Sin(radians), math.Cos(radians)
	var out Quad
	for i, p := range q {
		out[i] = Point{X: p.X*cos - p.Y*sin, Y: p.X*sin + p.Y*cos}
	}
	return out
}

func lineTexts(lines []Line) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = line.Text()
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sortStringSlices(slices [][]string) {
	for _, s := range slices {
		sort.Strings(s)
	}
	sort.Slice(slices, func(i, j int) bool {
		return joinStrings(slices[i]) < joinStrings(slices[j])
	})
}

func joinStrings(s []string) string {
	out := ""
	for _, v := range s {
		out += v + "\x00"
	}
	return out
}

func equalStringSlices(a, b [][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !equalStrings(a[i], b[i]) {
			return false
		}
	}
	return true
}

// bruteForcePartition enumerates every set partition by restricted growth
// strings. It is intentionally independent of the solver's pruning logic.
func bruteForcePartition(words []Word, opts Options) (float64, [][]string) {
	opts, err := normalizeOptions(opts)
	if err != nil {
		panic(err)
	}
	n := len(words)
	ids := make([]string, n)
	base := make([]float64, n)
	left := make([]float64, n)
	right := make([]float64, n)
	width := make([]float64, n)
	for i, w := range words {
		ids[i] = w.ID
		b := w.Quad.Bounds()
		base[i] = Baseline(w.Quad)
		left[i] = b.MinX
		right[i] = b.MaxX
		width[i] = b.Width()
	}

	allowed := func(i, j int) bool {
		if math.Abs(base[i]-base[j]) > opts.BaselineThreshold+geometryEpsilon {
			return false
		}
		overlap := math.Min(right[i], right[j]) - math.Max(left[i], left[j])
		if overlap < 0 {
			overlap = 0
		}
		gap := math.Max(left[i], left[j]) - math.Min(right[i], right[j])
		if gap < 0 {
			gap = 0
		}
		short := math.Min(width[i], width[j])
		return overlap <= opts.MaxOverlapRatio*short+geometryEpsilon &&
			gap <= opts.MaxGapRatio*short+geometryEpsilon
	}

	bestCost := math.Inf(1)
	var bestBlocks [][]string
	blockOf := make([]int, n)
	var visit func(pos, blocks int)
	visit = func(pos, blocks int) {
		if pos == n {
			cost := float64(blocks) * opts.LinePenalty
			blockIDs := make([][]string, blocks)
			for i := 0; i < n; i++ {
				for j := i + 1; j < n; j++ {
					if blockOf[j] != blockOf[i] {
						continue
					}
					between := false
					for k := i + 1; k < j; k++ {
						if blockOf[k] == blockOf[i] {
							between = true
							break
						}
					}
					if between {
						continue
					}
					if !allowed(i, j) {
						cost = math.Inf(1)
					} else {
						dy := base[j] - base[i]
						cost += dy * dy
					}
				}
			}
			if math.IsInf(cost, 1) {
				return
			}
			for b := 0; b < blocks; b++ {
				for i := 0; i < n; i++ {
					if blockOf[i] == b {
						blockIDs[b] = append(blockIDs[b], ids[i])
					}
				}
				sort.Strings(blockIDs[b])
			}
			sortStringSlices(blockIDs)
			if cost < bestCost || (nearlyEqual(cost, bestCost) &&
				joinStringSlices(blockIDs) < joinStringSlices(bestBlocks)) {
				bestCost = cost
				bestBlocks = blockIDs
			}
			return
		}
		for b := 0; b <= blocks; b++ {
			blockOf[pos] = b
			if b == blocks {
				visit(pos+1, blocks+1)
			} else {
				visit(pos+1, blocks)
			}
		}
	}
	visit(0, 0)
	return bestCost, bestBlocks
}

func joinStringSlices(slices [][]string) string {
	out := ""
	for _, s := range slices {
		out += joinStrings(s) + "\x01"
	}
	return out
}

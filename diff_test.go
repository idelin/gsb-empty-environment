package taskpkg

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

func node(id, role, text string, children ...*Block) *Block {
	return &Block{ID: id, Role: role, Text: text, Children: children}
}

func testOptions() Options {
	return Options{
		DeleteCost:  4,
		InsertCost:  4,
		RolePenalty: 2,
		MoveCost:    1,
		Budget:      2_000_000,
		MaxBlocks:   MaxBlocks,
	}
}

func TestLevenshteinRunes(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"kitten", "sitting", 3},
		{"brown", "red", 4},
		{"价格100", "价格120", 1},
		{"广告轮换", "广告轮换", 0},
	}
	for _, tc := range cases {
		if got := Levenshtein(tc.a, tc.b); got != tc.want {
			t.Errorf("Levenshtein(%q,%q)=%d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestLevenshteinAgainstNaive(t *testing.T) {
	alphabet := []rune("abc价格")
	rng := rand.New(rand.NewSource(99))
	for n := 0; n < 300; n++ {
		a := randomRunes(rng, alphabet, rng.Intn(8))
		b := randomRunes(rng, alphabet, rng.Intn(8))
		got := Levenshtein(string(a), string(b))
		want := naiveLevenshtein(a, b)
		if got != want {
			t.Fatalf("Levenshtein(%q,%q)=%d, want %d", string(a), string(b), got, want)
		}
	}
}

func randomRunes(rng *rand.Rand, alphabet []rune, n int) []rune {
	out := make([]rune, n)
	for i := range out {
		out[i] = alphabet[rng.Intn(len(alphabet))]
	}
	return out
}

func naiveLevenshtein(a, b []rune) int {
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
		dp[i][0] = i
	}
	for j := 0; j <= len(b); j++ {
		dp[0][j] = j
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := dp[i-1][j-1]
			if a[i-1] != b[j-1] {
				cost++
			}
			if dp[i-1][j]+1 < cost {
				cost = dp[i-1][j] + 1
			}
			if dp[i][j-1]+1 < cost {
				cost = dp[i][j-1] + 1
			}
			dp[i][j] = cost
		}
	}
	return dp[len(a)][len(b)]
}

func TestDuplicateParagraphs(t *testing.T) {
	oldRoot := node("root", "document", "",
		node("p1", "paragraph", "dup"),
		node("p2", "paragraph", "dup"),
		node("p3", "paragraph", "tail"),
	)
	newRoot := node("root", "document", "",
		node("p1", "paragraph", "dup"),
		node("p3", "paragraph", "tail"),
		node("p2", "paragraph", "dup"),
	)

	plan, err := Diff(oldRoot, newRoot, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Optimal {
		t.Fatalf("expected optimal plan, reason=%q", plan.Reason)
	}
	if plan.TotalCost != 0 {
		t.Fatalf("total cost=%d, want 0; ops=%+v", plan.TotalCost, plan.Operations)
	}
	if plan.Stats.Deleted != 0 || plan.Stats.Inserted != 0 {
		t.Fatalf("duplicate reordering used delete/insert: %+v", plan.Stats)
	}
	if plan.Stats.Matched != 4 {
		t.Fatalf("matched=%d, want 4 (root plus three paragraphs)", plan.Stats.Matched)
	}
	for _, op := range plan.Operations {
		if op.Kind != OpMatch {
			t.Fatalf("unexpected op %+v", op)
		}
	}
}

func TestCrossParentMoveIsSeparateFromDeleteInsert(t *testing.T) {
	oldRoot := node("root", "document", "",
		node("a", "section", "Left",
			node("p1", "paragraph", "shared"),
		),
		node("b", "section", "Right",
			node("p2", "paragraph", "other"),
		),
	)
	newRoot := node("root", "document", "",
		node("a", "section", "Left",
			node("p2", "paragraph", "other"),
		),
		node("b", "section", "Right",
			node("p1", "paragraph", "shared"),
		),
	)

	opts := testOptions()
	opts.DeleteCost = 10
	opts.InsertCost = 10
	opts.MoveCost = 1
	plan, err := Diff(oldRoot, newRoot, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Optimal {
		t.Fatalf("expected optimal plan: %s", plan.Reason)
	}
	if plan.TotalCost != 2 {
		t.Fatalf("total cost=%d, want 2 (two moves); ops=%+v", plan.TotalCost, plan.Operations)
	}
	if plan.Stats.Moved != 2 || plan.Stats.Deleted != 0 || plan.Stats.Inserted != 0 {
		t.Fatalf("moves not separated from delete/insert: %+v", plan.Stats)
	}
	for _, op := range plan.Operations {
		if op.Kind == OpMove {
			if !op.Moved || op.MovePenalty != opts.MoveCost {
				t.Fatalf("bad move op: %+v", op)
			}
		}
		if op.Kind == OpDelete || op.Kind == OpInsert {
			t.Fatalf("cross-parent move emitted as %s: %+v", op.Kind, op)
		}
	}
}

func TestCrossParentMoveWithoutIDs(t *testing.T) {
	oldRoot := node("", "document", "",
		node("", "left", "",
			node("", "paragraph", "shared"),
		),
		node("", "right", "",
			node("", "paragraph", "other"),
		),
	)
	newRoot := node("", "document", "",
		node("", "left", "",
			node("", "paragraph", "other"),
		),
		node("", "right", "",
			node("", "paragraph", "shared"),
		),
	)
	opts := testOptions()
	opts.MoveCost = 1
	plan, err := Diff(oldRoot, newRoot, opts)
	if err != nil {
		t.Fatal(err)
	}
	if plan.TotalCost != 2 || plan.Stats.Moved != 2 || plan.Stats.Deleted != 0 || plan.Stats.Inserted != 0 {
		t.Fatalf("no-ID move plan=%+v", plan)
	}
	for _, op := range plan.Operations {
		if op.Kind == OpMove {
			if op.OldParentOrder < 0 || op.NewParentOrder < 0 ||
				op.OldParentOrder == op.NewParentOrder {
				t.Fatalf("move parent orders not reported: %+v", op)
			}
		}
	}
}

func TestIgnoredAdRotationDoesNotChangeBody(t *testing.T) {
	oldAd := node("ad", "ad", "buy widgets now", node("", "cta", "click"))
	oldAd.Ignore = true
	newAd := node("ad", "ad", "buy gizmos now", node("", "cta", "tap"))
	newAd.Ignore = true

	oldRoot := node("root", "document", "",
		node("article", "article", "",
			node("p1", "paragraph", "the body is stable"),
		),
		oldAd,
	)
	newRoot := node("root", "document", "",
		node("article", "article", "",
			node("p1", "paragraph", "the body is stable"),
		),
		newAd,
	)

	plan, err := Diff(oldRoot, newRoot, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Optimal || plan.TotalCost != 0 {
		t.Fatalf("ignored ad leaked into body diff: total=%d optimal=%v ops=%+v", plan.TotalCost, plan.Optimal, plan.Operations)
	}
	if plan.Stats.Ignored != 4 {
		t.Fatalf("ignored=%d, want 4 (two nodes in each ad subtree)", plan.Stats.Ignored)
	}
	for _, op := range plan.Operations {
		if op.Kind == OpDelete || op.Kind == OpInsert {
			t.Fatalf("ignored ad emitted operation: %+v", op)
		}
		if op.OldText == "buy widgets now" || op.NewText == "buy gizmos now" {
			t.Fatalf("ignored ad text appeared in operation: %+v", op)
		}
	}
}

func TestNonIgnoredContentReplacementReportsOriginalSpan(t *testing.T) {
	oldRoot := node("root", "document", "",
		node("p", "paragraph", "the quick brown fox"),
	)
	newRoot := node("root", "document", "",
		node("p", "paragraph", "the quick red fox"),
	)

	plan, err := Diff(oldRoot, newRoot, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Optimal {
		t.Fatalf("not optimal: %s", plan.Reason)
	}
	wantDistance := Levenshtein("the quick brown fox", "the quick red fox")
	if plan.TotalCost != wantDistance {
		t.Fatalf("total cost=%d, want %d", plan.TotalCost, wantDistance)
	}
	var replacement *Operation
	for i := range plan.Operations {
		op := &plan.Operations[i]
		if op.Kind == OpMatch && op.OldID == "p" {
			replacement = op
			break
		}
	}
	if replacement == nil {
		t.Fatalf("paragraph replacement missing from plan: %+v", plan.Operations)
	}
	if replacement.TextDistance != wantDistance {
		t.Fatalf("text distance=%d, want %d", replacement.TextDistance, wantDistance)
	}
	if len(replacement.OriginalIntervals) == 0 {
		t.Fatalf("original intervals=%+v, want changed intervals", replacement.OriginalIntervals)
	}
	got := replacement.OriginalIntervals
	minStart, maxEnd := got[0].RuneStart, got[0].RuneEnd
	for _, s := range got {
		if s.RuneStart < 10 || s.RuneEnd > 15 {
			t.Fatalf("changed span=%+v outside 'brown' rune range [10,15)", s)
		}
		if s.RuneStart < minStart {
			minStart = s.RuneStart
		}
		if s.RuneEnd > maxEnd {
			maxEnd = s.RuneEnd
		}
	}
	if minStart != 10 || maxEnd != 15 {
		t.Fatalf("changed intervals=%+v do not cover 'brown' bounds [10,15)", got)
	}
	oldSpan := replacement.OldSpan
	if oldSpan.RuneStart != 0 || oldSpan.RuneEnd != 19 {
		t.Fatalf("old block span=%+v, want [0,19)", oldSpan)
	}
	doc := PlainText(oldRoot)
	for _, s := range got {
		if s.ByteStart < 0 || s.ByteEnd > len(doc) || s.ByteStart > s.ByteEnd {
			t.Fatalf("invalid byte span %+v over %q", s, doc)
		}
	}
}

func TestPureTextInsertionHasZeroWidthOriginalInterval(t *testing.T) {
	oldRoot := node("root", "document", "", node("p", "paragraph", "abcd"))
	newRoot := node("root", "document", "", node("p", "paragraph", "abXYcd"))
	plan, err := Diff(oldRoot, newRoot, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	var op *Operation
	for i := range plan.Operations {
		if plan.Operations[i].Kind == OpMatch && plan.Operations[i].OldID == "p" {
			op = &plan.Operations[i]
		}
	}
	if op == nil {
		t.Fatal("replacement operation not found")
	}
	if len(op.OriginalIntervals) != 1 {
		t.Fatalf("intervals=%+v", op.OriginalIntervals)
	}
	if s := op.OriginalIntervals[0]; s.RuneStart != 2 || s.RuneEnd != 2 {
		t.Fatalf("insertion anchor=%+v, want zero-width at rune 2", s)
	}
}

func TestUnicodeSpanOffsets(t *testing.T) {
	oldRoot := node("root", "document", "", node("p", "paragraph", "价格100"))
	newRoot := node("root", "document", "", node("p", "paragraph", "价格120"))
	plan, err := Diff(oldRoot, newRoot, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	var op *Operation
	for i := range plan.Operations {
		if plan.Operations[i].Kind == OpMatch && plan.Operations[i].OldID == "p" {
			op = &plan.Operations[i]
		}
	}
	if op == nil {
		t.Fatal("replacement operation not found")
	}
	if op.TextDistance != 1 {
		t.Fatalf("distance=%d, want 1", op.TextDistance)
	}
	if op.OldSpan.ByteEnd != len("价格100") {
		t.Fatalf("old byte end=%d, want %d", op.OldSpan.ByteEnd, len("价格100"))
	}
	if len(op.OriginalIntervals) != 1 {
		t.Fatalf("intervals=%+v", op.OriginalIntervals)
	}
	s := op.OriginalIntervals[0]
	doc := PlainText(oldRoot)
	if doc[s.ByteStart:s.ByteEnd] != "0" {
		t.Fatalf("changed byte span maps to %q, want %q", doc[s.ByteStart:s.ByteEnd], "0")
	}
}

func TestMultipleChangedRuns(t *testing.T) {
	oldRoot := node("root", "document", "", node("p", "paragraph", "abcXdefYghi"))
	newRoot := node("root", "document", "", node("p", "paragraph", "abcZdefWghi"))
	plan, err := Diff(oldRoot, newRoot, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	var op *Operation
	for i := range plan.Operations {
		if plan.Operations[i].Kind == OpMatch && plan.Operations[i].OldID == "p" {
			op = &plan.Operations[i]
		}
	}
	if op == nil {
		t.Fatal("replacement operation not found")
	}
	want := []TextSpan{
		{RuneStart: 3, RuneEnd: 4, ByteStart: 3, ByteEnd: 4},
		{RuneStart: 7, RuneEnd: 8, ByteStart: 7, ByteEnd: 8},
	}
	if len(op.OriginalIntervals) != len(want) {
		t.Fatalf("original intervals=%+v, want %+v", op.OriginalIntervals, want)
	}
	for i := range want {
		if op.OriginalIntervals[i] != want[i] {
			t.Fatalf("original interval %d=%+v, want %+v", i, op.OriginalIntervals[i], want[i])
		}
	}
}

func TestRolePenaltyIsAddedToMatch(t *testing.T) {
	oldRoot := node("root", "document", "", node("p", "heading", "same"))
	newRoot := node("root", "document", "", node("p", "paragraph", "same"))
	opts := testOptions()
	opts.RolePenalty = 3
	opts.DeleteCost = 10
	opts.InsertCost = 10
	plan, err := Diff(oldRoot, newRoot, opts)
	if err != nil {
		t.Fatal(err)
	}
	if plan.TotalCost != 3 || plan.Stats.Matched != 2 {
		t.Fatalf("plan=%+v, want match cost 3", plan)
	}
	var op *Operation
	for i := range plan.Operations {
		if plan.Operations[i].OldID == "p" {
			op = &plan.Operations[i]
		}
	}
	if op == nil || op.RolePenalty != 3 || op.TextDistance != 0 {
		t.Fatalf("bad role-penalty op: %+v", op)
	}
}

func TestBudgetExhaustionMarksPlanNonOptimal(t *testing.T) {
	oldRoot := node("root", "document", "",
		node("a", "section", "Left", node("p1", "paragraph", "shared")),
		node("b", "section", "Right", node("p2", "paragraph", "other")),
	)
	newRoot := node("root", "document", "",
		node("a", "section", "Left", node("p2", "paragraph", "other")),
		node("b", "section", "Right", node("p1", "paragraph", "shared")),
	)
	opts := testOptions()
	opts.Budget = 1
	plan, err := Diff(oldRoot, newRoot, opts)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Optimal || !plan.BudgetExhausted {
		t.Fatalf("budget flag: optimal=%v exhausted=%v states=%d", plan.Optimal, plan.BudgetExhausted, plan.States)
	}
	if plan.Reason == "" {
		t.Fatal("expected a non-optimal reason")
	}
	if got, want := plan.TotalCost, bruteForceCost(t, oldRoot, newRoot, opts); got != want {
		t.Fatalf("best-effort cost=%d, brute=%d", got, want)
	}
}

func TestHardBlockLimit(t *testing.T) {
	children := make([]*Block, 0, 40)
	for i := 0; i < 40; i++ {
		children = append(children, node(fmt.Sprintf("p%d", i), "paragraph", "x"))
	}
	root := node("root", "document", "", children...)
	if _, err := Diff(root, node("root2", "document", ""), testOptions()); !errors.Is(err, ErrTooManyBlocks) {
		t.Fatalf("err=%v, want ErrTooManyBlocks", err)
	}
}

func TestFortyBlocksWithinLimit(t *testing.T) {
	oldChildren := make([]*Block, 0, 39)
	newChildren := make([]*Block, 0, 39)
	for i := 0; i < 39; i++ {
		text := fmt.Sprintf("paragraph-%02d", i)
		oldChildren = append(oldChildren, node(fmt.Sprintf("p%d", i), "paragraph", text))
		if i == 7 {
			text = fmt.Sprintf("paragraph-%02d", i+99)
		}
		newChildren = append(newChildren, node(fmt.Sprintf("p%d", i), "paragraph", text))
	}
	oldRoot := node("root", "document", "", oldChildren...)
	newRoot := node("root", "document", "", newChildren...)
	opts := testOptions()
	opts.DeleteCost = 100
	opts.InsertCost = 100
	plan, err := Diff(oldRoot, newRoot, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Optimal {
		t.Fatalf("40-block plan not proven optimal: %s", plan.Reason)
	}
	if plan.Stats.OldBlocks != 40 || plan.Stats.NewBlocks != 40 || plan.Stats.Matched != 40 {
		t.Fatalf("stats=%+v", plan.Stats)
	}
	if want := Levenshtein(oldChildren[7].Text, newChildren[7].Text); plan.TotalCost != want {
		t.Fatalf("cost=%d, want %d", plan.TotalCost, want)
	}
}

func TestAllBlocksIgnored(t *testing.T) {
	oldAd := node("ad", "ad", "old", node("", "cta", "old cta"))
	oldAd.Ignore = true
	newAd := node("ad", "ad", "new", node("", "cta", "new cta"))
	newAd.Ignore = true
	plan, err := Diff(oldAd, newAd, testOptions())
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Optimal || plan.TotalCost != 0 || len(plan.Operations) != 0 {
		t.Fatalf("ignored-only diff=%+v", plan)
	}
	if plan.Stats.Ignored != 4 {
		t.Fatalf("ignored=%d, want 4", plan.Stats.Ignored)
	}
}

func TestExhaustiveSmallTrees(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		useIDs := seed%3 == 0
		oldRoot := randomTree(rng, 5, useIDs)
		newRoot := randomTree(rng, 5, useIDs)
		opts := Options{
			DeleteCost:  1 + rng.Intn(6),
			InsertCost:  1 + rng.Intn(6),
			RolePenalty: rng.Intn(4),
			MoveCost:    rng.Intn(6),
			Budget:      4_000_000,
			MaxBlocks:   MaxBlocks,
		}
		plan, err := Diff(oldRoot, newRoot, opts)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if !plan.Optimal {
			t.Fatalf("seed %d: expected exhaustive small-tree solve, reason=%s states=%d", seed, plan.Reason, plan.States)
		}
		want := bruteForceCost(t, oldRoot, newRoot, opts)
		if plan.TotalCost != want {
			t.Fatalf("seed %d: Diff cost=%d, brute force=%d", seed, plan.TotalCost, want)
		}
		validatePlan(t, plan, oldRoot, newRoot)
	}
}

func randomTree(rng *rand.Rand, count int, useIDs bool) *Block {
	roles := []string{"document", "section", "paragraph", "heading", "ad"}
	texts := []string{"", "a", "a", "b", "c", "tail", "alpha", "beta"}
	id := func(n int) string {
		if !useIDs {
			return ""
		}
		return fmt.Sprintf("n%d", n)
	}
	root := node(id(0), "document", "")
	nodes := []*Block{root}
	for n := 1; n < count; n++ {
		parent := nodes[rng.Intn(len(nodes))]
		b := node(id(n), roles[rng.Intn(len(roles))], texts[rng.Intn(len(texts))])
		parent.Children = append(parent.Children, b)
		nodes = append(nodes, b)
	}
	return root
}

func validatePlan(t *testing.T, plan Plan, oldRoot, newRoot *Block) {
	t.Helper()
	sum := 0
	oldSeen := make(map[int]bool)
	newSeen := make(map[int]bool)
	for _, op := range plan.Operations {
		sum += op.Cost
		switch op.Kind {
		case OpMatch, OpMove:
			if op.OldOrder < 0 || op.NewOrder < 0 {
				t.Fatalf("matched op has missing side: %+v", op)
			}
			if oldSeen[op.OldOrder] || newSeen[op.NewOrder] {
				t.Fatalf("duplicate match: %+v", op)
			}
			oldSeen[op.OldOrder] = true
			newSeen[op.NewOrder] = true
			if op.Kind == OpMove && !op.Moved {
				t.Fatalf("move op not flagged: %+v", op)
			}
			if op.Kind == OpMatch && op.Moved {
				t.Fatalf("match op flagged moved: %+v", op)
			}
			if op.Cost != op.TextDistance+op.RolePenalty+op.MovePenalty {
				t.Fatalf("cost breakdown mismatch: %+v", op)
			}
		case OpDelete:
			if op.OldOrder < 0 || oldSeen[op.OldOrder] {
				t.Fatalf("invalid/duplicate delete: %+v", op)
			}
			oldSeen[op.OldOrder] = true
		case OpInsert:
			if op.NewOrder < 0 || newSeen[op.NewOrder] {
				t.Fatalf("invalid/duplicate insert: %+v", op)
			}
			newSeen[op.NewOrder] = true
		default:
			t.Fatalf("unknown op kind %q", op.Kind)
		}
	}
	if sum != plan.TotalCost {
		t.Fatalf("sum(op costs)=%d, plan total=%d", sum, plan.TotalCost)
	}
	oldCount, newCount := activeBlockCount(oldRoot), activeBlockCount(newRoot)
	for i := 0; i < oldCount; i++ {
		if !oldSeen[i] {
			t.Fatalf("old block %d missing from plan", i)
		}
	}
	for j := 0; j < newCount; j++ {
		if !newSeen[j] {
			t.Fatalf("new block %d missing from plan", j)
		}
	}
}

func activeBlockCount(root *Block) int {
	if root == nil || root.Ignore {
		return 0
	}
	n := 1
	for _, child := range root.Children {
		n += activeBlockCount(child)
	}
	return n
}

// bruteForceCost independently enumerates every injective old->new partial
// matching and insert/delete completion for small trees.
func bruteForceCost(t *testing.T, oldRoot, newRoot *Block, opts Options) int {
	t.Helper()
	oldNodes := bruteFlatten(oldRoot)
	newNodes := bruteFlatten(newRoot)
	nO, nN := len(oldNodes), len(newNodes)
	base := make([][]int, nO)
	for i := range base {
		base[i] = make([]int, nN)
		for j := 0; j < nN; j++ {
			d := Levenshtein(oldNodes[i].b.Text, newNodes[j].b.Text)
			if oldNodes[i].b.Role != newNodes[j].b.Role {
				d += opts.RolePenalty
			}
			base[i][j] = d
		}
	}

	best := int(^uint(0) >> 1)
	assign := make([]int, nO)
	used := make([]bool, nN)
	var dfs func(i, cost int)
	dfs = func(i, cost int) {
		if i == nO {
			left := 0
			for j := 0; j < nN; j++ {
				if !used[j] {
					left++
				}
			}
			if total := cost + left*opts.InsertCost; total < best {
				best = total
			}
			return
		}
		assign[i] = -1
		dfs(i+1, cost+opts.DeleteCost)
		for j := 0; j < nN; j++ {
			if used[j] {
				continue
			}
			mc := base[i][j]
			p, q := oldNodes[i].parent, newNodes[j].parent
			moved := false
			if p < 0 && q < 0 {
				moved = false
			} else if p < 0 || q < 0 {
				moved = true
			} else {
				moved = assign[p] != q
			}
			if moved {
				mc += opts.MoveCost
			}
			assign[i] = j
			used[j] = true
			dfs(i+1, cost+mc)
			used[j] = false
			assign[i] = -1
		}
	}
	dfs(0, 0)
	return best
}

type bruteNode struct {
	b      *Block
	parent int
}

func bruteFlatten(root *Block) []bruteNode {
	if root == nil || root.Ignore {
		return nil
	}
	var out []bruteNode
	var walk func(*Block, int, bool)
	walk = func(b *Block, parent int, inheritedIgnore bool) {
		if b == nil || inheritedIgnore || b.Ignore {
			return
		}
		idx := len(out)
		out = append(out, bruteNode{b: b, parent: parent})
		for _, child := range b.Children {
			walk(child, idx, false)
		}
	}
	walk(root, -1, false)
	return out
}

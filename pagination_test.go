package taskpkg_test

import (
	"errors"
	"reflect"
	"sort"
	"testing"

	taskpkg "example.org/domainreissue/q074"
)

func TestDynamicInsertionDoesNotStopAtSharedBoundary(t *testing.T) {
	tracker := taskpkg.NewTracker(10)

	results := []taskpkg.Progress{
		tracker.Observe(taskpkg.NextPage(
			[]string{"pin-a", "a", "b"},
			[]bool{true, false, false},
			"cursor-1",
		)),
		tracker.Observe(taskpkg.NextPage(
			[]string{"pin-a", "b", "inserted-x", "c"},
			[]bool{true, true, false, false},
			"cursor-2",
		)),
		tracker.Observe(taskpkg.NextPage(
			[]string{"pin-a", "inserted-x", "c", "d"},
			[]bool{true, false, false, false},
			"cursor-3",
		)),
		tracker.Observe(taskpkg.LastPage(
			[]string{"pin-a", "d", "e"},
			[]bool{true, false, false},
		)),
	}

	if status := results[1].Status; status != taskpkg.StatusContinue {
		t.Fatalf("shared pinned boundary stopped pagination: status=%s", status)
	}
	if !results[1].Progressed {
		t.Fatal("page with inserted non-pinned keys must advance progress")
	}
	assertStrings(t, results[1].NewKeys, []string{"inserted-x", "c"})
	assertStrings(t, results[1].DuplicatePinnedKeys, []string{"pin-a", "b"})

	if status := results[3].Status; status != taskpkg.StatusComplete {
		t.Fatalf("last page status=%s, want %s", status, taskpkg.StatusComplete)
	}

	summary := tracker.Summary()
	if !summary.Complete {
		t.Fatal("explicit last page must complete the collection")
	}
	assertStrings(t, summary.CollectedKeys, []string{
		"pin-a", "a", "b", "inserted-x", "c", "d", "e",
	})
	assertNoDuplicates(t, summary.CollectedKeys)
}

func TestAllPinnedPagesAdvanceOnlyByNewCursor(t *testing.T) {
	tracker := taskpkg.NewTracker(10)

	first := tracker.Observe(taskpkg.NextPage(
		[]string{"pin-1", "pin-2"},
		[]bool{true, true},
		"cursor-1",
	))
	second := tracker.Observe(taskpkg.NextPage(
		[]string{"pin-2", "pin-3"},
		[]bool{true, true},
		"cursor-2",
	))
	last := tracker.Observe(taskpkg.LastPage(
		[]string{"pin-3"},
		[]bool{true},
	))

	for index, result := range []taskpkg.Progress{first, second, last} {
		if len(result.NewKeys) != 0 {
			t.Fatalf("page %d produced non-pinned progress from pinned keys: %v", index, result.NewKeys)
		}
	}
	if !first.Progressed || !first.NewCursor {
		t.Fatal("first all-pinned page must progress because its next cursor is new")
	}
	if !second.Progressed || !second.NewCursor {
		t.Fatal("second all-pinned page must progress because its next cursor is new")
	}
	if second.Status != taskpkg.StatusContinue {
		t.Fatalf("second page status=%s, want continue", second.Status)
	}
	if last.Status != taskpkg.StatusComplete {
		t.Fatalf("last all-pinned page status=%s, want complete", last.Status)
	}
	assertStrings(t, tracker.CollectedKeys(), []string{"pin-1", "pin-2", "pin-3"})
}

func TestEmptyPageWithNewCursorContinues(t *testing.T) {
	tracker := taskpkg.NewTracker(10)

	empty := tracker.Observe(taskpkg.NextPage(nil, nil, "cursor-1"))
	if empty.Status != taskpkg.StatusContinue {
		t.Fatalf("empty page with new cursor status=%s, want continue", empty.Status)
	}
	if !empty.Progressed || !empty.NewCursor {
		t.Fatal("empty page with new cursor must advance progress")
	}
	if summary := tracker.Summary(); summary.Complete {
		t.Fatal("empty page must not be treated as upstream completion")
	}

	last := tracker.Observe(taskpkg.LastPage([]string{"a"}, nil))
	if last.Status != taskpkg.StatusComplete {
		t.Fatalf("explicit last page status=%s, want complete", last.Status)
	}
}

func TestBudgetExhaustionIsIncomplete(t *testing.T) {
	tracker := taskpkg.NewTracker(2)

	first := tracker.Observe(taskpkg.NextPage([]string{"a"}, nil, "cursor-1"))
	if first.Status != taskpkg.StatusContinue {
		t.Fatalf("first page status=%s, want continue", first.Status)
	}
	second := tracker.Observe(taskpkg.NextPage([]string{"b"}, nil, "cursor-2"))
	if second.Status != taskpkg.StatusBudgetExhausted {
		t.Fatalf("second page status=%s, want %s", second.Status, taskpkg.StatusBudgetExhausted)
	}

	summary := tracker.Summary()
	if summary.Complete || summary.Status.IsComplete() {
		t.Fatal("budget exhaustion must not report upstream completion")
	}
	if !summary.Status.IsIncomplete() {
		t.Fatalf("budget exhaustion status=%s, want incomplete", summary.Status)
	}
	if !summary.Incomplete || summary.Complete {
		t.Fatalf("budget summary complete=%v incomplete=%v", summary.Complete, summary.Incomplete)
	}
	if summary.Consumed != 2 || summary.BudgetRemaining != 0 {
		t.Fatalf("budget accounting consumed=%d remaining=%d", summary.Consumed, summary.BudgetRemaining)
	}
	assertStrings(t, summary.CollectedKeys, []string{"a", "b"})
}

func TestExplicitLastPageWinsOverBudgetEdge(t *testing.T) {
	tracker := taskpkg.NewTracker(1)
	result := tracker.Observe(taskpkg.LastPage([]string{"a"}, nil))

	if result.Status != taskpkg.StatusComplete {
		t.Fatalf("status=%s, want complete", result.Status)
	}
	if !tracker.Summary().Complete {
		t.Fatal("explicit no-next signal must complete even when the budget is now zero")
	}
}

func TestCursorCycleReportsCompleteEvidence(t *testing.T) {
	tracker := taskpkg.NewTracker(10)
	tracker.Observe(taskpkg.NextPage([]string{"a"}, nil, "cursor-1"))
	tracker.Observe(taskpkg.NextPage([]string{"b"}, nil, "cursor-2"))
	result := tracker.Observe(taskpkg.NextPage([]string{"c"}, nil, "cursor-1"))

	if result.Status != taskpkg.StatusCursorCycle {
		t.Fatalf("status=%s, want %s", result.Status, taskpkg.StatusCursorCycle)
	}
	if !result.Incomplete || result.Complete {
		t.Fatalf("cycle result complete=%v incomplete=%v", result.Complete, result.Incomplete)
	}
	if !result.CursorRepeated {
		t.Fatal("repeated cursor must be marked")
	}
	if !result.Progressed {
		t.Fatal("the page still collected a new non-pinned key before the cycle closed")
	}
	if result.Cycle == nil {
		t.Fatal("cursor cycle must include evidence")
	}

	evidence := result.Cycle
	if evidence.StartCursor != "cursor-1" {
		t.Fatalf("cycle start=%q, want cursor-1", evidence.StartCursor)
	}
	assertStrings(t, evidence.Cursors, []string{"cursor-1", "cursor-2", "cursor-1"})
	if len(evidence.Pages) != 2 {
		t.Fatalf("cycle pages=%d, want 2", len(evidence.Pages))
	}
	if evidence.Pages[0].Index != 1 || evidence.Pages[1].Index != 2 {
		t.Fatalf("cycle page indexes=%d,%d, want 1,2", evidence.Pages[0].Index, evidence.Pages[1].Index)
	}
	if len(evidence.Edges) != 2 {
		t.Fatalf("cycle edges=%d, want 2", len(evidence.Edges))
	}
	if evidence.Edges[0].From != "cursor-1" || evidence.Edges[0].To != "cursor-2" {
		t.Fatalf("first edge=%v", evidence.Edges[0])
	}
	if evidence.Edges[1].From != "cursor-2" || evidence.Edges[1].To != "cursor-1" {
		t.Fatalf("second edge=%v", evidence.Edges[1])
	}

	summary := tracker.Summary()
	if summary.Complete {
		t.Fatal("a cursor cycle is not upstream completion")
	}
	assertStrings(t, summary.CollectedKeys, []string{"a", "b", "c"})
}

func TestSelfCycleFromStartingCursor(t *testing.T) {
	tracker := taskpkg.NewTrackerAt(10, "start")
	result := tracker.Observe(taskpkg.NextPage([]string{"a"}, nil, "start"))

	if result.Status != taskpkg.StatusCursorCycle {
		t.Fatalf("status=%s, want cursor cycle", result.Status)
	}
	if result.Cycle == nil || result.Cycle.Length() != 1 {
		t.Fatalf("self cycle evidence=%+v, want one page", result.Cycle)
	}
	assertStrings(t, result.Cycle.Cursors, []string{"start", "start"})
}

func TestAllPinnedPageWithRepeatedCursorIsCycle(t *testing.T) {
	tracker := taskpkg.NewTrackerAt(10, "start")
	tracker.Observe(taskpkg.NextPage(
		[]string{"pin-1"},
		[]bool{true},
		"cursor-1",
	))
	result := tracker.Observe(taskpkg.NextPage(
		[]string{"pin-1"},
		[]bool{true},
		"cursor-1",
	))

	if result.Status != taskpkg.StatusCursorCycle {
		t.Fatalf("status=%s, want cursor cycle", result.Status)
	}
	if len(result.NewKeys) != 0 {
		t.Fatalf("all-pinned page produced non-pinned progress: %v", result.NewKeys)
	}
	if result.Cycle == nil || result.Cycle.Length() != 1 {
		t.Fatalf("cycle evidence=%+v, want one self-loop page", result.Cycle)
	}
	assertStrings(t, result.Cycle.Cursors, []string{"cursor-1", "cursor-1"})
}

func TestRunningOutOfInputIsNotCompletion(t *testing.T) {
	pages := []taskpkg.Page{
		taskpkg.NextPage([]string{"a"}, nil, "cursor-1"),
		taskpkg.NextPage([]string{"b"}, nil, "cursor-2"),
	}
	summary := taskpkg.Evaluate(pages, 10)

	if summary.Complete {
		t.Fatal("running out of supplied pages must not infer upstream completion")
	}
	if summary.Status != taskpkg.StatusContinue {
		t.Fatalf("status=%s, want continue", summary.Status)
	}
	if summary.Done() {
		t.Fatal("continue status must not be terminal")
	}
}

func TestInvalidPageDoesNotConsumeBudgetOrState(t *testing.T) {
	tracker := taskpkg.NewTracker(2)
	invalid := tracker.Observe(taskpkg.Page{
		Keys:       []string{"a", "b"},
		Pinned:     []bool{true},
		NextCursor: "cursor-1",
		HasNext:    true,
	})

	if invalid.Status != taskpkg.StatusInvalidPage {
		t.Fatalf("status=%s, want invalid page", invalid.Status)
	}
	if !errors.Is(invalid.Error, taskpkg.ErrPinnedLength) {
		t.Fatalf("error=%v, want ErrPinnedLength", invalid.Error)
	}
	if tracker.BudgetRemaining() != 2 {
		t.Fatalf("invalid page consumed budget: remaining=%d", tracker.BudgetRemaining())
	}
	if len(tracker.CollectedKeys()) != 0 {
		t.Fatalf("invalid page changed collection: %v", tracker.CollectedKeys())
	}

	first := tracker.Observe(taskpkg.NextPage([]string{"a"}, nil, "cursor-1"))
	if first.Status != taskpkg.StatusContinue {
		t.Fatalf("valid page after invalid status=%s, want continue", first.Status)
	}
	last := tracker.Observe(taskpkg.LastPage([]string{"b"}, nil))
	if last.Status != taskpkg.StatusComplete {
		t.Fatalf("last page status=%s, want complete", last.Status)
	}
}

func TestDeduplicatedCollectionSetMatchesObservedUnion(t *testing.T) {
	pages := []taskpkg.Page{
		taskpkg.NextPage([]string{"a", "b"}, []bool{true, false}, "cursor-1"),
		taskpkg.NextPage([]string{"b", "c", "c"}, []bool{true, false, false}, "cursor-2"),
		taskpkg.LastPage([]string{"c", "d"}, []bool{true, false}),
	}

	summary := taskpkg.Evaluate(pages, 10)
	if !summary.Complete {
		t.Fatalf("status=%s, want complete", summary.Status)
	}
	assertStrings(t, summary.CollectedKeys, []string{"a", "b", "c", "d"})
	assertNoDuplicates(t, summary.CollectedKeys)
	if got, want := len(summary.CollectedKeys), 4; got != want {
		t.Fatalf("collected size=%d, want %d", got, want)
	}
}

func TestOversizedBudgetAndZeroBudget(t *testing.T) {
	zero := taskpkg.NewTracker(0)
	result := zero.Observe(taskpkg.NextPage([]string{"a"}, nil, "cursor-1"))
	if result.Status != taskpkg.StatusBudgetExhausted {
		t.Fatalf("zero-budget status=%s, want budget exhausted", result.Status)
	}
	if zero.Summary().Consumed != 0 {
		t.Fatalf("zero-budget tracker consumed=%d, want 0", zero.Summary().Consumed)
	}

	unlimited := taskpkg.NewTracker(-1)
	unlimited.Observe(taskpkg.NextPage([]string{"a"}, nil, "cursor-1"))
	if remaining := unlimited.BudgetRemaining(); remaining != -1 {
		t.Fatalf("unlimited remaining=%d, want -1", remaining)
	}
}

func TestCollectionModelWithCursorGraph(t *testing.T) {
	model := map[string]taskpkg.Page{
		"start":  taskpkg.NextPage([]string{"pin-1", "a"}, []bool{true, false}, "page-2"),
		"page-2": taskpkg.NextPage([]string{"pin-1", "b", "inserted"}, []bool{true, false, false}, "page-3"),
		"page-3": taskpkg.NextPage([]string{"pin-1", "inserted", "c"}, []bool{true, false, false}, "page-4"),
		"page-4": taskpkg.LastPage([]string{"pin-1", "d"}, []bool{true, false}),
	}

	tracker := taskpkg.NewTrackerAt(20, "start")
	cursor := "start"
	for {
		page, ok := model[cursor]
		if !ok {
			t.Fatalf("model has no page for cursor %q", cursor)
		}
		result := tracker.Observe(page)
		if result.Status.IsTerminal() {
			if result.Status != taskpkg.StatusComplete {
				t.Fatalf("model ended with status %s", result.Status)
			}
			break
		}
		cursor = result.NextCursor
	}

	summary := tracker.Summary()
	assertStrings(t, summary.CollectedKeys, []string{"pin-1", "a", "b", "inserted", "c", "d"})
	assertNoDuplicates(t, summary.CollectedKeys)
}

func assertStrings(t *testing.T, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func assertNoDuplicates(t *testing.T, keys []string) {
	t.Helper()
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	for index := 1; index < len(sorted); index++ {
		if sorted[index] == sorted[index-1] {
			t.Fatalf("duplicate key %q in collection %v", sorted[index], keys)
		}
	}
}

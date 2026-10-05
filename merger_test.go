package assertmerge

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func mustSubmit(t *testing.T, m *Merger, ev Event) {
	t.Helper()
	if err := m.Submit(ev); err != nil {
		t.Fatalf("Submit(%s %s/%s): %v", ev.Kind, ev.TestID, ev.NodeID, err)
	}
}

// Children may arrive before parents; trees come out in declaration order
// even when a later-declared test finishes first.
func TestOutOfOrderAssemblyAndDeclarationOrder(t *testing.T) {
	m := New([]string{"t1", "t2"}, 0)

	// t2 (declared second) completes first.
	mustSubmit(t, m, Event{TestID: "t2", NodeID: "root", Kind: KindAssert, Name: "root", Passed: true})
	mustSubmit(t, m, Event{TestID: "t2", Kind: KindDone})
	if got := m.Ready(); len(got) != 0 {
		t.Fatalf("expected no trees before t1 finishes, got %d", len(got))
	}

	// t1: grandchild, child, then root — fully out of order.
	mustSubmit(t, m, Event{TestID: "t1", NodeID: "a.b.1", ParentID: "a.b", Kind: KindAssert, Name: "leaf"})
	mustSubmit(t, m, Event{TestID: "t1", NodeID: "a.b", ParentID: "a", Kind: KindAssert, Name: "mid"})
	mustSubmit(t, m, Event{TestID: "t1", NodeID: "a", Kind: KindAssert, Name: "root"})
	mustSubmit(t, m, Event{TestID: "t1", Kind: KindDone})

	trees := m.Ready()
	if len(trees) != 2 || trees[0].TestID != "t1" || trees[1].TestID != "t2" {
		t.Fatalf("expected [t1 t2], got %v", trees)
	}
	root := trees[0].Find("a")
	if root == nil || len(root.Children) != 1 || root.Children[0].ID != "a.b" {
		t.Fatalf("t1 tree not linked: %+v", trees[0].Roots)
	}
	if leaf := root.Children[0].Children; len(leaf) != 1 || leaf[0].ID != "a.b.1" {
		t.Fatalf("t1 leaf not linked: %+v", root.Children[0])
	}
	if trees[0].Stats.Nodes != 3 || trees[0].Stats.Gaps != 0 {
		t.Fatalf("unexpected stats: %+v", trees[0].Stats)
	}
}

// Duplicate assertion and log events are dropped and counted; the first
// occurrence wins.
func TestDuplicateEvents(t *testing.T) {
	m := New([]string{"t"}, 0)
	mustSubmit(t, m, Event{TestID: "t", NodeID: "n1", Kind: KindAssert, Name: "first", Message: "orig"})
	mustSubmit(t, m, Event{TestID: "t", NodeID: "n1", Kind: KindAssert, Name: "dup", Message: "changed"})
	mustSubmit(t, m, Event{TestID: "t", NodeID: "l1", ParentID: "n1", Kind: KindLog, Message: "log line"})
	mustSubmit(t, m, Event{TestID: "t", NodeID: "l1", ParentID: "n1", Kind: KindLog, Message: "log line again"})
	mustSubmit(t, m, Event{TestID: "t", Kind: KindDone})

	trees := m.Ready()
	if len(trees) != 1 {
		t.Fatalf("expected 1 tree, got %d", len(trees))
	}
	tr := trees[0]
	if tr.Stats.Duplicates != 2 {
		t.Fatalf("expected 2 duplicates, got %d", tr.Stats.Duplicates)
	}
	n := tr.Find("n1")
	if n == nil || n.Name != "first" || n.Message != "orig" {
		t.Fatalf("first occurrence should win: %+v", n)
	}
	if len(n.Logs) != 1 || n.Logs[0] != "log line" {
		t.Fatalf("expected 1 deduped log, got %v", n.Logs)
	}
}

// A log supplement arriving after a failing assertion attaches to the
// failed node.
func TestLogSupplementAfterFailure(t *testing.T) {
	m := New([]string{"t"}, 0)
	mustSubmit(t, m, Event{TestID: "t", NodeID: "n1", Kind: KindAssert, Name: "check", Passed: false, Message: "boom"})
	mustSubmit(t, m, Event{TestID: "t", NodeID: "l1", ParentID: "n1", Kind: KindLog, Message: "stack trace: ..."})
	mustSubmit(t, m, Event{TestID: "t", NodeID: "l2", ParentID: "n1", Kind: KindLog, Message: "dump: ..."})
	mustSubmit(t, m, Event{TestID: "t", Kind: KindDone})

	tr := m.Ready()[0]
	n := tr.Find("n1")
	if n == nil || n.Passed {
		t.Fatalf("expected failed node n1, got %+v", n)
	}
	if len(n.Logs) != 2 || n.Logs[0] != "stack trace: ..." || n.Logs[1] != "dump: ..." {
		t.Fatalf("logs not attached to failed node: %v", n.Logs)
	}
	if tr.Stats.Logs != 2 {
		t.Fatalf("expected 2 logs in stats, got %+v", tr.Stats)
	}
}

// Early termination via Close: parents that never arrived become gap nodes
// and their children are preserved, not dropped.
func TestCloseWithMissingParentsYieldsGaps(t *testing.T) {
	m := New([]string{"t1", "t2"}, 0)
	// t1: two children share a missing parent "m"; "m" itself never arrives.
	mustSubmit(t, m, Event{TestID: "t1", NodeID: "c1", ParentID: "m", Kind: KindAssert, Name: "c1"})
	mustSubmit(t, m, Event{TestID: "t1", NodeID: "c2", ParentID: "m", Kind: KindAssert, Name: "c2"})
	// t2: no events at all.

	trees := m.Close()
	if len(trees) != 2 || trees[0].TestID != "t1" || trees[1].TestID != "t2" {
		t.Fatalf("expected [t1 t2] after Close, got %v", trees)
	}
	tr := trees[0]
	gap := tr.Find("m")
	if gap == nil || !gap.Gap {
		t.Fatalf("expected gap node for missing parent m: %+v", tr.Roots)
	}
	if len(gap.Children) != 2 || gap.Children[0].ID != "c1" || gap.Children[1].ID != "c2" {
		t.Fatalf("children dropped instead of attached to gap: %+v", gap.Children)
	}
	if tr.Stats.Gaps != 1 || tr.Stats.Nodes != 2 {
		t.Fatalf("unexpected stats: %+v", tr.Stats)
	}
	if len(trees[1].Roots) != 0 {
		t.Fatalf("expected empty tree for t2, got %+v", trees[1].Roots)
	}
	if err := m.Submit(Event{TestID: "t1", NodeID: "x", Kind: KindAssert}); !errors.Is(err, ErrClosed) {
		t.Fatalf("expected ErrClosed after Close, got %v", err)
	}
}

// A log whose target assertion never arrives is kept on a gap node.
func TestLogForMissingNodeYieldsGap(t *testing.T) {
	m := New([]string{"t"}, 0)
	mustSubmit(t, m, Event{TestID: "t", NodeID: "l1", ParentID: "ghost", Kind: KindLog, Message: "orphan log"})
	mustSubmit(t, m, Event{TestID: "t", Kind: KindDone})

	tr := m.Ready()[0]
	gap := tr.Find("ghost")
	if gap == nil || !gap.Gap || len(gap.Logs) != 1 || gap.Logs[0] != "orphan log" {
		t.Fatalf("expected gap node carrying orphan log: %+v", tr.Roots)
	}
}

// Abort terminates one test early; other tests are unaffected.
func TestAbortEmitsPartialTree(t *testing.T) {
	m := New([]string{"t1", "t2"}, 0)
	mustSubmit(t, m, Event{TestID: "t1", NodeID: "c", ParentID: "never", Kind: KindAssert})
	if err := m.Abort("t1"); err != nil {
		t.Fatal(err)
	}

	trees := m.Ready()
	if len(trees) != 1 || !trees[0].Aborted {
		t.Fatalf("expected aborted t1 tree, got %+v", trees)
	}
	if gap := trees[0].Find("never"); gap == nil || !gap.Gap || len(gap.Children) != 1 {
		t.Fatalf("expected gap for missing parent after abort: %+v", trees[0].Roots)
	}
	// Late assertion for the aborted test is rejected.
	if err := m.Submit(Event{TestID: "t1", NodeID: "late", Kind: KindAssert}); !errors.Is(err, ErrTestFinished) {
		t.Fatalf("expected ErrTestFinished, got %v", err)
	}
	// t2 still open.
	mustSubmit(t, m, Event{TestID: "t2", NodeID: "r", Kind: KindAssert})
	mustSubmit(t, m, Event{TestID: "t2", Kind: KindDone})
	if got := m.Ready(); len(got) != 1 || got[0].TestID != "t2" || got[0].Aborted {
		t.Fatalf("expected clean t2 tree, got %+v", got)
	}
}

// The buffer is bounded: Submit fails with ErrBufferFull at capacity, and
// emitting a tree frees space again.
func TestBoundedBuffer(t *testing.T) {
	m := New([]string{"t"}, 2)
	mustSubmit(t, m, Event{TestID: "t", NodeID: "a", Kind: KindAssert})
	mustSubmit(t, m, Event{TestID: "t", NodeID: "b", Kind: KindAssert})
	if err := m.Submit(Event{TestID: "t", NodeID: "c", Kind: KindAssert}); !errors.Is(err, ErrBufferFull) {
		t.Fatalf("expected ErrBufferFull, got %v", err)
	}
	// Duplicates do not consume buffer space.
	mustSubmit(t, m, Event{TestID: "t", NodeID: "a", Kind: KindAssert})
	if p := m.Pending(); p != 2 {
		t.Fatalf("expected 2 pending, got %d", p)
	}

	mustSubmit(t, m, Event{TestID: "t", Kind: KindDone})
	if got := m.Ready(); len(got) != 1 || got[0].Stats.Nodes != 2 {
		t.Fatalf("expected emitted tree with 2 nodes, got %+v", got)
	}
	if p := m.Pending(); p != 0 {
		t.Fatalf("expected buffer freed after emit, got %d", p)
	}
}

// Events for undeclared tests are rejected.
func TestUnknownTest(t *testing.T) {
	m := New([]string{"t"}, 0)
	if err := m.Submit(Event{TestID: "nope", NodeID: "a", Kind: KindAssert}); !errors.Is(err, ErrUnknownTest) {
		t.Fatalf("expected ErrUnknownTest, got %v", err)
	}
}

// Parallel producers submitting interleaved events still merge correctly.
func TestConcurrentSubmit(t *testing.T) {
	const tests = 8
	const perTest = 50
	order := make([]string, 0, tests)
	for i := 0; i < tests; i++ {
		order = append(order, fmt.Sprintf("t%d", i))
	}
	m := New(order, 0)

	var wg sync.WaitGroup
	for i := 0; i < tests; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := order[i]
			// Children first, then parents, then logs, then done.
			for j := perTest - 1; j >= 0; j-- {
				node := fmt.Sprintf("n%d", j)
				parent := ""
				if j > 0 {
					parent = fmt.Sprintf("n%d", (j-1)/4)
				}
				if err := m.Submit(Event{TestID: id, NodeID: node, ParentID: parent, Kind: KindAssert}); err != nil {
					t.Error(err)
					return
				}
				if err := m.Submit(Event{TestID: id, NodeID: "log" + node, ParentID: node, Kind: KindLog, Message: "m"}); err != nil {
					t.Error(err)
					return
				}
			}
			if err := m.Submit(Event{TestID: id, Kind: KindDone}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()

	trees := m.Close()
	if len(trees) != tests {
		t.Fatalf("expected %d trees, got %d", tests, len(trees))
	}
	for i, tr := range trees {
		if tr.TestID != order[i] {
			t.Fatalf("tree %d out of declaration order: %s", i, tr.TestID)
		}
		if tr.Stats.Nodes != perTest || tr.Stats.Logs != perTest || tr.Stats.Gaps != 0 {
			t.Fatalf("test %s: unexpected stats %+v", tr.TestID, tr.Stats)
		}
	}
	if p := m.Pending(); p != 0 {
		t.Fatalf("expected empty buffer, got %d", p)
	}
}

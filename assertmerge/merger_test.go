package assertmerge

import (
	"fmt"
	"testing"
	"time"
)

func collect(m *Merger) []Tree {
	var trees []Tree
	for tr := range m.Output() {
		trees = append(trees, tr)
	}
	return trees
}

func find(n *Node, id string) *Node {
	if n == nil {
		return nil
	}
	if n.ID == id {
		return n
	}
	for _, c := range n.Children {
		if f := find(c, id); f != nil {
			return f
		}
	}
	return nil
}

func assertEvent(testID, id, parent, label string, st Status) Event {
	return Event{TestID: testID, ID: id, ParentID: parent, Kind: KindAssertion, Label: label, Status: st}
}

func logEvent(testID, id, target, msg string) Event {
	return Event{TestID: testID, ID: id, ParentID: target, Kind: KindLog, Message: msg}
}

// Children may arrive before parents; the tree must still assemble correctly.
func TestOutOfOrderAssembly(t *testing.T) {
	m := NewMerger(16, []string{"t1"})
	// grandchild, child, log-before-target, then root.
	mustSubmit(t, m, assertEvent("t1", "c", "b", "grandchild", StatusPass))
	mustSubmit(t, m, logEvent("t1", "l1", "b", "log for b"))
	mustSubmit(t, m, assertEvent("t1", "b", "a", "child", StatusPass))
	mustSubmit(t, m, assertEvent("t1", "a", "", "root", StatusPass))
	if err := m.Finish("t1"); err != nil {
		t.Fatal(err)
	}
	m.Close()

	trees := collect(m)
	m.Wait()
	if len(trees) != 1 {
		t.Fatalf("want 1 tree, got %d", len(trees))
	}
	tr := trees[0]
	if !tr.Complete || tr.Failed {
		t.Fatalf("want complete non-failed tree, got %+v", tr)
	}
	if tr.Root == nil || tr.Root.ID != "a" {
		t.Fatalf("root = %+v", tr.Root)
	}
	b := find(tr.Root, "b")
	if b == nil || len(b.Children) != 1 || b.Children[0].ID != "c" {
		t.Fatalf("bad hierarchy: %+v", b)
	}
	if len(b.Logs) != 1 || b.Logs[0] != "log for b" {
		t.Fatalf("log not attached to b: %v", b.Logs)
	}
	if got := m.Stats().Received; got != 4 {
		t.Fatalf("Received = %d, want 4", got)
	}
}

// Trees come out in declaration order even if later tests finish first.
func TestDeclarationOrder(t *testing.T) {
	m := NewMerger(16, []string{"t1", "t2", "t3"})
	for _, id := range []string{"t3", "t2", "t1"} {
		mustSubmit(t, m, assertEvent(id, id+"-root", "", "root", StatusPass))
	}
	// Finish in reverse declaration order.
	mustFinish(t, m, "t3")
	mustFinish(t, m, "t2")
	mustFinish(t, m, "t1")
	m.Close()

	trees := collect(m)
	m.Wait()
	if len(trees) != 3 {
		t.Fatalf("want 3 trees, got %d", len(trees))
	}
	for i, want := range []string{"t1", "t2", "t3"} {
		if trees[i].TestID != want {
			t.Fatalf("trees[%d].TestID = %q, want %q", i, trees[i].TestID, want)
		}
		if !trees[i].Complete {
			t.Fatalf("tree %q not complete", want)
		}
	}
}

// Duplicate assertion and log events are discarded idempotently.
func TestDuplicateEvents(t *testing.T) {
	m := NewMerger(16, []string{"t1"})
	ev := assertEvent("t1", "a", "", "root", StatusPass)
	lg := logEvent("t1", "l1", "a", "hello")
	mustSubmit(t, m, ev)
	mustSubmit(t, m, lg)
	mustSubmit(t, m, ev) // duplicate assertion
	mustSubmit(t, m, lg) // duplicate log
	mustSubmit(t, m, ev) // duplicate assertion again
	mustFinish(t, m, "t1")
	m.Close()

	trees := collect(m)
	m.Wait()
	if len(trees) != 1 {
		t.Fatalf("want 1 tree, got %d", len(trees))
	}
	root := trees[0].Root
	if root == nil || len(root.Children) != 0 {
		t.Fatalf("duplicate assertions leaked: %+v", root)
	}
	if len(root.Logs) != 1 {
		t.Fatalf("duplicate logs leaked: %v", root.Logs)
	}
	st := m.Stats()
	if st.Duplicates != 3 {
		t.Fatalf("Duplicates = %d, want 3", st.Duplicates)
	}
	if st.Received != 2 {
		t.Fatalf("Received = %d, want 2", st.Received)
	}
}

// Logs submitted after a failure (and after Finish) must still be merged:
// failed trees are held until Close.
func TestLateLogsAfterFailure(t *testing.T) {
	m := NewMerger(16, []string{"t1"})
	mustSubmit(t, m, assertEvent("t1", "a", "", "root", StatusPass))
	mustSubmit(t, m, assertEvent("t1", "b", "a", "doomed", StatusFail))
	mustFinish(t, m, "t1")

	// Give the merger a chance to (incorrectly) stream the failed tree.
	time.Sleep(50 * time.Millisecond)
	select {
	case tr := <-m.Output():
		t.Fatalf("failed tree emitted before Close: %+v", tr)
	default:
	}

	// Supplementary logs arrive after the failure was reported.
	mustSubmit(t, m, logEvent("t1", "l1", "b", "stack trace line 1"))
	mustSubmit(t, m, logEvent("t1", "l2", "b", "stack trace line 2"))
	m.Close()

	trees := collect(m)
	m.Wait()
	if len(trees) != 1 {
		t.Fatalf("want 1 tree, got %d", len(trees))
	}
	tr := trees[0]
	if !tr.Failed {
		t.Fatal("tree not marked failed")
	}
	if !tr.Complete {
		t.Fatal("failed but fully resolved tree should be complete")
	}
	b := find(tr.Root, "b")
	if b == nil {
		t.Fatal("node b missing")
	}
	if len(b.Logs) != 2 || b.Logs[0] != "stack trace line 1" || b.Logs[1] != "stack trace line 2" {
		t.Fatalf("late logs not merged in arrival order: %v", b.Logs)
	}
}

// At Close, children whose parents never arrived are attached under a gap
// node instead of being dropped.
func TestMissingParentGap(t *testing.T) {
	m := NewMerger(16, []string{"t1"})
	mustSubmit(t, m, assertEvent("t1", "a", "", "root", StatusPass))
	mustSubmit(t, m, assertEvent("t1", "orphan", "ghost", "orphan child", StatusPass))
	mustSubmit(t, m, logEvent("t1", "l1", "ghost-log", "dangling log"))
	mustFinish(t, m, "t1")
	m.Close()

	trees := collect(m)
	m.Wait()
	if len(trees) != 1 {
		t.Fatalf("want 1 tree, got %d", len(trees))
	}
	tr := trees[0]
	if tr.Complete {
		t.Fatal("tree with gaps must not be complete")
	}
	if tr.Root == nil || tr.Root.ID != "a" {
		t.Fatalf("root = %+v", tr.Root)
	}
	gap := find(tr.Root, "gap:ghost")
	if gap == nil || !gap.Gap {
		t.Fatal("gap node for missing parent not found")
	}
	if len(gap.Children) != 1 || gap.Children[0].ID != "orphan" {
		t.Fatalf("orphan not preserved under gap: %+v", gap.Children)
	}
	lgap := find(tr.Root, "gap:ghost-log")
	if lgap == nil || len(lgap.Logs) != 1 || lgap.Logs[0] != "dangling log" {
		t.Fatalf("dangling log not preserved under gap: %+v", lgap)
	}
	if m.Stats().Gaps != 2 {
		t.Fatalf("Gaps = %d, want 2", m.Stats().Gaps)
	}
}

// When no root arrives at all, everything is gathered under a synthetic
// gap root.
func TestMissingRootGap(t *testing.T) {
	m := NewMerger(16, []string{"t1"})
	mustSubmit(t, m, assertEvent("t1", "b", "ghost", "child", StatusPass))
	mustFinish(t, m, "t1")
	m.Close()

	trees := collect(m)
	m.Wait()
	root := trees[0].Root
	if root == nil || root.ID != "gap:root" || !root.Gap {
		t.Fatalf("want synthetic gap root, got %+v", root)
	}
	if find(root, "b") == nil {
		t.Fatal("child dropped instead of gap-preserved")
	}
	if trees[0].Complete {
		t.Fatal("tree with gap root must not be complete")
	}
}

// Abort flushes immediately with gaps and rejects further submissions.
func TestAbortEarly(t *testing.T) {
	m := NewMerger(4, []string{"t1", "t2"})
	mustSubmit(t, m, assertEvent("t1", "a", "", "root", StatusPass))
	mustSubmit(t, m, assertEvent("t1", "b", "a", "child", StatusPass))
	// t2 never sees any event; t1 is never finished.
	m.Abort()

	trees := collect(m)
	m.Wait()
	if len(trees) != 2 {
		t.Fatalf("want 2 trees, got %d", len(trees))
	}
	if trees[0].TestID != "t1" || trees[1].TestID != "t2" {
		t.Fatalf("declaration order violated: %q, %q", trees[0].TestID, trees[1].TestID)
	}
	if trees[0].Complete {
		t.Fatal("aborted tree must not be complete")
	}
	if trees[1].Root != nil {
		t.Fatal("test without events must have nil root")
	}
	if err := m.Submit(assertEvent("t1", "x", "a", "late", StatusPass)); err != ErrClosed {
		t.Fatalf("Submit after Abort = %v, want ErrClosed", err)
	}
	if err := m.Finish("t1"); err != ErrClosed {
		t.Fatalf("Finish after Abort = %v, want ErrClosed", err)
	}
}

// A bounded buffer applies backpressure without losing events.
func TestBoundedBufferBackpressure(t *testing.T) {
	const n = 200
	m := NewMerger(1, []string{"t1"})
	done := make(chan error, 1)
	go func() {
		if err := m.Submit(assertEvent("t1", "root", "", "root", StatusPass)); err != nil {
			done <- err
			return
		}
		for i := 0; i < n; i++ {
			ev := assertEvent("t1", fmt.Sprintf("child-%03d", i), "root", "child", StatusPass)
			if err := m.Submit(ev); err != nil {
				done <- err
				return
			}
		}
		done <- m.Finish("t1")
	}()

	// The tree streams out as soon as it is complete; consume concurrently
	// while the producer fights the tiny buffer.
	var trees []Tree
	for tr := range m.Output() {
		trees = append(trees, tr)
		if len(trees) == 1 {
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			m.Close()
		}
	}
	m.Wait()

	if len(trees) != 1 {
		t.Fatalf("want 1 tree, got %d", len(trees))
	}
	root := trees[0].Root
	if root == nil || len(root.Children) != n {
		t.Fatalf("root has %d children, want %d (events lost under backpressure)",
			len(root.Children), n)
	}
	if got := m.Stats().Received; got != n+1 {
		t.Fatalf("Received = %d, want %d", got, n+1)
	}
}

// TrySubmit never blocks and reports a full buffer; after Close it reports
// ErrClosed.
func TestTrySubmit(t *testing.T) {
	m := NewMerger(1, []string{"t1"})
	if err := m.TrySubmit(assertEvent("t1", "a", "", "root", StatusPass)); err != nil {
		t.Fatalf("TrySubmit on empty buffer: %v", err)
	}
	m.Close()
	m.Wait()
	// Drain.
	for range m.Output() {
	}
	if err := m.TrySubmit(assertEvent("t1", "b", "a", "late", StatusPass)); err != ErrClosed {
		t.Fatalf("TrySubmit after Close = %v, want ErrClosed", err)
	}
}

func mustSubmit(t *testing.T, m *Merger, ev Event) {
	t.Helper()
	if err := m.Submit(ev); err != nil {
		t.Fatalf("Submit(%+v): %v", ev, err)
	}
}

func mustFinish(t *testing.T, m *Merger, id string) {
	t.Helper()
	if err := m.Finish(id); err != nil {
		t.Fatalf("Finish(%q): %v", id, err)
	}
}

package diskdeque

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

func testOpts() *Options {
	return &Options{BlockCapacity: 4, MaxBlocks: 64}
}

func openTest(t *testing.T, opts *Options) *Deque {
	t.Helper()
	dir := t.TempDir()
	if opts == nil {
		opts = testOpts()
	}
	d, err := Open(dir, opts)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func allElems(t *testing.T, d *Deque) []string {
	t.Helper()
	got, err := d.ReadRange(0, d.Len())
	if err != nil {
		t.Fatalf("ReadRange: %v", err)
	}
	out := make([]string, len(got))
	for i, e := range got {
		out[i] = string(e)
	}
	return out
}

func expectContents(t *testing.T, d *Deque, want []string) {
	t.Helper()
	if got := allElems(t, d); !slices.Equal(got, want) {
		t.Fatalf("contents = %v, want %v", got, want)
	}
	if d.Len() != uint64(len(want)) {
		t.Fatalf("Len = %d, want %d", d.Len(), len(want))
	}
}

func pushRange(t *testing.T, d *Deque, right bool, vals ...string) {
	t.Helper()
	tx := d.Begin()
	for _, v := range vals {
		var err error
		if right {
			err = tx.PushRight([]byte(v))
		} else {
			err = tx.PushLeft([]byte(v))
		}
		if err != nil {
			t.Fatalf("push: %v", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func blockFiles(t *testing.T, dir string) map[uint64]bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[uint64]bool{}
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, "block_") && !strings.HasSuffix(n, ".tmp") {
			var id uint64
			fmt.Sscanf(strings.TrimPrefix(n, "block_"), "%d", &id)
			out[id] = true
		}
	}
	return out
}

func TestBasicPushPop(t *testing.T) {
	d := openTest(t, nil)
	if d.Len() != 0 {
		t.Fatalf("new deque not empty: %d", d.Len())
	}
	if d.Head() != 0 || d.Tail() != 0 {
		t.Fatalf("empty head/tail = %d/%d, want 0/0", d.Head(), d.Tail())
	}

	pushRange(t, d, true, "1", "2", "3")
	pushRange(t, d, false, "L1", "L2")
	expectContents(t, d, []string{"L2", "L1", "1", "2", "3"})

	tx := d.Begin()
	v, err := tx.PopLeft()
	if err != nil || string(v) != "L2" {
		t.Fatalf("PopLeft = %q,%v", v, err)
	}
	v, err = tx.PopRight()
	if err != nil || string(v) != "3" {
		t.Fatalf("PopRight = %q,%v", v, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	expectContents(t, d, []string{"L1", "1", "2"})

	pushRange(t, d, true, "4", "5", "6", "7", "8")
	expectContents(t, d, []string{"L1", "1", "2", "4", "5", "6", "7", "8"})
}

func TestReadRange(t *testing.T) {
	d := openTest(t, nil)
	var want []string
	for i := 0; i < 25; i++ {
		want = append(want, fmt.Sprintf("e%02d", i))
	}
	pushRange(t, d, true, want...)

	cases := []struct{ start, n uint64 }{
		{0, 1}, {0, 4}, {3, 2}, {0, 25}, {10, 7}, {20, 100}, {25, 1}, {100, 3},
	}
	for _, c := range cases {
		got, err := d.ReadRange(c.start, c.n)
		if err != nil {
			t.Fatalf("ReadRange(%d,%d): %v", c.start, c.n, err)
		}
		end := c.start + c.n
		if end > uint64(len(want)) {
			end = uint64(len(want))
		}
		var w []string
		if c.start < uint64(len(want)) {
			w = append(w, want[c.start:end]...)
		}
		var gs []string
		for _, e := range got {
			gs = append(gs, string(e))
		}
		if !slices.Equal(gs, w) {
			t.Fatalf("ReadRange(%d,%d) = %v, want %v", c.start, c.n, gs, w)
		}
	}
}

// 同事务左入 + 右出：提交失败必须把两者一起回滚。
func TestTxAtomicRollbackBothEnds(t *testing.T) {
	defer ClearFaultHooks()
	d := openTest(t, nil)
	pushRange(t, d, true, "a", "b", "c")

	tx := d.Begin()
	if err := tx.PushLeft([]byte("x")); err != nil {
		t.Fatal(err)
	}
	popped, err := tx.PopRight()
	if err != nil || string(popped) != "c" {
		t.Fatalf("PopRight = %q,%v", popped, err)
	}
	SetFaultHook(FaultBeforeJournal, func() error { return ErrInjected })
	if err := tx.Commit(); !errors.Is(err, ErrInjected) {
		t.Fatalf("commit err = %v, want ErrInjected", err)
	}

	expectContents(t, d, []string{"a", "b", "c"})
	if d.Head() == 0 || d.Tail() == 0 {
		t.Fatalf("head/tail unexpectedly zero after rollback")
	}

	ClearFaultHooks()
	tx = d.Begin()
	tx.PushLeft([]byte("x"))
	if v, err := tx.PopRight(); err != nil || string(v) != "c" {
		t.Fatalf("PopRight after rollback = %q,%v", v, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("retry commit: %v", err)
	}
	expectContents(t, d, []string{"x", "a", "b"})
}

func TestEmptyToNonEmptyAndLastElement(t *testing.T) {
	d := openTest(t, nil)
	dir := d.dir

	pushRange(t, d, true, "only")
	expectContents(t, d, []string{"only"})

	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d2, err := Open(dir, testOpts())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d2.Close() })
	expectContents(t, d2, []string{"only"})

	tx := d2.Begin()
	v, err := tx.PopLeft()
	if err != nil || string(v) != "only" {
		t.Fatalf("PopLeft = %q,%v", v, err)
	}
	if _, err := tx.PopLeft(); !errors.Is(err, ErrEmpty) {
		t.Fatalf("PopLeft empty = %v, want ErrEmpty", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if d2.Len() != 0 {
		t.Fatalf("Len = %d, want 0", d2.Len())
	}
	if d2.Head() != d2.Tail() || d2.Head() != 0 {
		t.Fatalf("after last pop Head=%d Tail=%d, want both 0", d2.Head(), d2.Tail())
	}

	pushRange(t, d2, false, "again")
	expectContents(t, d2, []string{"again"})
}

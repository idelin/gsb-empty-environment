package netsec

import "testing"

func TestFactInterningAndLimit(t *testing.T) {
	c := NewCompiler()
	if c.Fact("spf") != c.Fact("spf") {
		t.Fatal("same fact id must return the same node")
	}
	if c.FactCount() != 1 {
		t.Fatalf("FactCount = %d, want 1", c.FactCount())
	}

	for i := 1; i < MaxFacts; i++ {
		c.Fact("f" + itoa(i))
	}
	if c.FactCount() != MaxFacts {
		t.Fatalf("FactCount = %d, want %d", c.FactCount(), MaxFacts)
	}
	assertPanic(t, "21st fact must panic", func() { c.Fact("overflow") })
}

func TestStructuralSharing(t *testing.T) {
	c := NewCompiler()
	a := c.Fact("a")
	b := c.Fact("b")

	left := c.And(a, b)
	right := c.And(b, a)
	if left != right {
		t.Fatal("equivalent AND subexpressions must share one node")
	}

	root := c.Or(left, c.Not(right))
	if len(root.Children()) != 2 {
		t.Fatalf("root children = %d, want 2", len(root.Children()))
	}
	if root.Children()[0] == root.Children()[1] {
		t.Fatal("NOT node must be distinct from its operand")
	}
	if root.Children()[1].Children()[0] != left {
		t.Fatal("shared AND subexpression must be reused under NOT")
	}

	// 三个父节点共享同一子式：left、root 的两个分支最终都指向同一个 AND。
	again := c.Or(c.And(a, b), a)
	if again.Children()[0] != left {
		t.Fatal("rebuilt AND must be the shared node")
	}
}

func TestCompilerValidation(t *testing.T) {
	c := NewCompiler()
	other := NewCompiler()
	foreign := other.Fact("x")

	assertPanic(t, "empty fact id", func() { c.Fact("") })
	assertPanic(t, "nil child", func() { c.And(nil) })
	assertPanic(t, "empty AND", func() { c.And() })
	assertPanic(t, "empty OR", func() { c.Or() })
	assertPanic(t, "foreign AND child", func() { c.And(foreign) })
	assertPanic(t, "foreign NOT child", func() { c.Not(foreign) })
}

func TestFactsSorted(t *testing.T) {
	c := NewCompiler()
	for _, id := range []string{"dmarc", "spf", "dkim"} {
		c.Fact(id)
	}
	got := c.Facts()
	want := []string{"dkim", "dmarc", "spf"}
	if len(got) != len(want) {
		t.Fatalf("Facts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Facts = %v, want %v", got, want)
		}
	}
}

func assertPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatalf("%s: expected panic", name)
		}
	}()
	fn()
}

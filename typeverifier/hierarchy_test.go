package typeverifier

import "testing"

func TestHierarchyAssignableAndLCA(t *testing.T) {
	h := testHierarchy(t)
	cases := []struct {
		sub, super string
		want       bool
	}{
		{"SubSubX", "X", true},
		{"SubX", "SubX", true},
		{"X", "SubX", false},
		{"Concrete", RootClass, true},
		{"X", "Y", false},
		{"Missing", "X", false},
	}
	for _, c := range cases {
		if got := h.IsAssignable(c.sub, c.super); got != c.want {
			t.Errorf("IsAssignable(%s,%s)=%v want %v", c.sub, c.super, got, c.want)
		}
	}
	if got := h.LCA("Concrete", "OtherConcrete"); got != "Middle" {
		t.Fatalf("LCA 兄弟类 = %s, want Middle", got)
	}
	if got := h.LCA("SubSubX", "Y"); got != RootClass {
		t.Fatalf("LCA 远亲 = %s, want Object", got)
	}
}

func TestHierarchyRejectsCycleAndMissingParent(t *testing.T) {
	if _, err := NewHierarchy(map[string]string{"A": "B", "B": "A"}); err == nil {
		t.Fatal("期望类树成环被拒绝")
	}
	if _, err := NewHierarchy(map[string]string{"A": "Ghost"}); err == nil {
		t.Fatal("期望未定义父类被拒绝")
	}
	if _, err := NewHierarchy(map[string]string{RootClass: "A"}); err == nil {
		t.Fatal("期望给 Object 声明父类被拒绝")
	}
}

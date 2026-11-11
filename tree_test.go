package netsec

import "testing"

func TestNewTreeAndGeneralize(t *testing.T) {
	// 地区泛化树：街区 -> 城市 -> 省 -> *
	tree, err := NewTree("region", [][2]string{
		{"海淀", "北京"},
		{"朝阳", "北京"},
		{"浦东", "上海"},
		{"北京", "华北"},
		{"上海", "华东"},
		{"华北", "*"},
		{"华东", "*"},
	})
	if err != nil {
		t.Fatalf("构建泛化树失败: %v", err)
	}
	if tree.Name() != "region" || tree.Root() != "*" || tree.Height() != 3 {
		t.Fatalf("树元数据错误: name=%q root=%q height=%d", tree.Name(), tree.Root(), tree.Height())
	}

	cases := []struct {
		value string
		steps int
		want  string
	}{
		{"海淀", 0, "海淀"},
		{"海淀", 1, "北京"},
		{"海淀", 2, "华北"},
		{"海淀", 3, "*"},
		{"浦东", 2, "华东"},
		{"*", 0, "*"},
	}
	for _, tc := range cases {
		got, err := tree.Generalize(tc.value, tc.steps)
		if err != nil {
			t.Fatalf("Generalize(%q,%d) 意外报错: %v", tc.value, tc.steps, err)
		}
		if got != tc.want {
			t.Fatalf("Generalize(%q,%d)=%q, 期望 %q", tc.value, tc.steps, got, tc.want)
		}
	}

	steps, err := tree.StepsToRoot("朝阳")
	if err != nil || steps != 3 {
		t.Fatalf("StepsToRoot(朝阳)=%d,%v, 期望 3", steps, err)
	}
}

func TestNewTreeErrors(t *testing.T) {
	if _, err := NewTree("   ", [][2]string{{"a", "b"}}); err == nil {
		t.Fatal("空树名应报错")
	}
	if _, err := NewTree("t", nil); err == nil {
		t.Fatal("无边树应报错")
	}
	// 双根
	if _, err := NewTree("t", [][2]string{{"a", "r1"}, {"b", "r2"}}); err == nil {
		t.Fatal("双根树应报错")
	}
	// 一个节点两个父节点
	if _, err := NewTree("t", [][2]string{{"a", "x"}, {"a", "y"}, {"x", "r"}, {"y", "r"}}); err == nil {
		t.Fatal("重复父节点应报错")
	}
	// 环
	if _, err := NewTree("t", [][2]string{{"a", "b"}, {"b", "a"}}); err == nil {
		t.Fatal("含环树应报错")
	}
	// 含 NUL
	if _, err := NewTree("t", [][2]string{{"a\x00", "r"}}); err == nil {
		t.Fatal("含 NUL 节点应报错")
	}
}

func TestGeneralizeErrors(t *testing.T) {
	tree, err := NewTree("region", [][2]string{{"海淀", "北京"}, {"北京", "*"}})
	if err != nil {
		t.Fatalf("构建树失败: %v", err)
	}
	if _, err := tree.Generalize("海淀", -1); err == nil {
		t.Fatal("负步数应报错")
	}
	if _, err := tree.Generalize("海淀", 3); err == nil {
		t.Fatal("超过到根距离应报错")
	}
	if _, err := tree.Generalize("未知区", 1); err == nil {
		t.Fatal("未知值应报错")
	}
}

package overlay

import (
	"strings"
	"testing"
)

// child 取目录子节点，失败即终止测试。
func child(t *testing.T, dir *Node, name string) *Node {
	t.Helper()
	if dir == nil || dir.Kind != KindDir {
		t.Fatalf("期望目录，得到 %v", dir)
	}
	n, ok := dir.Children[name]
	if !ok {
		t.Fatalf("目录 %q 缺少子项 %q", dir.Name, name)
	}
	return n
}

func absent(t *testing.T, dir *Node, name string) {
	t.Helper()
	if _, ok := dir.Children[name]; ok {
		t.Fatalf("目录 %q 不应包含子项 %q", dir.Name, name)
	}
}

// buildLayers 构造三层用于逐层展开核对：
//
//	layer0: /etc/app.conf="v1"  /data/keep.txt  /data/stale.txt
//	        /srv/old.txt        /var (dir) /var/x
//	layer1: /etc/app.conf="v2"  白化 /data/stale.txt
//	        /var 不透明 + /var/y （屏蔽下层 /var/x）
//	        文件 /srv 替换下层目录 /srv（类型替换）
//	layer2: /etc/app.conf="v3"  /doc/a.txt 与硬链接 /doc/b.txt
//	        白化目录 /data
func buildLayers(t *testing.T) []*Layer {
	t.Helper()

	l0 := NewLayer()
	l0.File("etc/app.conf", "v1")
	l0.File("data/keep.txt", "keep")
	l0.File("data/stale.txt", "stale")
	l0.File("srv/old.txt", "old")
	l0.File("var/x", "lower-x")

	l1 := NewLayer()
	l1.File("etc/app.conf", "v2")
	l1.Whiteout("data/stale.txt")
	l1.MarkOpaque("var")
	l1.File("var/y", "upper-y")
	l1.File("srv", "now-a-file") // 类型替换：文件盖目录

	l2 := NewLayer()
	l2.File("etc/app.conf", "v3")
	a := l2.File("doc/a.txt", "shared")
	if err := l2.Link("doc/b.txt", a); err != nil {
		t.Fatalf("层内硬链接失败: %v", err)
	}
	l2.Whiteout("data") // 白化整个目录

	return []*Layer{l0, l1, l2}
}

// TestLayerByLayerExpansion 逐层展开：每叠加一层就核对一次中间树，
// 验证白化、不透明屏蔽与类型替换在每一步的行为。
func TestLayerByLayerExpansion(t *testing.T) {
	layers := buildLayers(t)

	// 第 0 层：原样呈现。
	m0 := Merge(layers[0])
	if got := child(t, child(t, m0, "etc"), "app.conf").Data; got != "v1" {
		t.Fatalf("layer0 app.conf = %q, 期望 v1", got)
	}
	child(t, child(t, m0, "data"), "stale.txt")
	child(t, child(t, m0, "var"), "x")
	if n := child(t, m0, "srv"); n.Kind != KindDir {
		t.Fatalf("layer0 /srv 应为目录, 得到 %v", n.Kind)
	}

	// 叠加第 1 层：白化删除、不透明屏蔽、类型替换。
	m1 := Merge(layers[0], layers[1])
	if got := child(t, child(t, m1, "etc"), "app.conf").Data; got != "v2" {
		t.Fatalf("layer1 app.conf = %q, 期望 v2", got)
	}
	data := child(t, m1, "data")
	child(t, data, "keep.txt")   // 同目录其余子项保留
	absent(t, data, "stale.txt") // 白化删除下层同名节点
	vdir := child(t, m1, "var")
	child(t, vdir, "y")  // 不透明目录保留本层子项
	absent(t, vdir, "x") // 不透明目录屏蔽下层子项
	srv := child(t, m1, "srv")
	if srv.Kind != KindFile || srv.Data != "now-a-file" {
		t.Fatalf("类型替换失败: /srv = %v %q", srv.Kind, srv.Data)
	}
	if srv.Layer != 1 {
		t.Fatalf("/srv 来源层 = %d, 期望 1", srv.Layer)
	}

	// 叠加第 2 层：目录级白化与层内硬链接。
	m2 := Merge(layers[0], layers[1], layers[2])
	absent(t, m2, "data") // 整个目录被白化
	if got := child(t, child(t, m2, "etc"), "app.conf").Data; got != "v3" {
		t.Fatalf("layer2 app.conf = %q, 期望 v3", got)
	}
	doc := child(t, m2, "doc")
	a, b := child(t, doc, "a.txt"), child(t, doc, "b.txt")
	if a != b {
		t.Fatalf("同层硬链接在合并后应共享同一节点")
	}
	if a.Layer != 2 || b.Layer != 2 {
		t.Fatalf("硬链接来源层应为 2, 得到 %d/%d", a.Layer, b.Layer)
	}
	// 白化项自身不得出现在结果中。
	var walk func(n *Node)
	walk = func(n *Node) {
		if n.Whiteout {
			t.Fatalf("结果树中出现白化项 %q", n.Name)
		}
		if n.Kind == KindDir {
			for _, c := range n.Children {
				walk(c)
			}
		}
	}
	walk(m2)
}

// TestHardlinkLayerLocal 核对硬链接仅在层内解析。
func TestHardlinkLayerLocal(t *testing.T) {
	l0 := NewLayer()
	lower := l0.File("bin/tool", "v1")

	l1 := NewLayer()
	// 跨层引用：l0 的 inode 在 l1 中不可解析。
	if err := l1.Link("bin/tool2", lower); err == nil {
		t.Fatalf("跨层硬链接应被拒绝")
	}
	if err := l1.LinkInode("bin/tool3", lower.Inode); err == nil {
		t.Fatalf("未知 inode 应解析失败")
	}
	// 层内引用正常。
	upper := l1.File("bin/tool", "v2")
	if err := l1.LinkInode("bin/alias", upper.Inode); err != nil {
		t.Fatalf("层内 LinkInode 失败: %v", err)
	}

	m := Merge(l0, l1)
	bin := child(t, m, "bin")
	tool, alias := child(t, bin, "tool"), child(t, bin, "alias")
	if tool != alias || tool.Data != "v2" {
		t.Fatalf("层内硬链接应共享上层节点 v2")
	}
	absent(t, bin, "tool2")
	absent(t, bin, "tool3")
}

// TestRenderProvenance 核对渲染输出包含来源层与硬链接标注。
func TestRenderProvenance(t *testing.T) {
	m := Merge(buildLayers(t)...)
	out := Render(m)
	t.Log("\n" + out)
	for _, want := range []string{
		"app.conf (file) [layer 2]",
		"b.txt -> &", "(hardlink, 同 a.txt)",
		"y (file) [layer 1]",
		"srv (file) [layer 1]",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("渲染输出缺少 %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "stale.txt") || strings.Contains(out, "keep.txt") {
		t.Fatalf("被白化的节点不应出现在渲染中:\n%s", out)
	}
}

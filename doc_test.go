package peritext

import (
	"reflect"
	"testing"
)

// newSyncedPair 创建两个已同步的副本，内容为 text。
func newSyncedPair(text string) (a, b *Document) {
	a = New("alice")
	a.InsertText(0, text)
	b = New("bob")
	b.Merge(a)
	return a, b
}

// formatsOf 将 Formats 结果转成便于断言的形式。
func formatsOf(d *Document) []map[string]string { return d.Formats() }

func assertConverged(t *testing.T, docs ...*Document) {
	t.Helper()
	text := docs[0].Text()
	fm := docs[0].Formats()
	for _, d := range docs[1:] {
		if d.Text() != text {
			t.Fatalf("文本未收敛: %q vs %q", text, d.Text())
		}
		if !reflect.DeepEqual(d.Formats(), fm) {
			t.Fatalf("格式未收敛:\n%v\n%v", fm, d.Formats())
		}
	}
}

// 验收边界：'ABC' 上，用户1 给 'AB' 加粗，用户2 并发给 'BC' 加下划线。
// 合并后：A 加粗，B 加粗+下划线，C 下划线，无格式断裂。
func TestOverlappingBoldUnderline(t *testing.T) {
	alice, bob := newSyncedPair("ABC")

	alice.MarkRange("bold", "true", 0, 2)    // 用户1: AB 加粗
	bob.MarkRange("underline", "true", 1, 3) // 用户2: BC 并发下划线

	alice.Merge(bob)
	bob.Merge(alice)

	want := []map[string]string{
		{"bold": "true"},
		{"bold": "true", "underline": "true"},
		{"underline": "true"},
	}
	if got := formatsOf(alice); !reflect.DeepEqual(got, want) {
		t.Fatalf("格式不符合验收边界:\n got %v\nwant %v", got, want)
	}
	assertConverged(t, alice, bob)
}

// 同一 key 的因果冲突：B 看到 A 的加粗后再取消 => 取消必胜。
func TestCausalUnboldWins(t *testing.T) {
	alice, bob := newSyncedPair("ABC")

	alice.MarkRange("bold", "true", 0, 3)
	bob.Merge(alice)                     // B 先看到加粗
	bob.MarkRange("bold", "false", 0, 3) // 再取消加粗

	alice.Merge(bob)
	bob.Merge(alice)

	want := []map[string]string{{}, {}, {}}
	if got := formatsOf(alice); !reflect.DeepEqual(got, want) {
		t.Fatalf("因果在后的取消应生效:\n got %v\nwant %v", got, want)
	}
	assertConverged(t, alice, bob)
}

// 同一 key 的并发冲突：A 加粗、B 并发取消。
// 按 (Lamport, Actor) 确定性裁决，所有副本必须收敛到同一结果。
func TestConcurrentBoldVsUnboldDeterministic(t *testing.T) {
	alice, bob := newSyncedPair("ABC")

	alice.MarkRange("bold", "true", 0, 3)
	bob.MarkRange("bold", "false", 0, 3) // 并发取消

	alice.Merge(bob)
	bob.Merge(alice)

	// 双方 Lamport 相同，Actor 名字大者胜出："bob" > "alice"，取消生效。
	want := []map[string]string{{}, {}, {}}
	if got := formatsOf(alice); !reflect.DeepEqual(got, want) {
		t.Fatalf("并发冲突应按确定性规则裁决:\n got %v\nwant %v", got, want)
	}
	assertConverged(t, alice, bob)
}

// 部分重叠的同 key 并发冲突：A 给 AB 加粗，B 并发取消 BC。
// 逐字符判定：A 只被加粗覆盖 => 加粗；B 被两者覆盖 => 胜者决定；
// C 只被取消覆盖 => 不加粗。
func TestPartialOverlapSameKey(t *testing.T) {
	alice, bob := newSyncedPair("ABC")

	alice.MarkRange("bold", "true", 0, 2) // AB 加粗
	bob.MarkRange("bold", "false", 1, 3)  // 并发取消 BC

	alice.Merge(bob)
	bob.Merge(alice)

	// B 字符处两条标注并发，"bob" 的取消胜出。
	want := []map[string]string{
		{"bold": "true"}, // 仅 alice 的加粗覆盖
		{},               // 并发冲突，bob 的取消胜出
		{},               // 仅 bob 的取消覆盖
	}
	if got := formatsOf(alice); !reflect.DeepEqual(got, want) {
		t.Fatalf("部分重叠冲突判定错误:\n got %v\nwant %v", got, want)
	}
	assertConverged(t, alice, bob)
}

// 超链接冲突：同一区间并发设置不同 URL，LWW 取唯一胜者。
func TestLinkConflictLWW(t *testing.T) {
	alice, bob := newSyncedPair("ABC")

	alice.MarkRange("link", "https://a.example", 0, 3)
	bob.MarkRange("link", "https://b.example", 0, 3)

	alice.Merge(bob)
	bob.Merge(alice)

	want := []map[string]string{
		{"link": "https://b.example"}, // "bob" 在并发裁决中胜出
		{"link": "https://b.example"},
		{"link": "https://b.example"},
	}
	if got := formatsOf(alice); !reflect.DeepEqual(got, want) {
		t.Fatalf("链接冲突应收敛到唯一取值:\n got %v\nwant %v", got, want)
	}
	assertConverged(t, alice, bob)
}

// 边界锚定：在加粗区间末尾追加字符会继承加粗（终点向右展开），
// 在区间起点前插入字符不继承（起点不展开），区间不断裂。
func TestBoundaryExpansion(t *testing.T) {
	d := New("alice")
	d.InsertText(0, "ABC")
	d.MarkRange("bold", "true", 0, 2) // AB 加粗

	d.InsertText(2, "x") // 在 B 之后（区间末尾）插入 => 进入加粗区间
	d.InsertText(0, "y") // 在 A 之前（区间起点）插入 => 不进入

	if got := d.Text(); got != "yABxC" {
		t.Fatalf("文本错误: %q", got)
	}
	want := []map[string]string{
		{},               // y：起点不展开
		{"bold": "true"}, // A
		{"bold": "true"}, // B
		{"bold": "true"}, // x：终点展开
		{},               // C
	}
	if got := d.Formats(); !reflect.DeepEqual(got, want) {
		t.Fatalf("边界展开行为错误:\n got %v\nwant %v", got, want)
	}
}

// 并发插入同一位置：文本归并确定且收敛，格式标注不错位。
func TestConcurrentInsertWithMarks(t *testing.T) {
	alice, bob := newSyncedPair("AC")
	alice.MarkRange("bold", "true", 0, 2) // AC 加粗

	alice.InsertText(1, "x") // A|x|C
	bob.InsertText(1, "y")   // A|y|C（并发）

	alice.Merge(bob)
	bob.Merge(alice)

	assertConverged(t, alice, bob)
	if got := alice.Text(); got != "AxyC" && got != "AyxC" {
		t.Fatalf("并发插入归并结果异常: %q", got)
	}
	// 无论 x/y 相对顺序如何，两者都落在加粗区间内。
	for i, fm := range alice.Formats() {
		if fm["bold"] != "true" {
			t.Fatalf("字符 %d 丢失加粗: %v", i, alice.Formats())
		}
	}
}

// 合并的幂等性、交换律与结合律。
func TestMergeProperties(t *testing.T) {
	base := New("base")
	base.InsertText(0, "ABC")
	alice, bob, carol := New("alice"), New("bob"), New("carol")
	alice.Merge(base)
	bob.Merge(base)
	carol.Merge(base)

	alice.MarkRange("bold", "true", 0, 2)
	bob.MarkRange("underline", "true", 1, 3)
	bob.MarkRange("italic", "true", 0, 1)
	carol.MarkRange("bold", "false", 2, 3)

	// 幂等：重复合并结果不变。
	a1 := New("d1")
	a1.Merge(alice)
	a1.Merge(alice)
	a1.Merge(bob)
	a1.Merge(bob)

	// 交换：不同合并顺序结果一致。
	a2 := New("d2")
	a2.Merge(bob)
	a2.Merge(alice)

	assertConverged(t, a1, a2)

	// 结合：(a∪b)∪c == a∪(b∪c)
	left := New("d3")
	left.Merge(alice)
	left.Merge(bob)
	left.Merge(carol)
	bc := New("d4")
	bc.Merge(bob)
	bc.Merge(carol)
	right := New("d5")
	right.Merge(alice)
	right.Merge(bc)
	assertConverged(t, left, right)
}

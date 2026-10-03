package peritext

import (
	"reflect"
	"testing"
)

// setupABC returns a doc containing "ABC" and its character IDs.
func setupABC(t *testing.T) (*Doc, []ID) {
	t.Helper()
	d := New("alice")
	ids := d.Insert(ID{}, "ABC")
	if got := d.Text(); got != "ABC" {
		t.Fatalf("setup: got text %q", got)
	}
	return d, ids
}

func checkFormats(t *testing.T, d *Doc, want []map[string]string) {
	t.Helper()
	for i, w := range want {
		if got := d.FormatAt(i); !reflect.DeepEqual(got, w) {
			t.Errorf("char %d (%q): got format %v, want %v", i, string(d.Text()[i]), got, w)
		}
	}
}

// 验收场景: "ABC", 用户1 给 "AB" 加粗, 用户2 并发给 "BC" 加下划线。
// 合并后: A=bold, B=bold+underline, C=underline, 无格式断裂, 双向合并收敛。
func TestAcceptanceOverlappingFormats(t *testing.T) {
	base, ids := setupABC(t)
	alice := base.Fork("alice")
	bob := base.Fork("bob")

	alice.AddMarkRange("bold", "true", DocStart, After(ids[1]))         // AB bold
	bob.AddMarkRange("underline", "true", After(ids[0]), After(ids[2])) // BC underline

	alice.Merge(bob)
	bob.Merge(alice)

	want := []map[string]string{
		{"bold": "true"},
		{"bold": "true", "underline": "true"},
		{"underline": "true"},
	}
	for _, replica := range []*Doc{alice, bob} {
		if got := replica.Text(); got != "ABC" {
			t.Fatalf("text corrupted: %q", got)
		}
		checkFormats(t, replica, want)
	}

	// 无格式断裂: 恰好 3 个连续区间, 每个区间格式一致。
	spans := alice.Spans()
	if len(spans) != 3 {
		t.Fatalf("expected 3 contiguous spans, got %+v", spans)
	}
	for i, s := range spans {
		if len(s.Text) != 1 || !reflect.DeepEqual(s.Format, want[i]) {
			t.Errorf("span %d: got %+v, want text len 1 with format %v", i, s, want[i])
		}
	}
	if !reflect.DeepEqual(alice.Spans(), bob.Spans()) {
		t.Fatal("replicas diverged")
	}
}

// 因果边界: bob 先看到 alice 的加粗, 再取消 -> 取消生效。
func TestCausalRemoveWins(t *testing.T) {
	base, ids := setupABC(t)
	alice := base.Fork("alice")
	alice.AddMarkRange("bold", "true", DocStart, After(ids[2])) // bold ABC

	bob := alice.Fork("bob") // bob 已经看到加粗
	bob.RemoveMarkRange("bold", DocStart, After(ids[2]))
	alice.Merge(bob)

	for _, d := range []*Doc{alice, bob} {
		checkFormats(t, d, []map[string]string{nil, nil, nil})
	}
}

// 因果边界: 并发 加粗 vs 取消加粗 (互相没看到) -> 加粗确定性保留。
func TestConcurrentRemoveDoesNotCancelAdd(t *testing.T) {
	base, ids := setupABC(t)
	alice := base.Fork("alice")
	bob := base.Fork("bob")

	alice.AddMarkRange("bold", "true", DocStart, After(ids[2]))
	bob.RemoveMarkRange("bold", DocStart, After(ids[2])) // 尚未看到 alice 的加粗

	alice.Merge(bob)
	bob.Merge(alice)

	want := []map[string]string{
		{"bold": "true"},
		{"bold": "true"},
		{"bold": "true"},
	}
	for _, d := range []*Doc{alice, bob} {
		checkFormats(t, d, want)
	}
}

// 部分取消: 对 "ABC" 的加粗只取消 "B" -> A、C 保持加粗, 区间正确分裂。
func TestPartialUnboldSplitsSpan(t *testing.T) {
	base, ids := setupABC(t)
	alice := base.Fork("alice")
	alice.AddMarkRange("bold", "true", DocStart, After(ids[2])) // bold ABC

	bob := alice.Fork("bob")
	bob.RemoveMarkRange("bold", After(ids[0]), After(ids[1])) // unbold B
	alice.Merge(bob)

	want := []map[string]string{
		{"bold": "true"},
		nil,
		{"bold": "true"},
	}
	for _, d := range []*Doc{alice, bob} {
		checkFormats(t, d, want)
	}
}

// 取消后重新加粗: 新的 add 不在 remove 的取消集合里 -> 生效。
func TestReboldAfterUnbold(t *testing.T) {
	base, ids := setupABC(t)
	alice := base.Fork("alice")
	alice.AddMarkRange("bold", "true", DocStart, After(ids[2]))

	bob := alice.Fork("bob")
	bob.RemoveMarkRange("bold", DocStart, After(ids[2]))
	alice.Merge(bob)
	checkFormats(t, alice, []map[string]string{nil, nil, nil})

	alice.AddMarkRange("bold", "true", DocStart, After(ids[2])) // re-bold
	bob.Merge(alice)

	want := []map[string]string{
		{"bold": "true"},
		{"bold": "true"},
		{"bold": "true"},
	}
	for _, d := range []*Doc{alice, bob} {
		checkFormats(t, d, want)
	}
}

// 并发插入同一位置: 文本确定性收敛, 顺序由字符 ID 决定。
func TestConcurrentInsertsConverge(t *testing.T) {
	base := New("alice")
	ids := base.Insert(ID{}, "AC")
	alice := base.Fork("alice")
	bob := base.Fork("bob")

	alice.Insert(ids[0], "B") // after A
	bob.Insert(ids[0], "X")   // after A, concurrently

	alice.Merge(bob)
	bob.Merge(alice)

	if alice.Text() != bob.Text() {
		t.Fatalf("diverged: %q vs %q", alice.Text(), bob.Text())
	}
	// 兄弟节点按 (Seq 降序, Actor 降序): (bob,3) 在 (alice,3) 之前。
	if got := alice.Text(); got != "AXBC" {
		t.Fatalf("got %q, want %q", got, "AXBC")
	}
}

// 并发插入不破坏既有格式区间: 在加粗区间内部插入的字符继承格式。
func TestInsertInsideSpanInheritsFormat(t *testing.T) {
	base, ids := setupABC(t)
	alice := base.Fork("alice")
	alice.AddMarkRange("bold", "true", DocStart, After(ids[1])) // bold AB

	bob := alice.Fork("bob")
	bob.Insert(ids[0], "x") // 在 A 之后(加粗区间内部)插入 x
	alice.Merge(bob)

	if got := alice.Text(); got != "AxBC" {
		t.Fatalf("got %q", got)
	}
	checkFormats(t, alice, []map[string]string{
		{"bold": "true"},
		{"bold": "true"}, // x 落在 (DocStart, B] 内
		{"bold": "true"},
		nil,
	})
}

// 超链接冲突: 并发给重叠区间设置不同 URL, 重叠部分按
// (Lamport 时间戳, Actor) 确定性裁决, 非重叠部分各自保留。
func TestLinkConflictDeterministic(t *testing.T) {
	base, ids := setupABC(t)
	alice := base.Fork("alice")
	bob := base.Fork("bob")

	alice.AddMarkRange("link", "https://a.example", DocStart, After(ids[1]))    // AB
	bob.AddMarkRange("link", "https://b.example", After(ids[0]), After(ids[2])) // BC

	alice.Merge(bob)
	bob.Merge(alice)

	// B 上两个 add 时间戳相同, actor "bob" > "alice" -> bob 的链接胜出。
	want := []map[string]string{
		{"link": "https://a.example"},
		{"link": "https://b.example"},
		{"link": "https://b.example"},
	}
	for _, d := range []*Doc{alice, bob} {
		checkFormats(t, d, want)
	}
}

// 合并幂等且可交换: 重复合并、不同合并顺序结果一致。
func TestMergeIdempotentAndCommutative(t *testing.T) {
	base, ids := setupABC(t)
	alice := base.Fork("alice")
	bob := base.Fork("bob")
	carol := base.Fork("carol")

	alice.AddMarkRange("bold", "true", DocStart, After(ids[1]))
	bob.AddMarkRange("underline", "true", After(ids[0]), After(ids[2]))
	carol.AddMarkRange("italic", "true", After(ids[1]), DocEnd)

	m1 := base.Fork("d1")
	m1.Merge(alice)
	m1.Merge(bob)
	m1.Merge(carol)
	m1.Merge(alice) // 重复合并
	m1.Merge(bob)

	m2 := base.Fork("d2")
	m2.Merge(carol)
	m2.Merge(alice)
	m2.Merge(bob)

	if !reflect.DeepEqual(m1.Spans(), m2.Spans()) {
		t.Fatalf("merge order changed result:\n%v\n%v", m1.Spans(), m2.Spans())
	}
	want := []map[string]string{
		{"bold": "true"},
		{"bold": "true", "underline": "true"},
		{"underline": "true", "italic": "true"},
	}
	checkFormats(t, m1, want)
	checkFormats(t, m2, want)
}

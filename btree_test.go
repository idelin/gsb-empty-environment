package bptree

import (
	"math/rand"
	"reflect"
	"testing"
)

func makeKeys(n int) []int {
	keys := make([]int, n)
	for i := range keys {
		keys[i] = i + 1
	}
	return keys
}

// 满树构建：500 条记录、页容量 4、扇出 5，应得到 4 层满树，
// 页面数 = 125 叶子 + 25 + 5 + 1 内部 = 156。
func TestBulkLoadFullTree(t *testing.T) {
	tr := BulkLoad(makeKeys(500))
	if tr.Height() != 4 {
		t.Fatalf("height = %d, want 4", tr.Height())
	}
	if got, want := tr.ActivePages(), 156; got != want {
		t.Fatalf("active pages = %d, want %d", got, want)
	}
	if err := tr.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	for _, k := range []int{1, 250, 500} {
		if !tr.Get(k) {
			t.Fatalf("Get(%d) = false, want true", k)
		}
	}
	for _, k := range []int{0, 501, 1000} {
		if tr.Get(k) {
			t.Fatalf("Get(%d) = true, want false", k)
		}
	}
}

// 验收边界：从 500 条记录的满树逐一删除至剩 3 条，
// 树高由 4 层安全缩减至 1 层，空余页面全部回收至空闲链表。
func TestCascadingMergeShrinksTree(t *testing.T) {
	tr := BulkLoad(makeKeys(500))
	if tr.Height() != 4 {
		t.Fatalf("initial height = %d, want 4", tr.Height())
	}
	totalPages := tr.ActivePages()

	// 观察树高变化：必须严格沿 4 -> 3 -> 2 -> 1 单调不增地收缩。
	heights := []int{tr.Height()}
	for k := 1; k <= 497; k++ {
		if !tr.Delete(k) {
			t.Fatalf("Delete(%d) = false, want true", k)
		}
		if err := tr.Validate(); err != nil {
			t.Fatalf("after Delete(%d): %v", k, err)
		}
		if tr.Get(k) {
			t.Fatalf("after Delete(%d): key still present", k)
		}
		if h := tr.Height(); h != heights[len(heights)-1] {
			if h > heights[len(heights)-1] {
				t.Fatalf("height increased: %d -> %d", heights[len(heights)-1], h)
			}
			heights = append(heights, h)
		}
		// 页面守恒：在用页 + 空闲页 == 历史总页数，且释放数 == 空闲链表长度。
		if tr.ActivePages()+tr.FreeListLen() != totalPages {
			t.Fatalf("page leak after Delete(%d): active=%d free=%d total=%d",
				k, tr.ActivePages(), tr.FreeListLen(), totalPages)
		}
		if tr.TotalFrees() != tr.FreeListLen() {
			t.Fatalf("freed pages not on free list: frees=%d list=%d", tr.TotalFrees(), tr.FreeListLen())
		}
	}

	if want := []int{4, 3, 2, 1}; !reflect.DeepEqual(heights, want) {
		t.Fatalf("height transitions = %v, want %v", heights, want)
	}
	if tr.Height() != 1 {
		t.Fatalf("final height = %d, want 1", tr.Height())
	}
	if got, want := tr.Keys(), []int{498, 499, 500}; !reflect.DeepEqual(got, want) {
		t.Fatalf("remaining keys = %v, want %v", got, want)
	}
	for _, k := range []int{498, 499, 500} {
		if !tr.Get(k) {
			t.Fatalf("Get(%d) = false after merges, want true", k)
		}
	}
	// 仅剩根叶一页，其余 155 页全部物理回收至空闲链表。
	if got := tr.ActivePages(); got != 1 {
		t.Fatalf("active pages = %d, want 1", got)
	}
	if got, want := tr.FreeListLen(), totalPages-1; got != want {
		t.Fatalf("free list len = %d, want %d", got, want)
	}
	if tr.Merges == 0 {
		t.Fatal("expected physical merges to occur")
	}
	t.Logf("height path %v, merges=%d borrows=%d, freed %d pages to free list",
		heights, tr.Merges, tr.Borrows, tr.TotalFrees())
}

// 随机删除顺序下，级联合并同样保证结构合法并收缩到 1 层。
func TestCascadingMergeRandomOrder(t *testing.T) {
	keys := makeKeys(500)
	tr := BulkLoad(keys)
	remaining := make(map[int]bool, 500)
	for _, k := range keys {
		remaining[k] = true
	}
	order := rand.New(rand.NewSource(42)).Perm(500)
	for i, idx := range order[:497] {
		k := keys[idx]
		if !tr.Delete(k) {
			t.Fatalf("Delete(%d) = false, want true", k)
		}
		delete(remaining, k)
		if i%25 == 0 || i == 496 {
			if err := tr.Validate(); err != nil {
				t.Fatalf("after %d deletions: %v", i+1, err)
			}
		}
	}
	if tr.Height() != 1 {
		t.Fatalf("final height = %d, want 1", tr.Height())
	}
	if got := tr.ActivePages(); got != 1 {
		t.Fatalf("active pages = %d, want 1", got)
	}
	for _, k := range tr.Keys() {
		if !remaining[k] {
			t.Fatalf("key %d should have been deleted", k)
		}
	}
	if len(tr.Keys()) != 3 {
		t.Fatalf("remaining = %d keys, want 3", len(tr.Keys()))
	}
}

// 删除不存在的键不应改变树。
func TestDeleteMissingKey(t *testing.T) {
	tr := BulkLoad(makeKeys(500))
	before := tr.Keys()
	if tr.Delete(0) || tr.Delete(501) || tr.Delete(250) != true {
		t.Fatal("unexpected Delete result")
	}
	if tr.Delete(250) {
		t.Fatal("double delete should report false")
	}
	if err := tr.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	after := tr.Keys()
	if len(after) != len(before)-1 {
		t.Fatalf("key count = %d, want %d", len(after), len(before)-1)
	}
}

// 空闲链表中的页面应被后续分配复用。
func TestFreeListReuse(t *testing.T) {
	p := &pager{}
	a := p.alloc(&node{isLeaf: true})
	b := p.alloc(&node{isLeaf: true})
	p.free(a)
	p.free(b)
	if got := p.alloc(&node{isLeaf: true}); got != b {
		t.Fatalf("alloc reused %d, want %d (LIFO)", got, b)
	}
	if got := p.alloc(&node{isLeaf: true}); got != a {
		t.Fatalf("alloc reused %d, want %d", got, a)
	}
	if p.active() != 2 {
		t.Fatalf("active = %d, want 2", p.active())
	}
}

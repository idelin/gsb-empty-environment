package pager

import (
	"fmt"
	"reflect"
	"testing"
)

func ids(page Page) []string {
	out := make([]string, len(page.Items))
	for i, r := range page.Items {
		out[i] = r.ID
	}
	return out
}

func rec(id string, fields map[string]Value) Record {
	return Record{ID: id, Fields: fields}
}

func mustPage(t *testing.T, p *Paginator, req PageRequest) Page {
	t.Helper()
	page, err := p.Page(req)
	if err != nil {
		t.Fatalf("Page() error = %v", err)
	}
	return page
}

// 空值排序：升序默认空值最后、降序默认空值最前、显式 NullsFirst/NullsLast。
func TestNullOrdering(t *testing.T) {
	store := NewStore()
	store.Put(rec("a", map[string]Value{"age": Int(30)}))
	store.Put(rec("b", map[string]Value{"age": Null(KindInt)}))
	store.Put(rec("c", map[string]Value{"age": Int(25)}))
	store.Put(rec("d", map[string]Value{})) // 字段缺失也视为空值
	p := NewPaginator(store)

	cases := []struct {
		name string
		sort SortSpec
		want []string
	}{
		{"asc 默认空值最后", SortSpec{Asc("age")}, []string{"c", "a", "b", "d"}},
		{"desc 默认空值最前", SortSpec{Desc("age")}, []string{"b", "d", "a", "c"}},
		{"asc + NullsFirst", SortSpec{Asc("age").WithNulls(NullsFirst)}, []string{"b", "d", "c", "a"}},
		{"desc + NullsLast", SortSpec{Desc("age").WithNulls(NullsLast)}, []string{"a", "c", "b", "d"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			page := mustPage(t, p, PageRequest{Sort: tc.sort, Size: 10})
			if got := ids(page); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// 相同排序键：全部记录键相同，完全由唯一编号决胜，翻页不重不漏。
func TestEqualSortKeysTieBreak(t *testing.T) {
	store := NewStore()
	const n = 10
	for i := 0; i < n; i++ {
		store.Put(rec(fmt.Sprintf("id-%02d", i), map[string]Value{"grp": String("same")}))
	}
	p := NewPaginator(store)
	sort := SortSpec{Asc("grp")}

	var got []string
	cursor := ""
	for {
		page := mustPage(t, p, PageRequest{Sort: sort, Size: 4, Cursor: cursor})
		got = append(got, ids(page)...)
		if !page.HasNext {
			break
		}
		cursor = page.NextCursor
	}
	if len(got) != n {
		t.Fatalf("got %d rows, want %d", len(got), n)
	}
	for i, id := range got {
		if want := fmt.Sprintf("id-%02d", i); id != want {
			t.Fatalf("row %d = %s, want %s", i, id, want)
		}
	}
}

// 翻页期间删掉上一页末行：续查不依赖该行存在，不漏不重。
func TestDeleteLastRowOfPreviousPage(t *testing.T) {
	store := NewStore()
	for i := 0; i < 8; i++ {
		store.Put(rec(fmt.Sprintf("id-%d", i), map[string]Value{"score": Int(int64(i))}))
	}
	p := NewPaginator(store)
	sort := SortSpec{Asc("score")}

	page1 := mustPage(t, p, PageRequest{Sort: sort, Size: 3})
	if got, want := ids(page1), []string{"id-0", "id-1", "id-2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("page1 = %v, want %v", got, want)
	}

	// 删掉上一页末行，并插入、修改其他记录。
	store.Delete("id-2")
	store.Put(rec("id-new", map[string]Value{"score": Int(100)}))
	store.Put(rec("id-5", map[string]Value{"score": Int(-1)}))

	page2 := mustPage(t, p, PageRequest{Sort: sort, Size: 3, Cursor: page1.NextCursor})
	if got, want := ids(page2), []string{"id-3", "id-4", "id-5"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("page2 = %v, want %v (快照不受增删改影响)", got, want)
	}

	page3 := mustPage(t, p, PageRequest{Sort: sort, Size: 3, Cursor: page2.NextCursor})
	if got, want := ids(page3), []string{"id-6", "id-7"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("page3 = %v, want %v", got, want)
	}
	if page3.HasNext {
		t.Fatal("page3 should be the last page")
	}
}

// 正向与反向翻页互逆：正翻到第 3 页，再反翻应逐页回到第 1 页。
func TestForwardBackwardInverse(t *testing.T) {
	store := NewStore()
	for i := 0; i < 11; i++ {
		store.Put(rec(fmt.Sprintf("id-%02d", i), map[string]Value{"k": Int(int64(i % 3))}))
	}
	p := NewPaginator(store)
	sort := SortSpec{Asc("k")}

	var forward []Page
	cursor := ""
	for {
		page := mustPage(t, p, PageRequest{Sort: sort, Size: 4, Cursor: cursor})
		forward = append(forward, page)
		if !page.HasNext {
			break
		}
		cursor = page.NextCursor
	}
	if len(forward) != 3 {
		t.Fatalf("got %d forward pages, want 3", len(forward))
	}

	// 从最后一页反向翻回，每页应与正向对应页完全一致。
	cursor = forward[len(forward)-1].PrevCursor
	for i := len(forward) - 2; i >= 0; i-- {
		page := mustPage(t, p, PageRequest{Sort: sort, Size: 4, Cursor: cursor, Dir: Backward})
		if got, want := ids(page), ids(forward[i]); !reflect.DeepEqual(got, want) {
			t.Fatalf("backward page = %v, want %v (forward page %d)", got, want, i)
		}
		if i > 0 {
			if !page.HasPrev {
				t.Fatalf("page %d should have prev", i)
			}
			cursor = page.PrevCursor
		} else if page.HasPrev {
			t.Fatal("first page should not have prev")
		}
	}
}

// 反向首页（无游标）返回最后一页，可一路反翻回开头。
func TestBackwardFromTail(t *testing.T) {
	store := NewStore()
	for i := 0; i < 7; i++ {
		store.Put(rec(fmt.Sprintf("id-%d", i), map[string]Value{"k": Int(int64(i))}))
	}
	p := NewPaginator(store)
	sort := SortSpec{Asc("k")}

	var got []string
	cursor := ""
	for {
		page := mustPage(t, p, PageRequest{Sort: sort, Size: 3, Cursor: cursor, Dir: Backward})
		got = append(ids(page), got...)
		if !page.HasPrev {
			break
		}
		cursor = page.PrevCursor
	}
	want := []string{"id-0", "id-1", "id-2", "id-3", "id-4", "id-5", "id-6"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("backward traversal = %v, want %v", got, want)
	}
}

// 游标失效的各类明确报错。
func TestCursorErrors(t *testing.T) {
	store := NewStore()
	for i := 0; i < 5; i++ {
		store.Put(rec(fmt.Sprintf("id-%d", i), map[string]Value{"k": Int(int64(i))}))
	}
	p := NewPaginator(store)
	sort := SortSpec{Asc("k")}

	page1 := mustPage(t, p, PageRequest{Sort: sort, Size: 2})

	cases := []struct {
		name string
		req  PageRequest
		want error
	}{
		{"乱码游标", PageRequest{Sort: sort, Size: 2, Cursor: "not-a-cursor"}, ErrCursorMalformed},
		{"伪造前缀", PageRequest{Sort: sort, Size: 2, Cursor: "sp1:###"}, ErrCursorMalformed},
		{"空页大小", PageRequest{Sort: sort, Size: 0}, ErrInvalidPageSize},
		{"无排序字段", PageRequest{Size: 2}, ErrNoSortFields},
		{"排序规则变更", PageRequest{Sort: SortSpec{Desc("k")}, Size: 2, Cursor: page1.NextCursor}, ErrSortMismatch},
		{"方向不匹配", PageRequest{Sort: sort, Size: 2, Cursor: page1.NextCursor, Dir: Backward}, ErrDirectionMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := p.Page(tc.req); err != tc.want {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}

	t.Run("快照已释放", func(t *testing.T) {
		store.ReleaseSnapshot(page1.SnapshotID)
		if _, err := p.Page(PageRequest{Sort: sort, Size: 2, Cursor: page1.NextCursor}); err != ErrSnapshotExpired {
			t.Fatalf("error = %v, want %v", err, ErrSnapshotExpired)
		}
	})
}

// 多字段排序 + 空值混合，且翻页期间的修改不影响快照内记录内容。
func TestMultiFieldSortAndSnapshotIsolation(t *testing.T) {
	store := NewStore()
	store.Put(rec("r1", map[string]Value{"city": String("bj"), "age": Int(20)}))
	store.Put(rec("r2", map[string]Value{"city": String("bj"), "age": Null(KindInt)}))
	store.Put(rec("r3", map[string]Value{"city": Null(KindString), "age": Int(10)}))
	store.Put(rec("r4", map[string]Value{"city": String("sh"), "age": Int(30)}))
	p := NewPaginator(store)
	sort := SortSpec{Asc("city"), Desc("age")}

	page1 := mustPage(t, p, PageRequest{Sort: sort, Size: 2})
	// city 升序空值最后：bj 行在前；bj 内 age 降序空值最前。
	if got, want := ids(page1), []string{"r2", "r1"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("page1 = %v, want %v", got, want)
	}

	// 修改 r3 的排序键，快照内仍应取到旧值。
	store.Put(rec("r3", map[string]Value{"city": String("zz"), "age": Int(99)}))

	page2 := mustPage(t, p, PageRequest{Sort: sort, Size: 2, Cursor: page1.NextCursor})
	if got, want := ids(page2), []string{"r4", "r3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("page2 = %v, want %v", got, want)
	}
	if got := page2.Items[1].Fields["city"]; got != String("sh") && got != Null(KindString) {
		t.Fatalf("snapshot leaked live update: %v", got)
	}
	if page2.Items[1].Fields["age"] != Int(10) {
		t.Fatalf("snapshot record mutated by live update: %v", page2.Items[1].Fields)
	}
}

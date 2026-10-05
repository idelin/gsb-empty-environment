package pager_test

import (
	"fmt"

	"pager"
)

func Example() {
	store := pager.NewStore()
	store.Put(pager.Record{ID: "u1", Fields: map[string]pager.Value{
		"city": pager.String("sh"), "age": pager.Int(30)}})
	store.Put(pager.Record{ID: "u2", Fields: map[string]pager.Value{
		"city": pager.String("bj"), "age": pager.Null(pager.KindInt)}})
	store.Put(pager.Record{ID: "u3", Fields: map[string]pager.Value{
		"city": pager.String("bj"), "age": pager.Int(25)}})

	p := pager.NewPaginator(store)
	sort := pager.SortSpec{pager.Asc("city"), pager.Desc("age")}

	// 首页：开启快照。
	page, _ := p.Page(pager.PageRequest{Sort: sort, Size: 2})
	for _, r := range page.Items {
		fmt.Println("page1:", r.ID)
	}

	// 翻页期间的增删改不影响本次快照。
	store.Delete("u2")
	store.Put(pager.Record{ID: "u4", Fields: map[string]pager.Value{
		"city": pager.String("bj"), "age": pager.Int(18)}})

	// 正向续查。
	page, _ = p.Page(pager.PageRequest{Sort: sort, Size: 2, Cursor: page.NextCursor})
	for _, r := range page.Items {
		fmt.Println("page2:", r.ID)
	}

	// 反向翻回，与首页互逆。
	page, _ = p.Page(pager.PageRequest{Sort: sort, Size: 2, Cursor: page.PrevCursor, Dir: pager.Backward})
	for _, r := range page.Items {
		fmt.Println("back :", r.ID)
	}

	// Output:
	// page1: u2
	// page1: u3
	// page2: u1
	// back : u2
	// back : u3
}

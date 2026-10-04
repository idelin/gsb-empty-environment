package snapshotpage

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func rec(id string, fields map[string]any) Record {
	return Record{ID: id, Fields: fields}
}

func ids(page Page) []string {
	out := make([]string, len(page.Items))
	for i, r := range page.Items {
		out[i] = r.ID
	}
	return out
}

func seed(t *testing.T, s *Store, recs ...Record) {
	t.Helper()
	for _, r := range recs {
		if err := s.Upsert(r); err != nil {
			t.Fatalf("Upsert(%s): %v", r.ID, err)
		}
	}
}

// collectForward follows Next cursors until exhausted.
func collectForward(t *testing.T, p *Paginator, spec SortSpec, limit int) []string {
	t.Helper()
	var all []string
	req := PageRequest{Spec: spec, Limit: limit}
	for {
		page, err := p.Page(req)
		if err != nil {
			t.Fatalf("Page: %v", err)
		}
		all = append(all, ids(page)...)
		if page.Next == "" {
			return all
		}
		req = PageRequest{Spec: spec, Limit: limit, After: page.Next}
	}
}

func TestNullOrderingAndDirections(t *testing.T) {
	store := NewStore()
	seed(t, store,
		rec("a", map[string]any{"score": int64(3), "name": "x"}),
		rec("b", map[string]any{"score": nil, "name": "y"}),
		rec("c", map[string]any{"score": int64(1), "name": "z"}),
		rec("d", map[string]any{"score": nil, "name": "w"}),
	)
	p := NewPaginator(store)

	cases := []struct {
		name string
		spec SortSpec
		want []string
	}{
		{"asc nulls last", SortSpec{{Name: "score", Dir: Asc, Nulls: NullsLast}}, []string{"c", "a", "b", "d"}},
		{"asc nulls first", SortSpec{{Name: "score", Dir: Asc, Nulls: NullsFirst}}, []string{"b", "d", "c", "a"}},
		{"desc nulls last", SortSpec{{Name: "score", Dir: Desc, Nulls: NullsLast}}, []string{"a", "c", "b", "d"}},
		{"desc nulls first", SortSpec{{Name: "score", Dir: Desc, Nulls: NullsFirst}}, []string{"b", "d", "a", "c"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := collectForward(t, p, tc.spec, 2)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEqualSortKeysTieBreakByID(t *testing.T) {
	store := NewStore()
	seed(t, store,
		rec("id-3", map[string]any{"grp": "g", "v": int64(1)}),
		rec("id-1", map[string]any{"grp": "g", "v": int64(1)}),
		rec("id-2", map[string]any{"grp": "g", "v": nil}),
		rec("id-4", map[string]any{"grp": "g", "v": nil}),
	)
	p := NewPaginator(store)
	spec := SortSpec{{Name: "v", Dir: Asc, Nulls: NullsFirst}}
	// nils first, ties broken by ascending ID.
	want := []string{"id-2", "id-4", "id-1", "id-3"}
	got := collectForward(t, p, spec, 1) // page size 1 stresses the tie-breaker
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMultiFieldSort(t *testing.T) {
	store := NewStore()
	seed(t, store,
		rec("r1", map[string]any{"dept": "eng", "lvl": int64(2)}),
		rec("r2", map[string]any{"dept": "eng", "lvl": int64(1)}),
		rec("r3", map[string]any{"dept": "ops", "lvl": nil}),
		rec("r4", map[string]any{"dept": "eng", "lvl": nil}),
	)
	p := NewPaginator(store)
	spec := SortSpec{
		{Name: "dept", Dir: Asc, Nulls: NullsLast},
		{Name: "lvl", Dir: Desc, Nulls: NullsFirst},
	}
	want := []string{"r4", "r1", "r2", "r3"}
	got := collectForward(t, p, spec, 3)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestForwardBackwardAreInverse(t *testing.T) {
	store := NewStore()
	for i := 0; i < 23; i++ {
		seed(t, store, rec(fmt.Sprintf("r%02d", i), map[string]any{
			"bucket": fmt.Sprintf("b%d", i%3),
			"n":      int64((i * 7) % 5),
		}))
	}
	p := NewPaginator(store)
	spec := SortSpec{
		{Name: "n", Dir: Desc, Nulls: NullsLast},
		{Name: "bucket", Dir: Asc, Nulls: NullsLast},
	}
	const limit = 4

	// Walk forward, keeping every page.
	var pages []Page
	req := PageRequest{Spec: spec, Limit: limit}
	for {
		page, err := p.Page(req)
		if err != nil {
			t.Fatalf("forward Page: %v", err)
		}
		pages = append(pages, page)
		if page.Next == "" {
			break
		}
		req = PageRequest{Spec: spec, Limit: limit, After: page.Next}
	}
	if len(pages) < 3 {
		t.Fatalf("expected several pages, got %d", len(pages))
	}

	// From the last page, walk backward with Prev cursors: must reproduce
	// the forward pages in exact reverse.
	for i := len(pages) - 1; i > 0; i-- {
		prevToken := pages[i].Prev
		if prevToken == "" {
			t.Fatalf("page %d has no Prev cursor", i)
		}
		back, err := p.Page(PageRequest{Spec: spec, Limit: limit, Before: prevToken})
		if err != nil {
			t.Fatalf("backward Page from page %d: %v", i, err)
		}
		if !reflect.DeepEqual(ids(back), ids(pages[i-1])) {
			t.Fatalf("backward from page %d: got %v, want %v", i, ids(back), ids(pages[i-1]))
		}
		// And going forward again from that page must return page i.
		fwd, err := p.Page(PageRequest{Spec: spec, Limit: limit, After: back.Next})
		if err != nil {
			t.Fatalf("re-forward Page: %v", err)
		}
		if !reflect.DeepEqual(ids(fwd), ids(pages[i])) {
			t.Fatalf("re-forward to page %d: got %v, want %v", i, ids(fwd), ids(pages[i]))
		}
	}

	// First page must have no Prev; last page no Next.
	if pages[0].Prev != "" {
		t.Fatalf("first page unexpectedly has Prev")
	}
	if pages[len(pages)-1].Next != "" {
		t.Fatalf("last page unexpectedly has Next")
	}
}

func TestDeleteBoundaryRowBetweenPages(t *testing.T) {
	store := NewStore()
	seed(t, store,
		rec("a", map[string]any{"v": int64(1)}),
		rec("b", map[string]any{"v": int64(2)}),
		rec("c", map[string]any{"v": int64(3)}),
		rec("d", map[string]any{"v": int64(4)}),
	)
	p := NewPaginator(store)
	spec := SortSpec{{Name: "v", Dir: Asc, Nulls: NullsLast}}

	page1, err := p.Page(PageRequest{Spec: spec, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(page1); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("page1 = %v", got)
	}

	// Delete the last row of page 1 (the cursor's boundary row), then continue.
	store.Delete("b")
	page2, err := p.Page(PageRequest{Spec: spec, Limit: 2, After: page1.Next})
	if err != nil {
		t.Fatalf("page after deleting boundary row: %v", err)
	}
	if got := ids(page2); !reflect.DeepEqual(got, []string{"c", "d"}) {
		t.Fatalf("page2 = %v, want [c d]", got)
	}

	// Backward across the deleted boundary must still land on the remaining
	// prefix: rows before key(v=3,id=c) are just {a}.
	back, err := p.Page(PageRequest{Spec: spec, Limit: 2, Before: page2.Prev})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(back); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("backward across deleted row = %v, want [a]", got)
	}
}

func TestMutationsDuringPagination(t *testing.T) {
	store := NewStore()
	for i := 0; i < 10; i++ {
		seed(t, store, rec(fmt.Sprintf("r%d", i), map[string]any{"v": int64(i)}))
	}
	p := NewPaginator(store)
	spec := SortSpec{{Name: "v", Dir: Asc, Nulls: NullsLast}}

	page1, err := p.Page(PageRequest{Spec: spec, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(page1); !reflect.DeepEqual(got, []string{"r0", "r1", "r2"}) {
		t.Fatalf("page1 = %v", got)
	}

	// Insert before the boundary, update a row across the boundary, delete
	// an upcoming row — none of this may shift the next page.
	seed(t, store, rec("new", map[string]any{"v": int64(0)})) // sorts before boundary
	seed(t, store, rec("r5", map[string]any{"v": int64(-1)})) // moved before boundary
	store.Delete("r4")

	page2, err := p.Page(PageRequest{Spec: spec, Limit: 3, After: page1.Next})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(page2); !reflect.DeepEqual(got, []string{"r3", "r6", "r7"}) {
		t.Fatalf("page2 = %v, want [r3 r6 r7]", got)
	}
}

func TestCursorValidationErrors(t *testing.T) {
	store := NewStore()
	seed(t, store, rec("a", map[string]any{"v": int64(1)}))
	p := NewPaginator(store)
	spec := SortSpec{{Name: "v", Dir: Asc, Nulls: NullsLast}}

	page, err := p.Page(PageRequest{Spec: spec, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("malformed", func(t *testing.T) {
		_, err := p.Page(PageRequest{Spec: spec, Limit: 1, After: "not-a-cursor!!!"})
		if !errors.Is(err, ErrCursorMalformed) {
			t.Fatalf("err = %v, want ErrCursorMalformed", err)
		}
	})

	t.Run("snapshot mismatch", func(t *testing.T) {
		other := NewStore()
		seed(t, other, rec("a", map[string]any{"v": int64(1)}))
		_, err := NewPaginator(other).Page(PageRequest{Spec: spec, Limit: 1, After: page.Prev + "x"})
		if !errors.Is(err, ErrCursorMalformed) {
			t.Fatalf("corrupted token: err = %v, want ErrCursorMalformed", err)
		}
		_, err = NewPaginator(other).Page(PageRequest{Spec: spec, Limit: 1, After: mustNext(t, p, spec)})
		if !errors.Is(err, ErrSnapshotMismatch) {
			t.Fatalf("err = %v, want ErrSnapshotMismatch", err)
		}
	})

	t.Run("reset invalidates cursors", func(t *testing.T) {
		token := mustNext(t, p, spec)
		store.Reset()
		seed(t, store, rec("a", map[string]any{"v": int64(1)}))
		_, err := p.Page(PageRequest{Spec: spec, Limit: 1, After: token})
		if !errors.Is(err, ErrSnapshotMismatch) {
			t.Fatalf("err = %v, want ErrSnapshotMismatch", err)
		}
	})

	t.Run("sort mismatch", func(t *testing.T) {
		store2 := NewStore()
		seed(t, store2,
			rec("a", map[string]any{"v": int64(1)}),
			rec("b", map[string]any{"v": int64(2)}),
		)
		p2 := NewPaginator(store2)
		pg, err := p2.Page(PageRequest{Spec: spec, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		other := SortSpec{{Name: "v", Dir: Desc, Nulls: NullsLast}}
		_, err = p2.Page(PageRequest{Spec: other, Limit: 1, After: pg.Next})
		if !errors.Is(err, ErrSortMismatch) {
			t.Fatalf("err = %v, want ErrSortMismatch", err)
		}
	})
}

func mustNext(t *testing.T, p *Paginator, spec SortSpec) string {
	t.Helper()
	store := NewStore()
	seed(t, store,
		rec("a", map[string]any{"v": int64(1)}),
		rec("b", map[string]any{"v": int64(2)}),
	)
	page, err := NewPaginator(store).Page(PageRequest{Spec: spec, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if page.Next == "" {
		t.Fatal("expected Next cursor")
	}
	return page.Next
}

func TestRequestValidation(t *testing.T) {
	p := NewPaginator(NewStore())
	if _, err := p.Page(PageRequest{Limit: 0}); !errors.Is(err, ErrInvalidLimit) {
		t.Fatalf("err = %v, want ErrInvalidLimit", err)
	}
	if _, err := p.Page(PageRequest{Limit: 1, After: "x", Before: "y"}); !errors.Is(err, ErrConflictingCursors) {
		t.Fatalf("err = %v, want ErrConflictingCursors", err)
	}
}

func TestEmptyAndPartialPages(t *testing.T) {
	store := NewStore()
	p := NewPaginator(store)
	spec := SortSpec{{Name: "v", Dir: Asc, Nulls: NullsLast}}

	page, err := p.Page(PageRequest{Spec: spec, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 || page.Next != "" || page.Prev != "" {
		t.Fatalf("empty store: got %+v", page)
	}

	seed(t, store, rec("only", map[string]any{"v": nil}))
	page, err = p.Page(PageRequest{Spec: spec, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(page); !reflect.DeepEqual(got, []string{"only"}) || page.Next != "" || page.Prev != "" {
		t.Fatalf("single row: got %+v", page)
	}
}

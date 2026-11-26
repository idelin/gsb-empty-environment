package gin

import (
	"reflect"
	"testing"
)

func TestPostingTreeOperationsStayOrdered(t *testing.T) {
	tree := NewPostingTree([]TID{9, 1, 5, 3, 7, 3})
	want := []TID{1, 3, 5, 7, 9}
	if got := tree.TIDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("initial TIDs=%v, want %v", got, want)
	}

	tree.Insert(4)
	tree.Insert(4)
	want = []TID{1, 3, 4, 5, 7, 9}
	if got := tree.TIDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("inserted TIDs=%v, want %v", got, want)
	}
	for _, tid := range want {
		if !tree.Contains(tid) {
			t.Fatalf("tree does not contain %d", tid)
		}
	}

	for _, tid := range []TID{5, 1, 9, 4} {
		tree.Delete(tid)
	}
	want = []TID{3, 7}
	if got := tree.TIDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("deleted TIDs=%v, want %v", got, want)
	}
	if tree.Len() != len(want) {
		t.Fatalf("tree length=%d, want %d", tree.Len(), len(want))
	}
	if tree.Height() > 2 {
		t.Fatalf("unexpected tree height after deletes: %d", tree.Height())
	}
}

package treekem

import (
	"reflect"
	"testing"
)

func TestLBBTIndexVectors(t *testing.T) {
	if got := Leaf(3); got != 6 {
		t.Fatalf("Leaf(3)=%d, want 6", got)
	}
	vectors := []struct {
		level, position int
		want            Index
	}{
		{0, 0, 0}, {1, 0, 1}, {2, 0, 3}, {3, 0, 7},
		{1, 1, 5}, {2, 1, 11},
	}
	for _, vector := range vectors {
		if got := NodeIndexAt(vector.level, vector.position); got != vector.want {
			t.Fatalf("node(%d,%d)=%d, want %d", vector.level, vector.position, got, vector.want)
		}
	}

	levels := map[Index]int{0: 0, 2: 0, 1: 1, 5: 1, 9: 1, 3: 2, 11: 2, 7: 3}
	for index, want := range levels {
		if got := Level(index); got != want {
			t.Fatalf("Level(%d)=%d, want %d", index, got, want)
		}
	}

	if got := Left(7); got != 3 {
		t.Fatalf("Left(7)=%d, want 3", got)
	}
	if got := Right(7); got != 11 {
		t.Fatalf("Right(7)=%d, want ideal 11", got)
	}
	if got := Parent(5); got != 3 {
		t.Fatalf("Parent(5)=%d, want 3", got)
	}
}

func TestCompactLBBTBoundedNavigation(t *testing.T) {
	if got := RootIndex(1); got != 0 {
		t.Fatalf("root n=1 = %d", got)
	}
	if got := RootIndex(4); got != 3 {
		t.Fatalf("root n=4 = %d", got)
	}
	if got := RootIndex(5); got != 7 {
		t.Fatalf("root n=5 = %d", got)
	}

	parent8, err := ParentBounded(8, 5)
	if err != nil || parent8 != 7 {
		t.Fatalf("ParentBounded(8,5)=(%d,%v), want 7", parent8, err)
	}
	right7, err := RightBounded(7, 5)
	if err != nil || right7 != 8 {
		t.Fatalf("RightBounded(7,5)=(%d,%v), want 8", right7, err)
	}
	right7n6, err := RightBounded(7, 6)
	if err != nil || right7n6 != 9 {
		t.Fatalf("RightBounded(7,6)=(%d,%v), want 9", right7n6, err)
	}
	right11n7, err := RightBounded(11, 7)
	if err != nil || right11n7 != 12 {
		t.Fatalf("RightBounded(11,7)=(%d,%v), want 12", right11n7, err)
	}

	path8, err := DirectPath(8, 5)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Index{8, 7}; !reflect.DeepEqual(path8, want) {
		t.Fatalf("DirectPath(8,5)=%v, want %v", path8, want)
	}
	copath8, err := Copath(8, 5)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Index{3}; !reflect.DeepEqual(copath8, want) {
		t.Fatalf("Copath(8,5)=%v, want %v", copath8, want)
	}

	path0, err := DirectPath(0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Index{0, 1, 3, 7}; !reflect.DeepEqual(path0, want) {
		t.Fatalf("DirectPath(0,5)=%v, want %v", path0, want)
	}
	copath0, err := Copath(0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Index{2, 5, 8}; !reflect.DeepEqual(copath0, want) {
		t.Fatalf("Copath(0,5)=%v, want %v", copath0, want)
	}
}

func TestEveryCopathIsValidSibling(t *testing.T) {
	for leaves := 1; leaves <= 32; leaves++ {
		for position := 0; position < leaves; position++ {
			leafIndex := Leaf(position)
			path, err := DirectPath(leafIndex, leaves)
			if err != nil {
				t.Fatalf("leaves=%d leaf=%d path: %v", leaves, position, err)
			}
			copath, err := Copath(leafIndex, leaves)
			if err != nil {
				t.Fatalf("leaves=%d leaf=%d copath: %v", leaves, position, err)
			}
			if len(copath) != len(path)-1 {
				t.Fatalf("leaves=%d leaf=%d copath length %d path %d", leaves, position, len(copath), len(path))
			}
			for i, siblingIndex := range copath {
				parentIndex := path[i+1]
				leftChild, err := LeftBounded(parentIndex, leaves)
				if err != nil {
					t.Fatal(err)
				}
				rightChild, err := RightBounded(parentIndex, leaves)
				if err != nil {
					t.Fatal(err)
				}
				if siblingIndex != leftChild && siblingIndex != rightChild {
					t.Fatalf("leaves=%d pos=%d copath[%d]=%d not child of %d (%d,%d)",
						leaves, position, i, siblingIndex, parentIndex, leftChild, rightChild)
				}
				if siblingIndex == path[i] {
					t.Fatalf("copath entry duplicates direct-path child")
				}
			}
		}
	}
}

func TestResolutionBlankAndUnmerged(t *testing.T) {
	tree, err := NewRatchetTree(3)
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	for _, position := range []int{0, 1, 2} {
		index := Leaf(position)
		tree.Nodes[index] = &TreeNode{Index: index, Leaf: &LeafNode{Position: position, EncryptionKey: key, SignatureKey: make([]byte, 32)}}
	}
	tree.Nodes[1] = &TreeNode{Index: 1, Parent: &ParentNode{EncryptionKey: key}}
	tree.Nodes[3] = &TreeNode{Index: 3, Parent: &ParentNode{
		EncryptionKey:  key,
		UnmergedLeaves: []int{2},
	}}

	got, err := tree.Resolution(1)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Index{1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Resolution(1)=%v, want %v", got, want)
	}

	got, err = tree.Resolution(3)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Index{3, 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Resolution(3)=%v, want %v", got, want)
	}

	// Blank parent 1 must resolve through leaves 0 and 2.
	tree.Nodes[1] = &TreeNode{Index: 1}
	got, err = tree.Resolution(1)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Index{0, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("blank Resolution(1)=%v, want %v", got, want)
	}
}

func TestResolutionUnmergedUsesDirectPath(t *testing.T) {
	tree, err := NewRatchetTree(4)
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	for position := 0; position < 4; position++ {
		index := Leaf(position)
		tree.Nodes[index] = &TreeNode{Index: index, Leaf: &LeafNode{Position: position, EncryptionKey: key, SignatureKey: make([]byte, 32)}}
	}
	// Root 3 exists from an earlier epoch; leaf 3 is unmerged and parent 5 is blank.
	tree.Nodes[3] = &TreeNode{Index: 3, Parent: &ParentNode{EncryptionKey: key, UnmergedLeaves: []int{3}}}
	tree.Nodes[5] = &TreeNode{Index: 5}

	got, err := tree.Resolution(3)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Index{3, 6}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Resolution(root)=%v, want %v", got, want)
	}

	// A non-blank intermediate direct-path node is included in path order.
	tree.Nodes[5] = &TreeNode{Index: 5, Parent: &ParentNode{EncryptionKey: key}}
	got, err = tree.Resolution(3)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Index{3, 6, 5}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Resolution(root with intermediate)=%v, want %v", got, want)
	}
}

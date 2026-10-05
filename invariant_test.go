package adaptivehuffman

import (
	"sort"
	"strings"
	"testing"
)

// checkModelInvariants independently verifies the structural invariants of
// the adaptive tree, deriving everything from the tree itself and the order
// list (not from any encoder/decoder bookkeeping):
//
//   - the order list is exactly the set of tree nodes, ranks consistent;
//   - node ids are unique and dense (0..N-1), nextID == N;
//   - every internal node has exactly two children whose parent pointers
//     point back, and weight == left.weight + right.weight;
//   - parents always rank above their children; siblings occupy adjacent
//     ranks;
//   - the NYT leaf is unique, has weight 0 and rank 0; every other leaf has
//     weight >= 1 and is registered in the leaves table;
//   - the root has no parent and sits at the top of the order list.
func checkModelInvariants(t *testing.T, m *model) {
	t.Helper()
	if m.root == nil {
		t.Fatal("nil root")
	}
	if m.root.parent != nil {
		t.Fatalf("root %d has a parent", m.root.id)
	}
	visited := make(map[*node]bool)
	ids := make(map[int]bool)
	nytCount := 0
	leafCount := 0
	var walk func(n *node)
	walk = func(n *node) {
		if visited[n] {
			t.Fatalf("node %d visited twice (cycle or shared subtree)", n.id)
		}
		visited[n] = true
		if ids[n.id] {
			t.Fatalf("duplicate node id %d", n.id)
		}
		ids[n.id] = true
		if n.rank < 0 || n.rank >= len(m.order) || m.order[n.rank] != n {
			t.Fatalf("node %d: rank %d inconsistent with order list", n.id, n.rank)
		}
		if n.parent != nil {
			if n.parent.left != n && n.parent.right != n {
				t.Fatalf("node %d: parent %d does not reference it as a child", n.id, n.parent.id)
			}
			if n.parent.rank <= n.rank {
				t.Fatalf("node %d: parent rank %d not above child rank %d", n.id, n.parent.rank, n.rank)
			}
		}
		if n.left == nil || n.right == nil {
			if n.left != nil || n.right != nil {
				t.Fatalf("node %d: exactly one child", n.id)
			}
			leafCount++
			switch {
			case n.symbol == symNYT:
				nytCount++
				if n.weight != 0 {
					t.Fatalf("NYT node has weight %d, want 0", n.weight)
				}
				if m.nyt != n {
					t.Fatal("m.nyt does not point at the NYT leaf")
				}
				if n.rank != 0 {
					t.Fatalf("NYT node has rank %d, want 0", n.rank)
				}
			case n.symbol >= 0 && n.symbol < numSymbols:
				if n.weight == 0 {
					t.Fatalf("leaf for symbol %d has weight 0", n.symbol)
				}
				if m.leaves[n.symbol] != n {
					t.Fatalf("leaves[%d] does not point at its leaf", n.symbol)
				}
			default:
				t.Fatalf("leaf with invalid symbol value %d", n.symbol)
			}
			return
		}
		if n.symbol != symInternal {
			t.Fatalf("internal node %d has symbol %d", n.id, n.symbol)
		}
		if n.weight != n.left.weight+n.right.weight {
			t.Fatalf("internal node %d: weight %d != children sum %d",
				n.id, n.weight, n.left.weight+n.right.weight)
		}
		if d := n.left.rank - n.right.rank; d != 1 && d != -1 {
			t.Fatalf("siblings at ranks %d and %d are not adjacent", n.left.rank, n.right.rank)
		}
		walk(n.left)
		walk(n.right)
	}
	walk(m.root)

	if nytCount != 1 {
		t.Fatalf("found %d NYT leaves, want exactly 1", nytCount)
	}
	if leafCount != m.numSyms+1 {
		t.Fatalf("found %d leaves, want numSyms+1 = %d", leafCount, m.numSyms+1)
	}
	if len(visited) != len(m.order) {
		t.Fatalf("tree has %d nodes but order list has %d", len(visited), len(m.order))
	}
	for i := 0; i < len(visited); i++ {
		if !ids[i] {
			t.Fatalf("node id %d missing: ids not dense", i)
		}
	}
	if m.nextID != len(visited) {
		t.Fatalf("nextID %d != node count %d", m.nextID, len(visited))
	}
	if m.order[len(m.order)-1] != m.root {
		t.Fatal("root is not the last (highest-rank) node in the order list")
	}
}

// checkPrefixFree enumerates the codes of all leaves from the tree structure
// and verifies that no code is a prefix of another.
func checkPrefixFree(t *testing.T, m *model) {
	t.Helper()
	var codes []string
	var walk func(n *node, prefix string)
	walk = func(n *node, prefix string) {
		if n.left == nil {
			codes = append(codes, prefix)
			return
		}
		walk(n.left, prefix+"0")
		walk(n.right, prefix+"1")
	}
	walk(m.root, "")
	if len(codes) != m.numSyms+1 {
		t.Fatalf("found %d leaf codes, want %d", len(codes), m.numSyms+1)
	}
	sort.Strings(codes)
	for i := 1; i < len(codes); i++ {
		if strings.HasPrefix(codes[i], codes[i-1]) {
			t.Fatalf("code %q is a prefix of code %q", codes[i-1], codes[i])
		}
	}
}

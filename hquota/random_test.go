package hquota

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func dumpTree(tr *Tree) string {
	var sb strings.Builder
	var walk func(n *Node, d int)
	walk = func(n *Node, d int) {
		fmt.Fprintf(&sb, "%*snode %s cap %d\n", d*2, "", n.ID, n.Cap)
		for _, lf := range n.Leaves {
			fmt.Fprintf(&sb, "%*sleaf %d demand %d weight %d\n", d*2+2, "", lf.ID, lf.Demand, lf.Weight)
		}
		for _, c := range n.Children {
			walk(c, d+1)
		}
	}
	walk(tr.Root, 0)
	return sb.String()
}

// 随机树回归：任意随机结构下，分配结果必须可行、不浪费、
// 且通过公平性核验；多批分配（含随机缩容）同样成立。
func TestRandomizedTrees(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 1000; trial++ {
		tr, err := NewTree("root", int64(rng.Intn(50)))
		if err != nil {
			t.Fatalf("trial %d: %v", trial, err)
		}
		nodeIDs := []string{"root"}
		numNodes := rng.Intn(4)
		for i := 0; i < numNodes; i++ {
			id := string(rune('a' + i))
			parent := nodeIDs[rng.Intn(len(nodeIDs))]
			if err := tr.AddNode(parent, id, int64(rng.Intn(40))); err != nil {
				t.Fatalf("trial %d: %v", trial, err)
			}
			nodeIDs = append(nodeIDs, id)
		}
		numLeaves := 1 + rng.Intn(8)
		for i := 0; i < numLeaves; i++ {
			parent := nodeIDs[rng.Intn(len(nodeIDs))]
			demand := int64(rng.Intn(20))
			weight := int64(1 + rng.Intn(5))
			if err := tr.AddLeaf(parent, i, demand, weight); err != nil {
				t.Fatalf("trial %d: %v", trial, err)
			}
		}

		a := NewAllocator(tr)
		for batch := 0; batch < 3; batch++ {
			alloc := a.Allocate()
			if v := a.Verify(); len(v) != 0 {
				t.Fatalf("trial %d batch %d: Verify violations: %v (alloc %v, exact %v)\n%s", trial, batch, v, alloc, a.Exact(), dumpTree(tr))
			}
			if v := tr.VerifyAllocation(alloc); len(v) != 0 {
				t.Fatalf("trial %d batch %d: VerifyAllocation violations: %v (alloc %v)", trial, batch, v, alloc)
			}
			// 随机缩小某节点额度后重新分配。
			victim := nodeIDs[rng.Intn(len(nodeIDs))]
			if _, err := a.SetCap(victim, int64(rng.Intn(30))); err != nil {
				t.Fatalf("trial %d: %v", trial, err)
			}
		}
	}
}

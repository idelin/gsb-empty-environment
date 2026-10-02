// Command bptree-demo builds a balanced-parentheses tree index and prints
// the encoding layout, byte occupancy, sample queries, and the number of
// block accesses each query performs.
package main

import (
	"fmt"
	"strings"

	"bptree"
)

func main() {
	//          0
	//        / | \
	//       1  2  4
	//          |  / \
	//          3 5   6
	//              / \
	//             7   8
	bp := "(()(())(()(()())))"
	ix, err := bptree.Parse(bp)
	if err != nil {
		panic(err)
	}
	fmt.Printf("tree BP: %s  (%d nodes, %d bits)\n\n", bp, ix.NumNodes(), ix.NumBits())

	lay := ix.Layout()
	fmt.Println("== encoding layout ==")
	fmt.Printf("bitvector: %d words x 8B = %d B\n", lay.Words, lay.BitvectorBytes)
	fmt.Printf("blocks:    %d x 10B (base i32, total i16, relMin i16, relOpen u16) = %d B\n", lay.NumBlocks, lay.BlockBytes)
	fmt.Printf("supers:    %d x 32B (excess, min, opens, total i64)   = %d B\n", lay.NumSupers, lay.SuperBytes)
	fmt.Printf("total:     %d B  (%.1f bits/node, tables are %.1f%% of bitvector)\n\n",
		lay.TotalBytes, lay.BitsPerNode, lay.OverheadPct)

	query := func(name string, f func() string) {
		ix.ResetAccesses()
		out := f()
		fmt.Printf("%-28s -> %-22s block accesses: %d\n", name, out, ix.Accesses())
	}

	fmt.Println("== sample queries (block accesses counted) ==")
	query("Depth(8)", func() string { return fmt.Sprint(ix.Depth(8)) })
	query("Parent(8)", func() string { return fmt.Sprint(ix.Parent(8)) })
	query("FirstChild(0)", func() string { return fmt.Sprint(ix.FirstChild(0)) })
	query("NextSibling(4)", func() string { return fmt.Sprint(ix.NextSibling(4)) })
	query("PrevSibling(4)", func() string { return fmt.Sprint(ix.PrevSibling(4)) })
	query("Degree(0)", func() string { return fmt.Sprint(ix.Degree(0)) })
	query("FindClosePos(Select(4))", func() string {
		o := ix.Select(4)
		return fmt.Sprintf("open=%d close=%d", o, ix.FindClosePos(o))
	})
	query("EnclosePos(Select(8))", func() string { return fmt.Sprint(ix.EnclosePos(ix.Select(8))) })
	query("LCA(2, 8)", func() string { return fmt.Sprint(ix.LCA(2, 8)) })
	query("LCA(5, 6)", func() string { return fmt.Sprint(ix.LCA(5, 6)) })
	query("SubtreeInterval(4)", func() string {
		o, c := ix.SubtreeInterval(4)
		return fmt.Sprintf("[%d,%d] size=%d", o, c, ix.SubtreeSize(4))
	})
	query("Rank/Select roundtrip", func() string {
		ok := true
		for v := 0; v < ix.NumNodes(); v++ {
			ok = ok && ix.Rank(ix.Select(v)) == v
		}
		return fmt.Sprintf("all %d ids ok=%v", ix.NumNodes(), ok)
	})

	data := ix.MarshalBinary()
	fmt.Printf("\nserialized: %d B (bitvector + verified summaries)\n", len(data))
	if _, err := bptree.Unmarshal(data); err != nil {
		panic(err)
	}
	fmt.Println("unmarshal: summaries recomputed and verified OK")

	// deep chain: no recursion anywhere, queries skip blocks via summaries
	const n = 1 << 20
	deep, err := bptree.Parse(strings.Repeat("(", n) + strings.Repeat(")", n))
	if err != nil {
		panic(err)
	}
	dlay := deep.Layout()
	fmt.Printf("\n== deep chain: %d nested nodes ==\n", n)
	fmt.Printf("layout: bitvector %d B + blocks %d B + supers %d B = %d B (%.2f bits/node, +%.1f%%)\n",
		dlay.BitvectorBytes, dlay.BlockBytes, dlay.SuperBytes, dlay.TotalBytes, dlay.BitsPerNode, dlay.OverheadPct)
	deep.ResetAccesses()
	c := deep.FindClosePos(0)
	fmt.Printf("FindClosePos(0) = %d (block accesses: %d, bits scanned: 0 of %d)\n", c, deep.Accesses(), 2*n)
	deep.ResetAccesses()
	d := deep.Depth(n - 1)
	fmt.Printf("Depth(deepest) = %d (block accesses: %d)\n", d, deep.Accesses())
	deep.ResetAccesses()
	steps := 0
	for v := n - 1; v != 0; v = deep.Parent(v) {
		steps++
	}
	fmt.Printf("iterative parent walk to root: %d steps, total block accesses: %d\n", steps, deep.Accesses())
}

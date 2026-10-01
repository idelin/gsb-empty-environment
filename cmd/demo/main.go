// Command demo showcases the succinct balanced-parentheses tree index:
// encoding layout, byte footprint, navigation queries, serialization with
// verification, deep-chain robustness, and per-query block-access stats.
package main

import (
	"fmt"
	"math/rand"
	"strings"

	"bptree/bp"
	"bptree/naive"
)

func main() {
	smallDemo()
	bigDemo()
	deepDemo()
}

func smallDemo() {
	fmt.Println("=== small tree ===")
	//       r
	//      / \
	//     a   b
	//        / \
	//       c   d
	word := "(()(()()))"
	ix, err := bp.Build(word)
	must(err)
	fmt.Printf("encoding: %s  (%d parens, %d nodes)\n", ix.String(), ix.Len(), ix.NumNodes())
	fmt.Println("positions: 0123456789  (node id = preorder rank of its '(')")
	fmt.Println()

	fmt.Println("node table:")
	fmt.Println(" id depth parent childrank firstchild nextsib prevsib subtreesize subtreerange parens")
	for v := 0; v < ix.NumNodes(); v++ {
		p, _ := ix.Parent(v)
		fc, _ := ix.FirstChild(v)
		ns, _ := ix.NextSibling(v)
		ps, _ := ix.PrevSibling(v)
		lo, hi := ix.SubtreeRange(v)
		o, c := ix.SubtreeParens(v)
		fmt.Printf(" %2d %5d %6d %9d %10d %7d %7d %11d   [%d,%d)     [%d,%d]\n",
			v, ix.Depth(v), p, ix.ChildRank(v), fc, ns, ps, ix.SubtreeSize(v), lo, hi, o, c)
	}
	fmt.Println()
	fmt.Printf("LCA(3,4)=%d (c,d -> b)   LCA(1,4)=%d (a,d -> r)   LCA(2,4)=%d (b,d -> b)\n",
		ix.LCA(3, 4), ix.LCA(1, 4), ix.LCA(2, 4))
	fmt.Printf("findclose(Open(2))=%d  enclose(Open(4))=%d  children(2)=%v\n",
		ix.FindClose(ix.Open(2)), ix.Enclose(ix.Open(4)), ix.Children(2))
	fmt.Println()

	bitsB, sbB, blkB, rmqB := ix.SizeBytes()
	fmt.Printf("layout: BlockSize=%d SuperblockSize=%d -> %d block(s), %d superblock(s)\n",
		bp.BlockSize, bp.SuperblockSize, ix.NumBlocks(), ix.NumSuperblocks())
	fmt.Printf("bytes: bits=%d superblocks=%d blocks=%d rmq=%d total=%d (raw payload %d B)\n",
		bitsB, sbB, blkB, rmqB, ix.TotalBytes(), ix.PayloadBytes())
	base, absMin := ix.BlockSummary(0)
	fmt.Printf("block 0 summary: baseExcess=%d absMinExcess=%d\n", base, absMin)
	fmt.Println()

	data := ix.MarshalBinary()
	ix2, err := bp.Load(data)
	must(err)
	fmt.Printf("serialized %d B -> Load verified (balance, min prefix excess, counts, summaries)\n",
		len(data))
	fmt.Printf("reloaded LCA(1,4)=%d, depth(4)=%d\n\n", ix2.LCA(1, 4), ix2.Depth(4))
}

func bigDemo() {
	fmt.Println("=== random tree, 1<<20 nodes ===")
	rng := rand.New(rand.NewSource(42))
	root := naive.Random(1<<20, rng)
	ix, err := bp.Build(naive.Encode(root))
	must(err)

	bitsB, sbB, blkB, rmqB := ix.SizeBytes()
	total := ix.TotalBytes()
	payload := ix.PayloadBytes()
	fmt.Printf("nodes=%d parens=%d  blocks=%d (%d B each)  superblocks=%d (%d B each)\n",
		ix.NumNodes(), ix.Len(), ix.NumBlocks(), 6, ix.NumSuperblocks(), 12)
	fmt.Printf("bytes: bits=%d superblocks=%d blocks=%d rmq=%d total=%d\n",
		bitsB, sbB, blkB, rmqB, total)
	fmt.Printf("raw payload (1 bit/paren)=%d B  ->  index overhead %.1f%%\n",
		payload, 100*float64(total-payload)/float64(payload))
	fmt.Printf("a pointer tree costs ~%d B (40+ B/node); a map-based index is not a compressed index\n",
		40*ix.NumNodes())
	fmt.Println()

	fmt.Println("sample superblock summaries (absExcess absMin absOpens):")
	for si := 0; si < 3 && si < ix.NumSuperblocks(); si++ {
		e, m, o := ix.SuperblockSummary(si)
		fmt.Printf("  sb %d: %d %d %d\n", si, e, m, o)
	}
	fmt.Println("sample block summaries (baseExcess absMinExcess):")
	for bi := 0; bi < 5 && bi < ix.NumBlocks(); bi++ {
		e, m := ix.BlockSummary(bi)
		fmt.Printf("  blk %d: %d %d\n", bi, e, m)
	}
	fmt.Println()

	// Per-query block-access statistics over random queries.
	st := ix.Stats()
	st.Reset()
	const q = 20000
	for i := 0; i < q; i++ {
		u := rng.Intn(ix.NumNodes())
		v := rng.Intn(ix.NumNodes())
		ix.Depth(u)
		ix.Parent(u)
		ix.FirstChild(u)
		ix.NextSibling(u)
		ix.PrevSibling(u)
		ix.ChildRank(u)
		ix.SubtreeSize(u)
		ix.LCA(u, v)
		ix.FindClose(ix.Open(u))
		ix.Enclose(ix.Open(v))
	}
	fmt.Printf("per-query block accesses over %d random queries per op\n", q)
	fmt.Printf("(a full scan would touch up to %d blocks per query)\n", ix.NumBlocks())
	fmt.Print(st.Report())

	data := ix.MarshalBinary()
	ix2, err := bp.Load(data)
	must(err)
	fmt.Printf("serialized %d B, reloaded and verified; spot check LCA(0,%d)=%d matches=%v\n\n",
		len(data), ix.NumNodes()-1, ix2.LCA(0, ix.NumNodes()-1),
		ix2.LCA(0, ix.NumNodes()-1) == ix.LCA(0, ix.NumNodes()-1))
}

func deepDemo() {
	fmt.Println("=== deep chain, no recursion ===")
	d := 1 << 20
	word := strings.Repeat("(", d) + strings.Repeat(")", d)
	ix, err := bp.Build(word)
	must(err)
	fmt.Printf("chain of %d nodes built; depth of deepest node = %d\n", d, ix.Depth(d-1))
	fmt.Printf("LCA(0, deepest)=%d  root subtree size=%d  bytes=%d\n",
		ix.LCA(0, d-1), ix.SubtreeSize(0), ix.TotalBytes())
	ix2, err := bp.Load(ix.MarshalBinary())
	must(err)
	fmt.Printf("serialized+verified reload OK, reloaded depth=%d\n", ix2.Depth(d-1))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

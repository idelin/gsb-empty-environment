// Command demo builds a block index, prints its real encoding layout and
// byte occupancy, and exercises membership, non-membership, rank/select and
// range proofs against an independent verifier.
package main

import (
	"encoding/binary"
	"fmt"

	"blockindex"
)

func main() {
	const (
		version  = 7
		n        = 200000
		blockCap = 64
		fanout   = 8
	)

	// Keys are spaced by 10 so every key+5 is a gap (non-member).
	entries := make([]blockindex.Entry, n)
	for i := range entries {
		var v [8]byte
		binary.BigEndian.PutUint64(v[:], uint64(i))
		entries[i] = blockindex.Entry{Key: uint64(1000 + 10*i), Value: v[:]}
	}
	idx, err := blockindex.Build(version, entries, blockCap, fanout)
	if err != nil {
		panic(err)
	}
	root := idx.Root

	fmt.Println("=== index construction ===")
	fmt.Printf("entries=%d blocks=%d (cap %d) fanout=%d height=%d\n",
		n, idx.NumBlocks(), blockCap, fanout, root.Height)
	blockBytes := idx.BlockBytes()
	summaryBytes := idx.SummaryBytes()
	fmt.Printf("block bytes=%d (%.2f bytes/entry, naive fixed-width would be 16.00)\n",
		blockBytes, float64(blockBytes)/n)
	fmt.Printf("summary bytes=%d (%.2f%% of block bytes) max block=%d bytes\n",
		summaryBytes, 100*float64(summaryBytes)/float64(blockBytes), idx.MaxBlockBytes())
	fmt.Printf("root: version=%d entries=%d blocks=%d digest=%x...\n",
		root.Version, root.TotalEntries, root.TotalBlocks, root.RootDigest[:8])

	maxProof := blockindex.MaxProofBytes(idx.NumBlocks(), fanout, idx.MaxBlockBytes())
	maxAccess := blockindex.MaxAccessStats(idx.NumBlocks(), fanout)
	fmt.Printf("point proof bound: <= %d bytes; verifier reads <= %d block(s), <= %d child summaries, 0 index accesses\n",
		maxProof, maxAccess.BlocksRead, maxAccess.ChildInfosRead)

	fmt.Println("\n=== membership proof (key=1000030) ===")
	const memberKey = 1000 + 10*3 // 4th entry
	p := idx.Prove(memberKey)
	res, err := blockindex.VerifyMembership(root, memberKey, p)
	must(err)
	st := blockindex.AccessStatsOf(p)
	fmt.Printf("member=%v rank=%d value=%x proof=%d bytes (bound %d) verifier reads: %d block, %d child summaries\n",
		res.Member, res.Rank, res.Value, p.Size(), maxProof, st.BlocksRead, st.ChildInfosRead)

	fmt.Println("\n=== non-membership proof (key=1000035, a gap) ===")
	np := idx.Prove(memberKey + 5)
	nres, err := blockindex.VerifyMembership(root, memberKey+5, np)
	must(err)
	fmt.Printf("member=%v proof=%d bytes (gap certified by committed neighbour ranges)\n",
		nres.Member, np.Size())

	fmt.Println("\n=== select by rank (rank=123456) ===")
	sp := idx.ProveSelect(123456)
	e, err := blockindex.VerifySelect(root, 123456, sp)
	must(err)
	fmt.Printf("entry key=%d value=%x proof=%d bytes\n", e.Key, e.Value, sp.Size())

	fmt.Println("\n=== range query certificate [1000000, 1000000+5000] ===")
	const lo = 1000000
	const hi = lo + 5000
	rp := idx.ProveRange(lo, hi)
	got, err := blockindex.VerifyRange(root, lo, hi, rp)
	must(err)
	fmt.Printf("entries returned=%d blocks accessed=%d proof=%d bytes (full index never loaded by verifier)\n",
		len(got), len(rp.Items), rp.Size())

	fmt.Println("\n=== tamper demonstration ===")
	bad := *p
	bad.Path = append([]blockindex.PathNode(nil), p.Path...)
	bad.Path[0].Children = append([]blockindex.ChildInfo(nil), p.Path[0].Children...)
	// Flip only a metadata count of a sibling child; content untouched.
	bad.Path[0].Children[0].Entries++
	if _, err := blockindex.VerifyMembership(root, memberKey, &bad); err != nil {
		fmt.Printf("metadata count tamper rejected: %v\n", err)
	} else {
		fmt.Println("ERROR: tampered proof accepted")
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

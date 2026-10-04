// Command demo builds an authenticated block index, prints its real encoding
// layout and byte occupancy, then generates and verifies membership,
// rank/select and range certificates, and finally shows a metadata tamper
// being localized to the smallest bad node.
package main

import (
	"fmt"

	"blkidx"
)

func main() {
	// 1. Build an index over 20,000 entries.
	var entries []blkidx.Entry
	for i := 0; i < 20000; i++ {
		entries = append(entries, blkidx.Entry{
			Key:   fmt.Sprintf("user:%08d", i*7+13),
			Value: []byte(fmt.Sprintf("payload-%08d", i)),
		})
	}
	ix, err := blkidx.Build(entries, blkidx.Config{Version: 1, LeafCap: 16, Fanout: 8})
	if err != nil {
		panic(err)
	}
	root := ix.RootHash()

	fmt.Println("=== index layout (real encoding, bytes) ===")
	st := ix.Layout()
	fmt.Printf("entries=%d  leafBlocks=%d  internalNodes=%d  depth=%d\n",
		ix.TotalEntries(), st.Blocks, st.InternalNodes, ix.Depth())
	fmt.Printf("leaf headers : %8d B  (%d blocks x %d B)\n", st.HeaderBytes, st.Blocks, blkidx.LeafHeaderSize)
	fmt.Printf("entry regions: %8d B  (prefix-compressed keys + values)\n", st.EntriesBytes)
	fmt.Printf("restart array: %8d B\n", st.RestartBytes)
	fmt.Printf("internal nodes:%8d B  (order/range/count-binding summaries)\n", st.InternalBytes)
	fmt.Printf("total        : %8d B  (raw keys+values = %d B)\n", st.TotalBytes, st.RawKeyBytes+st.RawValueBytes)
	fmt.Printf("root hash    : %x...\n\n", root[:8])

	// 2. Membership proof (present key).
	key := entries[12345].Key
	p := ix.ProveMembership(key)
	var stats blkidx.VerifyStats
	res, verr := blkidx.VerifyMembership(root, p, &stats)
	fmt.Println("=== membership proof ===")
	fmt.Printf("key=%s found=%v rank=%d value=%s\n", key, res.Found, res.Rank, res.Value)
	fmt.Printf("proof=%d B, verifier read %v leaves + %d internal nodes (err=%v)\n\n",
		p.Size(), len(stats.LeavesRead), len(stats.NodesRead), verr)

	// 3. Absence proof.
	absent := "user:00086429" // falls into a gap between committed keys
	pa := ix.ProveMembership(absent)
	stats.Reset()
	resA, verrA := blkidx.VerifyMembership(root, pa, &stats)
	fmt.Println("=== absence proof ===")
	fmt.Printf("key=%s found=%v rank=%d (neighbours straddle the gap, err=%v)\n\n",
		absent, resA.Found, resA.Rank, verrA)

	// 4. Select proof: entry at global position 7777.
	ps := ix.ProveSelect(7777)
	stats.Reset()
	resS, verrS := blkidx.VerifySelect(root, 7777, ps, &stats)
	fmt.Println("=== select proof (position + value bound) ===")
	fmt.Printf("select(7777) -> key=%s value=%s (err=%v)\n\n", ps.Key, resS.Value, verrS)

	// 5. Range query certificate with completeness.
	lo, hi := entries[5000].Key, entries[5199].Key
	pr := ix.ProveRange(lo, hi)
	stats.Reset()
	answer, verrR := blkidx.VerifyRange(root, pr, &stats)
	fmt.Println("=== range certificate [excludes omitted blocks] ===")
	fmt.Printf("range [%s .. %s] -> %d verified entries\n", lo, hi, len(answer))
	fmt.Printf("certificate=%d B over %d consecutive blocks, verifier read %d leaves + %d nodes (err=%v)\n\n",
		pr.Size(), len(pr.Blocks), len(stats.LeavesRead), len(stats.NodesRead), verrR)

	// 6. Tamper demo: flip one count in an internal summary node, regenerate
	// the proof, and watch verification fail at exactly that node.
	fmt.Println("=== tamper demo (metadata count only) ===")
	leafIdx := ix.LocateLeaf(key)
	parent, slot := ix.LeafParent(leafIdx)
	ix.CorruptChildCount(parent, (slot+1)%ix.NumChildren(parent), +1)
	bad := ix.ProveMembership(key)
	_, verrT := blkidx.VerifyMembership(root, bad, nil)
	fmt.Printf("tampered count in internal node %d -> %v\n", parent, verrT)
}

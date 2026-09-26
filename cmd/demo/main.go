// Demo of the blockstore package: block-backed persistent byte slices with
// copy-on-write edits, branch snapshots, and physical-block accounting.
package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"blockstore"
)

func main() {
	st := blockstore.NewStore(64, 1<<20)

	fmt.Println("== build a slice across physical blocks ==")
	base, _ := st.NewSlice([]byte(strings.Repeat("the quick brown fox. ", 200))) // 4200 bytes
	fmt.Printf("logical=%d bytes, blocks=%d, live=%d bytes, index=%d bytes (naive 8*N=%d)\n",
		base.Len(), st.Stats().Blocks, st.Stats().LiveBytes, base.IndexBytes(), 8*st.Stats().Blocks)

	fmt.Println("\n== branch snapshots diverge independently ==")
	branch := base.Snapshot() // zero-copy
	v1, _ := base.Overwrite(4, []byte("SLOW!"))
	v2, _ := branch.Overwrite(4, []byte("LAZY!"))
	fmt.Printf("base  : %.24s...\n", base.Bytes())
	fmt.Printf("v1    : %.24s...\n", v1.Bytes())
	fmt.Printf("v2    : %.24s...\n", v2.Bytes())
	fmt.Printf("snapshot added 0 blocks: live still %d bytes\n", st.Stats().LiveBytes)

	fmt.Println("\n== small edit copies almost nothing ==")
	big, _ := st.NewSlice(make([]byte, 512<<10)) // 512 KiB = 8192 blocks
	before := st.Stats()
	edited, _ := big.Overwrite(300_000, []byte{0xAA})
	delta := st.Stats().CopiedBytes - before.CopiedBytes
	shared := 0
	a, b := big.BlockIDs(), edited.BlockIDs()
	for i := range a {
		if a[i] == b[i] {
			shared++
		}
	}
	fmt.Printf("1-byte edit of %d bytes: copied=%d bytes, shared=%d/%d blocks\n",
		big.Len(), delta, shared, len(a))

	fmt.Println("\n== move with overlapping rvalue (pre-op read) ==")
	s, _ := st.NewSlice([]byte("ABCDEFGH"))
	mv, _ := s.Move(0, 2, 4)
	cp, _ := s.CopyWithin(2, 0, 4)
	fmt.Printf("move CDEF->front: %s   copy ABCD over CDEF: %s\n", mv.Bytes(), cp.Bytes())

	fmt.Println("\n== cross-block endian reads ==")
	buf := make([]byte, 64)
	binary.LittleEndian.PutUint32(buf[30:], 0xDEADBEEF) // straddles blocks
	num, _ := st.NewSlice(buf)
	v, _ := num.Uint32(30, binary.LittleEndian)
	fmt.Printf("uint32 LE @30 across blocks: 0x%08X\n", v)

	fmt.Println("\n== rollback on limit ==")
	tiny := blockstore.NewStore(16, 16*4) // room for 4 blocks
	ok, _ := tiny.NewSlice(make([]byte, 16*4))
	pre := tiny.Stats()
	_, err := ok.Overwrite(0, make([]byte, 16))
	fmt.Printf("overwrite needing a 5th block: err=%v (limit exceeded: %v)\n", err, errors.Is(err, blockstore.ErrLimitExceeded))
	post := tiny.Stats()
	fmt.Printf("ledger unchanged: live %d->%d bytes, temp=%d, content intact=%v\n",
		pre.LiveBytes, post.LiveBytes, post.TempBytes, ok.Len() == 64)

	fmt.Println("\n== ledger verification & reclamation ==")
	fmt.Printf("stats: %+v\n", st.Stats())
	if err := st.VerifyLedger(); err != nil {
		fmt.Println("ledger error:", err)
	} else {
		fmt.Println("independent traversal matches ledger: OK")
	}
	for _, sl := range []*blockstore.Slice{base, branch, v1, v2, big, edited, s, mv, cp, num} {
		sl.Release()
	}
	ok.Release()
	fmt.Printf("after releasing all slices: blocks=%d live=%d temp=%d\n",
		st.Stats().Blocks, st.Stats().LiveBytes, st.Stats().TempBytes)
}

// Command lpc-demo demonstrates length-limited prefix-code construction,
// the infeasibility rejection, why a truncated Huffman code is unsafe, and
// bounded decode quotas. Standard library only.
package main

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"strings"

	"lpc"
)

func main() {
	feasibleCase()
	infeasibleCase()
	lengthBindsCase()
	boundedDecodeCase()
}

func feasibleCase() {
	fmt.Println("== case 1: build a code under L = 4 ==")
	weights := []uint64{1, 2, 4, 8, 16}
	rep, err := lpc.Build(weights, 4)
	must(err)

	fmt.Printf("symbols=%d  L=%d\n", rep.Code.SymbolCount(), rep.Code.MaxLen)
	var b strings.Builder
	b.WriteString("lengths :")
	for _, l := range rep.Code.Lengths {
		fmt.Fprintf(&b, " %d", l)
	}
	b.WriteString("\nwords   :")
	for s := range rep.Code.Lengths {
		w, l, err := rep.Code.Word(s)
		must(err)
		fmt.Fprintf(&b, " %0*b", l, w)
	}
	b.WriteString("\n")
	fmt.Print(b.String())
	fmt.Printf("kraft   : %s (must be exactly 1)\n", rep.Code.Kraft.RatString())
	fmt.Printf("weighted length cost: %s/%s = %s bits/symbol\n",
		rep.CostNum, rep.CostDen, rep.Cost.Text('f', 6))

	hdr, err := rep.Code.Marshal()
	must(err)
	fmt.Printf("header  : %s  (%d bytes, magic=%q version=%d, CRC-32 last 4 bytes)\n",
		hex.EncodeToString(hdr), len(hdr), string(hdr[0:4]), hdr[4])
	fmt.Println()
}

func infeasibleCase() {
	fmt.Println("== case 2: reject when 2^L < number of symbols ==")
	weights := make([]uint64, 5)
	for i := range weights {
		weights[i] = uint64(i + 1)
	}
	_, err := lpc.Build(weights, 2) // 2^2 = 4 < 5
	fmt.Printf("Build(5 symbols, L=2) -> %v\n", err)
	fmt.Println()
}

func lengthBindsCase() {
	fmt.Println("== case 3: length limit binds; truncated Huffman would collide ==")
	weights := []uint64{100, 1, 1, 1}

	// Plain Huffman (large L) gives {1,3,3,2}.
	loose, err := lpc.Build(weights, 3)
	must(err)
	fmt.Printf("Huffman, L=3: lengths %v, cost %s bits/symbol\n",
		loose.Code.Lengths, loose.Cost.Text('f', 4))
	for s := range loose.Code.Lengths {
		w, l, _ := loose.Code.Word(s)
		fmt.Printf("  symbol %d -> %0*b (len %d)\n", s, l, w, l)
	}
	fmt.Println("  naive truncation to 2 bits reuses word 11 for two symbols: collision")

	// Exact length-limited optimum: {2,2,2,2}.
	bound, err := lpc.Build(weights, 2)
	must(err)
	fmt.Printf("package-merge, L=2: lengths %v, kraft %s, cost %s bits/symbol\n",
		bound.Code.Lengths, bound.Code.Kraft.RatString(), bound.Cost.Text('f', 4))
	for s := range bound.Code.Lengths {
		w, l, _ := bound.Code.Word(s)
		fmt.Printf("  symbol %d -> %0*b (len %d)\n", s, l, w, l)
	}

	// Feeding the truncated Huffman lengths to the header parser is
	// rejected: {1,2,2,2} has Kraft 5/4 and colliding code words.
	bad := makeHeader(2, []int{1, 2, 2, 2})
	if _, err := lpc.UnmarshalCode(bad); err != nil {
		fmt.Printf("truncated table {1,2,2,2} rejected by UnmarshalCode: %v\n", err)
	}
	fmt.Println()
}

func boundedDecodeCase() {
	fmt.Println("== case 4: bounded decode quota ==")
	rep, err := lpc.Build([]uint64{10, 20, 30, 40}, 2)
	must(err)
	msg := []int{0, 1, 2, 3, 0, 1}
	data, err := rep.Code.Encode(msg)
	must(err)
	fmt.Printf("encoded %d symbols into %d bytes (self-describing)\n", len(msg), len(data))

	got, err := rep.Code.Decode(data, 8)
	if err != nil {
		must(err)
	}
	fmt.Printf("Decode(data, quota=8) -> %v\n", got)

	_, err = rep.Code.Decode(data, 5)
	fmt.Printf("Decode(data, quota=5) -> %v\n", err)

	// Tamper with the frame checksum: corruption is detected.
	tampered := append([]byte(nil), data...)
	tampered[len(tampered)-1] ^= 0xff
	_, err = rep.Code.Decode(tampered, 8)
	fmt.Printf("Decode(tampered)      -> %v\n", err)
}

// makeHeader builds the documented v1 header for a length table. It lets
// the demo hand the parser an intentionally invalid (truncated) table.
func makeHeader(L int, lengths []int) []byte {
	buf := make([]byte, 11+len(lengths)+4)
	copy(buf[0:4], lpc.Magic)
	buf[4] = lpc.FormatVersion
	buf[5] = byte(L)
	binary.BigEndian.PutUint32(buf[7:11], uint32(len(lengths)))
	for i, l := range lengths {
		buf[11+i] = byte(l)
	}
	binary.BigEndian.PutUint32(buf[11+len(lengths):], crc32.ChecksumIEEE(buf[:11+len(lengths)]))
	return buf
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

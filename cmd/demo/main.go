// Command demo builds a compressed bitmap, prints its real serialized
// layout, and runs sample rank1/select1 queries with per-query cost
// metrics (bytes actually read, blocks actually decoded).
package main

import (
	"fmt"
	"math/rand"

	"bitmapidx/bitvec"
)

func main() {
	const n = 1 << 20 // 1 M bits
	rng := rand.New(rand.NewSource(2024))
	bits := make([]bool, n)
	for i := 0; i < 12000; i++ {
		bits[rng.Intn(n)] = true
	}

	buf := bitvec.Encode(bits)
	m, err := bitvec.Map(buf)
	if err != nil {
		panic(err)
	}

	l := m.Layout()
	fmt.Printf("bitmap: %d bits, %d ones\n", m.BitLen(), m.TotalOnes())
	fmt.Printf("serialized layout (bytes):\n")
	fmt.Printf("  header       %6d\n", l.Header)
	fmt.Printf("  directory    %6d  (%d blocks x 8 B)\n", l.Directory, (m.BitLen()+bitvec.BlockBits-1)/bitvec.BlockBits)
	fmt.Printf("  super index  %6d  (1 u64 per %d blocks)\n", l.SuperIndex, bitvec.BlocksPerSuper)
	fmt.Printf("  select index %6d  (1 sample per %d ones)\n", l.SelectIndex, bitvec.SelectRate)
	fmt.Printf("  block data   %6d\n", l.Data)
	fmt.Printf("  total        %6d  (raw bits would need %d)\n\n", l.Total, n/8)

	var mx bitvec.Metrics
	for _, i := range []uint64{0, 12345, 500000, m.BitLen()} {
		mx.Reset()
		r := m.Rank1(i, &mx)
		fmt.Printf("rank1(%7d) = %5d   [cost: %3d B, %d block]\n", i, r, mx.BytesRead, mx.BlocksTouched)
	}
	for _, k := range []uint64{0, 1, 500, 4000, m.TotalOnes() - 1, m.TotalOnes()} {
		mx.Reset()
		pos, ok := m.Select1(k, &mx)
		fmt.Printf("select1(%5d) = (%7d, %5v) [cost: %3d B, %d block]\n", k, pos, ok, mx.BytesRead, mx.BlocksTouched)
	}
}

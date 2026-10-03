// acdemo 演示：对 90% 为 0 的二元符号流做算术编码，
// 对比原始大小、霍夫曼理论下限与香农熵极限。
package main

import (
	"fmt"
	"math"
	"math/rand"

	"arithmetic"
)

func main() {
	const n = 100000
	r := rand.New(rand.NewSource(2026))
	data := make([]byte, n)
	zeros := 0
	for i := range data {
		if r.Float64() < 0.9 {
			data[i] = 0
			zeros++
		} else {
			data[i] = 1
		}
	}

	// 由数据统计构建概率模型（解码端持有同一模型）。
	freqs := []uint64{1, 1}
	for _, s := range data {
		freqs[s]++
	}
	model := arithmetic.NewModel(freqs)

	enc := arithmetic.Encode(data, model)
	dec := arithmetic.Decode(enc, model)

	p0 := float64(zeros) / float64(n)
	entropy := -(p0*math.Log2(p0) + (1-p0)*math.Log2(1-p0))

	fmt.Printf("符号数:            %d (0 占比 %.1f%%)\n", n, p0*100)
	fmt.Printf("原始大小:          %d 字节 (1 字节/符号)\n", n)
	fmt.Printf("霍夫曼理论下限:    %d 字节 (1 bit/符号)\n", n/8)
	fmt.Printf("香农熵极限:        %.0f 字节 (%.4f bit/符号)\n", entropy*n/8, entropy)
	fmt.Printf("算术编码输出:      %d 字节 (%.4f bit/符号)\n", len(enc), float64(len(enc)-8)*8/n)
	fmt.Printf("相对霍夫曼下限:    %.1f%%\n", float64(len(enc))*8/float64(n)*100)

	match := len(dec) == n
	for i := range dec {
		if dec[i] != data[i] {
			match = false
			break
		}
	}
	fmt.Printf("解压验证:          %v (完美终止, 逐符号一致)\n", match)
}

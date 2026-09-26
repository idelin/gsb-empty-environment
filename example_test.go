package wavelet_test

import (
	"fmt"

	"wavelet"
)

func Example() {
	data := []int{7, -2, 7, 3, -2, 9, 0}
	tr := wavelet.New(data)

	fmt.Println("len:", tr.Len())
	fmt.Println("access(3):", tr.Access(3))
	fmt.Println("access(0):", tr.Access(0))

	kth, err := tr.KthSmallest(1, 6, 2) // 子数组 [-2 7 3 -2 9] 第 3 小(0 起)
	if err != nil {
		panic(err)
	}
	fmt.Println("kth(1,6,2):", kth)

	fmt.Println("freq(0,7,7):", tr.Freq(0, 7, 7))
	fmt.Println("rangeFreq(0,7,-2,3):", tr.RangeFreq(0, 7, -2, 3))

	_, err = tr.KthSmallest(4, 4, 0)
	fmt.Println("empty range err:", err)

	fmt.Println("bytes:", tr.Bytes())
	fmt.Println("layout:", tr.Layout())

	// Output:
	// len: 7
	// access(3): 3
	// access(0): 7
	// kth(1,6,2): 3
	// freq(0,7,7): 2
	// rangeFreq(0,7,-2,3): 4
	// empty range err: wavelet: empty range or k out of bounds
	// bytes: 192
	// layout: 每层一个 bitVector：比特打包进 []uint64（64 bit/字），每 512 bit 一个 uint64 超块前缀 popcount；秩->原值用升序去重 []int 映射，原序秩序列 []int 保留原始顺序
}

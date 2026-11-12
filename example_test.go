package netsec_test

import (
	"fmt"
	"math/big"

	netsec "example.org/netsec/q0452"
)

// 演示宿主如何构造模型、滤波一个含丢失掩码的序列，并搜索达到阈值的
// 最短观察序列。全部计算使用 *big.Rat，判定无浮点误差。
func Example() {
	compiled, err := netsec.NewModel(&netsec.Model{
		Prior: []*big.Rat{big.NewRat(1, 2), big.NewRat(1, 2)},
		Transitions: [][]*big.Rat{
			{big.NewRat(3, 5), big.NewRat(2, 5)},
			{big.NewRat(1, 4), big.NewRat(3, 4)},
		},
		Emissions: []map[int]*big.Rat{
			{10: big.NewRat(3, 4), 30: big.NewRat(1, 4)},
			{10: big.NewRat(1, 4), 30: big.NewRat(3, 4)},
		},
		BlockSize: 1,
	})
	if err != nil {
		panic(err)
	}

	res, err := netsec.Filter(compiled, []netsec.Observation{
		{Length: 10, Lost: false},
		{Lost: true}, // 显式丢失：对全部发射长度边缘化
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(res.Possible, res.MAP, res.MAPPosterior.RatString())

	shortest, err := netsec.Shortest(compiled, big.NewRat(2, 3))
	if err != nil {
		panic(err)
	}
	fmt.Println(shortest.Depth, shortest.Observations[0].Length,
		shortest.Result.MAPPosterior.RatString())
	// Output:
	// true 0 41/80
	// 1 10 3/4
}

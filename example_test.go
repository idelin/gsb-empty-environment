package cutstock_test

import (
	"fmt"

	"cutstock"
)

func ExampleSolve() {
	stocks := []cutstock.Stock{
		{Length: 12, Price: 8},
		{Length: 7, Price: 4},
	}
	orders := []cutstock.Order{
		{Length: 5, Qty: 2}, // 订单#0
		{Length: 4, Qty: 2}, // 订单#1
	}
	sol, err := cutstock.Solve(stocks, orders, 2)
	if err != nil {
		panic(err)
	}
	if err := cutstock.Verify(stocks, orders, 2, sol); err != nil {
		panic(err)
	}
	fmt.Print(sol.Layout())
	// Output:
	// 共 2 根母材, 采购成本 16, 总余料 2, 总锯缝损耗 4
	// 第1根: 母材#0 长=12 价=8 | 订单#1长4 订单#1长4 | 刀数=1 余料=2
	// 第2根: 母材#0 长=12 价=8 | 订单#0长5 订单#0长5 | 刀数=1 余料=0
}

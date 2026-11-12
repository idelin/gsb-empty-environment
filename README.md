# 精确二维布尔区域

面向 Go 矢量管线的二维布尔区域包。坐标全程使用 `math/big.Rat`，输入为
闭合点环（最后一点自动连回第一点），支持非零（nonzero）和奇偶
（even-odd）填充规则。支持交、并、差，输出逆时针外环、顺时针孔，以及
每条输出边对应的原操作数、环和线段索引。

## 用法

```go
package main

import (
	"fmt"

	taskpkg "example.org/domainreissue/q046"
)

func main() {
	a := taskpkg.NewRegion(taskpkg.Nonzero, taskpkg.RingFromInts(
		[2]int{0, 0}, [2]int{4, 0}, [2]int{4, 3}, [2]int{0, 3},
	))
	b := taskpkg.NewRegion(taskpkg.Nonzero, taskpkg.RingFromInts(
		[2]int{2, 1}, [2]int{6, 1}, [2]int{6, 4}, [2]int{2, 4},
	))

	union := taskpkg.Union(a, b)
	fmt.Println(union.Area())          // 20
	fmt.Println(len(union.Outers))     // 1
	fmt.Println(len(union.Holes))      // 0
	fmt.Println(union.Outers[0].Edges[0].Sources[0].Operand)
}
```

主要入口：

- `NewRegion(rule, rings...)` / `RingFromInts(...)` / `MakeRing(...)`
- `Union`, `Intersection`, `Difference`（也有 `...E` 错误返回形式）
- `BuildArrangement`：查看分裂后的原子边、正反重数和来源
- `Result.Outers`、`Result.Holes`、`Result.Components`
- `Contour.Edges[i].Sources`：原边来源，`Direction` 为相对输出方向

## 精确性

- 所有交点、分裂参数、侧别采样点和环绕数均为有理数精确计算。
- 共线重叠先在所有端点处分裂，再按无向原子边合并重数与方向。
- 交点坐标使用规范有理数字符串索引，不同交点不会因浮点 epsilon 被合并。
- 点接触和仅共边接触不会产生面积；输出环按左侧为内部的规则装配。

## 验证

`go test ./...` 包含小整数网格逐单元参考、交并交换律、面积恒等式
`|A∪B|+|A∩B|=|A|+|B|`、`|A\B|+|A∩B|=|A|`，以及共线重叠、点/边接触、
偶奇与非零规则和邻近有理交点的回归测试。

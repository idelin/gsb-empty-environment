# hvsched：分层虚拟完成时间权重诊断

模块根包 `hvsched` 提供可导入的 Go API；`diagnose` 子包提供同名诊断入口的类型别名转发。

- 虚时标签全部使用 `math/big.Rat`，比较和排序不经过 `float64`。
- 输入支持候选整数区间、`Count × Length` 压缩到达记录、真实叶发射序列和可选的精确部分完成/虚完成标签。
- 求解器用有理分支表达式、区间端点极值和层总和传播裁剪候选盒，不逐个代入 `2^32` 候选值。
- 结果支持精确计数、字典序第 k 个解、遍历和最短矛盾观测前缀。
- 测试包含小区间全枚举、独立正向重放对拍、空树重新激活、层总和计数、层级调度和仅平局可解释轨迹。

## 调度模型

每个非根节点有正整数权重。调度器对活跃子节点计算：

```text
start  = max(parent_virtual_time, local_last_finish)
finish = start + packet_length / child_weight
```

调度器选择 `finish` 最小的子节点；`finish` 完全相等时选择 `Tie` 租户编号更小的节点。子树无包时不更新其本地完成标签；重新活跃时继续使用 `max(...)` 规则。

默认 `ClockFinishTag` 把父虚钟推进到被选标签的完成时间；`ClockStartTag` 可切换为 SFQ 风格的开始标签虚钟。两种模式都参与全枚举对拍。

## 示例

```go
package main

import (
    "fmt"

    hvsched "prescreen.local/ps-8f572c36d0"
)

func main() {
    p := hvsched.Problem{
        Vars: []hvsched.VarSpec{
            {Name: "wa", Min: 1, Max: 1 << 32, Layer: "l"},
            {Name: "wb", Min: 1, Max: 1 << 32, Layer: "l"},
        },
        LayerSums: map[string]uint64{"l": 6_000_000_000},
        Nodes: []hvsched.NodeSpec{
            {ID: "root"},
            {ID: "a", Parent: "root", Tie: 1, WeightVar: "wa", Layer: "l"},
            {ID: "b", Parent: "root", Tie: 2, WeightVar: "wb", Layer: "l"},
        },
        Arrivals: []hvsched.ArrivalSpec{
            {Leaf: "a", Before: 0, Count: 1, Length: 1},
            {Leaf: "b", Before: 0, Count: 1, Length: 1},
        },
        Observations: []hvsched.Observation{
            {Leaf: "a"}, // 可另设 PartialCompletion: big.NewRat(3, 2)
        },
    }

    set, err := hvsched.Solve(p)
    if err != nil {
        panic(err)
    }

    fmt.Println(set.CountString()) // 精确解数
    first, _ := set.KthInt(1)       // 第一个字典序解，一基编号
    fmt.Println(first)

    if set.Count().Sign() == 0 {
        fmt.Println(set.BadPrefixLength(), set.BadPrefix())
    }
}
```

`ArrivalSpec.Before` 是零基发射序号：`0` 表示首次调度前入队，`1` 表示第一次发射后、第二次调度前入队。

## 计数与第 k 个解

`Solve` 返回若干互不相交的可行整数盒 `Region`。盒内所有满足层总和的向量都已被有理区间判定证明可复现观测。

- `SolutionSet.Count()`：`*big.Int` 精确总数。
- `SolutionSet.Kth(k)`：一基字典序第 k 个解。
- `SolutionSet.Nth(rank)`：零基字典序反查。
- `SolutionSet.Iterate(...)`：按字典序遍历。
- 有层总和时，盒内计数使用有界整数分拆的容斥公式，不展开候选值。

无解时：

- `BadPrefixLength()` 是最短矛盾前缀长度。
- `ContradictionIndex()` 是导致排除全部候选的零基观测下标。
- `BadPrefix()` 返回该前缀副本。
- `hvsched.ContradictionPrefix(p)` 可一次获得长度、前缀和结果集。

## 正向重放

```go
res, err := hvsched.Replay(p, hvsched.WeightVector{2, 4})
// 或不构造占位 Observation：
res, err = hvsched.ReplayCount(p, weights, 10)
ok, err := hvsched.Verify(p, weights)
```

`Replay` 使用独立的具体有理数状态和压缩包队列；求解器在区间缩成单点时也会调用同一重放路径核验。

## 验证

```sh
go test -count=1 ./...
```

测试覆盖：

- 两叶和四节点三层调度的小区间全候选枚举。
- 对每个真实权重生成轨迹，再比较求解器解集合、计数和每个字典序第 k 解。
- 所有短观测序列的最短矛盾前缀。
- 空树后重新活跃以及 `max(parentTime, lastFinish)` 的本地/父钟两种主导情形。
- 相等虚时标签必须依靠租户编号解释的轨迹。
- `2^32` 边界和大区间层总和下的快速计数与 kth 端点。

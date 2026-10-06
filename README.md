# intervalmotion

Go 库：两组一维闭区间的端点以给定（整数）速度线性移动，在有理时间范围
`[0, T]` 内，精确求出每对区间相交的时间闭区间集合。

## 模型

- 每个区间有两个端点，端点 `k` 在时刻 `t` 的位置为 `Pk + Vk*t`（位置、速度
  均为有理数，提供整数构造器）。
- 端点允许交叉：时刻 `t` 的区间取两端点的 `[min, max]`。
- 全部运算使用 `math/big.Rat` 精确有理数，无浮点误差。
- 瞬时接触（仅某一时刻相切）保留为退化闭区间 `[t, t]`。

## 方法

四条端点直线的相对次序只会在两两交叉时刻改变。把 `0`、`T` 与全部
`(0, T)` 内的交叉时刻排序后，每个开段内相交条件退化为两条符号恒定的
线性不等式，因此：

1. 在每个临界时刻直接判定（闭区间端点、瞬时接触）；
2. 在每个开段中点采样判定，可行则整段（闭段）可行；
3. 相邻可行闭段合并为不相交的闭区间列表。

`VerifyIntersectionTimes` 用精确有理数采样做交叉验证：每个结果区间的
端点与内部采样点必须相交，每个间隙的采样点必须不相交。

## 用法

```go
a := intervalmotion.NewMovingInterval(0, 0, 1, 0)   // 静止区间 [0, 1]
b := intervalmotion.NewMovingInterval(5, -1, 6, -1) // 区间 [5,6] 以速度 -1 移动
T := big.NewRat(10, 1)

times := intervalmotion.IntersectionTimes(a, b, T) // [[4, 6]]
err := intervalmotion.VerifyIntersectionTimes(a, b, T, times, 4)

// 两组区间逐对计算：
results := intervalmotion.AllPairsIntersectionTimes(groupA, groupB, T)
```

## 测试

```sh
GO111MODULE=on go test ./...
```

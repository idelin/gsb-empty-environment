# 安全覆盖路径频次去偏

## estimate 包

`estimate` 子包按测试运行（run）汇总路径签名的去偏加权频次：

- 每次运行内签名先去重排序，同一签名在一次运行中只计一次；
- 入样概率 `p` 以 `*big.Rat` 传入，Horvitz–Thompson 权重取精确有理值 `1/p`；
- 每个签名维护精确的加权计数 `S1 = Σ 1/p` 与平方和 `S2 = Σ (1/p)²`（置信区间用）；
- `Add` 按运行 ID 幂等：内容相同为空操作，内容不同返回包装 `ErrRunConflict` 的错误；
- `Retract` 按运行 ID 精确撤回贡献，撤回结果与用剩余运行重算一致；
- `ConfidenceInterval(sig, z)` 返回 `Estimate ± z·sqrt(S2)` 的 `*big.Float` 近似区间。

```go
agg := estimate.New()
err := agg.Add(estimate.Run{
    ID:          "run-42",
    Probability: big.NewRat(1, 10), // 入样概率 p
    Signatures:  []estimate.Signature{"edge/a->b", "edge/b->c", "edge/a->b"},
})
// 去重后两个签名各得权重 10
point := agg.Estimate("edge/a->b")    // *big.Rat，精确值 10
square := agg.SumSquared("edge/a->b") // *big.Rat，精确值 100
lo, hi, err := agg.ConfidenceInterval("edge/a->b", big.NewFloat(1.96))

err = agg.Retract("run-42") // 精确撤回
```

验证见 `estimate/estimate_test.go`：对完整入样空间 `2^R` 枚举，按各结果
发生概率加权核对期望，并在每步撤回后与全新重算结果逐签名比对。

覆盖采样对长执行记录赋予更多权重，会误导安全测试调度。写供Go测试服务调用的估计包，每次运行只对出现过的路径签名计一次，输入含抽样概率的有理权重；维护可撤回的精确加权计数和置信区间所需平方和。重复运行ID幂等，冲突拒绝。用完整采样空间枚举验证估计期望及撤回后与重算一致。

# cutting

一维下料（cutting stock）优化库：母材有多种整数长度与单价，锯路损耗
`kerf`/刀，订单零件总数不超过 12 件。目标先**最低采购成本**，再**最少废料**。

规则：

- 每根母材上的最后一件不需要收尾刀，故 `n` 件零件只切 `n-1` 刀；
- 同长度零件可属于不同订单，按 `Part.Order` 区分；
- 每根约束：`sum(零件长) + kerf*(件数-1) <= 母材长`，余料即废料。

算法：枚举全部 `2^n` 子集，为每个子集选最便宜的可用母材（同价取最短），
再做子集 DP（限定子集含最低位以避免重复划分）求全局最优。

## 用法

```go
sol, err := cutting.Optimize(stocks, kerf, parts) // 求解
err = cutting.Verify(sol, stocks, kerf, parts)    // 子集分组枚举核对刀数与剩料
fmt.Print(sol)                                    // 逐根布局
```

运行示例与测试：

```sh
go run ./cmd/cutdemo
go test ./...
```

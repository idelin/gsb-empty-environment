# 敏感分布距离保护

包 `netsec` 供 Go 发布宿主核验：每个分组的敏感属性分布是否足够接近总体分布。

- 敏感属性取自**有序有限域**（`Domain`），相邻取值的地面距离为 1。
- 差异用**一维搬运距离**（Wasserstein-1 / EMD）度量，等于规范化 CDF 差的前缀和绝对值之和。
- 概率、边流量、距离、阈值全部使用 `math/big` **有理数**，阈值判定不含浮点误差；仅当距离严格大于阈值才判定超标。
- `AddRecord` / `WithdrawRecord` 在同一操作内同时更新分组与总体计数；撤回不足时报错且状态不变。
- 审计报告给出超标的组、精确距离，以及逐边界的规范化搬运证据（`EdgeFlow`）。
- 零样本组没有可定义的分布，单独标记为 `StatusZeroSample`，不参与距离比较。

## 快速使用

```go
domain, err := netsec.NewDomain([]string{"low", "mid", "high"})
if err != nil { /* ... */ }
v := netsec.NewVerifier(domain)

_ = v.AddRecord("group-a", "low")
_ = v.AddRecord("group-a", "high")
_ = v.AddRecord("group-b", "mid")
// 撤回：分组与总体同时减一
if err := v.WithdrawRecord("group-a", "low"); err != nil { /* ... */ }

threshold := big.NewRat(1, 2)
reports, err := v.Audit(threshold)
for _, r := range netsec.Exceeding(reports) {
    // r.Distance 为精确 *big.Rat；r.Transport 为各边界的搬运边流量，
    // 各边 |Flow| 之和恰好等于 r.Distance。
    _ = r
}
```

## 验证

- 全量重算：在增删记录后，用仅含存活记录的全新核验器重算并逐组比对状态与距离。
- 小分布穷举：枚举域大小 1–4、总量不超过 5 的全部计数向量，与独立的逐边界贪心搬运参考实现交叉验证，并覆盖零向量（零样本）与端点向量（距离恰为 `k-1`）。
- 核验器层穷举两个分组、三个取值、每组总量不超过 3 的全部计数表，含撤回后重算。
- `go test -race ./...` 通过。

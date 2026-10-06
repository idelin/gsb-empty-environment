# tracebatch

批次追溯库：按有理比例（`math/big.Rat`，精确无舍入）记录批次的混合、
分装与报废，并沿实际来源贡献传播污染标记。

## 模型

- 每个批次携带**符号源向量** `comp: origin -> mass`，记录其当前存量中
  每个原始批次的确切有理质量。
- 混合 `Mix` / 分装 `Split` 按比例从源批次抽取质量，源向量随之精确缩放；
  抽取量为零被拒绝（`ErrNonPositiveQuantity`），零贡献不建边、不传播。
- 报废 `Scrap` 使批次终止（不可再取用），其质量计入按源分类的报废账户，
  质量守恒仍可核对。
- 图为 DAG：边只指向新建批次，另有显式环守卫（`reaches`）与
  `ValidateDAG`（Kahn 算法）双重保证。

## 查询与核对

- `AffectedProportions(source)`：按创建序（拓扑序）做动态规划折叠，
  每个下游批的受影响比例只计算一次——共享路径天然去重。
- `VerifyTrace(source)`：独立的符号核对模型，显式枚举所有 source→batch
  路径并求路径权（`amt/创建量`）之积的和，与折叠结果逐批比对，
  证明共享前缀既不丢失也不重复计数。
- `CheckConservation()`：对每个原始批核对
  `注册量 == 各存活批持有量之和 + 报废量`。
- `Composition(id)`：返回批次的符号源向量副本。

## 示例

```go
l := tracebatch.NewLedger()
l.Register("S", big.NewRat(100, 1))   // 污染源
l.Register("W", big.NewRat(900, 1))   // 清洁原料
l.Mix("B", []tracebatch.Contribution{
    {Source: "S", Amount: big.NewRat(25, 1)},
    {Source: "W", Amount: big.NewRat(75, 1)},
})
l.Split("B", []tracebatch.SplitPart{
    {ID: "X", Amount: big.NewRat(40, 1)},
    {ID: "Y", Amount: big.NewRat(60, 1)},
})
aff, _ := l.AffectedProportions("S") // B、X、Y 各 1/4
```

测试：`go test ./...`（本环境需 `GOCACHE=/private/tmp/gocache GO111MODULE=off`）。

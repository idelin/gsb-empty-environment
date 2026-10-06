# traceability

Go 批次追溯库：按有理比例混合、分装、报废，污染标记沿实际来源贡献传播。

## 模型

- 每个批次携带**符号源向量** `map[sourceID]*big.Rat`：该批质量中来自各原始源的精确有理比例，行和为 1。
- `Mix`：输出向量 = 各输入按抽取量加权的向量和；`Split`：子批继承父批向量；`Discard`：报废质量保留源归因，计入守恒。
- 零抽取量不产生边、不传播污染；图按"输出必须为新 ID + 可达性检查"双重保证无环。
- 共享路径天然去重：贡献按边聚合而非按路径计数。

## 用法

```go
l := trace.NewLedger()
l.AddSource("S1", big.NewRat(100, 1))
l.AddSource("S2", big.NewRat(200, 1))
l.Mix("A", trace.Portion{Batch: "S1", Amount: big.NewRat(30, 1)},
             trace.Portion{Batch: "S2", Amount: big.NewRat(60, 1)})
l.Split("B", "A", big.NewRat(30, 1))
l.Discard("B", big.NewRat(5, 1))

aff := l.AffectedBy("S1")      // 每下游批受影响比例（精确有理数）
err := l.VerifyConservation("S1")   // 质量守恒：存活 + 报废 == 初始
err = l.VerifyAgainstPaths("S1")    // 路径枚举交叉核对：重复路径去重
```

## 核对

`VerifyAgainstPaths` 独立于向量模型，显式枚举源到每批的所有路径，按
"抽取比 × 存活因子"求和（存活因子 = 兄弟流/报废抽走后剩余份额），
与向量模型对该批"曾进入总质量"的归因逐一比对，可捕获共享路径重复
计数与质量泄漏。

## 测试

```sh
go test ./...
```

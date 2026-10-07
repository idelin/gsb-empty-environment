# intervalidx

版本化区间索引：每个版本保存一组互不交的半开区间 `[Start, End)` 及其值。

- **分裂更新**：`Set`/`Delete` 在半开范围边界处分裂旧段，覆盖或删除重叠部分。
- **相邻合并**：端点相接且值相等的段自动合并。
- **不可变快照**：每次非空更新追加新版本，旧版本仍可 `Get`/`Segments` 查询。
- **空范围无版本**：`Start >= End` 时不产生新版本，返回当前版本号。
- **有理端点**：端点为 `*math/big.Rat`，支持任意有理数边界。

```go
ix := intervalidx.New()
v1 := ix.Set(big.NewRat(0, 1), big.NewRat(10, 1), "A") // [0,10)=A
v2 := ix.Delete(big.NewRat(3, 1), big.NewRat(5, 1))    // [0,3)=A [5,10)=A
val, ok := ix.Get(v1, big.NewRat(4, 1))                // 旧快照: ("A", true)
val, ok = ix.Get(v2, big.NewRat(4, 1))                 // 新版本: (nil, false)
```

测试：`go test ./...`
- 单元测试覆盖：整域覆盖、部分删除分裂、分裂后回填合并、空范围、有理端点。
- `TestModelIntegerPointSnapshots`：随机操作序列下，以逐整数点快照模型
  核对全部历史版本的逐点查询结果，并校验有序、互不交、相邻不同值的结构不变量。

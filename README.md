# contab

`contab` 按两个分类维度的整数边际计数构造列联表与记录，禁用组合（结构零）保持为零；
不可行时返回可独立核验的证据。总量为百级记录的场景（默认测试覆盖 100 条）轻松适用。

## 用法

```go
spec := contab.Spec{
    RowCounts: []int{30, 45, 25},   // 维度 A 各类别目标计数
    ColCounts: []int{40, 35, 25},   // 维度 B 各类别目标计数
    Forbidden: []contab.Cell{{Row: 0, Col: 2}}, // 禁用组合
}

// 只列联表：
table, ev, err := contab.Solve(spec)

// 或直接生成 100 条记录（打乱只影响顺序，不影响配额）：
recs, ev, err := contab.Generate(spec, true, rand.New(rand.NewSource(1)))
```

- `Solve` 用最大流建模运输问题；不可行时返回 `*Evidence`：给出一个行子集，
  其合计需求超过其可用列的合计供给（Gale–Hoffman 型证书），或指出边际总量不一致。
- `Generate` 展开表格为记录；`shuffle=true` 时仅随机重排顺序，各单元格配额不变，
  可用 `Quotas` 回收计数并用 `Verify` 做行列守恒校验。
- 畸形输入（负计数、越界/重复禁用格）返回 `error`；合法但不可行的输入返回 `Evidence`。

## 测试

```
go test ./...
```

覆盖：行列守恒、结构零、不可行证据的独立复核、2×2–3×3 小实例与暴力枚举对拍、
打乱前后配额一致、总量 100 的随机实例。

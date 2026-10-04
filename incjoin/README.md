# incjoin

多重集（允许重复行）语义下两表等值连接的增量维护库。

## 语义

- 表为多重集：每行带正整数重数，插入/删除按重数计数。
- 连接结果重数为两侧重数之积：`(k,l,v)` 在 L 中重数 a、`(k,v',w)` 在 R 中重数 b，
  则结果行 `(k,l,w)` 重数贡献为 a*b。
- 一个批次可同时修改两表，结果差量严格按
  `Δ(L⋈R) = ΔL⋈R + L⋈ΔR + ΔL⋈ΔR` 计算，含交叉增量 `ΔL⋈ΔR`。
- 批次原子：任一操作会使某行重数变负（或 Count<=0），整批拒绝，状态不变。
- 净变化为零的项（如同批先删后加同一行）不出现在差量中。

## 用法

```go
e := incjoin.New()
delta, err := e.Apply([]incjoin.Op{
    {Side: incjoin.Left,  Kind: incjoin.Insert, Row: incjoin.Row{Key: "k", Val: "a"}, Count: 2},
    {Side: incjoin.Right, Kind: incjoin.Insert, Row: incjoin.Row{Key: "k", Val: "x"}, Count: 3},
})
// delta[{k a x}] == 6
```

- `Apply(ops)`：原子应用批次，返回结果差量 `map[OutRow]int64`（仅非零项）。
- `Result()`：当前增量维护的连接结果。
- `FullJoin()`：从表状态全量重算，用于对照验证。

## 测试

```sh
go test ./...
```

覆盖：重复键重数乘积、删除至零、同批先删后加、负重数整批拒绝、
同批双表修改的交叉增量，以及 500 个随机批次与全量连接的对拍。

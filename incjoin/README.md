# incjoin

增量等值连接库：两张允许重复行的表（bag 语义），按重数（multiplicity）
维护连接结果。一个批次可同时修改两表，交叉增量 `dA x dB` 被精确计入一次。

## 模型

- 表 = `map[key]multiplicity`，重数恒为非负。
- 连接结果 = `key -> multA * multB`。
- 批次增量公式：`dR = dA·B_old + A_old·dB + dA·dB`。
- 同批操作按顺序生效（先删后加合法；删除量不得使重数为负）。
- `N <= 0` 的操作、以及会导致负重数的删除，整批拒绝（原子，不留副作用）。

## 用法

```go
e := incjoin.New()
delta, err := e.Apply(incjoin.Batch{
    {Side: incjoin.A, Key: "k", N: 3},               // A 插入 3 个 "k"
    {Side: incjoin.B, Key: "k", N: 2},               // B 插入 2 个 "k"
    {Side: incjoin.A, Key: "k", N: 1, Delete: true}, // A 删除 1 个 "k"
})
// delta["k"] == 2*2 - 0 = 4（净 dA=+2，交叉项已含）
res := e.Result() // 当前全量结果
```

`incjoin.FullJoin(a, b)` 为全量对照实现，测试中用它验证增量结果。

## 测试

```sh
go test ./...
```

覆盖：重复键重数、删除至零、同批先删后加、批内交叉增量、
非法输入拒绝（非正计数 / 负重数）、500 步随机批次对照全量连接。

# mvsg — 多版本序列化图分析库

根据事务历史中**每次读取实际观察到的版本**和**已提交事务的写集**构建多版本序列化图（MVSG），检测并解释序列化异常。

## 设计要点

- **版本精确的依赖**：同键访问不一律视为冲突。读-读永不冲突；读与写只在版本链上相邻时才产生边。
- **三种依赖边**：
  - `WR`（写→读）：某事务读到了另一事务写出的版本；
  - `RW`（读→写反依赖）：某事务读到的版本被另一事务以**紧随其后的下一个版本**覆盖（不会指向所有更晚的写者）；
  - `WW`（写→写）：同一键相邻两个已提交版本之间的写次序。
- **只纳入已提交事务**：未提交事务既不产生版本，也不参与任何边；读到未提交/未知版本会记入 `Warnings` 并忽略该读。
- **环 + 证据输出**：用 Johnson 算法枚举所有基本环，每条环边附带产生该边的具体操作（事务、操作序号、键、版本、值）。
- **有限串行执行枚举核对**：对环中事务枚举全部串行排列（上限 9 个事务），逐一模拟执行；若没有任何串行次序能复现观测到的读取与版本次序，则该环被标记为 `Verified`。读取环外事务产生的版本时不施加约束（外部写者可插入任意位置），此时环可能保持 `unverified`。

## 用法

```go
h := &mvsg.History{Txns: []*mvsg.Transaction{
    {ID: "T1", Committed: true, Ops: []mvsg.Operation{
        mvsg.ReadInitial("y", "10"),
        mvsg.Write("x", "10"),
    }},
    {ID: "T2", Committed: true, Ops: []mvsg.Operation{
        mvsg.ReadInitial("x", "10"),
        mvsg.Write("y", "10"),
    }},
}}
rep := mvsg.Analyze(h)
fmt.Println(rep.Serializable()) // false
fmt.Println(rep)                // 环及每条边的操作证据
```

约定：

- `History.Txns` 中已提交事务按**提交顺序**排列，该顺序定义每个键的版本次序（初始版本在最前）。
- 读操作必须标注实际观察到的版本：`mvsg.Read(key, 版本写者, 值)` 或 `mvsg.ReadInitial(key, 值)`。
- 写操作的版本自动推导为 `键@所属事务`；同一事务对同一键多次写取最后一次。

## API 概览

- `Analyze(h *History) *Report`：建图、枚举环、串行核对，一站式入口。
- `BuildGraph(h *History) *Graph`：只构建依赖图（`Deps`、`Between`、`HasEdge`、`Warnings`）。
- `Report.Serializable() bool`：图无环即为可串行。
- `Report.Cycles`：每个环含 `Txns`、`Edges`（带 `Dependency` 证据）、`Verified`、`PermutationsChecked`。

## 测试

```sh
go test ./...
```

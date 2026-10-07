# sercheck

Go 可串行化分析库：根据**每次读取的具体版本**和各事务的**提交写集**构建依赖图，
区分三种依赖边，输出序列化环及操作级证据，并用**有限串行执行枚举**交叉核对。

## 核心原则

- **版本精确，而非键相等**：冲突只来自具体的版本关系。两个事务访问同一个键
  不必然构成冲突——例如 T3 读到的是 T2 写的最新版本，则 T1 更早的同键写与 T3
  之间没有任何边。
- **未提交事务不参与分析**：其操作不产生任何节点或边；已提交事务若读到了
  未提交版本（脏读），记为 `Anomaly`，且串行枚举必然无法复现。
- **双方法核对**：依赖图无环 ⟺ 存在复现所有读取的串行执行。`Report.Consistent`
  标记两种结论是否一致（不一致通常意味着盲写导致的 view-serializable 但非
  conflict-serializable 历史，或实现 bug）。

## 依赖边

| 边 | 含义 | 产生条件 |
|----|------|----------|
| `WR` | 写-读 | T_j 读到的版本正是 T_i 写的 |
| `WW` | 写-写 | T_i、T_j 的版本在键的版本链上相邻（按提交序排列） |
| `RW` | 读-写反依赖 | T_j 读到版本 v，版本链上 v 之后第一个非自身版本由 T_i 写入 |

每个键的版本链以初始版本（`InitialTxn`）为首，之后按事务提交顺序排列。

## 用法

```go
h := sercheck.NewHistory()
h.SetInitial("x", "0")

h.Read(1, "x", "0", sercheck.InitialTxn) // T1 读到 x 的初始版本
h.Write(1, "y", "1")
h.Read(2, "y", "0", sercheck.InitialTxn) // T2 读到 y 的初始版本
h.Write(2, "x", "2")
h.Commit(1)
h.Commit(2) // 提交顺序决定版本链顺序

rep := sercheck.Check(h, sercheck.DefaultEnumLimit)
fmt.Print(rep)
// serializable: false
// cycle: T1 -[RW x]->  T2 -[RW y]-> T1
//   evidence ...: T1:R(x)="0"<-T0
//   ...
// serial witness: none
// graph/enumeration consistent: true
```

- `History.Graph()`：构建依赖图，边携带产生它的读写操作作为证据（`Edge.Reads` /
  `Edge.Writes`）。
- `Graph.Cycles()`：枚举所有基本环（Tarjan SCC + 环枚举），每个 `Cycle` 的
  `Hops` 含逐跳的边与操作证据。
- `History.EnumerateSerial(limit)`：枚举已提交事务的全部排列，逐步模拟并剪枝，
  返回第一个能复现所有已记录读取（版本与值都匹配）的串行序。
- `Check(h, limit)`：一次给出图判定、环、串行见证与一致性标记。

## 运行测试

```sh
go test ./...
```

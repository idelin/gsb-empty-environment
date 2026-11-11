# NAT策略符号可达性

网络审计需要知道经过地址转换后某个隔离区是否能访问管理服务。给Go审计服务写可导入的符号执行包，输入有限有向网络图，每个节点含有序过滤与源或目标区间平移规则；路径先过滤再转换，转换前后均检查整数溢出。求满足入口与出口约束的包及经过路径，循环用状态区间不动点处理。用缩小地址域全枚举对照并返回真实可重放见证。

## 包结构

`netsec` 是可直接 `import` 的 Go 库（模块 `example.org/netsec/uid1290`），无第三方依赖。

| 文件 | 职责 |
| --- | --- |
| `types.go` | 地址区间、过滤/平移规则、节点、边、图与约束的输入类型与校验 |
| `rect.go` | 符号状态 `Rect = 源区间 × 目标区间` 的相交、相减、相邻合并与规范化 |
| `semantics.go` | 具体包与符号矩形两套语义：有序过滤 → 有序平移，双重整数溢出检查 |
| `engine.go` | 工作列表混沌迭代与矩形包含不动点，记录循环归并事件与逐节点计数 |
| `domain.go` | 缩小地址域：规则/约束边界播种，再按平移 delta 闭包，超预算报错 |
| `enumerator.go` | 缩小域上的 BFS 全枚举，记录入口原始包、路径与溢出/过滤计数 |
| `witness.go` | 逐跳追踪与独立重放校验的真实见证 |
| `analysis.go` | 顶层 `Analyze` 编排、符号/枚举双向集合对照、见证物化 |

## 语义约定

- 每个节点先跑**有序、首匹配**过滤规则；未命中默认放行，命中即按 `Allow` 终判。
- 再跑**有序、首匹配**平移规则；未命中保持原样穿透，支持目标字段匹配加可选守卫字段。
- 平移 `Delta` 为有符号 `int64`：先做 int64 中间值前置检查，再做 uint32 结果后置检查；任一失败即按溢出丢弃该具体包或符号子区域。
- 符号态是闭区间笛卡尔积，节点处理时按规则边界精确切片，不存在宽度损失。

## 循环与终止

引擎对到达同一节点且已被累计状态覆盖的矩形做包含归并（`LoopEvent` 留痕），不再重复传播。所有规则边界把 `uint32 × uint32` 划分为有限分区，因此不动点必然收敛；`MaxFixpointIterations` 仅作为防御性上限。

## 缩小域与对照

`BuildNarrowedDomain` 以全部规则/约束的边界（小区间取全量内部点）播种，并对每条适用的平移规则做 delta 闭包，保证包永远不会被平移到域之外。枚举与符号结果在每个节点做**双向集合相等**对照，差异通过 `AnalysisReport.Mismatches` 返回；无差异时对照通过。

## 见证

`Witness` 携带入口原始包、节点路径和每跳 `HopTrace`。`Witness.Replay` 只依赖具体语义从头重放并逐跳比对，是可独立复核的真实 uint32 见证，而不是抽象结论。

## 快速使用

```go
report, err := netsec.Analyze(graph, netsec.AnalysisConfig{
    Entry:   1,
    Exit:    3,
    Ingress: []netsec.Constraint{ /* 入口区间约束 */ },
    Egress:  []netsec.Constraint{ /* 出口区间约束 */ },
})
if err != nil { /* 域超预算等 */ }
fmt.Println(report.Reachable(), len(report.Mismatches))
final, err := report.Witnesses[0].Replay(graph)
```

完整可运行示例见 `example_test.go` 的 `ExampleAnalyze`。

## 测试

- `go test ./...`：单元测试、DMZ→SNAT→管理区端到端、溢出与过滤、域闭包、伪造符号结论的对照检测。
- `go test -race ./...`：竞态检测。
- `TestRandomDifferential`：200 组随机小图上的逐点符号/枚举等价性证明（差分测试）。

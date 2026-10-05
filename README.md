# dom — 控制流图支配关系分析

针对带一个或多个入口的控制流图（CFG）的支配关系库。

## 功能

- `Graph`：有向 CFG，支持多入口（等价于虚拟超级根指向每个入口）；
  重复边自动忽略，允许自回边。
- `Graph.Analyze()`：全量支配数据流分析。
- `Analysis.Dominates(d, n)`：查询 d 是否支配 n。
- `Analysis.IDom` / `Analysis.Dominators` / `Analysis.Tree()`：
  直接支配点、支配点集合与支配树（多入口时为森林，根含多入口汇合点）。
- `Analysis.Unreachable()`：不可达节点单独列出，不参与支配关系。
- `Analysis.AddEdge` / `Analysis.RemoveEdge`：单边更新后只重算受影响
  区域（新边：新图中从目标点可达的节点；删边：旧图中从目标点可达的
  节点），区域外节点的支配信息作为固定边界保留；返回受影响节点与
  可达性变化。
- `Analysis.Evidence(d, n)`：返回 n 到 d 的直接支配链作为支配证据。
- `Graph.Verify(a)` / `Graph.VerifyEvidence(chain)`：独立核对方法——
  从图中删除候选支配点后检查可达性，与数据流结果交叉验证。

## 示例

```go
g := dom.NewGraph()
g.AddEntry(0)
g.AddEdge(0, 1)
g.AddEdge(1, 2)
g.AddEdge(2, 1) // 循环回边

a := g.Analyze()
a.Dominates(1, 2)      // true
id, _ := a.IDom(2)     // 1
fmt.Print(a.Tree())    // 支配树

res := a.AddEdge(0, 2) // 增量重算受影响区域
a = res.Analysis
_ = g.Verify(a)        // 独立方法核对

chain, _ := a.Evidence(0, 2)
_ = g.VerifyEvidence(chain) // 验证支配路径证据
```

## 测试

```sh
go test ./...
```

测试覆盖循环、自回边、多入口模拟超级根、不可达节点、重复边，
并将每次增量更新后的结果与全量重算及删除点可达性核对方法比对。

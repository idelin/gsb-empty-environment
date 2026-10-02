# dynclique — 动态图极大团枚举与团追踪

纯 Go 标准库实现的无向图极大团（maximal clique）发现组件：

- **静态枚举**：Bron–Kerbosch 算法 + Tomita 主元剪枝（`MaximalCliques`）。
- **动态维护**：`Graph` 在增删边时增量维护极大团集合——
  - 加边 `(u,v)`：新诞生的极大团形如 `{u,v} ∪ M`，其中 `M` 是 `u,v`
    公共邻居诱导子图中的极大团；被新团包含的旧团被吸除。
  - 删边 `(u,v)`：同时含 `u,v` 的极大团消解，候选 `K\{u}`、`K\{v}`
    若不被其他现存极大团包含则晋升为新的极大团。
- **查询**：`CliquesContaining(nodes...)` 返回包含指定节点集合的全部极大团。

## 目录结构

- `bronkerbosch.go` — 带主元剪枝的 Bron–Kerbosch 枚举器
- `graph.go` — 动态图与极大团的增量维护、查询 API
- `graph_test.go` — 单元测试 + 随机动态操作 vs 静态重算的一致性校验
- `cmd/social/main.go` — 社交网络紧密圈子发现示例

## 运行

```sh
go test ./...        # 运行测试
go run ./cmd/social  # 运行社交网络示例
```

## 示例输出（节选）

```
== Tight circles (maximal cliques) ==
  {Alice, Carol, Dave, Erin}  (size 4)
  {Alice, Frank}  (size 2)
  {Bob, Carol, Dave, Erin}  (size 4)
  {Bob, Frank}  (size 2)

== Bob meets Alice: one edge completes a 5-person circle ==
+ friendship Alice -- Bob
New maximal clique born; the two absorbed 4-cliques are gone:
  {Alice, Bob, Carol, Dave, Erin}  (size 5)
  {Alice, Bob, Frank}  (size 3)

== Bob and Alice fall out: the circle degrades ==
- friendship Alice -- Bob
  {Alice, Carol, Dave, Erin}  (size 4)
  {Alice, Frank}  (size 2)
  {Bob, Carol, Dave, Erin}  (size 4)
  {Bob, Frank}  (size 2)
```

## 测试结果

```
=== RUN   TestBronKerboschKnownGraph
--- PASS: TestBronKerboschKnownGraph (0.00s)
=== RUN   TestBronKerboschAgainstBruteForce
--- PASS: TestBronKerboschAgainstBruteForce (0.02s)
=== RUN   TestAddEdgeBirthsFiveClique
--- PASS: TestAddEdgeBirthsFiveClique (0.00s)
=== RUN   TestRemoveEdgeDegrades
--- PASS: TestRemoveEdgeDegrades (0.00s)
=== RUN   TestRemoveEdgeCandidateAbsorbed
--- PASS: TestRemoveEdgeCandidateAbsorbed (0.00s)
=== RUN   TestCliquesContaining
--- PASS: TestCliquesContaining (0.00s)
=== RUN   TestDynamicRandomConsistency
--- PASS: TestDynamicRandomConsistency (0.02s)
=== RUN   TestDuplicateAndMissingEdgeOps
--- PASS: TestDuplicateAndMissingEdgeOps (0.00s)
PASS
ok  	dynclique	0.146s
```

测试覆盖：

- 已知图的标准答案校验；
- 200 组随机图与暴力枚举真值对比（主元剪枝正确性）；
- 验收场景：加一条边促成 5 节点完全子图，新极大团诞生、被包含的
  4 团被剔除；删边后正确退化为两个 4 团、继续退化；
- 删边候选被现存极大团吸收时不重复产生；
- 100 组随机增删边操作后，动态维护结果与静态重算完全一致。

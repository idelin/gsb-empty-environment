# clique — 动态图极大团枚举与团追踪

无向图极大团（maximal clique）发现与列举，仅用 Go 标准库。

## 特性

- **Bron–Kerbosch + 主元剪枝**：选择使 `|P ∩ N(u)|` 最大的主元 `u ∈ P ∪ X`，仅对 `P \ N(u)` 递归。
- **动态增删边**：每次边变更只在 `N[u] ∪ N[v]` 闭邻域内局部重算，与未受影响的
  全局团集合做包含关系合并，返回本次**诞生**与**消解**的极大团。
- **按节点集合过滤**：`CliquesContaining(nodes...)` 输出包含指定节点集合的全部极大团。

## API

```go
g := clique.New()
g.AddNode(1)
born, died := g.AddEdge(1, 2)    // 返回诞生/消解的极大团
born, died  = g.RemoveEdge(1, 2)
all := g.MaximalCliques()        // 当前全部极大团
sub := g.CliquesContaining(1, 2) // 包含 {1,2} 的极大团清单
chk := g.Recompute()             // 全量重算（用于校验增量状态）
```

## 运行

```sh
GO111MODULE=on go test -v ./...   # 测试
GO111MODULE=on go run ./cmd/demo  # 样例：社交网络紧密圈子发现
```

## 样例输出

```
== 初始社交网络的极大团（紧密圈子） ==
   [1 2 3 4]  [1 2 6]  [2 3 4 5]  [6 7]

== 包含节点集合 {1,2} 的极大团清单 ==
   [1 2 3 4]  [1 2 6]

>>> 1 和 5 成为好友：加入边 (1,5)，促成 5 节点完全子图
   诞生的极大团: [1 2 3 4 5]
   消解的极大团: [1 2 3 4]  [2 3 4 5]

>>> 2 和 4 断交：删除边 (2,4)，5 团退化
   诞生的极大团: [1 2 3 5]  [1 3 4 5]
   消解的极大团: [1 2 3 4 5]
```

## 测试结果

```
--- PASS: TestKnownGraph                     # Wikipedia 经典样例图
--- PASS: TestAddEdgeBirthsFiveClique        # 加边促成 5 团，子团被吞并
--- PASS: TestRemoveEdgeDegenerates          # 删边后 5 团正确退化
--- PASS: TestCliquesContaining              # 按节点集合过滤
--- PASS: TestIsolatedNodesAndNoops          # 孤立节点与空操作
--- PASS: TestIncrementalMatchesBruteForce   # 200 步随机增删 vs 暴力枚举
ok  	clique
```

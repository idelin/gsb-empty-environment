# proofdag

把有向无环推导树压缩为共享图（hash-consing DAG）的 Go 证明库。

## 设计

- **节点身份**：由 `(规则名, 结论, 有序前提列表)` 共同决定。仅结论相同但前提
  不同（或前提顺序不同）的节点是不同节点，不会被合并；结构完全相同的节点
  自动共享。见 `proof/graph.go` 的 `Builder.Add`。
- **构建即无环**：`Builder` 要求前提先于结论加入，构造出的图天然是 DAG。
  `NewGraph` 可绕过该保证（用于反序列化/测试），正确性由验证兜底。
- **拓扑验证**：`Verify`（`proof/verify.go`）用 Kahn 算法按拓扑序检查——
  排不完则有环；每个节点的规则必须在公开有限规则集 `RuleSet` 中登记，
  前提个数匹配，且结论由规则从有序前提的结论合法推出。
- **公开有限规则**：`proof/rule.go` 内置 `lit`/`add`/`mul`/`neg` 四条规则
  组成的极小整数演算，可替换为任意实现 `Rule` 接口的有限规则集。

## 验证方式（测试）

- **展开后树比较**：`Unroll` 把共享图展开为推导树，与独立构造的树做结构
  比较（`TestUnrolledTreeMatchesIndependentTree`）；共享图 4 节点展开为
  7 节点树（`TestSharingCompressesDAG`）。
- **环注入**：用 `NewGraph` 注入互指/自环的节点，验证必须拒绝
  （`TestVerifyRejectsInjectedCycle`、`TestVerifyRejectsSelfLoop`）。
- 另覆盖：错误结论、未登记规则、悬挂前提、同结论不同前提不合并、
  前提顺序参与身份。

## 运行

```sh
GOCACHE=/private/tmp/gocache go test ./...
```

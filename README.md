# bplusinspect — B+ 树完整性校验器与全树不变量巡检仪 (Go)

数据库自愈与一致性离线巡检工具。自顶向下遍历整棵 B+ 树，验证全部结构不变量，
并对违规输出带路径的精确定位报告。仅使用 Go 标准库。

## 布局

- `bplustree/bplustree.go` — B+ 树实现（插入/删除/分裂/借还/合并，叶子双向链表）。
  内部节点采用栅栏键语义：`keys[i] == children[i]` 子树的最大键。
- `bplustree/checker.go` — 巡检仪 `Inspect(*Tree) *Report`。
- `bplustree/checker_test.go` — `go test` 测试（含并发写删压力与故障注入）。
- `cmd/inspect/main.go` — 可运行演示：并发压测 → 巡检 → 注入故障 → 再巡检。

## 巡检的不变量

- 每个节点内键严格递增（`KEYS_NOT_SORTED`）
- 路由键与左右子树一致：`keys[i]` 必须等于左子树最大键、小于右子树最小键
  （`ROUTE_KEY_GT_LEFT_MAX` / `ROUTE_KEY_LT_LEFT_MAX` / `ROUTE_KEY_GE_RIGHT_MIN`）
- 子树键范围不重叠、不越界（`KEY_BELOW_RANGE` / `KEY_ABOVE_RANGE`，祖先界限逐层收紧）
- 节点填充率上下限（`NODE_UNDERFULL` / `NODE_OVERFULL`）、扇出与键数匹配（`ARITY_MISMATCH`）
- 父指针回指一致（`PARENT_PTR_MISMATCH`）、无共享/成环节点（`NODE_CYCLE`）
- 所有叶子处于同一深度（`LEAF_DEPTH_SKEW`）
- 叶子链表：从最左叶出发无环（`LEAF_CHAIN_CYCLE`）、`Prev` 双向完全闭环
  （`LEAF_PREV_MISMATCH`）、链上键序连续（`LEAF_CHAIN_DISORDER`）、
  链覆盖且仅覆盖树内叶子（`LEAF_MISSING_FROM_CHAIN` / `LEAF_NOT_IN_TREE`）

每条违规携带 `Path`（如 `root/child[3]/child[0]`）与含具体键值的 `Detail`。

## 运行

```sh
go test ./...      # 测试
go run ./cmd/inspect
```

## 运行样例（go run ./cmd/inspect）

```
== phase 1: 8 goroutines x 5000 random insert/delete ==
tree settled: 2742 live keys

== phase 2: full invariant sweep on healthy tree ==
inspection: nodes=695 leaves=568 keys=3309 depth=4 violations=0
  OK: all invariants hold

== phase 3: inject 'routing key > left subtree max' fault ==
fault injected at an internal node near the root

== phase 4: re-inspect corrupted tree ==
inspection: nodes=695 leaves=568 keys=3309 depth=4 violations=18
  [ROUTE_KEY_GT_LEFT_MAX] root: routing key keys[0]=2856 exceeds max key 1856 of left subtree child[0]
  [ROUTE_KEY_GE_RIGHT_MIN] root: routing key keys[0]=2856 >= min key 1857 of right subtree child[1] (ranges overlap)
  [KEY_BELOW_RANGE] root/child[1]: key 2032 <= lower bound 2856
  ...
```

## 测试结果（go test ./... -v）

```
=== RUN   TestInspectHealthyAfterConcurrentWorkload   --- PASS  (8 goroutine 并发写删后巡检通过)
=== RUN   TestDetectsRouteKeyGreaterThanLeftMax       --- PASS  (注入故障被捕获并定位)
=== RUN   TestDetectsUnsortedKeys                     --- PASS
=== RUN   TestDetectsLeafCycle                        --- PASS
=== RUN   TestDetectsBrokenPrevPointer                --- PASS
=== RUN   TestDetectsDetachedLeaf                     --- PASS
=== RUN   TestDeleteEverythingKeepsTreeValid          --- PASS
=== RUN   TestRandomizedInsertDeleteInvariant         --- PASS
PASS
ok      bplusinspect/bplustree
```

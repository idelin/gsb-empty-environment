# B+ 树删除：叶子节点兄弟借调（Go 实现）

仅使用 Go 标准库实现的 B+ 树，重点覆盖**删除导致下溢时的重平衡**：
当叶子节点记录数低于最小装填因子 `M/2` 时，优先从左兄弟借调其最大一条 Key
（其次从右兄弟借调最小一条 Key），并精确修正父节点的路由分界 Key；
仅当左右兄弟都处于最小装填时才退化为合并（Merge）。

## 结构

- `tree.go` — B+ 树实现：`Insert` / `Delete` / `Get`，叶子借调 `rebalanceLeaf`，
  祖先路由 Key 修正 `updateAncestors`，内部节点借调/合并 `rebalanceInternal`
- `tree_test.go` — `go test` 测试（含验收场景与随机化不变量校验）
- `cmd/demo/main.go` — 运行样例

## 关键逻辑（`tree.go` `rebalanceLeaf`）

1. 左兄弟富余（`len(left.keys) > M/2`）：将其最大 Key 前置到本叶子，
   父节点分界 Key `parent.keys[idx-1]` 更新为借来的 Key；
2. 否则右兄弟富余：将其最小 Key 追加到本叶子，
   父节点分界 Key `parent.keys[idx]` 更新为右兄弟的新最小 Key；
3. 否则与兄弟合并，并向上递归修复内部节点（必要时树高减一）。

## 运行样例

```
$ go run ./cmd/demo
=== After inserting 1..14 (order M=4, min fill M/2=2) ===
L0: [4 7 10 13]
L1: [1 2 3] [4 5 6] [7 8 9] [10 11 12] [13 14]
height=2 size=14 keys=[1 2 3 4 5 6 7 8 9 10 11 12 13 14]

=== Delete 13: leaf [14] underflows (1 < 2), borrow max key 12 from left sibling ===
L0: [4 7 10 12]
L1: [1 2 3] [4 5 6] [7 8 9] [10 11] [12 14]
height=2 size=13 keys=[1 2 3 4 5 6 7 8 9 10 11 12 14]

=== Delete 1, 4, 5: leaf [6] underflows, left sibling at min, borrow min key 7 from right sibling ===
L0: [6 8 10 12]
L1: [2 3] [6 7] [8 9] [10 11] [12 14]
height=2 size=10 keys=[2 3 6 7 8 9 10 11 12 14]
```

删除 `13` 后叶子 `[14]` 下溢（1 < M/2=2），从左兄弟 `[10 11 12]` 偷取其最大
Key `12`，父节点分界 Key 由 `13` 精确修正为 `12`，树高保持 2 不变。

## 测试结果

```
$ go test ./... -v
=== RUN   TestDeleteBorrowFromLeftSibling
--- PASS: TestDeleteBorrowFromLeftSibling (0.00s)
=== RUN   TestDeleteBorrowFromRightSibling
--- PASS: TestDeleteBorrowFromRightSibling (0.00s)
=== RUN   TestDeleteMergeFallback
--- PASS: TestDeleteMergeFallback (0.00s)
=== RUN   TestDeleteMissingKey
--- PASS: TestDeleteMissingKey (0.00s)
=== RUN   TestRandomized
=== RUN   TestRandomized/order=3
=== RUN   TestRandomized/order=4
=== RUN   TestRandomized/order=5
=== RUN   TestRandomized/order=8
--- PASS: TestRandomized (0.76s)
PASS
ok  	bplustree	1.179s
```

- `TestDeleteBorrowFromLeftSibling` — 验收场景：删 `13` 触发下溢，从左兄弟
  借调最大 Key `12`，断言树高不变、根分界 Key 精确变为 `[4 7 10 12]`、
  叶子变为 `[12 14]`、左兄弟变为 `[10 11]`；
- `TestDeleteBorrowFromRightSibling` — 镜像场景：左兄弟无富余时从右兄弟借调；
- `TestDeleteMergeFallback` — 兄弟均无富余时退化为合并，根收缩、树高减一；
- `TestDeleteMissingKey` / `TestRandomized` — 缺失 Key 删除为 no-op；
  多阶数下 5000 步随机插入/删除与 `map` 对照，并逐步校验 B+ 树不变量
  （Key 有序、分界 Key 等于右子树最小 Key、装填因子界、叶子同层）。

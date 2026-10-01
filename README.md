# dynidom — 动态图支配树维护与反例生成器

纯 Go 标准库实现。控制流图支配关系计算器：从入口节点计算所有可达节点的
直接支配者（Immediate Dominator），支持添加/删除单条控制流边后的增量局部
更新，并可对不支配关系输出反例证明路径。

## 算法

- 支配树计算采用 Cooper–Harvey–Kennedy 迭代数据流算法（逆后序 + intersect
  手指算法）。
- 单条边增删后做增量局部更新：可达性重新计算（一条边可能连通/断开整个子图），
  idom 不动点迭代以旧支配树热启动，未受影响区域立即收敛，只有受影响节点被
  重新处理。
- 反例生成：对“node 是否支配 target”的否定查询，从入口做避开 node 的 BFS，
  返回一条真实存在的 entry → target 见证路径；支配成立时返回 `ErrNoPath`。

## 结构

- `dominator/dominator.go` — `Graph` 类型：`AddEdge` / `RemoveEdge` /
  `Idom` / `Dominates` / `Dominators` / `Counterexample`
- `dominator/dominator_test.go` — 验收测试（go test）
- `cmd/demo/main.go` — 运行样例

## 运行

```sh
go test ./...     # 运行全部测试
go run ./cmd/demo # 运行验收场景样例
```

## 验收场景输出

```
== diamond S->A, S->B, A->T, B->T ==
  idom(T) = S
== after adding bypass edge S->T ==
  idom(T) = S
== after removing edge B->T from the diamond ==
  idom(T) = A
counterexample: A does not dominate T, witness path: S -> B -> T
```

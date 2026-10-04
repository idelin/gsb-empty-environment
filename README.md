# dagagg — 有向无环组织图的层级汇总库

## 问题

组织图是 DAG：同一个叶（员工/指标）可归属多个上级。要求：

- 同一上级对同一叶**只计一次**（多条路径不重复计）；
- 不同上级**各自独立**汇总（共享叶分别计入各上级）；
- 支持叶值修改、归属边增删，输出**差量与来源叶集合**；
- **禁止**把子节点汇总值直接相加——共享叶会被重复计算。

## 设计

每个节点维护两个增量状态：

- `leaves`：从该节点出发可到达的**去重叶集合**（叶节点包含自身）；
- `agg`：汇总值 = `leaves` 中各叶取值之和。

因为按“叶集合”而非“子节点汇总值”归并，共享叶天然只计一次。

变更传播：

- `AddEdge(p, c)`：先做成环检测，再把 `leaves(c) - leaves(p)` 的差集沿父边
  递归向上传播；上级已含的叶（存在其他路径）直接跳过。
- `RemoveEdge(p, c)`：先断边，再找出 `p` 的其余孩子都不再可达的叶集合并向上
  传播；上级只要还有任一孩子可达该叶就不受影响。
- `SetLeafValue(l, v)`：差值 `d` 沿父边 BFS 传播到全部祖先（每个祖先只更新一次）。

所有变更方法返回 `[]Delta`，每项包含受影响节点、有符号差量、以及来源叶集合
（`Added` / `Removed` / `Changed`）。

## API

```go
g := dagagg.NewGraph()
g.AddNode("root"); g.AddNode("A"); g.AddNode("B")
g.AddLeaf("L", 10)

g.AddEdge("root", "A"); g.AddEdge("root", "B")
g.AddEdge("A", "L")
deltas, _ := g.AddEdge("B", "L")   // 只有 B 出现 +10 的 Delta；root 不变

agg, _   := g.Aggregate("root")    // 10 —— 共享叶只计一次，而非 10+10
leaves, _ := g.SourceLeaves("root") // [L]

deltas, _ = g.SetLeafValue("L", 14) // L/A/B/root 各 +4，Changed=[L]
deltas, _ = g.RemoveEdge("A", "L")  // A -10；root 仍经 B 到达 L，不变
```

## 复杂度

设变更影响的叶差集大小为 `k`，受影响祖先数为 `a`：

- `AddEdge`：O(k · a)；
- `RemoveEdge`：O(k · a · 出度)（逐上级检查其余孩子是否仍可达）；
- `SetLeafValue`：O(a)；
- 空间：每节点 O(其可到达叶数)。

## 测试

```sh
go test ./...
```

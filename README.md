# hieragg — DAG 层级汇总库

在有向无环组织图（DAG）上做层级汇总：同一叶节点可归属多个上级，
**每个上级对该叶只计一次，不同上级各自独立计数**。

## 核心设计

不能将直接子节点的汇总值相加——共享叶会被重复计算。因此每个节点维护：

- `leafSet`：可达叶节点的**去重集合**（即来源叶集合）
- `sum`：该集合中叶值之和

节点的汇总值 = 其来源叶集合（传递闭包去重）的值之和。

## 增量操作

| 操作 | 传播方式 | 复杂度直觉 |
|---|---|---|
| `SetLeafValue` | 标量差量沿所有祖先累加 | O(祖先数) |
| `AddEdge(p,c)` | 只把 `leafSet(c) - leafSet(p)` 的**差集**向上传播，已含共享叶自动去重 | O(实际新增叶 × 祖先) |
| `RemoveEdge(p,c)` | 对 p 及其祖先按拓扑序（子先父后）重算 `leafSet = ⋃ 子节点 leafSet` | O(受影响子图) |

每次操作返回 `Update`：

```go
type Update struct {
    Deltas  map[string]float64  // 各受影响节点的汇总差量
    Added   map[string][]string // 各节点新进入来源叶集合的叶
    Removed map[string][]string // 各节点离开来源叶集合的叶
}
```

## 示例

```go
g := hieragg.New()
g.AddLeaf("l1", 10)
g.AddNode("a"); g.AddNode("b"); g.AddNode("root")
g.AddEdge("a", "l1")
g.AddEdge("b", "l1")
g.AddEdge("root", "a")
g.AddEdge("root", "b")   // l1 共享：root 仍只计一次

g.Sum("a")    // 10
g.Sum("b")    // 10（不同上级分别计算）
g.Sum("root") // 10（不是 20）

u, _ := g.SetLeafValue("l1", 25)
u.Deltas // map[l1:15 a:15 b:15 root:15]

u, _ = g.RemoveEdge("a", "l1")
u.Deltas  // map[a:-10]（root 经 b 仍可达 l1，不受影响）
u.Removed // map[a:[l1]]
```

`AddEdge` 会拒绝成环（`child` 已能到达 `parent` 时报错）；叶节点不能有子节点；
重复加边幂等。

## 测试

```sh
go test ./...
```

覆盖：菱形共享去重、改值传播、加边差集传播、删边后共享叶保活/级联重算、
环检测、幂等与错误处理、增量结果与全量重算一致性。

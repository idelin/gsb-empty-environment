# mincostflow

Go 实现的最小费用流求解器，支持：

- 每条边整数流量上下界 `[Lower, Upper]`，每个节点整数供需 `Balance`（>0 为供应，<0 为需求）
- 线性费用，费用用 `math/big.Int` 表示，支持超出 64 位的精确运算
- 无可行流时返回**割证据**（Hoffman 环流条件）：节点集合 `S` 满足
  `balance(S) > upper(S, V\S) - lower(V\S, S)`
- 求解前的快速检查：总供需是否平衡、是否存在 `Lower > Upper` 的边
- 有可行流时通过负费用环消除得到最小费用流

## 算法

1. **可行性**：消去下界（边 `(u,v,l,u)` 变为容量 `u-l`，节点平衡调整为
   `b'(v) = b(v) - lowerOut(v) + lowerIn(v)`），加超级源汇跑 Dinic 最大流。
   最大流不足时，残量网络中从超级源可达的集合即为违反守恒的割证据。
2. **还原**：原网络的流 = 辅助网络的流 + 各边下界。
   辅助网络的流本身**不是**原网络的流，直接返回会破坏节点平衡。
3. **优化**：在原网络的残量网络上用 Bellman-Ford（`big.Int` 距离）找负费用环，
   按瓶颈容量增广，直到不存在负环，此时流即为最小费用流。

## 用法

```go
g := &mincostflow.Graph{
    NumNodes: 3,
    Edges: []mincostflow.Edge{
        {From: 0, To: 1, Lower: 0, Upper: 4, Cost: big.NewInt(2)},
        {From: 1, To: 2, Lower: 1, Upper: 4, Cost: big.NewInt(3)},
    },
    Balance: []int64{3, 0, -3},
}
r := mincostflow.Solve(g)
switch r.Status {
case mincostflow.Optimal:
    // r.Flow 是原网络每条边的流量，r.Cost 是 *big.Int 总费用
case mincostflow.Infeasible:
    // r.Certificate 给出违反守恒的割：supply(S) > upper(S,T) - lower(T,S)
case mincostflow.Unbalanced, mincostflow.BadBounds:
    // r.Message 说明原因
}
```

## 验证

`BruteForce` 在 `[Lower, Upper]` 内逐边枚举所有流，用于小规模对拍。
测试（`go test ./...`）包含：

- 确定性用例：供需不平衡、下界冲突、负环消除、割证据、大整数费用
- 400 组随机小图与暴力枚举对比可行性、最优费用，并独立复核割证据
- 100 组 ~2^90 随机大费用与暴力枚举对比

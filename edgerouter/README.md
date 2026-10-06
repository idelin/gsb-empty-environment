# edgerouter

Go 路由库：路网边带通行时间，部分连续边对禁止转向，转向有等待时间。
求从源顶点到目标顶点的**总时间最小**路径；同代价时按**边 id 序列字典序**取最小；
零代价环下保证不重复状态、必然终止。

## 为什么状态是"到达边"而不是顶点

下一步的代价与可行性取决于**从哪条边到达**当前顶点（转向限制、转向等待）。
若只按顶点记录最短距离，到达同一顶点的两条不同边会被错误合并：

```
s --e1(1)--> u --e3(1)--> t     禁止转向 (e1, e3)
s --e2(2)--> u
```

按顶点记 dist[u]=1（经 e1）后，e1→e3 被禁，顶点状态无法回溯改走 e2，
会误判不可达；正确答案是 e2,e3，总时间 3。见 `TestForbiddenTurnBeatsVertexState`。

## 算法

- 状态 = 到达边 id；源点的所有出边为初始状态。
- 转移 `e -> f`：要求 `e.To == f.From` 且 `(e,f)` 未被禁止；
  代价 = `turnWait(e,f) + f.Time`。
- Dijkstra 的优先级为二元组 `(总时间, 边 id 序列)`，序列按字典序比较，
  严格前缀更小。该序对"追加一条边"单调且保序，且追加后严格变大，
  因此 label-setting 成立，每个状态只定址一次——零代价环不会重复状态。
- 首次弹出 `edge.To == dst` 的状态即为答案（全局最优）。
- `src == dst` 返回空路径（总时间 0）。

## 枚举核对

`Enumerate` 对状态空间做 DFS，展开所有不重复状态的简单路径（零代价环
因此有限），取 `(总时间, 边 id 序列)` 最小者，用于小图交叉验证。
`TestRandomAgainstEnumeration` 用 2000 组随机小图（含零代价边、转向等待、
禁止转向）对拍两种实现。

## 用法

```go
g := edgerouter.NewGraph([]edgerouter.Edge{
    {ID: 1, From: 0, To: 1, Time: 1},
    {ID: 2, From: 0, To: 1, Time: 2},
    {ID: 3, From: 1, To: 2, Time: 1},
})
g.ForbidTurn(1, 3)
g.SetTurnWait(2, 3, 0)

path, ok := g.ShortestPath(0, 2)
// ok == true, path.Total == 3, path.Edges == []int{2, 3}
```

## 测试

```sh
go test ./...
```

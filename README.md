# pathcount

小规模有向图（最多 22 个顶点）上的简单路径计数与分页枚举工具（Go 实现）。

## 功能

- 统计 `s` 到 `t`、不重复顶点、且覆盖指定必经点集合的路径条数，计数使用 `math/big.Int`。
- 按字典序分页枚举路径，支持前缀条件（`prefix`）与 `offset`/`limit` 翻页。
- 计数与分页共享同一份子集状态记忆与图版本，二者必然对应同一版本；
  结果中携带 `Version` 便于调用方核对。
- 单次查询有状态展开预算（`budget`），预算耗尽返回 `Known=false`（未知），
  绝不把中断结果当成 0；未知结果不写入记忆，增大预算重试即可继续。

## 设计

- 状态为 `(当前顶点 v, 已访问集合 mask)`，`mask` 是 22 位以内的子集位掩码；
  必经点覆盖情况由 `mask & req` 推出，无需额外维度。
- `count` 递归 + `map[state]*big.Int` 记忆化，只缓存确定结果。
- 枚举按邻居编号升序 DFS，利用子树计数整棵跳过 `offset` 之前的路径，
  翻页成本与返回页大小而非路径总数相关。
- `Tool` 持有图的私有快照；`Update` 修改图会递增版本并清空记忆。

## 用法

```go
g := pathcount.NewGraph(4)
g.AddUndirectedEdge(0, 1)
g.AddUndirectedEdge(1, 2)
g.AddUndirectedEdge(2, 3)
g.AddEdge(0, 3)

tool := pathcount.NewTool(g, 0, 3, []int{2}, 0) // 必经点 {2}
res := tool.Count()                             // res.Value = 1, res.Known = true
page := tool.Enumerate([]int{0}, 0, 10)         // 第一页，最多 10 条
```

## 测试

```sh
go test ./...
```

测试用暴力全路径枚举在 400 个随机小图上交叉核对计数与分页，
并覆盖提前到达 t、孤立必经点、必经点互斥冲突、预算中断返回未知、
大整数计数（K15，超过 2^32）、版本一致性与非法前缀等场景。

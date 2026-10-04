# coreidx — 无向简单图的增量 core number 索引

维护一个无向简单图的 k-core 分解索引：初次全图分解后，支持原子批量
加/删边，并用增量算法（Sariyuce et al., *Incremental K-core Decomposition
in Dynamic Graphs*）在受影响局部传播 core 值变化，不做全图重算。

## 语义约定

- 节点 ID 为调用方指定的稳定 `int64`；孤立节点永久保留（core = 0）。
- 重复加边幂等；删除不存在的边是无操作；自环被拒绝。
- `ApplyBatch` 原子执行：任一操作非法（如自环）则整批回滚，图与索引
  恢复到批前状态。
- `ApplyBatch` 返回本批中 core 值发生净变化的节点集合（按 ID 排序）。

## 库用法

```go
g := coreidx.New()
g.AddNode(100)            // 孤立点
g.AddEdge(1, 2)           // 构造期原语，不维护 core
res := g.Build()          // 全图分解：res.Steps 为确定性剥离顺序
                          // （同剩余度数时按节点 ID 升序），
                          // 每步记录剥离时刻的剩余度数用于校验：
                          // core(vi) = max(d(vi), max{core(vj): j<i, vj~vi})

out, err := g.ApplyBatch([]coreidx.Op{
    coreidx.DeleteEdgeOp(2, 3),
    coreidx.AddEdgeOp(7, 8),
    coreidx.AddNodeOp(200),
})
// out.Changes: []CoreChange{Node, Old, New}
```

增量正确性基于：单边变化只会使 `core = K = min(core(u), core(v))` 的
节点变动一级。插入时在 K 层连通块内做 (K+1) 阈值局部剥离，幸存者升入
(K+1)-core；删除时从端点按“支持数 < K”逐出并级联，被逐出者降为 K-1。

## 命令行

```
coreidx build  -graph FILE          # 输出每点 core、确定性剥离顺序与剩余度数
coreidx update -graph FILE -ops FILE  # 原子应用整批操作，输出 core 变化的节点
```

图文件：`N <id>` 声明节点，`E <u> <v>` 声明边，`#` 为注释。
操作文件：`A <u> <v>` / `D <u> <v>` / `N <id>`。
批失败（如自环）时整批回滚，stderr 输出错误，退出码 1。

```
go run ./cmd/coreidx build  -graph examples/bridge.graph
go run ./cmd/coreidx update -graph examples/bridge.graph -ops examples/delete-key-edge.ops
```

## 测试

```
go test ./...
```

- 已知图形的 core 值、剥离顺序确定性与剩余度数校验关系；
- 两个 K5 高核子图经低核桥相连：删关键内部边引发一侧连锁降核
  (4→3)、桥与另一侧不受影响，恢复该边后整体升回；
- 幂等加边、删除不存在边、自环拒绝、孤立点保留、整批失败回滚；
- 小图枚举全部子集，验证 k-core 恰为所有最小度 ≥ k 的导出子图的并，
  并在每次增量更新后重验；
- 随机批量操作的差分测试：增量结果对照测试内的全图分解参考实现，
  并核对返回的受影响节点集合。

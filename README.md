# kcore — 图拓扑 K-核分解与剥离器

纯 Go 标准库实现的大型稀疏图核度（coreness）分解：

- `CoreNumbers`：Batagelj–Zaversnik 算法，O(V+E) 计算每个节点的 Core-Number。
- `PeelKCore` / `KCoreSubgraph`：剥离器 —— 动态移除（残余）度数 < K 的节点直到收敛，输出分层 k-core 子图。
- `Layers`：按 Core-Number 分层输出各层（k-shell）节点。
- `Maintainer`：增量维护。增删边时利用"单边更新最多改变核度 1"的性质，
  只在 core == min(core[u], core[v]) 的局部候选连通分量内重新传播核度调整，
  开销远小于全局重算。

## 运行样例

```sh
go run ./cmd/kcore-demo
```

场景：K5 稠密子网（core=4）+ 缺一条棱的三棱柱（core=2）+ 悬挂链（core=1）+ 孤立点（core=0）。
加入桥接边 (12,15) 补全棱柱后，6 个节点核度从 2 局部提升到 3，
局部更新仅访问 6 个节点（全局重算需 15 个）；删除该边后回落。

## 测试

```sh
go test ./...
```

覆盖：核度正确性、1..4 分层剥离、桥接边增删的局部提升/回落、
no-op 守卫、随机稀疏图上 1000 次增删边与全局重算逐步对拍、
以及多分量大图上的局部性（平均每次插入仅访问 6/600 个节点）。

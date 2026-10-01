# bptree — 平衡括号编码有序根树的 succinct 索引

纯 Go 标准库实现。有序根树用平衡括号序列编码：每个节点一对 `(` `)`,
子树按先序嵌套。节点编号 = 先序秩（第 v 个 `(` 即节点 v)。

## 编码布局（真实占用字节）

```
负载:  2n 个括号, 1 bit/括号, 打包进 []uint64          -> n/4 字节 (n=节点数)
超块:  每 8192 括号: absExcess i32, absOpens i32,
       relMin i16 (相对超块起点)                        -> 12 B/超块
块:    每 512 括号:  relExcess i16, relMin i16,
       relOpens i16 (均相对所属超块)                    -> 6 B/块
RMQ:   超块最小余量上的稀疏表 (i32, 派生结构不序列化)
       + 每超块内块最小余量稀疏表 (i16, 紧凑打包)       -> 亚线性
```

1M 节点实测：负载 262144 B，索引总占用 317488 B，开销 21.1%
（对比指针树 ~40 B/节点）。`Index.SizeBytes()` 报告分项字节。

## 查询（均不扫描整个括号串）

- 余量搜索原语 `searchForward/searchBackward`: 超块稀疏表 O(log) 跳跃 +
  超块内块稀疏表 O(log) 跳跃，只逐位扫描一个块。
- `FindClose`/`FindOpen`/`Enclose`: 一次余量搜索。
- `LCA(u,v)`: 区间 (ou,ov] 最小余量 m（超块 RMQ + 块 RMQ + 边缘块扫描），
  再向后找 `Excess == m-1` 的最右位置。
- `Parent/FirstChild/NextSibling/PrevSibling/ChildRank/Children/Depth/
  SubtreeSize/SubtreeRange/SubtreeParens`: 由上述原语组合。
- `Rank/Select/Excess`: 超块二分 + 块二分 + 单块 popcount。
- `Index.Stats()` 记录每次查询的块访问数（见 demo 输出，平均几次到几十次，
  而全串扫描需数千块）。
- 所有操作迭代实现，深链（测试到 2^20 深度）不会栈溢出。

## 序列化与校验

`MarshalBinary`/`Load`: magic | n | minPrefixExcess | totalExcess | 位串 |
超块表 | 块表。`Load` 重新从位串计算并拒绝任何不一致：负前缀平衡、
多根、最终余量非零、存储的最小前缀余量/总计数不符、任一超块/块摘要不符、
填充位非零、截断/尾随字节。RMQ 为派生结构，不序列化，加载后重建。

## 运行

```sh
go test ./...   # 含穷举核验: n<=7 全部有序树(Catalan 个)对照独立指针树
go run ./cmd/demo
```

# cgraph — 差分编码 + 跳表的压缩图索引

纯 Go 标准库实现的只读压缩邻接索引。**不是** `map` 包装：邻接表排序后做
差分 varint 编码存入单个字节流，平行边重数与差分交错存储，每 16 条记录
采样一个跳表项加速成员查找。

## 编码布局（真实字节级）

每个节点在共享缓冲区 `buf` 中占一段连续区间：

```
uvarint distinctCount        // 去重后的邻居数
uvarint totalDegree          // 重数总和（度数，O(1) 查询）
record × distinctCount:
  uvarint delta              // 首条：邻居 id 本身；其后：id - prevId（>= 1）
  uvarint multMinus1         // 平行边重数 - 1（重数不丢，另存于此）
```

- 邻居先排序再编码，所以差分恒为正，记录流本身即有序序列，交集可直接归并。
- **自环保留**：邻居 id == 自身的记录照常编码。
- **平行边不丢重数**：折叠为一条记录，`multMinus1` 存重数。

跳表（内存索引，每 `SkipInterval=16` 条记录采样一项）：

```
skipEntry { prevValue uint32;  // 该块前一条记录的绝对 id（首块为 0）
            offset    uint32 } // 该块首条记录在 buf 中的绝对偏移
```

`prevValue` 让解码器能从块边界冷启动：成员查找先对采样二分定位块，
再从块内解码（每块 ≤ 16 条记录），无需从头扫整条邻接表。

节点目录（内存，24 B/节点）：`{offset, count, degree, skipStart, skipCount}`。

## 图语义

`Build(n, edges)` 建无向投影：边 (u,v)（u≠v）双向各存一条；自环 (u,u)
在 u 自己的表中存一条；平行边累加重数。度数 = 重数总和（自环按 1 份计）。
空节点占 2 字节头部（count=0, degree=0）。

## 损坏拒绝

解码时校验：varint 截断、差分为 0（非递增）、**差分累加越出节点空间**
（`id >= numNodes`）都会返回 `ErrCorrupt`；`Validate()` 额外校验头部与
目录一致性、重数总和、段尾无垃圾字节。见 `TestCorrupt*` 系列测试。

## API

| 方法 | 复杂度 | 说明 |
|---|---|---|
| `Degree(u)` | O(1) | 度数（重数总和） |
| `DistinctNeighbors(u)` | O(1) | 去重邻居数 |
| `HasEdge(u,v)` / `Multiplicity(u,v)` | O(log(块数) + 16) | 跳表二分 + 块内解码 |
| `Neighbors(u)` | O(deg) | 解码整条邻接表 |
| `Intersection(u,w)` | O(deg_u + deg_w) | 双迭代器归并，不物化全表 |
| `Validate()` | O(全图) | 完整解码校验 |

## 实测字节占用（go run ./cmd/demo）

10,000 节点 / 202,116 条输入边（含 1,000 组三重平行边、50 个自环、
一个 12 节点团），无向存储后 399,367 条去重邻接项：

```
delta-encoded buffer :   1055924 bytes  (2.64 B/distinct entry)
skip index (1/16)    :    238152 bytes
node directory       :    240000 bytes
total                :   1534076 bytes
raw []uint32 baseline:   1597468 bytes  (4 B/entry, 且不含重数)
```

编码主体 2.64 B/项，低于裸 `[]uint32` 的 4 B/项（基线还不存重数）。
聚集性好的图上更低：`TestEncodingIsActuallyCompact` 实测 2.16 B/记录。

## 运行样例

```
$ go run ./cmd/demo
=== build ===
nodes=10000  input edges=202116  directed adjacency entries=404168 (distinct 399367)
=== size (real bytes) ===
delta-encoded buffer :   1055924 bytes  (2.64 B/distinct entry)
skip index (1/16)    :    238152 bytes
node directory       :    240000 bytes
total                :   1534076 bytes
raw []uint32 baseline:   1597468 bytes  (4 B/entry, multiplicities not even stored)
=== queries ===
degree(7) = 36
hasEdge(7,7) [self-loop] = true
multiplicity(8030,8552) = 1
intersection(9000,9001) = [9002 9003 9004 9005 9006 9007 9008 9009 9010 9011] (10 common neighbors)
=== corruption rejection ===
node 0 section before: 03 03 01 00 02 00 02 00
node 0 section after : 03 03 64 00 02 00 02 00
Validate -> cgraph: node 0: cgraph: corrupt encoding
Neighbors -> cgraph: node 0: cgraph: corrupt encoding
```

（把首条差分 `01` 改成 `0x64=100`，解码出 id 100 ≥ 8 个节点，被拒绝。）

## 测试结果

```
$ go test ./... -v
=== RUN   TestBasicGraph                  --- PASS   (自环/平行边/度数/交集)
=== RUN   TestEmptyAndIsolated            --- PASS   (空图、孤立点)
=== RUN   TestSkipIndexSeek               --- PASS   (200 邻居，跨 12 个跳表块)
=== RUN   TestRandomAgainstReference      --- PASS   (300 节点随机图对照 map 参考实现，
                                                      含平行边/自环，2 万次重数查询 +
                                                      2 千次交集全部一致)
=== RUN   TestCorruptDeltaOutOfRange      --- PASS   (差分越界 -> 拒绝)
=== RUN   TestCorruptZeroDelta            --- PASS   (差分为 0 -> 拒绝)
=== RUN   TestCorruptTruncated            --- PASS   (截断 -> 拒绝)
=== RUN   TestCorruptHeaderCount          --- PASS   (头部计数篡改 -> 拒绝)
=== RUN   TestBuildRejectsOutOfRangeEdge  --- PASS
=== RUN   TestQueryRejectsOutOfRangeNode  --- PASS
=== RUN   TestEncodingIsActuallyCompact   --- PASS   (2.16 bytes/record)
PASS
ok      cgraph  0.299s
```

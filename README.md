# Multi-Paxos 共识日志引擎 + 海量稀疏数据内存紧凑优化器 (Go)

纯标准库实现，无任何第三方依赖，库本身不启动任何 goroutine（严格无泄漏）。

## 结构

```
paxos/      Multi-Paxos 多数派共识与无空洞日志提交引擎
sparse/     海量稀疏数据容器与内存紧凑优化器
cmd/demo/   运行样例（5 节点分区接管 + 稀疏优化器展示）
```

## paxos 包

- **两阶段协议**：Prepare/Promise（Phase 1）与 Accept/Accepted（Phase 2）。
- **领导者租约**：心跳授予租约；租约有效期内领导者跳过 Phase 1，
  直接通过 Phase 2 提交连续 LogEntry；租约有效期内其他节点的
  Prepare/Accept 会被拒绝（`TestLeaseBypassesPhase1`、`TestLeaseRejectsForeignPrepare`）。
- **无空洞提交引擎**：`commitIndex` 只跨越连续槽位推进；新领导者通过
  Phase 1 收集多数派已接受条目，逐槽位采纳最高 ballot 的值，空洞用
  no-op 填补，并安全覆写少数派脏日志（`TestGapRepairFillsHoles`、
  `TestPhase1PreservesPossiblyChosenValue`、`TestPartitionMajorityTakeover`）。
- **确定性模拟网络**：同步 RPC、任意分区/单向断链、消息计数，
  配合手动时钟实现零 flaky 的租约测试。

## sparse 包

- **自适应容器**：稀疏（分桶有序数组，桶内二分、均摊 O(1) 插入）与
  密集（值数组 + 存在位图）两种表示，优化器在每次变更后估算两种
  表示的内存开销，带迟滞（20%）地自动转换。
- **压缩**：`Compact()` 将条目流压缩为 delta+varint 打包字节流
  （键差分 + zig-zag 值编码），packed 模式为冷存储只读，写入时自动解包。
- **边界安全**：`uint64` 全区间索引（含 `MaxUint64`）、负值、空容器、
  dense 扩张上限（防止 span 溢出/内存爆炸）、并发读写安全（`sync.RWMutex`）。

## 运行样例

```
$ go run ./cmd/demo
=== Multi-Paxos: 5-node cluster, network partition & takeover ===
[1] node 0 elected, commits v1 on all 5 nodes
  after v1                     n0(ci=1): v1 | n1(ci=1): v1 | n2(ci=1): v1 | n3(ci=1): v1 | n4(ci=1): v1 |
[2] partition {0,1} | {2,3,4}; old leader writes 'dirty' to minority
    old leader propose -> paxos: no quorum reachable (dirty log only on nodes 0,1)
  minority partitioned         n0(ci=1): v1 | n1(ci=1): v1 | n2(ci=1): v1 | n3(ci=1): v1 | n4(ci=1): v1 |
[3] leases expire; majority elects node 2, commits 'real'
  majority commits real        n0(ci=1): v1 | n1(ci=1): v1 | n2(ci=2): v1 real | n3(ci=2): v1 real | n4(ci=2): v1 real |
[4] heal: new leader's heartbeat overwrites dirty minority logs
  after heal                   n0(ci=2): v1 real | n1(ci=2): v1 real | n2(ci=2): v1 real | n3(ci=2): v1 real | n4(ci=2): v1 real |

=== Sparse optimizer: 1,000,000 entries over a 1e9 key space ===
  mode=sparse count=999496 density=0.000999
  footprint: 72594784 bytes (69.2 MiB); dense array would need ~7.5 GiB
  after Compact: mode=sparse-packed footprint=5005408 bytes (4.8 MiB)
  adaptive conversion: filling keys 0..9999 densely
  mode=dense density=1.00 footprint=81320 bytes
  after deleting 90% (spread): mode=sparse footprint=71008 bytes
```

## 测试结果

```
$ go test ./...
ok      mpaxos/paxos
ok      mpaxos/sparse

$ go test -race ./...
ok      mpaxos/paxos   1.968s
ok      mpaxos/sparse  47.969s
```

覆盖：基本选举与提交、租约绕过 Phase 1、租约拒绝外来 Prepare、
5 节点网络分区多数派接管（验收场景）、空洞修补、Phase 1 值保持、
已提交条目不可覆写、单领导者并发提交、选举竞争、goroutine 泄漏断言；
稀疏容器的空/单元素/极值索引/负值/双向转换/压缩往返/内存上界/
ForEach 顺序/混合并发/并发删除一致性与 goroutine 泄漏断言。

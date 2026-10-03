# Multi-Paxos 共识日志引擎 + 海量稀疏数据内存紧凑优化器

TypeScript 实现，零依赖，直接使用 Node.js ≥ 23.6 内置类型剥离与 `node:test` 运行。

## 架构

```
src/
├── paxos/
│   ├── types.ts      # Ballot / LogEntry / 消息协议定义
│   ├── network.ts    # 确定性模拟网络：分区、延迟、丢包、虚拟时钟
│   ├── node.ts       # Multi-Paxos 节点（两阶段 + 租约 + 补洞 + 同步）
│   └── cluster.ts    # 集群编排与一致性断言工具
└── sparse/
    ├── codec.ts          # varint / zigzag / delta / RLE 压缩编解码
    ├── sparse-vector.ts  # 自适应稀疏容器（sparse/dense/compressed 三态）
    ├── arena.ts          # CompactArena 紧凑分配器（slab + 空闲链表 + 压实）
    └── async-mutex.ts    # 异步互斥锁（并发交错安全）
```

## Multi-Paxos 共识引擎

- **两阶段协议**：Phase 1（Prepare/Promise）竞选与日志收敛；Phase 2（Accept/Accepted）多数派提交。
- **领导者租约**：租约有效期内跳过 Phase 1，直接以**连续下标**提交 `LogEntry`，保证无空洞；租约过期自动缓冲提案并重新发起 Phase 1。
- **空洞修补**：新领导者通过 Phase 1 收集多数派日志快照，逐槽位采纳最高 ballot 的条目，缺失槽位填 `NO_OP`，将 `committedUpTo+1 .. maxIndex` 连续化后重新 Accept 提交。
- **脏日志安全覆写**：少数派未提交条目（旧 ballot）在分区愈合后被多数派已提交条目覆写；已提交条目由多数派相交性保护，实现中另有防御性断言（committed 条目遇冲突覆写直接抛错）。
- **跟随者同步**：commit/heartbeat 触发空洞与脏槽检测，`sync-request/sync` 拉取权威已提交条目；仅当本地条目与提交者同属一个 ballot 时才依据 commit 消息标记提交，杜绝脏条目被误标。
- **提交推进**：`committedUpTo` 严格沿连续已提交前缀推进，任意时刻 `1..committedUpTo` 无空洞。

## 稀疏数据与内存紧凑优化器

- **自适应容器** `SparseVector`：
  - `sparse`（Map，密度 < 25%）⇄ `dense`（Float64Array 平铺，密度 > 50%）按密度阈值双向转换；远端孤点写入时拒绝不划算扩容，退回稀疏表示。
  - `compressed`：冷数据（连续只读超阈值）自动压缩为 `delta+varint` 下标 + `zigzag+RLE` 值字节流，写入时惰性解压；`compact()` 可强制压缩并返回前后字节数。
  - `estimateBytes()` 实时内存核算；`dispose()` 释放全部内部缓冲回到基线。
- **CompactArena**：单一 slab + 2 的幂尺寸分级空闲链表；`defrag()` 原地压实消除碎片（句柄透明重映射）；`assertNoLeaks()` 严格泄漏断言，未释放句柄精确报告；`dispose()` 释放底层 ArrayBuffer。

## 运行

```bash
npm test    # node --test tests/*.test.ts（38 个测试）
npm run demo  # 端到端演示：选举→分区→更替→收敛 + 稀疏优化器对比
```

## 验收场景（tests/partition.test.ts）

1. 5 节点集群，node0 当选并提交 3 条日志；
2. 分区 `{0,1} | {2,3,4}`，旧 Leader 的 `tx-4` 仅少数派接受（脏日志，未提交）；
3. 租约过期后多数派选举 node2 为新 Leader，Phase 1 继承已提交前缀并继续提交；
4. 旧 Leader 在少数派内重试无法获得多数派；
5. 分区愈合后心跳触发 sync，少数派脏日志被安全覆写，全集群收敛于
   `[tx-1, tx-2, tx-3, tx-4-new, tx-5, tx-6]`，`1..6` 逐槽位断言无空洞。

另含：新 Leader 用 `NO_OP` 补全空洞槽位、脑裂下双 Leader 不能提交冲突值等场景。

## 测试结果

```
ℹ tests 38
ℹ pass 38
ℹ fail 0
```

覆盖：编解码器边界（0/2^53-1/长游程/空序列）、三态容器转换、10 万元素千万级下标空间、
压缩率与无损性、冷数据自动压缩、异步并发交错（互斥锁下无丢失更新、与参考模型逐点一致）、
Arena 碎片压实/耗尽/双重释放/越界、10 万次 churn 后 `assertNoLeaks` 严格无泄漏。

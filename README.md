# Lock-Free MVCC Read Path (TypeScript / Node.js)

面向高频读业务的无锁 MVCC 读路径：**只读事务不获取任何锁、无 CAS 自旋、无重试、无
`Atomics.wait`**，仅通过一次原子加载取得快照，然后无锁遍历不可变版本节点。写线程
以事务方式向版本链头部追加节点，读操作完全免受阻塞且绝不读到半脏数据。

## 设计

存储建立在 `SharedArrayBuffer` 上（SoA 布局），可跨 `worker_threads` 共享：

- **不可变版本链**：每个键一条 prepend-only 链。版本节点（txId、key、value、next）
  写入后永不修改。
- **单写者提交协议**（`MvccWriter`）：
  1. 为事务内每个键构造节点，字段全部初始化后，用 `Atomics.store(heads, key, idx)`
     （seq_cst，相当于 release）发布到链头；
  2. 所有节点发布完毕后，最后一次 `Atomics.store` 推进全局 `commitCounter` 完成提交。
- **只读快照**（`MvccReader`）：
  1. `snapshot()`：一次 `Atomics.load(commitCounter)`（acquire）；
  2. 逐键遍历链头，跳过 `txId > snapshot` 的节点（属于快照时刻尚未提交的事务），
     落到最新已提交版本。

**零撕裂论证**：seq_cst 为所有原子操作提供单一全序。写者 `publish(node) < bump(counter=T)`；
读者 `load(counter) < load(head)`。若快照 ≥ T，则读者对链头的加载必然排在发布之后，
能看到新节点；若快照 < T，读者即使看到新节点也会按 txId 跳过，落到旧的已提交版本。
因此每个键都在同一逻辑提交点被读取，多键不变量（如总和守恒）恒成立，无需任何重试。

## 运行

要求 Node.js ≥ 23.6（原生 TypeScript type-stripping，无需编译）。

```bash
npm test     # node --test：7 个单线程语义测试 + 1 个多线程验收测试
npm run demo # 运行验收场景并打印报告
```

## 验收场景

1 个写线程对 1000 个键持续执行密集的双键转账事务（全局总和守恒）；5 个读线程并发
执行全表扫描校验和统计。写操作无等待，读者每次快照的总和必须恒等于初始总和。

## 测试结果

`npm test`（8 项全部通过）：

```
✔ reader sees base values on an empty store
✔ committed writes are visible to new snapshots
✔ snapshot isolation: a pinned snapshot keeps seeing old versions
✔ uncommitted (staged) writes are invisible to readers
✔ multi-key batch commits atomically: mid-transaction readers see neither key
✔ version chain traversal picks the newest version visible to the snapshot
✔ checksum stays conserved across many sequential transfer transactions
    writer: 120000 tx in 47.4ms (2533380 tx/s); readers: 10884 scans
✔ acceptance: 1 writer + 5 readers, dense updates on 1000 keys, zero snapshot tearing
ℹ pass 8  ℹ fail 0
```

`npm run demo`（300k 写事务 + 5 读线程）：

```
=== Lock-free MVCC stress report ===
keys:                1000
writer transactions: 300000 (2 keys each, sum-conserving transfers)
reader threads:      5 (full-table checksum scans)
expected sum:        1000000
final sum:           1000000
writer elapsed:      71.1 ms (4218769 tx/s, never blocked)
reader 0..4:         20224 scans total, 0 violations each
torn snapshots:      0
PASSED: zero tearing, checksum conserved across every snapshot
```

## 文件结构

- `src/mvcc.ts` — 无锁 MVCC 存储核心（版本链、写者提交协议、只读快照）
- `src/stress-worker.ts` — 压测 worker（写者转账 / 读者校验和扫描）
- `src/stress.ts` — 验收场景编排器（1 写 + 5 读）
- `src/demo.ts` — 可运行的演示入口
- `test/mvcc.test.ts` — 单线程语义单元测试
- `test/stress.test.ts` — 多线程零撕裂验收测试

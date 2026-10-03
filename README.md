# txn-chunk-streamer

大事务分片传输与内存保护限流器（TypeScript / Node.js，零依赖）。

针对**单个事务包含百万级行变更**的场景：传输器不会把整笔事务一次性加载进内存，
而是将变更流切分为带阶段标记的子分片（Chunk）流式传输；当接收端消费跟不上、
内存占用超过预算时，自动溢写临时磁盘文件并对生产端施加背压限流，防止接收端 OOM。

## 要求

- Node.js >= 23.6（原生 TypeScript 类型擦除，无需编译、无第三方依赖）

## 架构

```
source DB ──> chunker ──> SpillQueue (内存保护限流器) ──> receiver ──> sink
 (CDC 游标)   (分片+阶段标记)   内存预算 / 磁盘溢写 / 背压      (完整性校验+应用)
```

- `src/chunker.ts` — 以 `chunkSize` 行为单位切分事务；首片标记 `BEGIN`，中间片
  `CONTINUE`，末尾追加 `END` 标记片（携带 totalRows / totalChunks / 事务级校验和）。
  源端以异步迭代器惰性消费，任意时刻最多缓冲一个分片。
- `src/spill-queue.ts` — 字节级精确记账的有界异步队列：
  - 内存占用超过 `maxMemoryBytes` → 溢出项追加到临时磁盘文件（长度前缀记录），
    消费时按 FIFO 读回，防止堆膨胀导致 OOM；
  - 队列总量（内存+磁盘）达到 `maxQueuedBytes` 高水位 → `push()` 挂起，
    直到消费端排空（背压限流）；
  - 溢写文件排空后自动删除。
- `src/receiver.ts` — 逐片校验：阶段顺序、分片序号连续性、逐片 SHA-256、
  累计行数、END 总数与事务级链式校验和；行数据逐片应用即弃，不做留存。
- `src/transporter.ts` — 生产/消费并发流水线，两侧仅通过有界队列连接。

## 运行

```bash
npm test          # Node 内置测试（node:test）
npm run demo      # 验收场景：50 万行 / 100 批 / 峰值内存 < 10MB
npm run demo:naive  # 附加对比：朴素“全量加载”方案的内存占用
```

## 验收场景运行样例（`npm run demo`）

源库一次大更新产生 50 万条变更，切分为 100 个批次流式写入，接收端模拟慢速
消费以触发磁盘溢写与限流：

```
streaming transfer       : 500,000 rows in chunks of 5,000...

--- Transfer result ---
rows transferred     : 500,000
data chunks          : 100 (+1 END marker)
rows applied at sink : 500,000
integrity committed  : true
txn checksum         : 4e9925ea4e92e924...
elapsed              : 1748 ms

--- Memory guard ---
guard peak memory    : 0.52 MB (budget 1.00 MB)
spilled to disk      : 53.97 MB
read back from disk  : 53.97 MB
rate-limit stalls    : 94

--- Process memory (baseline 7.99 MB) ---
peak live heap delta : 1.61 MB (acceptance bound: 10.00 MB)
peak raw heap delta  : 4.54 MB (includes not-yet-collected garbage)

OK: 500k rows streamed in 100 batches, integrity verified, peak memory < 10MB
```

对比朴素方案（`npm run demo:naive`，同一进程全量加载 50 万行）：

```
naive load-all-into-memory : 75.43 MB heap for 500,000 rows
```

> 说明：`peak live heap delta` 在每个分片边界强制 GC 后采样，衡量真实驻留
> 工作集；`peak raw heap delta` 为未强制 GC 的原始采样（含待回收垃圾）。
> demo 以 `--max-old-space-size=16 --max-semi-space-size=2` 运行，
> 证明整个传输在受限堆内即可完成。

## 测试结果（`npm test`）

```
✔ chunker splits 500k-row transaction into 100 phased chunks + END marker
✔ end-to-end: 500k rows streamed in 100 batches with integrity verified
✔ peak live heap stays within 10MB while streaming 500k rows
✔ SpillQueue preserves FIFO order across the memory/disk boundary
✔ SpillQueue throttles producer at the high-water mark
✔ receiver rejects corrupted or out-of-order chunks

ℹ tests 6
ℹ pass 6
ℹ fail 0
```

覆盖点：

- 50 万行 → 100 个数据分片 + END 标记，阶段标记与累计行数正确；
- 端到端传输后事务完整性提交（行数、片数、事务级校验和一致）；
- 流式传输全程峰值驻留堆内存 < 10MB；
- 溢写队列跨内存/磁盘边界保持 FIFO，排空后清理临时文件；
- 高水位限流：生产端在队列满时被挂起，消费后排空解除阻塞；
- 接收端拒绝篡改（行内容/校验和）、乱序分片与伪造的 END 总数。

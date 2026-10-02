# log-stream-pipeline

高吞吐顺序日志管道：多生产者并发投递下维护单调 LSN，支持显式持久化屏障，
屏障前日志未全部落盘前禁止发布后续数据；写盘异常时回滚并释放未确认内存缓冲区。

## 设计

- **单调 LSN**：`append()` 同步分配 LSN（单线程事件循环下原子），多生产者并发安全。
- **有序落盘**：单写者 Promise 链按 LSN 顺序串行追加到 JSONL 文件（`FileStorage`）。
- **持久化屏障**：`insertBarrierAt(lsn)` / `insertBarrier()` 注册屏障；
  仅当 `committedLsn >= barrier.lsn` 时屏障解决；LSN 大于屏障边界的日志
  在屏障解决前不会被发布（`onPublish` 闸门）。
- **异常回滚**：某条日志写盘失败时，丢弃所有 LSN ≥ 失败点的未落盘条目，
  释放其内存缓冲槽，拒绝越界屏障，并将 LSN 游标回退到失败点以允许恢复写入。
- **内存守恒**：`BufferPool` 跟踪缓冲槽申请/释放，不变量
  `totalAcquired === totalReleased + outstanding`，回滚后 `outstanding === 0`。

## 结构

- `src/pipeline.ts` — 管道核心（LSN 分配、写盘链、屏障、发布闸门、回滚）
- `src/buffer-pool.ts` — 内存缓冲区计数
- `src/storage.ts` — `Storage` 接口与 JSONL 文件实现
- `test/pipeline.test.ts` — Node.js 内置测试（`node:test`）
- `demo.ts` — 运行样例

## 运行

要求 Node.js ≥ 22.6（利用原生 TypeScript 类型擦除，无需编译）。

```sh
npm test      # node --test test/pipeline.test.ts
npm run demo  # node demo.ts
```

## 验收场景（测试 1）

10 个并发流 × 各 200 条 = 2000 条日志；预注册屏障 @500、@1200；
注入 LSN 1250 写盘失败。断言：

1. 分配的 LSN 恰好为 1..2000（单调、唯一）；
2. 回滚发生于 LSN 1250，磁盘上 LSN 1..1249 按序全量持久（⊇ 屏障 @1200 范围）；
3. 屏障 @500、@1200 均 resolved，且任何 LSN>500 / LSN>1200 的发布事件
   均晚于对应屏障解决事件；
4. 发布序列与落盘序列完全一致；
5. 内存计数守恒：`totalAcquired === totalReleased === 2000`，`outstanding === 0`。

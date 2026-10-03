# ordered-log-pipeline

高吞吐顺序日志管道：多生产者并发投递下维护单调 LSN，支持显式持久化屏障（barrier），
异常时回滚并释放未确认内存缓冲区。纯 TypeScript 实现，零依赖，使用 Node.js 内置测试运行器验证。

## 设计

- **单调 LSN**：`LogStreamPipeline.append()` 在单线程事件循环内同步分配 LSN（从 1 开始严格递增、无空洞），
  多生产者并发调用无需外部锁即保证单调唯一。
- **批量顺序刷盘**：单个 flush 循环按 LSN 顺序把待写缓冲区切成批次写入 `Sink`，每批结束后 `sync()`（fsync），
  推进 `durableLsn`。写入与 fsync 均保序，持久化记录始终构成无空洞前缀 `1..N`。
- **持久化屏障**：`insertBarrier()` 捕获当前最大 LSN 并返回 Promise；只有当 `durableLsn` 越过该 LSN
  （即屏障前所有日志已 fsync 落盘）后 Promise 才兑现。发布指针 `publishedLsn` 永远不会超过
  `durableLsn`，因此屏障后的数据在屏障前数据落盘前绝不可被消费方看到。
- **异常回滚**：`Sink.write`/`sync` 任一失败时，管道进入失败态：
  已成功写入但未确认的记录尝试补救性 `sync()`；其余未确认缓冲区（未写出的 + 仍在队列中的）全部丢弃并
  计入 `rolledBack`/`released`，未满足的屏障被 reject，后续 `append` 抛出同一错误。
- **内存计数守恒**：每条记录的缓冲区生命周期为 `allocated → persisted + released` 或
  `allocated → rolledBack + released`。静止时恒有 `allocated === released` 且
  `persisted + rolledBack === allocated`、`bufferedCount === 0`，测试对此逐条断言。

## 目录结构

- `src/pipeline.ts` — 核心管道（LSN 分配、批量刷盘、屏障、发布指针、回滚与内存计数）
- `src/sink.ts` — `Sink` 接口与 `FileSink`（JSON 行 WAL 文件 + fsync）
- `test/pipeline.test.ts` — 单元测试：并发 LSN 单调性、屏障发布门控、失败回滚
- `test/acceptance.test.ts` — 验收测试（见下）
- `examples/demo.ts` — 运行样例

## 环境要求

Node.js >= 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，无需编译步骤；推荐 >= 23）。

## 运行

```bash
npm test        # node --test test/*.test.ts
npm run demo    # node examples/demo.ts
```

## 验收边界

`test/acceptance.test.ts`：10 个并发流各写 200 条（共 2000 条）日志，在第 500 与第 1200 条处
插入刷盘屏障；通过 `FailAtLsnSink` 在第 1250 条写入时注入失败。断言：

- 屏障 500/1200 均正常兑现，且兑现时 `durableLsn` 已越过屏障 LSN；
- 落盘文件中的记录构成无空洞前缀 `1..N`，`1200 <= N < 1250`（1200 之前全量持久，1250 起无记录）；
- 每条落盘记录的 payload 与发送时一致；
- 内存计数守恒：`released === allocated`、`persisted + rolledBack === allocated`、`bufferedCount === 0`；
- 发布指针单调递增、永不越过 `durableLsn`，且最终 `published === durable`，`1200 <= published < 1250`。

## 测试结果

```
$ node --test test/*.test.ts
acceptance summary: {
  allocated: 2000,
  persisted: 1249,
  rolledBack: 751,
  released: 2000,
  durable: 1249,
  published: 1249
}
✔ acceptance: 10 streams x 2000 logs, barriers at 500/1200, failure at 1250 (350.560292ms)
✔ monotonic unique LSNs across 10 concurrent producers (180.96625ms)
✔ barrier blocks publishing of subsequent data until pre-barrier data is durable (1.241041ms)
✔ failed write rolls back unconfirmed buffers and rejects later appends (1.053417ms)
ℹ tests 4
ℹ pass 4
ℹ fail 0
```

验收摘要解读：2000 条缓冲区全部分配；1249 条持久化（含 1200 屏障前全部日志）；
第 1250 条注入失败后，751 条未确认缓冲区回滚释放；`released === allocated === 2000`，计数守恒。

## 运行样例

```
$ node examples/demo.ts
[publish] durable frontier advanced to LSN 1
[publish] durable frontier advanced to LSN 164
[barrier] all logs up to LSN 50 are fsynced
[barrier] all logs up to LSN 100 are fsynced
[barrier] all logs up to LSN 150 are fsynced
[publish] durable frontier advanced to LSN 200
[barrier] all logs up to LSN 200 are fsynced
[done] file=/var/folders/.../log-pipeline-demo-XXXX/demo.log
[done] persisted 200 records, stats= { allocated: 200, released: 200, persisted: 200, rolledBack: 0 }
[done] first record: {"lsn":1,"streamId":"stream-0","payload":"stream-0 message #0"}
[done] last record: {"lsn":200,"streamId":"stream-3","payload":"stream-3 message #49"}
```

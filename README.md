# 可回退两阶段提交（2PC）协调器

离散事件仿真环境下的两阶段提交协调器，基于预写式日志（WAL）实现崩溃可恢复的状态转移，
覆盖参与者崩溃、投票超时、重复投票/重复消息等异常路径，并通过双向恢复机制杜绝孤儿事务。

## 架构

| 文件 | 职责 |
| --- | --- |
| `src/eventLoop.ts` | 离散事件循环：按虚拟时间调度回调，提供可取消定时器 |
| `src/bus.ts` | 消息总线：固定延迟投递，目标节点崩溃时消息丢失 |
| `src/wal.ts` | 预写式日志：每条记录 append + fsync 后状态才生效，支持按序重放 |
| `src/coordinator.ts` | 2PC 协调器：状态机 + WAL 恢复 + 超时/重试/未决审计 |
| `src/participant.ts` | 2PC 参与者：本地 WAL 持久化，崩溃恢复后主动追问协调器 |
| `src/demo.ts` | 运行样例（两个验收场景） |
| `test/coordinator.test.ts` | Node.js 内置测试（node:test） |

## 协议与恢复设计

**协调器 WAL 记录序列**：`begin → decision(COMMIT|ABORT) → ack* → end`，另有 `audit` 记录未决事件。

- **阶段一（PREPARING）**：向全部参与者发 `prepare`；重复投票按参与者去重；任一 `NO` 票或
  超时未收齐投票 → 写 `decision: ABORT`；全票 `YES` → 写 `decision: COMMIT`。
- **阶段二（COMMITTING/ABORTING）**：决议落盘后才向参与者发布；收齐全部 `ack` 后写 `end` 关闭事务。
- **协调器崩溃恢复**：重启重放 WAL——
  - 已写 `decision` 但未 `end`：向未确认参与者发 `query`，按其回报状态重发决议或确认，直至完结；
  - 未写 `decision`（PREPARING）：**推定中止**（presumed abort），补写 ABORT 决议并通知参与者。
- **无响应参与者**：按 `retryInterval` 重试查询，达到 `maxRetries` 上限后写入
  `audit: PENDING_PARTICIPANTS` 未决审计事件（含事务决议与未决参与者列表），事务按已决结果关闭。
- **参与者崩溃恢复**：重放本地 WAL，对仍处于 `PREPARED` 的事务主动向协调器发 `statusRequest`
  追问结果，避免已持锁资源成为孤儿；协调器无该事务记录时安全回复 ABORT。
- **幂等性**：重复 `prepare`/`decision`/`vote`/`ack` 均被去重或幂等应答。

## 运行

要求 Node.js ≥ 23.6（利用内置 TypeScript 类型擦除直接运行 `.ts`，无需编译）。

```bash
npm test       # 运行全部测试
npm run demo   # 运行验收场景样例
```

## 运行样例输出（`npm run demo`）

```
=== 场景一：协调器发布 Commit 前崩溃，重启后完成提交 ===
[t=7] 协调器已崩溃。参与者状态: p1=PREPARED p2=PREPARED p3=PREPARED
--- 重启协调器，重放 WAL 恢复 ---
[t=11] 恢复完成。参与者状态: p1=COMMITTED p2=COMMITTED p3=COMMITTED
协调器 WAL:
  {"type":"begin","txId":"tx-1","participants":["p1","p2","p3"]}
  {"type":"decision","txId":"tx-1","decision":"COMMIT"}
  {"type":"ack","txId":"tx-1","participant":"p1"}
  {"type":"ack","txId":"tx-1","participant":"p2"}
  {"type":"ack","txId":"tx-1","participant":"p3"}
  {"type":"end","txId":"tx-1","decision":"COMMIT"}
场景一通过：崩溃前已落盘的 COMMIT 决议在重启后完成发布，无孤儿事务。

=== 场景二：参与者永久无响应，重试上限后记录未决审计 ===
[t=7] 协调器与 p3 均已崩溃。p1=PREPARED p2=PREPARED
--- 重启协调器（p3 保持离线） ---
[t=22] p1=COMMITTED p2=COMMITTED，p3 重试 2 次仍无响应
未决审计事件: {"type":"audit","event":"PENDING_PARTICIPANTS","txId":"tx-2","decision":"COMMIT","participants":["p3"]}
场景二通过：在线参与者完成提交，无响应参与者记入未决审计。

全部场景断言通过。
```

## 测试结果（`npm test`）

```
✔ 正常路径：全票 YES 后提交，所有参与者 COMMITTED，WAL 记录完整
✔ 一票 NO：协调器中止事务，所有参与者 ABORTED
✔ 投票超时：参与者宕机未投票，协调器超时后中止
✔ 重复投票与重复 Prepare 被去重，决议只产生一次
✔ 验收1：协调器发布 Commit 前崩溃，重启后重放 WAL、查询参与者并完成提交
✔ 验收2：无响应参与者重试达上限后记录未决审计事件
✔ 参与者崩溃恢复后主动向协调器追问结果，不留孤儿事务
✔ 协调器在 PREPARING 阶段崩溃：重启后推定中止，参与者释放资源
ℹ tests 8  ℹ pass 8  ℹ fail 0
```

# 事务消息模拟器（事务性发件箱 + 崩溃恢复 + 故障注入）

以 TypeScript 实现的 transactional outbox 模拟：订单事件以**至少一次**语义投递（可重复），
**订单更新与待发通知登记在同一事务中原子完成**；通知交付确认允许丢失，接收方按通知标识去重。
通过**逐个持久化边界注入崩溃**，验证：订单只更新一次、通知不会永久遗漏。

## 架构

```
事件源(≥1次投递) → Database(WAL 事务) → OutboxRelay → NotificationReceiver(去重)
                        │                     │              │
                   dbDisk(持久日志)      确认可丢失     receiverDisk(持久日志)
```

- `src/disk.ts` — 模拟耐用存储；`append` 把“日志落盘 + 内存应用”建模为原子单元，
  崩溃只能发生在其 `before`（写入丢失）或 `after`（写入已持久、进程随即死亡）。
- `src/database.ts` — 内存事务日志（WAL）：`txn-begin → order-upsert → outbox-add → txn-commit`。
  commit 记录是原子点；恢复时只重放已提交事务，未提交的意图记录被丢弃。
  事件按 `eventId` 幂等，重复投递返回 `duplicate`。
- `src/relay.ts` — 发件箱中继：发布后持久化 `notification-sent` 确认；确认丢失则恢复后重投。
- `src/receiver.ts` — 接收方：去重记录与业务效果原子生效，按 `notificationId` 去重。
- `src/simulator.ts` — 编排：事件确认前崩溃会重投；`crashAndRestart` 丢弃易失状态、从日志恢复。

## 持久化边界（故障注入点）

| 边界 | before 崩溃语义 | after 崩溃语义 |
|---|---|---|
| `txn.begin` / `txn.orderUpsert` / `txn.outboxAdd` | 意图记录丢失 | 意图已落盘但无 commit，恢复时丢弃 |
| `txn.commit` | 整个事务丢失，事件重投 | 事务已生效但事件未确认，重投后幂等跳过 |
| `relay.publish` | 通知未发出 | 通知已投递但未确认，将重投 |
| `relay.markSent` | **交付确认丢失**，重投后接收方去重 | 确认已持久 |
| `receiver.record` | 接收方未处理，等待重投 | 去重记录已持久，重投被忽略 |

## 验证的不变量（`test/invariants.ts`）

1. 每个订单事件恰好被业务应用一次；
2. 每个通知在接收方恰好生效一次，且不会永久遗漏；
3. 每个已提交事务同时包含一条订单更新和一条通知登记（原子性）；
4. 恢复后的发件箱与已提交事务一致，静止时全部通知均已确认；
5. 订单终态与最后提交的事件一致。

## 运行

需要 Node ≥ 22.6（利用内置类型擦除直接运行 TypeScript，无需编译）：

```sh
npm test    # 19 个用例：无故障基线、重复投递、14 个边界×相位故障注入、确认丢失、混沌测试
npm run demo
```

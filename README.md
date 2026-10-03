# Recoverable Two-Phase Commit Coordinator

离散事件环境下的 2PC 协调器（TypeScript / Node.js，零依赖）。
协调器与参与者均基于预写式日志（WAL）做状态转移，崩溃重启后重放日志、
向参与者查询状态并完成决议，杜绝孤儿事务泄漏资源。

## 运行环境

- Node.js >= 23（利用内置 TypeScript 类型擦除直接运行 `.ts`，无需编译）
- 测试使用 Node 内置测试运行器 `node:test`

## 快速开始

```bash
npm test    # 运行全部测试
npm run demo # 运行验收场景演示
```

## 架构

| 模块 | 职责 |
| --- | --- |
| `src/scheduler.ts` | 确定性离散事件调度器（虚拟时钟 + 优先队列） |
| `src/transport.ts` | 模拟网络：固定时延投递，发送方/接收方崩溃时丢消息 |
| `src/wal.ts` | 追加式预写日志（JSONL），先落盘后转移状态 |
| `src/audit.ts` | 持久化审计日志（未决事件） |
| `src/participant.ts` | 参与者：自带 WAL，幂等处理 PREPARE/DECISION/QUERY |
| `src/coordinator.ts` | 协调器：投票收集、超时、决策、重试、崩溃恢复 |

## 协议与恢复语义

协调器 WAL 记录：`BEGIN → VOTE* → DECISION → ACK* → END`。
所有状态转移先写 WAL 再发消息，因此任意时刻崩溃都可恢复：

- **恢复时存在 DECISION 记录**：重入 `COMMITTING`/`ABORTING`，
  向未确认的参与者发送 `QUERY`；参与者回复 `STATUS` 后，
  已应用决议的记为 ACK，未应用的重发决议，直至收齐 ACK 写入 `END`。
- **恢复时无 DECISION 记录**： presumptive abort —— 没有任何参与者
  可能已提交，直接记 `DECISION ABORT` 并通知所有参与者。
- **重复投票/重复 ACK**：按参与者去重，WAL 中只出现一次。
- **准备阶段超时**：未收齐投票即决策 ABORT。
- **决议后参与者无响应**：按 `retryInterval` 重发决议，达到
  `maxRetries` 后写入 `PENDING_PARTICIPANT` 审计事件；事务保持未决
  （绝不遗忘），待参与者恢复后由下一轮恢复流程完成，避免孤儿事务。

## 验收场景（`npm run demo`）

1. 三参与者全部投 YES，协调器写入 `DECISION COMMIT` 后、
   Commit 消息投递前崩溃（t=2.5）。
2. 协调器以新实例重启，重放 WAL 恢复为 `COMMITTING`，
   向在线参与者发 `QUERY` 并完成提交；离线的 p3 重试 3 次后
   记录 `PENDING_PARTICIPANT` 审计事件。
3. p3 恢复上线，再次恢复流程查询并完成提交，WAL 写入 `END`，
   全部参与者状态为 `COMMITTED`。

## 测试

`test/coordinator.test.ts` 共 8 个用例：正常提交、NO 票中止、
准备阶段参与者崩溃超时、重复投票/ACK 去重、**验收：发布 Commit 前
崩溃后恢复完成提交**、**验收：无响应参与者重试上限后记录未决审计
并最终完成**、准备阶段崩溃的 presumptive abort、参与者自身 WAL 恢复。

# 长事务补偿与状态机持久化引擎（Saga Engine）

零依赖 TypeScript 实现（Node.js ≥ 23.6，利用原生类型擦除直接运行 `.ts`），
用于编排跨越数小时的异步业务事务：步骤切分为「状态迁移 + 外部调用」，
超时或外部拒绝自动触发反向补偿 Saga，状态机以追加式日志持久化，断电续跑不重不漏。

## 运行

```bash
npm test    # Node 内置测试运行器（node:test），7 个用例
npm run demo # 验收场景演示：采购事务超时补偿 + 断电续跑
```

## 架构

```
src/types.ts    步骤/Saga 定义、状态机状态、审计日志事件类型
src/clock.ts    时钟抽象：SystemClock（生产）/ ManualClock（测试，可瞬间跨越数小时）
src/journal.ts  追加式 JSONL 持久化日志（同步写盘，容忍断电撕裂尾行）
src/engine.ts   SagaEngine：状态机推进、超时监控、反向补偿、崩溃恢复
examples/procurement.ts  验收场景演示
test/engine.test.ts      node:test 测试
```

### 状态机

- 步骤级：`PENDING → RUNNING → COMPLETED | WAITING → COMPLETED | FAILED | TIMED_OUT`，补偿时 `→ COMPENSATING → COMPENSATED`
- 事务级：`RUNNING → WAITING_EXTERNAL → … → COMPLETED`，失败/超时 `→ COMPENSATING → COMPENSATED`（补偿失败 `→ FAILED` 留待人工介入）

### 不重不漏的保证

- 每次状态迁移**先同步写日志再推进内存状态**，重启后 `recover()` 重放日志重建状态机；
- `RUNNING` 中崩溃：以恒定幂等键 `txId:stepName` 重放该步骤动作，外部系统据此去重（不重）；
- `WAITING_EXTERNAL` 中崩溃：外部请求已发出，只按原截止时间重挂超时定时器，不重复调用（不重）；
- `COMPENSATING` 中崩溃：跳过已完成的补偿步骤，从未完成处继续（不漏）；
- 银行迟到/重复回调：`resolveExternal` 校验状态后返回 `false` 并忽略。

### 审计轨迹

所有事件（`TX_STARTED`、`STEP_STARTED/COMPLETED/TIMED_OUT`、`COMPENSATION_*`、`TX_COMPENSATED` 等）
带序号与时间戳追加到 JSONL 日志，`engine.getAuditTrail(txId)` 可输出全链路可复核轨迹。

## 验收场景映射

`examples/procurement.ts` 第一部分即验收边界：发起长采购事务（审批 → 采购 → 付款），
付款阶段等待外部银行超时 → 超时监控唤醒 → 逆向取消采购单、通知审批人 →
事务终态 `COMPENSATED`，日志中留下 14 条有序审计事件。

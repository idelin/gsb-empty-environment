# 长事务补偿与状态机持久化引擎（Saga Engine）

TypeScript 实现的 Saga 编排引擎：面向跨越数小时的异步业务事务，将步骤切分为
「状态迁移 + 外部调用」，支持超时 / 外部拒绝触发的逆向补偿，状态机经 append-only
审计日志持久化，断电恢复后精确续跑（不重不漏）。

## 运行环境

- Node.js ≥ 22.6（直接运行 TypeScript，无需编译；开发使用 v23.9 验证）
- 无第三方依赖

```bash
npm test        # Node.js 内置测试运行器（node:test）
npm run example # 运行采购长事务验收样例
```

## 架构

- `src/types.ts` — 步骤定义、实例状态机、审计事件的类型。
- `src/store.ts` — append-only JSONL 事件存储，每条事件同步落盘。
- `src/engine.ts` — 引擎核心：
  - 状态机：`running → waiting → completed | compensating → compensated | failed`；
  - 每次状态迁移先写审计日志再生效，恢复时重放日志折叠出一致状态；
  - 超时监控：`waiting` 步骤带持久化 deadline，定时器触发或恢复时发现已过期即补偿；
  - 恢复语义：已完成步骤绝不重跑（`step_completed` 已落盘），等待中的步骤按原
    deadline 重新武装定时器，补偿从断点继续；跨步骤业务数据随事件快照持久化。
- `examples/procurement.ts` — 验收场景：审批 → 采购 → 付款，银行超时未回执，
  引擎唤醒超时监控，逆向取消采购单并通知审批人，打印全链路审计轨迹。
- `test/engine.test.ts` — 5 个用例：正向完成、付款超时补偿、外部拒绝补偿、
  断电续跑（不重不漏）、恢复时 deadline 已过期立即补偿。

## 验收样例输出（节选）

```
== 终态: compensated ==
== 全链路审计轨迹 ==
  step_started    step=审批   → step_completed
  step_started    step=采购   → step_completed
  step_started    step=付款   → step_waiting (deadline)
  timeout_fired   step=付款
  compensation_started  reason=步骤 付款 等待外部回调超时
  compensation_step_completed  step=采购   (取消采购单)
  compensation_step_completed  step=审批   (通知审批人)
  instance_compensated
```

## 测试结果

```
ℹ tests 5   ℹ pass 5   ℹ fail 0
```

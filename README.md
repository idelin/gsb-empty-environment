# saga-dag-executor

TypeScript 实现的 Saga 工作流执行器：按有向无环依赖图（DAG）调度步骤，失败时仅补偿
已成功且未补偿的步骤，并按反向依赖顺序执行补偿。所有状态迁移写入持久化事件表，
支持崩溃恢复与补偿重试；成功补偿绝不重复。

## 运行要求

- Node.js >= 22.6（利用原生 type stripping 直接运行 `.ts`，无需编译；推荐 >= 23.6 免 flag）

```sh
node --test test/executor.test.ts
# 或 Node 22.x：
node --experimental-strip-types --test test/executor.test.ts
```

## 核心语义

- **DAG 调度**：步骤声明 `dependsOn`，执行器按拓扑序分层并发执行；构造时校验
  重复步骤 id、未知依赖与环。
- **失败补偿**：任一步骤失败后停止调度新步骤，仅对状态为 `completed` 的步骤发起补偿，
  顺序为拓扑序的逆序（依赖者先补偿，被依赖者后补偿）。
- **补偿重试**：补偿失败写入 `compensation_failed` 事件并返回 `compensation_failed`；
  之后可用新实例调用 `recover()` 从事件表重建状态并重试失败的补偿。
- **补偿不重复**：已写入 `step_compensated` 的步骤在后续任何 `recover()` 中都会被跳过；
  工作流到达终态（`workflow_completed` / `workflow_failed`）后 `recover()` 是幂等空操作。
- **回执丢失与幂等**：步骤副作用生效后、回执事件落盘前进程崩溃时，事件表中没有
  `step_completed`，恢复后该步骤会被重放。步骤 id 即幂等键，下游系统凭它去重，
  保证副作用恰好应用一次。补偿回执丢失同理（`compensating` 状态会被重试）。

## 持久化事件表

`EventStore`（`src/eventStore.ts`）是追加式 JSONL 文件，每次写入后 `fsync`。
事件类型：

| 事件 | 含义 |
| --- | --- |
| `run_started` | 步骤开始执行 |
| `step_completed` | 步骤成功回执 |
| `step_failed` | 步骤业务失败 |
| `compensation_started` | 补偿开始 |
| `step_compensated` | 补偿成功回执 |
| `compensation_failed` | 补偿失败（可恢复重试） |
| `workflow_completed` / `workflow_failed` | 工作流终态 |

执行器不保存内存状态，步骤状态完全由事件表折叠（fold）推导，因此进程崩溃后
用同一事件文件构造新实例调用 `recover()` 即可续跑。

## 故障注入

`FaultInjector`（`src/faults.ts`）在四个注入点生效：

- `run`：步骤执行前注入业务失败；
- `run_receipt`：步骤副作用完成后、`step_completed` 回执落盘前注入崩溃（模拟回执丢失）；
- `compensate`：补偿执行前注入业务失败；
- `compensate_receipt`：补偿副作用完成后、`step_compensated` 回执落盘前注入崩溃。

`crash` 动作抛出 `SimulatedCrashError`，执行器不捕获、不落任何事件，模拟进程断电；
`fail` 动作抛出普通错误，按业务失败处理。每条规则可配置触发次数，耗尽后自动失效，
因此恢复路径天然不再受同一故障影响。

## 测试覆盖（`test/executor.test.ts`）

- **回执丢失**：`run_receipt` 崩溃 → 恢复重放，幂等步骤 id 保证副作用只应用一次；
- **分支成功**：分支 C 失败、B 成功 → 只补偿 B、A，顺序为反向依赖序，D 不再调度；
- **补偿失败恢复**：补偿注入一次失败 → `recover()` 重试成功，终态后再次 `recover()` 零新事件；
- **补偿回执丢失**：`compensate_receipt` 崩溃 → 恢复重试补偿，幂等键去重；
- **幂等步骤 id**：重复 id / 未知依赖 / 环在构造期被拒绝；
- **happy path**：全部成功后 `recover()` 为幂等空操作。

## 文件结构

- `src/events.ts` — 事件类型定义
- `src/eventStore.ts` — 追加式持久化事件表（JSONL + fsync）
- `src/graph.ts` — 拓扑排序与环检测
- `src/faults.ts` — 故障注入器与 `SimulatedCrashError`
- `src/executor.ts` — `SagaExecutor`：调度、补偿、恢复
- `test/executor.test.ts` — 故障注入核对测试

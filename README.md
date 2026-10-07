# Saga DAG Executor (TypeScript)

按有向无环依赖图执行步骤的执行器,带持久化事件表、反向补偿与故障注入测试。

## 设计

- **事件表**(`src/event-store.ts`):追加式 JSONL 持久化事件表,每个状态迁移
  (`StepStarted` / `StepSucceeded` / `StepFailed` / `CompensationStarted` /
  `CompensationSucceeded` / `CompensationFailed` / 运行级事件)先落盘再推进,
  崩溃不丢已确认的回执。
- **DAG 执行**(`src/executor.ts` + `src/graph.ts`):Kahn 分层拓扑,同层并发、
  跨层按依赖顺序;任一步骤失败即停止调度并进入补偿。
- **补偿**:只补偿"已成功且未补偿"的步骤,按反向依赖顺序(被依赖方后补偿)。
  补偿失败记录 `CompensationFailed` 并返回 `compensation_failed`,`resume()`
  可从断点重试;已成功的补偿凭事件表跳过,绝不重复。
- **幂等步骤 id**:每个步骤拿到确定性幂等键 `${runId}:${stepId}`。回执丢失
  (副作用已应用、回执未落盘)后恢复重试用同一幂等键调用,下游按键去重,
  副作用只应用一次。
- **故障注入**(`src/faults.ts`):在"副作用已应用、回执未持久化"等精确位置
  注入进程崩溃(`SimulatedCrash`)与补偿失败。

## 测试场景(`test/run-tests.ts`)

1. DAG 正常路径按层执行,无补偿。
2. 分支成功:右分支失败时,仅补偿已成功的步骤,顺序为反向依赖序
   (`left2 → right1 → left1 → root`),失败/未运行的步骤不补偿。
3. 成功回执丢失:崩溃后 `resume()` 用同一幂等键重执行,副作用仅一次,
   回执只补写一条。
4. 补偿失败可恢复重试;已成功的补偿不重复;完全补偿后 `resume()` 为 no-op。
5. 补偿回执丢失:重试补偿按键去重,补偿副作用仅一次。
6. 事件表跨执行器实例持久化,重载后不重复执行。

## 运行

```sh
npm test   # tsc -p tsconfig.json && node dist/test/run-tests.js
```

需要 TypeScript 编译器(开发时使用本机已有的 typescript@4.9.5 与 @types/node@18)。

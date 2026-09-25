# obligation-guardian

策略门控的义务执行器：策略允许后按依赖顺序执行可回滚义务，失败时逆序补偿，
补偿失败保留待处理状态，全部可回滚义务成功后才进入显式提交阶段执行不可回滚义务。

纯 TypeScript（Node >= 22.6 类型擦除直接运行），零依赖，测试基于 Node 内置 `node:test`。

## 运行

```sh
npm test      # node --test（12 个用例）
npm run sample
```

## 需求映射

| 需求 | 实现 |
| --- | --- |
| 策略允许后才执行，且不提前返回成功 | `authorizeAndRun` 仅在全部义务完成后返回 `committed`（src/executor.ts） |
| 按依赖顺序执行 | `dependsOn` 拓扑排序，环/未知依赖直接报错 |
| 失败逆序补偿 | 已执行义务逆序调用 `compensate()` |
| 补偿失败保留待处理 | 状态 `compensation_pending`，`pendingCompensations` 列出未完成项 |
| 补偿重试幂等 | 稳定幂等键 `executionId:obligationId:compensate` + 日志记录已完成补偿，恢复时不重复调用 |
| 待处理可恢复 | `resumePendingCompensations(executionId, ...)` |
| 不可回滚义务排在提交阶段 | `kind: 'non-rollbackable'` 仅在所有可回滚义务成功后执行；可回滚义务禁止依赖它 |
| 策略只读显式属性 | 条件仅按名读取传入属性，无外部身份服务 |
| 限有限集合/整数/布尔运算 | `in`（有限集合）、`eq/lt/lte/gt/gte`（整数）、`and/or/not` |

## 最终状态机

`denied`（策略拒绝，零副作用）→ 执行 → 失败则 `compensated` 或 `compensation_pending`；
全部成功进入提交阶段 → `committed`；提交阶段失败 → `commit_failed`（不回滚）。

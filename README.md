# obligation-saga

策略门控的义务执行器：策略允许后按依赖顺序执行可回滚义务，失败时逆序补偿；
补偿失败保留待处理状态；不可回滚义务只出现在明确的提交阶段；
只有提交阶段全部完成才返回最终成功（`committed`），绝不提前返回。

## 要求

- Node.js >= 23.6（原生运行 TypeScript，使用内置 `node:test`，零依赖）

## 设计

### 策略（`src/policy.ts`）

- 只读调用方显式传入的属性，且仅限 `requiredAttributes` 白名单内的键；不接外部身份服务。
- 条件被限制为三种形式：
  - 有限集合成员：`{ op: 'in', attr, set: [...] }`
  - 整数比较：`{ op: 'eq' | 'neq' | 'lt' | 'lte' | 'gt' | 'gte', attr, value }`，两侧都必须是整数
  - 布尔运算：`and` / `or` / `not`
- 属性缺失、非整数参与整数比较、读取白名单外属性，一律拒绝（`denied`）。

### 义务与计划（`src/obligations.ts`）

- `rollbackable` 义务：`execute` + 幂等 `compensate`，按 `dependsOn` 拓扑排序进入回滚阶段。
- `commit` 义务：不可回滚，只能排在明确的提交阶段；可回滚义务依赖不可回滚义务会在
  `buildPlan` 直接报错。依赖环、未知依赖、重复 id 同样报错。
- `CompensationLedger` 记录已补偿集合（幂等键）与待补偿队列，支持进程重启后恢复。

### 执行器（`src/executor.ts`）

流程：`策略评估 -> 回滚阶段（拓扑序） -> 明确提交点 -> 提交阶段 -> committed`

- 策略拒绝：返回 `denied`，不执行任何义务。
- 第 N 个义务失败：对已执行的 N-1 个义务按**逆序**补偿，返回 `compensated`。
- 补偿失败：按 `maxCompensationAttempts` 重试（补偿必须幂等，效果只发生一次）；
  仍失败则返回 `pending_compensation` 并在台账中保留待处理状态，
  之后可用 `resumePendingCompensation` 恢复。
- 提交阶段任一义务失败：异常向上抛出，**不会**返回 `committed`。

## 运行

```sh
npm test       # node --test（内置测试运行器）
npm run sample # node samples/demo.ts
```

## 运行样例（`npm run sample` 输出节选）

场景 1：策略允许，可回滚义务按依赖序执行，提交阶段后返回最终成功：

```json
{ "status": "committed", "executed": ["reserve-funds", "update-ledger"], "committed": ["send-notification"] }
```

场景 2：策略拒绝，无任何义务执行：

```json
{ "status": "denied", "reasons": [] }
```

场景 3：第二义务 `update-ledger` 失败，逆序补偿第一义务：

```json
{ "status": "compensated", "failedObligation": "update-ledger", "compensated": ["reserve-funds"] }
```

场景 4：补偿持续失败时保留待处理状态，恢复后幂等重试、效果只发生一次：

```json
{ "status": "pending_compensation", "pending": ["reserve-funds"] }
{ "status": "compensated", "pending": [], "effects": 1 }
```

## 测试结果（`npm test`，Node v23.9.0）

```
✔ denied policy runs no obligations and never reports success
✔ rollbackable obligations run in dependency order, commit obligations after the commit point
✔ second obligation failure compensates the first in reverse order
✔ compensation retries are idempotent: effect applied exactly once
✔ compensation failure keeps pending state; resume completes it later
✔ success is only final after the commit phase completes
✔ rollbackable obligation may not depend on a non-rollbackable one
✔ dependency cycles are rejected
✔ allows when finite-set, integer and boolean conditions all hold
✔ denies when finite-set membership fails
✔ denies when integer bound is exceeded
✔ denies when a required attribute is missing
✔ denies non-integer attribute under integer condition
✔ denies non-integer comparison value in the condition itself
✔ denies conditions that read attributes outside the explicit list
ℹ tests 15
ℹ pass 15
ℹ fail 0
```

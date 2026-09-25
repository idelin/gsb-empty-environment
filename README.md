# sod-assignment

在一次 `solve` 调用内检查若干岗位是否由**不同主体**承担（职责分离，SoD）。
联合建模：岗位数、主体别名、资格条件（策略）、例外与例外预算；输出完整可行
指派、最小冲突岗位集，或未决（pending）。已有指派变化时，先**最少换人**，
再按**成本**优化。小规模精确搜索，并附**独立验证器**。

纯 TypeScript，零依赖，使用 Node.js 内置测试运行器（`node:test`）。

## 运行环境

- Node.js ≥ 23.6（或 ≥ 22.18）：直接运行 `.ts`（类型擦除）。
- Node.js 22.6–22.17：加 `--experimental-strip-types`。
- 本仓库 `package.json` 未固定 Node；请用满足条件的解释器执行。

```bash
node --test          # 运行全部测试
node examples/demo.ts  # 运行样例
```

## 模型

- `Position`：岗位，可带 `requires` 资格条件。
- `Subject`：主体，`id` 为规范名，`aliases` 为别名；别名解析到同一主体，
  **不能绕过**职责分离。两个主体争抢同一别名直接报错。
- `PolicyExpr`：策略只读显式属性（闭世界：属性缺失即不满足），仅支持
  有限集合（`in`/`notIn`）、整数条件（`lt/lte/gt/gte/eq/neq`）与布尔运算
  （`and/or/not/has`）。不接触任何外部身份服务。
- `SoDException`：显式例外，允许某主体同时持有指定岗位集合。
- `exceptionBudget`：一次指派最多可使用的例外条数（默认 0）。
- `fixed`：硬性部分赋值（部分赋值的可满足性判定）。
- `existingAssignment` + `costs`：重规划目标——字典序先最小化换人岗位数，
  再最小化总成本。前任无法解析（如已离职）的岗位，任何新 holder 都计一次换人。

## 结果语义

`solve` 三态：

- `feasible`：完整可行指派（含 `changes`、`cost`、`exceptionsUsed`）。
- `infeasible`：即使例外不限量也不可解。附：
  - `conflictPositions`：最小冲突岗位集（去掉这些岗位即可恢复可行）；
  - `hallDeficiency`：资格图上的 Hall 缺口证人（|岗位集| > |合格主体并集|）。
- `pending`：只报未决，绝不放行——
  - 例外预算耗尽（可行解存在但超预算，附 `requiredExceptions`）；
  - 搜索节点预算耗尽（安全阀）。

`verify`（独立验证器，不复用求解器内部逻辑）三态：

- `valid`：完整且合规；
- `invalid`：硬性违例（未知主体、不满足资格、无例外覆盖的冲突、超预算）；
- `pending`：无违例但**存在未指派岗位**——未指派返回未决而非通过。

## 运行样例（`node examples/demo.ts` 节选）

```
=== 2. initiator == reviewer, no exception ===
{ "status": "invalid",
  "violations": ["positions \"initiator\" and \"reviewer\" are both held by subject \"alice\" with no covering exception"] }

=== 3. alias bypass attempt (initiator dave, reviewer d.wu) ===
{ "status": "invalid",
  "violations": ["positions \"initiator\" and \"reviewer\" are both held by subject \"dave\" with no covering exception", ...] }

=== 4. emergency: explicit exception within budget 1 ===
{ "status": "feasible",
  "assignment": { "initiator": "alice", "approver": "carol", "reviewer": "alice" },
  "exceptionsUsed": ["EX-7"] }

=== 5. same exception, budget 0 ===
{ "status": "pending",
  "reason": "exception budget (0) exhausted; a plan would need 1 exception(s): EX-7",
  "requiredExceptions": ["EX-7"] }

=== 6. repair after withdrawing the exception ===
{ "status": "feasible",
  "assignment": { "approver": "carol", "initiator": "alice", "reviewer": "bob" },
  "changes": 1, "cost": 6 }

=== 7. Hall deficiency (3 audit roles, 2 auditors) ===
{ "status": "infeasible", "conflictPositions": ["r1"],
  "hallDeficiency": { "positions": ["r1", "r2", "r3"], "subjects": ["s1", "s2"] } }

=== 8. partial assignment check ===
{ "status": "pending", "unassigned": ["reviewer", "approver"] }
```

## 测试结果（`node --test`，Node v23.9.0）

```
✔ initiator and reviewer held by the same subject without exception is rejected
✔ aliases resolve to the same subject and cannot bypass separation
✔ two subjects claiming the same alias are rejected outright
✔ Hall deficiency is reported when qualified subjects are too few
✔ alias merge creates a new conflict and replan repairs it with minimal changes
✔ explicit exception allows shared subject within budget; budget exhaustion is pending
✔ withdrawing an exception triggers minimal-change, cost-optimised repair
✔ personnel changes are minimised before cost, cost breaks ties
✔ partial assignments: satisfiable extension and hard infeasibility
✔ unassigned positions yield pending, never a pass
✔ policy language: finite sets, integer conditions, boolean combinations
✔ solver output always passes the independent verifier (seeded random agreement)
ℹ tests 12  ℹ pass 12  ℹ fail 0
```

覆盖需求要点：Hall 缺口、别名合并导致新冲突并重规划修复、撤回例外后的
计划修复、发起人=复核人无例外拒绝、别名不可绕过、未指派岗位未决、
预算耗尽只报未决、最少换人优先于成本、求解器输出经独立验证器复核
（200 轮种子随机一致性）。

## 代码结构

- `src/types.ts` — 模型与结果类型
- `src/policy.ts` — 策略求值器（显式属性 / 有限集合 / 整数条件 / 布尔运算）
- `src/solver.ts` — 精确回溯搜索、例外最小覆盖、Hall 缺口、最小冲突集、
  字典序（换人, 成本）优化
- `src/verifier.ts` — 独立验证器（独立实现的名字解析与例外核算）
- `examples/demo.ts` — 运行样例
- `test/sod.test.ts` — `node:test` 测试

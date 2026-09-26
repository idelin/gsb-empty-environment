# finite-constraints

TypeScript 约束库：字段相等、大小关系、条件必需、列表总和的实例校验，以及有限整数域上的可满足性求解。零运行时依赖，不借用任何现成验证器。

## 运行

```sh
npm test          # Node 内置测试运行器（node:test），Node >= 22.6 原生执行 TS
npm run example   # 运行样例 examples/run.ts
npm run typecheck # 使用本地 tsc 做严格类型检查（可选）
```

## 承诺的 schema 子集（只承诺这些，其余一律拒绝）

```
{ kind: "int", min?, max? }   { kind: "bool" }   { kind: "string" }
{ kind: "list", items }       { kind: "object", fields, required? }
```

未知 kind、未知关键字（如 `minimum`、`pattern`）产生 `unsupported-feature` 问题并附规则路径，绝不静默忽略。规则侧同理，仅支持 `eq` / `cmp` / `requiredIf` / `sum` / `when`。

## 校验管线（阶段顺序固定）

1. **coverage**：规则引用的每个路径必须被 schema 描述，否则报 `uncovered-path`；
2. **type**：类型与存在性检查，类型/越界错误会阻断关系计算（类型检查先于关系计算）；
3. **relation**：三值逻辑求值规则。

## 缺失值语义

- 缺失 = 键/下标不存在，与 `null`、`0`、`false`、空列表严格区分，**绝不零填充**；
- 缺失先按类型与存在性解释（`missing-required`），关系再按三值逻辑处理；
- 缺失操作数使关系为 unknown，报 `unknown-operand`；
- 条件为 unknown 报 `unknown-condition`，**不会当作 false 悄悄跳过**；
- 空列表求和为 0；缺失列表的求和是 unknown，不是 0。

## 错误模型

每个问题同时携带实例路径与规则路径；大小关系违反（如结束小于开始）在 `related` 中报告双方实例路径：

```
[relation] order violated: 9 does not satisfy le 5 instance=/start rule=/rules/0 \
  related(instance=/start, left operand) related(instance=/end, right operand)
```

## 求解器

有限整数域上的约束：`eqConst`、`eqVar`、`diff`（x − y ≤ c）、`sum`（Σ ≤/≥/= k）、`when`（条件分支），差分与总和约束可联立。三种结果明确分开：

- `sat`：给出模型（见证），并用直接求值复核；
- `unsat`：给出**子集最小冲突**（删除式极小化；移除其中任意一条即恢复可满足）；
- `budget-exceeded`：预算（搜索节点/传播步数）不足，与不可满足严格区分。

## 增量更正与传播安全性

`IncrementalSolver` 按 id 断言/撤回约束，撤回即完全移除其影响（无残留传播状态）。`verifyByEnumeration()` 用独立穷举（直接求值、不经过传播）交叉核验传播求解器的判定，不一致则报 `mismatch`。

## 样例输出（npm run example）

```
=== 1. end < start: violation reports BOTH instance paths and the rule path ===
  [relation] order violated: 9 does not satisfy le 5 instance=/start rule=/rules/0 related(instance=/start, left operand) related(instance=/end, right operand)
=== 4. missing list is NOT zero-filled: sum is unknown, not 0 ===
  [missing-required] required field "items" is missing instance=/items rule=/schema/required
  [unknown-operand] list /items is missing; its sum is unknown ... instance=/items rule=/rules/2
=== 7. sat: difference + sum constraints solved together ===
  {"status":"sat","model":{"s1":1,"s2":4,"s3":7},"nodes":4}
=== 8. unsat: subset-minimal conflict (irrelevant constraints excluded) ===
  {"status":"unsat","conflict":["fix-x","x-before-y"],"nodes":1}
=== 9. budget-exceeded is distinct from unsat ===
  tiny budget:  {"status":"budget-exceeded","nodes":2}
=== 10. incremental: retracting a constraint withdraws its influence ===
  with both:      {"status":"unsat","conflict":["gap","fix"]}
  enumeration:    {"status":"verified","spaceSize":25,"sat":false}
  after retract:  {"status":"sat","model":{"x":0,"y":3}}
  enumeration:    {"status":"verified","spaceSize":25,"sat":true}
```

## 测试结果（npm test，node --test）

```
ℹ tests 29
ℹ pass 29
ℹ fail 0
```

覆盖：相等/大小/条件必需/列表总和校验、双方路径报告、unknown 条件不跳过、空列表 vs 缺失列表、类型先于关系、子集最小冲突的 1-极小性、预算与不可满足分离、增量撤回、枚举交叉核验、条件分支求解。

## 代码结构

- `src/schema.ts` — schema 子集编译 + 类型/存在性检查
- `src/rules.ts` — 规则 AST 与编译（未知种类即拒绝）
- `src/evaluate.ts` — 三阶段校验管线（coverage → type → relation）
- `src/solver.ts` — 传播 + 回溯求解、预算、子集最小冲突
- `src/incremental.ts` — 增量断言/撤回 + 独立枚举核验
- `src/issues.ts` — 错误模型（实例路径 + 规则路径 + 相关路径）

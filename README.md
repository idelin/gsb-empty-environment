# 最小代价 Schema 修复建议器（TypeScript，零第三方依赖）

自研 schema 校验 + 最小代价修复补丁搜索。**不借用任何现成验证器**（手写校验、手写候选生成、手写穷举与回滚），使用 Node.js 内置 `node:test` 验证。

## 只承诺实现的 schema 子集

| 关键字 | 含义 |
| --- | --- |
| `type` | `object` / `string` / `integer` / `number` / `boolean`（不含 array） |
| `properties` / `required` | 嵌套对象与必填字段 |
| `enum` | 字符串/数字枚举（用于近似匹配与有序枚举比较） |
| `nullable` | 是否允许 `null` |
| `requires`（字段级） | 跨字段存在性约束：该字段存在 => 目标字段必须存在 |
| `compare`（字段级） | 跨字段数值/有序枚举比较：`<` `<=` `>` `>=` |

子集之外的任何关键字/类型在 `compileSchema` 阶段**立即显式抛错**，不静默忽略。

## 修复建议分级

- **确定（certain）**：语义无损或机械安全，如数字字符串 `"12" -> 12`、唯一最近枚举、`number/boolean -> string`、删除非必填源字段。
- **需人工（human）**：可能改变语义或存在多种等代价选择，系统只并列建议、**不自动任选**：
  - `'001' -> 1` 可能丢失前导零编码语义；
  - 浮点转整数：截断 / 四舍五入两种等代价方式并列；
  - `'true'/'false' -> boolean` 语言相关；
  - 多个近似枚举编辑距离并列；无近似时枚举全体列为候选；
  - 缺失字段若无枚举可依，只能人工提供具体值（`pending`），**绝不注入隐藏默认值**。

每条补丁携带 `preconditionValue`（应用前置值）与 `originRulePath`（来源规则路径）。

## 最小代价合法补丁集

- 候选按“目标实例路径”归为互斥组（一个路径一次只能选一个补丁）；候选会相互影响跨字段约束，因此**不逐字段贪心**，而是 DFS 穷举所有 `∏(组大小+1)` 个组合，每个组合应用后**重新跑完整校验**。
- 叶节点必须消除**全部**初始错误（不能只修最先报错的字段）且不引入新错误，才算合法。
- 用分支定界剪枝；所有达到最优代价的**不同方案全部保留**（平局去重后返回，上限 64 个）。
- 状态：`valid` / `optimal-proven` / `optimal-pending-human` / `no-candidate` / `budget-exhausted`。
- 有限实例下穷举全部基本修复，最优性经核验；超过 `budget`（默认 20000 个组合）时中断，返回当前**可行上界**、未决人工路径与未完整展开的候选组，`optimalCost` 置空。

## 原子应用与回滚

`applyPlan` 在深拷贝上工作（**不自动改变输入**）：逐个校验补丁前置值，任一失败或应用后重新验证不合法，则**整体回滚**。含 `pending` 的计划拒绝自动应用，需 `providePendingValues` 人工供值后重新验证。

## 错误定位

每个错误同时给 `instancePath`（JSON Pointer）与 `rulePath`（schema 规则位置），
例如 `/maxAmount` 上的 `COMPARE_FAILED` 对应规则 `/properties/maxAmount/compare`。

## 运行

需要 Node ≥ 22.6（类型擦除直接运行 `.ts`；本仓库在 Node v23.9.0 验证）。

```bash
npm test          # node --test tests/repair.test.ts
npm run demo      # node samples/demo.ts，打印五个运行场景
```

## 代码结构

- `src/types.ts` — schema、错误、候选、计划、结果的类型定义
- `src/validate.ts` — schema 子集守门 + 手写递归校验器
- `src/paths.ts` — JSON Pointer、深拷贝、按路径读写/删除
- `src/candidates.ts` — 编辑距离、各错误类型的基本修复候选、跨字段两条修复路线、同组合并
- `src/search.ts` — 分组、DFS 穷举、分支定界、预算中断、平局保留、pending 判定
- `src/apply.ts` — 原子应用、前置值校验、整体回滚、人工供值
- `samples/demo.ts` — 运行样例
- `tests/repair.test.ts` — `node:test` 内置测试

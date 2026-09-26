# fd-constraint-kit

TypeScript 规则求值器 + 有限整数域约束求解器。零依赖，不借用任何现成验证器；
使用 Node.js 内置测试运行器（Node ≥ 23.6，原生 type-stripping，无需编译）。

```bash
npm test        # node --test test/*.test.ts
npm run sample  # node examples/sample.ts
```

## 承诺的 schema 子集（仅此五种规则，其余一律拒绝）

| 规则 | 含义 |
|---|---|
| `equals`   | 字段/字面量相等（类型须一致） |
| `compare`  | 大小关系 `< <= > >= == !=`（排序要求双方为 number） |
| `requiredIf` | 条件必需：条件为真时字段必须存在 |
| `sum`      | 列表求和（可选 `itemPath`），与 `== / <= / >=` 上限比较 |
| `typeIs`   | 字段类型断言 |

求解器约束子集：`eq`（相等）、`diff`（差分 `a-b ⋈ c`）、`sum`（带系数线性和）、
`implies`（条件分支：前提成立才施加内部约束）。

## 缺失值语义

- 缺失按**存在性与类型**解释：缺失 ≠ null ≠ 空列表，**绝不零填充**。
- 关系运算中任一操作数缺失 ⇒ 结果 `unknown`，**不是 false**。
- 条件为 `unknown` 时显式上报（`severity: "unknown"`），**不当 false 悄悄跳过**。
- **空列表求和为 0**；**缺列表**是 `unknown`，与 0 严格区分。
- 类型检查**先于**关系计算：类型不匹配是 `type-error`，不做任何隐式转换。

## 错误报告

每个 issue 同时携带：

- `instancePath`：实例路径（如 `/items/1/price`）
- `rulePath`：规则路径（如 `/rules/0`、`/rules/1/when`）
- `relatedPaths`：关系两端的路径——`end < start` 时双方路径都报告

## 求解器语义

- 有限整数域上判可满足：`sat`（给出模型/见证）、`unsat`、`unknown` 三态。
- **预算不足与不可满足明确分开**：预算耗尽返回
  `{ status: "unknown", reason: "budget-exhausted" }`，永不混入 `unsat`。
- `unsat` 时可提取**子集最小冲突**（删除式极小化；预算被打断时 `exact: false`）。
- 传播是界一致性（sound，不完备），叶节点用全量检查验证模型。

## 增量更正与独立核验

- `ConstraintStore` 按 id 增删约束；**撤回即完全撤销旧约束影响**（从活动集重解，无残留传播效果）。
- `checkVerified()` 用**独立枚举器**（与传播代码零共享）交叉核验求解结论，
  确认传播安全性；`sat` 模型还会回代全部约束验证。

## 目录

```
src/paths.ts       实例导航与缺失语义
src/rules.ts       规则 schema 子集定义
src/evaluator.ts   三态求值器（类型先行）
src/solver.ts      有限域求解器（传播 + 搜索 + 预算）
src/enumerator.ts  独立暴力枚举器（核验用）
src/store.ts       增量约束库 + 子集最小冲突
test/              node:test 测试（33 个）
examples/sample.ts 端到端样例
```

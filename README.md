# Pair-wise SoD Role Checker

在单次操作内判定若干角色（岗位）是否由不同主体承担的小规模精确求解器，使用 TypeScript 编写、Node.js 内置测试运行器验证。

## 建模

- **岗位数**：`positions` 中显式声明有限岗位集合，每个岗位带资格条件 `required` 与可选候选池 `candidates`（含首选人与替代人）。
- **主体别名**：`aliases` 把任意名字归并到唯一规范主体；支持别名链、环检测。别名映射到同一主体不能绕过分离规则。
- **资格条件**：只读主体的显式属性（`attrs`），支持有限集合（`in`/`eq`）、整数比较（`lte`/`gte`）与布尔运算（`and`/`or`/`not`）；缺属性/类型不符按 false 封闭判定，无任何外部身份服务。
- **成对分离规则**：`sodGroups` 中同一主体最多占一个岗位，除非有显式例外。
- **例外预算**：每个 `Exception` 带非负整数成本，`exceptionBudget` 限制单份指派可消耗的总成本。

## 两个入口

- `checkAssignment(problem, assignment)`：操作级三值检查。
  - `rejected`：未知主体、资格不符、超出候选池、或合并别名后出现无例外的同体冲突；
  - `undecided`：存在未指派岗位（永远不会算通过），或唯一可行读法所需例外成本超出预算（预算耗尽，只报未决）；部分赋值额外给出 `extendable` 与见证完整指派 `witness`；
  - `approved`：完整、合格、在池内、且全部冲突由预算内例外支付。
- `solveAssignment(problem, previous?, options?)`：精确回溯（MRV + 成本排序 + 字典序分支限界）。
  - 有预算内完整解：`satisfied`，按 **(换人数量 → 指派成本 → 例外成本 → 例外条数)** 优化，即已有指派变化时先最少换人、再按成本优化；
  - 只有超预算解：`undecided`，不返回“已决”指派；
  - 硬不可解：`infeasible`，枚举返回**最小基数冲突岗位集** `conflictSet`。

## 独立验证器

`src/verifier.ts` 独立重算语义（不信任检查器/求解器的结论）：重新归并别名、重算资格/候选池/冲突/例外成本，并验证见证指派、换人计数、成本与冲突核。所有返回给用户的结论都经验证器复核；测试中还包含对结论的篡改用例，验证器必须拒绝。

## 运行

要求 Node.js ≥ 22.6（本仓库在 Node 23 上以原生 TypeScript 剥离执行，无需安装依赖）。

```bash
npm test          # node --test 运行全部测试
npm run example   # 五个场景的可运行样例
```

## 目录

- `src/model.ts` 数据模型与条件语言
- `src/policy.ts` 属性条件求值、别名归并、候选域
- `src/validate.ts` 结构校验
- `src/search.ts` 精确搜索、修复优化、最小冲突核
- `src/checker.ts` 操作级三值检查
- `src/verifier.ts` 独立验证器
- `test/*.test.ts` node:test 用例
- `examples/run.ts` 运行样例

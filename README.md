# oneof-anyof-diagnoser

自实现的 JSON Schema **子集**验证器（不借任何现成验证器），重点解决
`oneOf` / `anyOf` 的诊断质量：失败时给出**有解释力的最小错误集**，成功分支
不被失败分支的错误污染，并对诊断做**独立有限实例充分性验证**。

零依赖，TypeScript 源码由 Node.js（>= 23.6）内置类型擦除直接运行，
测试使用 Node.js 内置测试运行器（`node:test`）。

## 承诺实现的 schema 子集（只承诺这些）

- 断言：`type`（含 `integer` 与类型数组）、`enum`、`const`、`minLength`、`maxLength`、
  `pattern`、`minimum`、`maximum`、`exclusiveMinimum`、`exclusiveMaximum`、`multipleOf`、
  `minItems`、`maxItems`、`required`
- 应用器：`properties`、`items`（单 schema 形式）、`additionalProperties: false`、
  `oneOf`、`anyOf`
- 布尔 schema（`true` / `false`）
- **不支持** `$ref`、`allOf`、`not`、`if/then/else`、`format`、`dependentSchemas` 等；
  未实现的关键字一律忽略（见 `test/validator.test.ts`）

## 语义与设计

- **分支求值**：`oneOf` 恰好一个分支成功才通过；两个分支都成功即失败并给出
  **歧义见证**（`witness`：实例路径 + 全部成功分支序号，实例本身即有限见证）。
  `anyOf` 一个分支成功即通过。
- **成功不被污染**：组合子一旦满足，失败分支的错误被整体丢弃，不上浮。
- **最小错误集**：所有分支失败时，把各分支错误按**规则身份**
  （`keyword + 实例路径 + 规则参数`，与规则书写位置无关）去重——不同分支里
  相同的规则失败合并为一个共享错误（`alsoAt` 记录其余规则路径），然后求
  **最小命中集**：能击中每个失败分支的最少错误集合，保证"所有失败分支均不成立"
  这一结论可被该集合说明。
- **最小覆盖或未证最优界限**：命中集精确求解（分支限界 + 不相交打包下界），
  搜索预算耗尽时退化为贪心解并返回 `cover: { optimal: false, lowerBound, upperBound }`，
  绝不把未证最优的结果声称成最优。
- **错误数额度的诚实性**：`maxErrors` 截断 `selectedErrors` 时，若某失败分支
  因此失去全部证据（唯一能否定它的证据被截掉），诊断标记 `complete: false`
  并列出 `uncoveredBranches`，不声称完整；只截掉冗余证据时仍为 `complete: true`。
- **双路径错误**：每个错误同时携带 `instancePath`（实例 JSON Pointer）与
  `schemaPath`（规则 JSON Pointer）；分支级原始错误保留各自路径
  （`diagnosis.branches[i].errors`）。
- **独立充分性验证**：`verifyDiagnosis(schema, instance, errors)` 独立重算——
  逐条原子错误在实例路径处的有限子实例上重放规则；逐分支重新验证并检查
  选中集确实击中每个失败分支（或与不完整声明一致）；歧义见证分支逐一复验。

## 运行

```sh
npm test     # node --test 'test/*.test.ts'
npm run demo # node examples/demo.ts
```

## 运行样例（`npm run demo` 输出节选）

样例 2：`contact` 同时满足两个 `oneOf` 分支 → 失败并给出歧义见证：

```json
{
  "instancePath": "#/contact",
  "schemaPath": "#/properties/contact/oneOf",
  "keyword": "oneOf",
  "message": "matches 2 of 2 branches, expected exactly 1",
  "diagnosis": {
    "reason": "multiple-branches-matched",
    "matched": [0, 1],
    "witness": { "instancePath": "#/contact", "matchedBranches": [0, 1] }
  }
}
```

样例 3：`contact: {"phone":"12"}` 两个分支都失败 → 最小错误集（每条都含双路径，
`cover.optimal: true` 证明最小）：

```json
"selectedErrors": [
  { "instancePath": "#/contact",
    "schemaPath": "#/properties/contact/oneOf/0/required",
    "keyword": "required", "message": "missing required \"email\"" },
  { "instancePath": "#/contact/phone",
    "schemaPath": "#/properties/contact/oneOf/1/properties/phone/minLength",
    "keyword": "minLength", "message": "length 2 < minLength 7" }
],
"cover": { "optimal": true, "lowerBound": 2, "upperBound": 2, "sharedErrors": 0 }
```

样例 5：同一实例加 `{ maxErrors: 1 }` → 截掉了分支 1 的唯一证据，诊断诚实降级：

```json
"selectedErrors": [ { "keyword": "required", "...": "..." } ],
"complete": false,
"uncoveredBranches": [1]
```

全部 5 个样例的 `verifyDiagnosis` 结果均为 `sufficient: true`（含样例 5——
验证器确认其"不完整"声明与独立重算一致）。

## 测试结果（`npm test`，Node v23.9.0）

```text
✔ anyOf: one successful branch is enough, no error surfaces
✔ oneOf: exactly one success passes and is not polluted by failed-branch errors
✔ oneOf: two successful branches fail with an ambiguity witness
✔ anyOf: shared rule deduplicates to a single covering error
✔ oneOf zero matches: minimal set refutes every branch, branch errors keep own paths
✔ minimal cover prefers a shared error over the naive union
✔ error budget: truncating sole evidence of a branch drops completeness
✔ error budget: truncating only redundant evidence stays complete
✔ verification rejects a tampered diagnosis
✔ exact minimum on a known instance
✔ shared candidate covers all sets
✔ budget exhaustion returns unproven bounds instead of a false optimum
✔ infeasible input (failed branch without errors) throws
✔ type error carries instance path and rule path
✔ required / enum / const / pattern / range keywords
✔ items and additionalProperties
✔ boolean schemas
✔ unknown keywords are ignored (only the implemented subset is promised)
✔ empty universe is trivially optimal
✔ verification rejects an unreproducible atomic error
ℹ tests 20
ℹ pass 20
ℹ fail 0
```

## 目录结构

- `src/validator.ts` — 子集验证器与 `oneOf`/`anyOf` 求值、最小错误集组装
- `src/cover.ts` — 最小命中集：精确分支限界（带节点预算）/ 贪心 + 打包下界
- `src/verify.ts` — 诊断充分性的独立有限实例验证
- `src/keywords.ts` — 原子关键字检查（验证器与验证器共用同一实现）
- `src/pointer.ts` — JSON Pointer 工具
- `examples/demo.ts` — 5 个运行样例
- `test/` — `node:test` 测试（20 个）

# 行级安全谓词排序

数据库行级安全（RLS）策略在查询计划下推谓词时，用户自定义函数若先于策略谓词执行，就能经错误消息或副作用泄漏被隐藏的行。本包为 TypeScript 查询规划组件提供谓词排序与计划校验。

## 核心规则

- 策略谓词与用户谓词分开处理。
- 所有 `PERMISSIVE` 策略以 `OR` 合并；`RESTRICTIVE` 策略以 `AND` 合并到该结果。
- 没有任何 `PERMISSIVE` 策略时，策略门为常量 `false`，即默认拒绝。
- 只有所有函数都被标记为 `leakproof` 的用户谓词，才能排在策略门之前。
- 非 leakproof 用户谓词位于完整策略门之后；校验接口会拒绝不满足该条件的既有计划。

## 基本用法

```ts
import {
  mergePolicies,
  orderPredicates,
  planPredicateOrder,
  validatePredicateOrder,
} from "./index.ts";

const policies = [
  {
    id: "tenant_policy",
    kind: "policy",
    policyMode: "permissive",
    expression: "tenant_id = current_setting('app.tenant')::int",
  },
  {
    id: "not_deleted",
    kind: "policy",
    policyMode: "restrictive",
    expression: "deleted_at IS NULL",
  },
];

const userPredicates = [
  {
    id: "builtin_filter",
    kind: "user",
    expression: "amount > 100",
  },
  {
    id: "leaky_filter",
    kind: "user",
    expression: "leaky_probe(amount)",
    function: { name: "leaky_probe", leakproof: false },
  },
];

const order = orderPredicates({ policies, userPredicates });
// ["builtin_filter", "tenant_policy", "not_deleted", "leaky_filter"]

const plan = planPredicateOrder({ policies, userPredicates });
plan.policyExpression;
// "(tenant_id = current_setting('app.tenant')::int AND deleted_at IS NULL)"

validatePredicateOrder(order); // true；不安全计划抛出 UnsafePredicateOrderError
```

也可以分别传入策略数组与用户谓词数组：

```ts
orderPredicates(policies, userPredicates);
mergePolicies(policies);
```

## 谓词元数据

规范字段：

- `id`：谓词标识。
- `kind`：`"policy"` 或 `"user"`。
- `policyMode`：策略谓词的 `"permissive"` 或 `"restrictive"`。
- `expression`：SQL 谓词表达式。
- `leakproof`：用户谓词整体是否安全，可省略。
- `function` 或 `functions`：`{ name, leakproof }` 形式的函数元数据。

兼容别名包括 `type`、`predicateType`、`policyType`、`policyKind`、`expr`、`sql`、`fn`、`functionNames`、`isLeakproof` 等。未知函数按非 leakproof 处理，不会提前执行。内置比较运算符（如 `=`、`<`、`>=`）默认视为 leakproof；也可通过 `functionRegistry` 注入函数目录。

## 默认拒绝

没有 `PERMISSIVE` 策略时，`mergePolicies` 返回 `defaultDeny: true` 和表达式 `false`。返回的执行顺序中会插入合成谓词 `__rls_default_deny__`，保证非 leakproof 用户谓词在常量 `false` 门之后执行。可用 `includeDefaultDenyPredicate: false` 关闭合成项，但此时仍应使用 `policyExpression === "false"` 执行拒绝。

## 计划校验

```ts
validatePredicateOrder([
  userPredicates[1], // 非 leakproof
  policies[0],
]); // throws UnsafePredicateOrderError
```

`checkPredicateOrder` 返回结构化的 `valid` 与 `violations`，`isPredicateOrderSafe` 返回布尔值。`planPredicateOrder({ ..., proposedOrder })` 也会先审计既有执行顺序。

## 测试

```sh
npm test
```

测试覆盖泄漏函数下推、内置 leakproof 比较、混合 PERMISSIVE/RESTRICTIVE 策略，以及无策略和无 PERMISSIVE 策略时的默认拒绝。

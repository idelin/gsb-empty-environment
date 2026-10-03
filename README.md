# contract-compat

判断**新契约是否接受所有旧契约允许的值**（即旧契约接受集 ⊆ 新契约接受集）。
纯 TypeScript 实现，零依赖，不借用任何现成验证器；测试使用 Node.js 内置
`node:test`（Node ≥ 23.6 原生运行 `.ts`）。

## 判定语义

`compareContracts(oldSchema, newSchema)` 返回三种结论之一：

- `compatible`：在支持的 schema 子集内**证明**了 old ⊆ new。
- `breaking`：给出具体反例。每个 issue 同时包含
  - `instancePath`：实例中的位置（JSON Pointer，如 `/items/0`）；
  - `rulePath`：新契约中拒绝该值的规则路径（如 `/properties/a/maximum`）；
  - `counterexample`：一个旧契约接受、新契约拒绝的具体值。
- `unknown`：任一契约使用了支持子集之外的关键字（如 `pattern`、`format`、
  `oneOf`、`$ref`……）或关键字取值非法。**无法证明时不报兼容**。
  例外：旧契约完全受支持且新契约的受支持部分已能产生反例时，仍报
  `breaking` 并附上 `unsupported` 说明（被受支持关键字拒绝 ⇒ 整体必拒绝）。

典型结论示例：收紧 `maximum` 是破坏（反例为旧最大值）；新增可选字段
**未必**破坏——旧契约 `additionalProperties:false` 时兼容，否则破坏；
`integer` 区间相对有限 `enum` 可精确判定包含关系。

## 支持的 schema 子集（只承诺这些）

- `type`：`null | boolean | integer | number | string | array | object`
  （单个或数组；`integer` ⊆ `number`）
- `enum` / `const`（有限枚举；`const` 折入枚举语义）
- 数值区间：`minimum` / `maximum` / `exclusiveMinimum` / `exclusiveMaximum`
  （数值或 draft-4 布尔形式）
- 字符串长度区间：`minLength` / `maxLength`（按码点计）
- 数组大小区间：`minItems` / `maxItems`；`items`（单一子 schema）
- 对象：`properties` / `required` / `additionalProperties`
- 布尔 schema：`true`（接受一切）/ `false`（拒绝一切）

其余关键字一律视为未支持 → `unknown`，绝不当作兼容。

## 方法概述

1. 旧侧有 `enum`/`const`：接受集有限，逐一用内置求值器 `accepts` 对新契约验证。
2. 新侧有 `enum`/`const`：接受集有限（大小 N）。对旧契约做**互异接受值**的
   惰性枚举；由鸽巢原理，若存在反例，必在 N+1 个值内出现；枚举耗尽则兼容。
3. 否则结构化比较：值类型集合包含、数值/长度/大小区间包含（含开闭端点）、
   `items` 与 `properties`/`required`/`additionalProperties` 递归比较；
   每个违例都构造出通过旧契约、被新契约拒绝的见证值，并把它提升为
   完整的数组/对象反例。

## 运行

```sh
npm test     # node --test（27 个用例）
npm run demo # 8 个典型场景的运行样例
```

```ts
import { compareContracts } from "./src/index.ts";

const r = compareContracts(
  { type: "integer", minimum: 0, maximum: 100 },
  { type: "integer", minimum: 0, maximum: 50 },
);
// r.status === "breaking"
// r.issues[0] = { instancePath: "", rulePath: "/maximum",
//                 counterexample: 100, message: "..." }
```

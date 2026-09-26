# defaulting-validator

一个零依赖的 JSON 校验 + 默认值填充库（TypeScript，Node.js 内置 test runner 验证）。
**不借用任何现成验证器**，只承诺并只实现下文列出的 schema 子集。

## 语义承诺

- **只对缺失字段补默认值**：以 `Object.hasOwn` 判定“缺失”。显式 `null` 不等于缺失，不会被默认值覆盖，且仍参与类型校验。
- **默认表达式可依赖其他字段，按拓扑序求值**：`default: { $expr: ... }` 中的 `$get` 引用同层对象字段；引擎先对同层待补默认值做拓扑排序再逐个求值。
- **依赖环报链**：如 `x -> y -> z -> x`，以 `defaultCycle` 错误报出完整环链。
- **校验失败整体回滚**：所有默认值先写在输入的深拷贝上；任何失败（含环、表达式求值失败、校验失败）都返回错误列表，调用方持有的输入绝不被修改。
- **嵌套数组元素互不共享可变引用**：每个默认值在物化时都经 `structuredClone` 深拷贝，数组各元素拿到的默认对象互不相干，schema 里的默认字面量也不会被污染。
- **错误同时给实例路径与规则路径**：`instancePath` 为 JSON Pointer（如 `/lines/0/sku`），`schemaPath` 为 `#` 前缀的 JSON Pointer（如 `#/properties/lines/items/properties/sku/pattern`）。

## 支持的 schema 子集（仅此这些关键字）

| 适用范围 | 关键字 |
| --- | --- |
| 通用 | `type`、`enum`、`const`、`default` |
| object | `properties`、`required`、`additionalProperties`（仅布尔） |
| array | `items`（单 schema）、`minItems`、`maxItems` |
| string | `minLength`、`maxLength`、`pattern`（非锚定，同 JSON Schema） |
| number/integer | `minimum`、`maximum`、`exclusiveMinimum`、`exclusiveMaximum` |

`type` 取值：`object | array | string | number | integer | boolean | null`（`integer` 是 `number` 的子类型）。

## 默认表达式

```jsonc
{
  "displayName": {
    "type": "string",
    "default": { "$expr": { "op": "concat", "args": [{ "$get": "firstName" }, " ", { "$get": "lastName" }] } }
  }
}
```

- `{ "$get": "a.b" }`：引用当前对象（可点路径下钻）的字段。
- 操作符：`concat`、`add`、`sub`、`mul`、`div`、`coalesce`、`literal`（`literal` 用于转义恰好形如表达式的字面量）。
- 非 `{ "$expr": ... }` 形态的 `default` 一律按字面量处理。
- 引用缺失字段报 `default` 错误；`$get` 路径首段构成拓扑排序的依赖边。

## API

```ts
import { applyDefaultsAndValidate } from "./src/index.ts";

const result = applyDefaultsAndValidate(schema, instance);
// { ok: true, value } —— value 是填好默认值的新对象，instance 未被触碰
// { ok: false, errors: [{ instancePath, schemaPath, keyword, message }] } —— 整体回滚
```

## 运行

```sh
npm test     # node --test（Node >= 22.6，利用原生 type stripping，无需编译）
npm run demo # 运行 examples/demo.ts 样例
```

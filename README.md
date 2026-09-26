# json-defaults-validator

零依赖的 JSON Schema **子集**校验器 + 默认值填充器（TypeScript，Node.js 内置 `node:test` 测试，不借用任何现成验证器）。

## 运行要求

- Node.js >= 22.6（利用原生 type stripping 直接运行 `.ts`，开发环境为 v23.9）

```bash
npm test     # node --test test/*.test.ts
npm run demo # node examples/demo.ts
```

## 支持的 schema 子集（只承诺这些）

| 类别 | 关键字 |
| --- | --- |
| 类型 | `type`: `object` / `array` / `string` / `number` / `integer` / `boolean` / `null` 或其数组（联合类型） |
| 对象 | `properties`、`required`、`additionalProperties`（仅布尔值） |
| 数组 | `items`（单一 schema）、`minItems`、`maxItems` |
| 字符串 | `minLength`、`maxLength`（按码点计）、`pattern` |
| 数值 | `minimum`、`maximum`、`exclusiveMinimum`、`exclusiveMaximum` |
| 通用 | `enum`、`const`、`default` |

不支持：`$ref`、`allOf/anyOf/oneOf/not`、`format`、`patternProperties`、schema 形式的 `additionalProperties` 等。非法 `pattern` 或未知 `type` 会抛 `SchemaError`（属于 schema 编写错误，而非校验错误）。

## 默认值语义

- `default` 可以是字面量，也可以是引用表达式 `{ "$from": "<pointer>" }`：
  - 绝对指针：`"/username"`（从文档根解析）
  - 兄弟相对指针：`"./firstName"`（相对当前字段所在对象，数组元素内同样适用）
- 只对缺失字段补默认值：键不存在才算缺失；显式 `null` 是已存在的值，不触发默认值（且仍需通过类型校验）。
- 拓扑求值：`$from` 依赖按需递归解析，与属性声明顺序无关；被依赖字段自身若缺失且有默认值，会先被求值。
- 依赖环报链：如 `a -> b -> a`，错误消息包含完整环链。
- 不共享可变引用：每次应用默认值都深拷贝，数组各元素的默认对象/数组互不影响，也不会污染 schema 中的默认值定义。
- 整体回滚：输入先被深拷贝，所有默认值应用在副本上；一旦出现任何错误（校验失败、依赖无法解析、依赖环），返回 `{ ok: false, errors }`，不产出部分填充的数据，输入对象保持不变。

## 错误格式

每个错误同时给出实例路径与规则路径：

```json
{
  "instancePath": "/addresses/0/city",
  "schemaPath": "#/properties/addresses/items/properties/city/type",
  "message": "expected type string, got number"
}
```

## API

```ts
import { validateAndFill } from "./src/index.ts";

const result = validateAndFill(schema, input);
// result: { ok: true, data } | { ok: false, errors: ValidationError[] }
```

校验顺序：先填充默认值（默认值可满足 `required`），再对填充后的文档做整体校验。

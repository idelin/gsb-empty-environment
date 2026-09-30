# budgeted-schema-validator

预算受限的 JSON Schema **子集**验证器，TypeScript 实现，零依赖，不借用任何现成验证器。

## 运行

```sh
npm test      # Node.js 内置测试运行器（node:test）
npm run sample  # 运行样例
```

要求 Node.js >= 23.6（利用内置 TypeScript type stripping 直接运行 .ts）。

## 承诺的 schema 子集

仅以下关键字有定义行为，其余关键字（如 `format`、`title`、`patternProperties`）一律忽略：

- `type`：`string | number | integer | boolean | object | array | null`，或字符串数组
- 对象：`properties`、`required`、`additionalProperties`（仅布尔形式）
- 数组：`items`（仅单 schema 形式）、`minItems`、`maxItems`
- 字符串：`minLength`、`maxLength`、`pattern`
- 数值：`minimum`、`maximum`、`exclusiveMinimum`、`exclusiveMaximum`（仅数字形式）
- 通用：`enum`、`const`、布尔 schema
- `$ref`：仅本地 JSON Pointer（`#` 或 `#/...`）

子集之外的用法（非本地 `$ref`、元组形式 `items` 等）抛出 `SchemaError`，属于调用方错误，不是验证结论。

## 结果模型

```ts
{ status: 'valid' | 'invalid' | 'incomplete', errors, incompleteReason?, budget }
```

- 每个错误同时给出 `instancePath`（实例中的 JSON Pointer）和 `schemaPath`（命中规则在 schema 中的 JSON Pointer）。
- `incomplete` 表示未完成的验证，**不是**不合格，原因有三种：
  - `budgetExhausted`：共享预算耗尽
  - `cancelled`：`AbortSignal` 取消
  - `errorLimitReached`：错误数达到 `maxErrors` 上限（与输入确实无效的 `invalid` 明确区分）

## 预算模型

解析（schema 遍历）、`$ref` 展开、字符串匹配（按输入长度）、错误收集共享同一个计数器，
通过 `validate(schema, instance, { budget, maxErrors, signal })` 配置：

- 每个 schema 节点访问计 1；每个 `$ref` 出现计 10（**即使目标已缓存也按次计费**，
  大量重复引用无法绕过预算）；指针逐段行走计 1；`pattern` 匹配按字符串长度计费；
  每条错误收集计 1。
- 递归深度硬上限 500，循环 `$ref` 由深度/预算兜底为 `incomplete`，不会栈溢出。
- 所有状态（预算、引用缓存、错误列表）每次调用新建，结束时清理；
  下次调用不会继承上次的耗尽状态。

## 文件

- `src/validator.ts` — 验证器实现
- `test/validator.test.ts` — 17 个测试（node:test）
- `examples/sample.ts` — 六种场景的运行样例

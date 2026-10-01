# budgeted-schema-validator

零依赖的 JSON Schema **子集**验证器（TypeScript，Node.js 内置测试运行器）。
解析、`$ref` 引用展开、字符串匹配与错误收集共享同一份预算；资源耗尽时返回
`incomplete` 而非 `invalid`——实例没有被证明不合格，只是本轮无法完成。

## 承诺的 schema 子集（其余关键字一律忽略，不承诺语义）

- `type`（`string | number | integer | boolean | null | array | object`，或这些值组成的数组）
- `properties`、`required`、`additionalProperties`（boolean 或 schema）
- `items`（单 schema）、`minItems`、`maxItems`
- `minLength`、`maxLength`、`pattern`
- `minimum`、`maximum`、`exclusiveMinimum`、`exclusiveMaximum`
- `enum`、`const`、`allOf`、`anyOf`
- `$ref`：仅本地 JSON Pointer（`#`、`#/...`，支持 `~0`/`~1` 与 URL 解码），带循环检测
- 布尔 schema（`true` / `false`）

## API

```ts
import { validate } from './src/index.ts';

const result = validate(schema, instance, {
  budget: 10_000,   // 共享预算上限（默认 10000）
  maxErrors: 100,   // 错误数上限（默认 100）
  maxDepth: 100,    // 递归深度上限（默认 100）
  signal,           // AbortSignal，每个预算检查点都会检查
});
```

`result.status`：

- `'valid'`：实例符合上述子集
- `'invalid'`：发现违规且在限额内完整报告
- `'incomplete'`：未完成，**不等于不合格**。`result.reason` 区分原因：
  `budget-exhausted` | `cancelled` | `error-limit-reached` | `depth-limit-reached`

每个错误同时给出 `instancePath`（实例内 JSON Pointer）与
`schemaPath`（失败规则在 schema 文档中的位置，`$ref` 显示解析后的定义位置）。

## 预算模型（每次 `validate()` 调用独立计数）

| 动作 | 花费 |
| --- | --- |
| 访问 schema 节点 / 分发关键字（解析份额） | 1 |
| 解一次 `$ref`（**即使命中缓存也计费**，重复引用无法绕过预算） | 5 |
| `pattern` 匹配（字符串匹配份额） | 2 + 字符串长度 |
| `enum`/`const` 深比较 | 每个比较节点 1 |
| 记录一条错误（错误收集份额） | 1 |

## 语义保证

- **超限即 `incomplete`**：预算、取消、错误上限、深度上限都不会被误报为 `invalid`。
- **错误上限 ≠ 输入无效**：达到 `maxErrors` 返回 `incomplete`/`error-limit-reached`；
  同样的输入在不设限时会得到 `invalid` 与全部错误。
- **支持取消并清理临时状态**：`AbortSignal` 在每个计费点检查；`finally` 中释放
  `$ref` 缓存与解析栈，所有退出路径都会清理。
- **调用间无状态**：每次调用重建全部内部状态，上一次耗尽的预算不会影响下一次。

## 运行

```sh
npm test      # node --test "test/**/*.test.ts"（Node ≥ 22.6，原生类型擦除）
npm run sample
```

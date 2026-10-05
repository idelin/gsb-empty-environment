# Recursive JSON Schema Validator

TypeScript 实现的 JSON 实例校验器，支持允许递归引用的 schema。

## 能力

- **类型检查**：`null` / `boolean` / `number` / `string` / `object` / `array`
- **必填字段**：`required`，缺失字段报在该字段路径上
- **数组长度**：`minItems` / `maxItems`
- **递归引用**：`$ref: "#"` 与 `$ref: "#/definitions/<name>"`，可成环
- **互斥分支**：`oneOf`，要求恰好一个分支通过；零通过时逐个解释各分支失败原因
- **实例深度预算**：`maxDepth` 限制容器嵌套深度（标量叶子不占预算）

## 核心设计

- **配对状态**：校验状态以 `(实例节点, schema节点, 深度)` 三元组为键缓存
  （`src/validator.ts` 中的 `pairKey`）。实例节点与 schema 节点按对象身份编号，
  因此共享引用（同一个 definition 被多处 `$ref`）各自独立判定且不会重复展开。
- **循环检测**：仍在调用栈上的配对被再次进入时直接接受（余归纳语义），
  递归 schema 不会无限展开；空递归（如 `{"$ref": "#"}`）终止且 vacuously 有效。
- **错误选择**：每个子树返回相对路径错误，父节点拼接路径段后再比较——
  先取最浅实例路径，路径等长时按字段字典序（数组下标优先于字符串键）决胜。
- **分支解释**：`oneOf` 失败时错误携带 `branches`，记录每个分支的
  最浅失败；`explainError` 可渲染为人类可读的多层说明。

## 使用

```ts
import { validate, explainError } from "./src/validator";

const result = validate(instance, schemaDocument, { maxDepth: 100 });
if (!result.valid) {
  console.log(explainError(result.error));
}
```

## 构建与测试

```sh
npm test   # tsc 编译并运行 dist/test/validator.test.js
```

测试覆盖：空递归、共享引用、实例深度预算、互斥分支解释、
最浅路径与字典序决胜、类型/必填/数组长度检查。

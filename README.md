# recursive-schema-validator

对有限 JSON 实例按 schema 做校验，支持递归引用（`$ref`）、互斥分支（`oneOf`）、
类型检查、必填字段与数组长度约束，并在失败时返回"最浅实例路径"的错误。

## 用法

```ts
import { validate } from './src';

const result = validate(instance, schema, { maxDepth: 100 });
if (!result.valid) {
  console.error(result.error.path, result.error.message, result.error.branches);
}
```

## Schema 能力

- 基本类型：`null` / `boolean` / `number` / `integer` / `string`
- `array`：`items`、`minItems`、`maxItems`
- `object`：`properties`、`required`
- `$ref`：本地 JSON Pointer（`#/$defs/Name`、`#/definitions/...`、`#`），可递归
- `oneOf`：互斥分支，必须恰好匹配一个分支
- `$defs`：定义可复用的命名 schema

## 关键设计

- **验证状态 = (实例节点, schema 节点)**：以实例路径 + schema 的 JSON Pointer 作为
  配对键，记录在 DFS 祖先栈 `active` 中。递归 schema 再次到达同一配对时直接
  接受（余归纳语义），保证空递归（如 `A = { $ref: A }`）也会终止而不会无限展开。
- **失败选择**：收集路径上的全部错误，先按实例路径长度取最浅，再按路径段
  字典序决胜（数字段按数值且排在字符串段之前），与对象键的插入顺序无关。
- **互斥分支解释**：`oneOf` 零匹配时，错误携带 `branches`，逐项给出每个分支的
  最佳子错误（可递归嵌套）；多匹配同样判失败并列出匹配的分支序号。
- **实例深度预算**：`maxDepth` 限制实例嵌套深度（根为 0），超深即在该实例
  路径报错，参与统一的最浅路径决胜。

## 构建与测试

```sh
npm test          # tsc 编译后运行 dist/test/validator.test.js
```

本环境无网络、未安装 devDependency 时，可直接使用本机已有的 tsc：

```sh
node /path/to/typescript/bin/tsc -p tsconfig.json && node dist/test/validator.test.js
```

# versioned-doc-pipeline

按文档版本字段选择 schema，经过**显式迁移链**迁移后再验证。零依赖，纯
TypeScript（Node ≥ 23.6 原生类型擦除运行），验证器为手写实现，不借用任何
现成验证库。

## 运行

```sh
npm test      # Node 内置测试运行器（node:test）
npm run sample
```

## 处理管线（分阶段报告错误）

`processDocument(registry, doc, options)` 依次经过四个阶段，前一阶段失败即
终止，错误按阶段归类（`err.phase`）：

1. `version-resolution` — 读取版本字段（默认 `version`，可用
   `versionField` 覆盖）。**缺版本拒绝**，除非调用方显式传
   `assumeVersion`（不存在"默认最新"的隐式行为）；**未知版本拒绝**并列出
   已知版本。
2. `validation` — 按文档自身版本的 schema 校验。
3. `migration` — 只走注册过的显式迁移步骤（BFS 找链），链不存在即报错；
   任一步骤抛错即失败，**结果中保留原始文档**（深拷贝副本，输入不被改动）。
4. `revalidation` — 迁移结果按目标版本 schema 再验证，失败同样保留原文。

每条错误同时给出：

- `instancePath` — 文档内 JSON Pointer，如 `/user/age`
- `schemaPath` — 规则内路径，如 `#/properties/user/properties/age/minimum`

## 只承诺已实现的 schema 子集

支持：`type`（object/array/string/number/integer/boolean/null）、
`properties`、`required`、`additionalProperties`（仅布尔）、`items`（单
schema）、`minItems`/`maxItems`、`minLength`/`maxLength`、`pattern`、
`minimum`/`maximum`/`exclusiveMinimum`/`exclusiveMaximum`、`enum`。

不支持（注册时即抛错）：`$ref`、`oneOf`/`anyOf`/`allOf`、
`patternProperties`、`format`、`if/then/else`、元组 `items` 等。

## 版本冲突检测

每个 schema 注册时计算规范化 JSON 的 SHA-256 摘要。相同版本号再次注册：
摘要相同为幂等无操作；**摘要不同抛 `SchemaConflictError`**，报出两个摘要。

## 样例输出（`npm run sample`）

```
--- v1.0 doc migrated to v2.0 ---
ok: 1.0 -> 2.0 (migrated: true)
{"version":"2.0","fullName":"Ada","email":"unknown@example.com"}

--- missing version (rejected) ---
failed in phase: version-resolution
  [required] missing version field "version"; no implicit default exists — pass assumeVersion explicitly to opt in
    instance: /version   rule: #
  original preserved: {"name":"Ada"}

--- unknown version (rejected) ---
failed in phase: version-resolution
  [enum] unknown version "9.9"; known versions: 1.0, 2.0

--- validation error with both paths ---
failed in phase: validation
  [minLength] string length 0 < minLength 1
    instance: /name   rule: #/properties/name/minLength

--- same version, different schema digest ---
schema conflict for version "1.0": existing digest 1cc9e1f6fa86bef0 != incoming digest a78931a60f78d56e
```

## 测试结果（`npm test`，Node v23.9.0）

```
ℹ tests 22
ℹ pass 22
ℹ fail 0
```

覆盖：双路径错误定位、required/type/enum/pattern/边界/additionalProperties、
子集外关键字拒绝、摘要稳定性与同版本冲突、显式迁移链解析、缺版本拒绝与显式
assumeVersion、未知版本拒绝、迁移失败保留原文、迁移后再验证失败保留原文、
自定义版本字段。

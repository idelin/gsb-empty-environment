# schema-diff-validator

对一个样本库分别用**旧 schema** 和**新 schema** 进行校验，输出：

- **新增失败（NEW FAILURES）**：旧 schema 通过、新 schema 失败 —— 单独列出；
- **已修复（FIXED）**：旧 schema 失败、新 schema 通过；
- **原因变化（REASON CHANGED）**：两边都失败，但触发失败的规则集合不同（记录被移除/新增的规则）；
- **未变失败（UNCHANGED FAILURES）**：两边都失败且失败规则完全相同；
- **持续通过（STILL PASSING）**：两边都通过。

校验结果按 **schema 摘要 + 样本库摘要** 双键缓存；样本或 schema 任一个发生变化都不会命中缓存，因此**改样本后绝不会复用旧的“通过”结果**。

校验器为手写，不依赖任何现成验证器（无 ajv / ajv-like 依赖）。

## 承诺的 schema 子集

仅实现以下关键字（`src/validator.ts` 的 `SUPPORTED_KEYWORDS`）：

`type`（含 `integer`、union 数组形式）、`enum`、`const`、`properties`、`required`、
`additionalProperties`（布尔/对象）、`items`、`minItems`、`maxItems`、
`minLength`、`maxLength`、`pattern`、`minimum`、`maximum`。

其余关键字（如示例中的 `format`）**不校验**，但会在报告的 `warnings` 中明确提示，
避免把未实现的规则误当成已验证。`$schema/$id/title/description` 为元数据，静默跳过。

每条错误同时给出：

- `instancePath`：实例中的 JSON Pointer（根为 `""`，显示为 `/`）；
- `rulePath`：schema 中失败规则的 JSON Pointer，如 `/properties/age/minimum`。

## 运行

需要 Node.js ≥ 23.6（使用内置 TypeScript type stripping，无需安装依赖）。

```bash
# 运行内置测试（node:test）
npm test

# 对比新旧 schema（默认缓存目录 .schemacache）
npm run diff -- schemas/old.schema.json schemas/new.schema.json samples/samples.json

# 指定缓存目录 / 关闭缓存
npm run diff -- old.json new.json samples.json --cache /tmp/cache
npm run diff -- old.json new.json samples.json --no-cache
```

进程退出码：存在新增失败时为 `1`，否则为 `0`（便于接入 CI）。

## 运行样例

`schemas/old.schema.json`（v1：`name` 最长 5，无 `age` 必填/下限）
与 `schemas/new.schema.json`（v2：必填 `age` 且 ≥0、`tag` 限小写、放宽 `name` 长度）
对 `samples/samples.json` 中 8 个样本的实际输出：

```text
schema digests: old=b2cbaeccd2042df6 new=7d53383df6776e67
samples digest: 22004b3bc1f934d5
cache: old=miss new=miss
warnings (unsupported keywords ignored):
  ! unsupported keyword "format" at /properties/email ignored

NEW FAILURES (old pass -> new fail): 3
  [s2-missing-age]
    instance / | rule /required (required): missing required property "age"
  [s4-negative-age]
    instance /age | rule /properties/age/minimum (minimum): -2 < minimum 0
  [s5-bad-tag]
    instance /tag | rule /properties/tag/pattern (pattern): does not match pattern ^[a-z]+$

FIXED (old fail -> new pass): 1
  [s3-long-name]

REASON CHANGED (fail both, different rules): 1
  [s6-rule-shift] removed: maxLength@/name | added: minimum@/age
    now: instance /age | rule /properties/age/minimum (minimum): -1 < minimum 0

UNCHANGED FAILURES (fail both, same rules): 2
  [s7-extra-field]
  [s8-wrong-type]

STILL PASSING: 1
  [s1-ok]
```

第二次运行（同一缓存目录）时 `cache: old=hit new=hit`；
把样本内容改掉后（即使样本 `id` 不变），摘要变化，缓存重新变为 `miss` 并重新校验，
旧的通过结果不会被复用。

## 缓存格式

每个缓存条目为 `<schema短摘要>-<样本短摘要>.json`，内容再次包含两个完整 sha256 摘要，
读取时双重校验，防篡改/碰撞：

```json
{
  "schemaDigest": "…",
  "samplesDigest": "…",
  "createdAt": "…",
  "payload": { "verdicts": [ { "id": "…", "valid": true, "errors": [] } ], "warnings": [] }
}
```

## 测试结果

`node --experimental-strip-types --test test/*.test.ts`（node:test）：

```text
ℹ tests 15
ℹ pass 15
ℹ fail 0
```

覆盖：实例路径/规则路径（含数组下标与嵌套属性）、各关键字规则路径、
新增失败/已修复/原因变化/未变失败的分类、不支持关键字只告警不校验、
缓存命中、改样本不命中（不复用旧通过）、改 schema 不命中、缓存条目被篡改后拒绝使用。

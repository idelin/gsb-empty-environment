# Schema 回归对比（自研验证器）

给定**旧 schema**、**新 schema** 和一个**样本库**，逐样本对比两份 schema 的校验结果，输出：

- **新增失败**：旧 schema 通过、新 schema 失败（单独成组，不与其他失败混淆）
- **已修复**：旧 schema 失败、新 schema 通过
- **原因变化**：两边都失败，但命中的规则路径集合不同（必须记录，不混同于普通失败）
- **仍然失败**：两边都失败且规则路径集合相同
- **通过**：两边都通过

校验结果带缓存，缓存键同时绑定 schema 摘要与样本摘要：`sha256(schema)#sha256(sample)`。
schema 或样本任一侧内容变化都会换键，因此**样本改变后不会复用旧的通过结果**。

零第三方依赖、不使用现成 JSON Schema 验证器；TypeScript + Node.js 内置测试模块 `node:test`。

## 承诺支持的 Schema 子集

| 类别 | 关键字 |
| --- | --- |
| 基本 | 布尔 schema（`true`/`false`）、`type`（含联合数组，integer 视为 number 子类型）、`enum`、`const` |
| 字符串 | `minLength`、`maxLength`、`pattern`（按 Unicode 码点计长） |
| 数值 | `minimum`、`maximum`、`exclusiveMinimum`、`exclusiveMaximum`、`multipleOf` |
| 对象 | `required`、`properties`、`additionalProperties`（boolean / schema；不含 `patternProperties`） |
| 数组 | `items`（单一 schema，**不**支持元组数组形式）、`minItems`、`maxItems`、`uniqueItems` |
| 组合 | `allOf`、`anyOf`、`oneOf`、`not` |

注释类关键字（`$schema`、`$id`、`$comment`、`title`、`description`、`default`、`examples`）识别但忽略。

**未在上表中的关键字（如 `$ref`、`format`、`$defs`、`dependentRequired` 等）一律抛 `UnsupportedKeywordError`**，
而不是静默跳过——只对明确承诺的子集给出结果。

## 错误格式：双路径

每条错误同时给出两个 [RFC 6901 JSON Pointer](https://www.rfc-editor.org/rfc/rfc6901)：

- `instancePath`：出错数据位置（根为 `/`）
- `schemaPath`：触发失败的规则在 schema 中的位置（末尾段即关键字，如 `/properties/email/pattern`）

“规则是否相同”按 `schemaPath` 集合比较，与错误文案无关；因此同一条规则换了提示文案仍算“仍然失败”。

## 目录

```
src/
  digest.ts     规范化序列化 + SHA-256 摘要
  validator.ts  自研子集验证器
  cache.ts      摘要绑定的校验缓存
  compare.ts    五类分类
  cli.ts        命令行报告
data/
  schemas/user.v1.json, user.v2.json
  samples/*.json  7 个样本，覆盖全部五类
test/*.test.ts    node:test 测试（19 个用例）
```

新 schema（v2）相对 v1 的变化：`email` 增加邮箱 `pattern`；`age` 增加 `minimum:0/maximum:150`；
`role` 枚举增加 `editor`；`tags` 增加 `minItems:1` 与 `uniqueItems:true`。

## 运行

需要 Node ≥ 23.6（可直接执行 TypeScript）。Node 22.6–23.5 需加 `--experimental-strip-types`。

```bash
# 测试
node --test test/*.test.ts

# 示例报告
node src/cli.ts data/schemas/user.v1.json data/schemas/user.v2.json data/samples
```

本机使用 `/Users/delin/.nvm/versions/node/v23.9.0/bin/node`（系统默认 node 为 v16，不支持 `node:test` 与 TS 直跑）。

## 运行样例

```
==================================================
 Schema 回归对比报告
==================================================
旧 schema: data/schemas/user.v1.json
  sha256(schema) = b42c2747806f12b58ce8c54bbdae0bda0c480597407461e4c1ad5295bca8c72d
新 schema: data/schemas/user.v2.json
  sha256(schema) = 3a6b7a7701880c2e1ce6fd49b0ac7eaf6b56038865e46ea9f4c6cd93490c885f
样本目录: data/samples（7 个样本）

【新增失败（旧通过、新失败）】 2 个
  - 002-new-failure-email.json
      新规则 - instancePath=/email schemaPath=/properties/email/pattern [pattern] string does not match pattern "^[^@\\s]+@[^@\\s]+\\.[^@\\s]+$"
  - 007-new-failure-tags.json
      新规则 - instancePath=/tags/1 schemaPath=/properties/tags/uniqueItems [uniqueItems] array items 0 and 1 are duplicates

【已修复】 1 个
  - 003-fixed-role.json
      旧规则 - instancePath=/role schemaPath=/properties/role/enum [enum] value must be one of ["user","admin"]

【原因变化（两边都失败、规则不同）】 1 个
  - 004-reason-changed.json
      旧规则 - instancePath=/role schemaPath=/properties/role/enum [enum] value must be one of ["user","admin"]
      新规则 - instancePath=/email schemaPath=/properties/email/pattern [pattern] string does not match pattern "^[^@\\s]+@[^@\\s]+\\.[^@\\s]+$"

【仍然失败（规则相同）】 2 个
  - 005-still-failing.json
      规则   - instancePath=/id schemaPath=/properties/id/minimum [minimum] 0 is less than minimum 1
      规则   - instancePath=/name schemaPath=/properties/name/minLength [minLength] string length 0 is less than minLength 1
  - 006-still-failing-extra.json
      规则   - instancePath=/ schemaPath=/additionalProperties [additionalProperties] unexpected additional property "hack"

【通过】 1 个
  - 001-valid-both.json

--------------------------------------------------
缓存键 = sha256(schema)#sha256(sample)；缓存命中 14 次，未命中（实际计算）14 次
样本内容或 schema 内容改变都会改变键，旧通过结果不会被复用。
```

样本 `004` 是“原因变化”的关键示例：v1 下因 `role=editor` 不在枚举中而失败；v2 放行了该枚举值，
但 `email="bad"` 又撞上新加的 `pattern`——两边都失败、规则路径完全不同。

## 测试结果

`node --test test/*.test.ts`（Node v23.9.0）：**19 passed / 0 failed**

- `test/validator.test.ts`（12）：通过路径、type/pattern/required/minimum/enum/additionalProperties/uniqueItems 的双路径、
  数组下标路径、布尔 schema、anyOf/oneOf/not、未承诺关键字（`$ref`/`format`/元组 items）直接拒绝
- `test/compare.test.ts`（2）：五类分类；规则按 schemaPath 集合比较（顺序与文案无关）
- `test/cache.test.ts`（4）：键绑定双方摘要；相同输入只算一次；**样本改变不复用旧通过结果**；schema 改变不复用
- `test/cli.test.ts`（1）：子进程端到端跑 CLI，断言五类计数、样本名、双路径与缓存命中次数

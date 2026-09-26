# exact-json-numbers

TypeScript（Node ≥23 直接运行，零依赖）实现的精确 JSON 数值处理库：

- **精确十进制结构**：`Decimal = { neg, digits, exp, raw }`，值为 `±0.digits × 10^exp`，
  规范化存储（无前后导零），同时保留原始词法 `raw`。
- **严格词法**：拒绝非法前导零（`01`）、`+1`、`.5`、`1.`、`NaN`、`Infinity` 等非 JSON 字面量。
- **双模式序列化**：`asis` 原样回放词法（`-0`、`1e+02` 原样保留）；`canonical` 输出每值唯一的
  规范形式（`1e+02 → 100`，且 `canonical(parse(canonical(x))) === canonical(x)` 幂等）。
- **增量解析器**：`IncrementalJsonParser.write(chunk)` 可在任意字节处暂停；数字的原始片段
  跨块累积，只有在分隔符或 EOF 证明词法完整后才发布事件——绝不发布半个数。
  `parseJson` 复用同一状态机，分块与一次性解析结果必然一致。
- **规范化比较**：按符号 → 调整指数 → 数字字符串比较，**不展开巨大指数**
  （`1e100000` vs `2e100000` 为 O(1)）。
- **签名用唯一表示**：`canonicalJson()` = 规范数字 + 键排序 + 最小转义，同一逻辑文档
  必然得到同一字节串。
- **精度预算**：`maxSignificantDigits`（默认 1024）与 `maxAbsExponent`（默认 ±1e6，
  作用于调整后的指数）；指数字面量先按 BigInt 解析再校验，`1e99999999999999999999`
  不会溢出 Number，而是被预算拒绝。零忽略指数（`0e999…` 合法）。
- **负零策略**：`-0` 与 `0` 数值相等；规范形式统一为 `"0"`；`asis` 模式保留 `-0` 词法。
- **可区分性**：`decimalEquals` 按数值，`decimalIdentical` 按词法——`1e+02` 与 `100`
  数值相等但词法可区分。

## Schema 子集（只承诺这些，其余关键字直接报错）

`type`（含 `integer`，按精确值判定：`1e2` 是整数，`1e-2` 不是）、`properties`、`required`、
`additionalProperties`（仅布尔）、`items`、`enum`、`const`、`minimum`、`maximum`、
`exclusiveMinimum`、`exclusiveMaximum`、`multipleOf`（精确十进制整除，`0.3` 是 `0.1` 的倍数）、
`minLength`、`maxLength`、`minItems`、`maxItems`、`minProperties`、`maxProperties`；
注解 `$schema`/`$id`/`title`/`description` 接受但忽略。数值比较全部走精确十进制，不经过
IEEE 754。校验器为手写，未借用任何现成验证器。

每个错误同时给出**实例路径**与**规则路径**（JSON Pointer）：

```
{ instancePath: "/a/1", schemaPath: "/properties/a/items/minimum", keyword: "minimum", message: "1 < minimum 2" }
```

## 运行

```sh
npm test    # node --test tests/*.test.ts（Node 内置测试运行器）
npm run demo
```

要求 Node ≥ 22.6（类型擦除运行 .ts；23.6+ 无需任何标志）。

## 样例输出（`npm run demo`）

```
=== 1. Exact parse: raw lexeme + canonical form ===
raw=1e+02                            canonical=100                      as-is=1e+02
raw=-0                               canonical=0                        as-is=-0
raw=9007199254740993                 canonical=9007199254740993         as-is=9007199254740993
canonical(1e+02)=100, re-canonicalized=100 (idempotent)

=== 2. Comparison without expanding huge exponents ===
1e100000 < 2e100000            : true
9007199254740993 > ...992      : true (Number() would tie)
canonical(1e100000)            : 1e+100000

=== 4. Unique canonical text for signing vs as-is ===
as-is    : {"b":1e+02,"a":[-0,2.50]}
canonical: {"a":[0,2.5],"b":100}

=== 5. Schema subset validation: instance path + rule path ===
invalid {"price": 0.305, "qty": 9007199254740993, "extra": 1}
  -> instance /price violates /properties/price/multipleOf: 0.305 is not a multiple of 0.01
  -> instance /qty violates /properties/qty/maximum: 9007199254740993 > maximum 9007199254740992
  -> instance /extra violates /additionalProperties: additional property "extra" is not allowed
```

## 测试覆盖（24 个用例全部通过）

- 穷举 `{-,+,0,1,9,.,e,E}` 上长度 1–4 的全部 4680 个串：合法性与 `JSON.parse` 完全一致，
  合法串 as-is 往返一致、规范形式幂等且保值。
- 指数边界：`1e999999`（调整指数 = 10⁶，接受）vs `1e1000000`（拒绝）；
  预算作用于调整后指数（`0.0001e1000002` 接受）；超 Number 范围的指数字面量被拒绝。
- 分块一致性：8 个文档 × 每个字节切分点 + 每文档 200 组随机多块切分，事件流与树
  均与一次性解析一致；数字在词法完成前不产生任何事件。
- 负零策略、>53 位整数精度、巨大指数不展开比较、精确 `multipleOf`、schema 双路径错误、
  不支持关键字的拒绝。

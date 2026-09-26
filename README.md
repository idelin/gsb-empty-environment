# Unicode 感知的 JSON Schema 子集校验器（TypeScript，零运行时依赖）

手写实现，**不使用任何现成 JSON Schema 验证器 / Unicode 库**：

- 规范化：`String.prototype.normalize`
- 字形簇：内置 `Intl.Segmenter`（`granularity: "grapheme"`）
- 脚本判定：正则 Unicode 属性转义 `\p{sc=...}`
- 测试：Node.js 内置测试运行器 `node:test`
- TypeScript：Node ≥ 22.6 的原生类型剥离 `--experimental-strip-types`，无需安装依赖

## 承诺实现的 Schema 子集

只实现下表中的关键字；**任何未知关键字在编译期直接报错**（指向规则路径），不会被静默忽略。

| 关键字 | 适用 | 说明 |
| --- | --- | --- |
| `type` | 全部 | 单个字符串或数组；支持 `null/boolean/number/integer/string/array/object`（integer 也是 number） |
| `const` / `enum` | 全部 | 深度相等比较；`const: null` 合法 |
| `normalization` | string | `none`(默认) / `NFC` / `NFD` / `NFKC` / `NFKD`，长度与字形簇都在规范化后的串上计算 |
| `minLength` / `maxLength` | string | 非负整数 |
| `minLengthUnit` / `maxLengthUnit` | string | `codepoints`(默认) / `graphemes` / `utf16`，可分别指定 |
| `allowedScripts` | string | Unicode 脚本名数组（如 `["Latin","Han","Common","Inherited"]`）；按**原码点**逐个检查 |
| `bidiControl` | string | `deny`(默认) / `allow`，覆盖 U+200E/F、U+202A..E、U+2066..U+2069 |
| `properties` / `required` | object | |
| `items` / `minItems` / `maxItems` | array | `items` 作用于所有元素 |
| `title` / `description` / `$comment` | 全部 | 注解，不参与校验 |

不承诺：`$ref/$defs`、`pattern/format`、`oneOf/anyOf/allOf/not`、`minimum/maximum`、
`uniqueItems/additionalProperties` 等。传入这些关键字会抛 `SchemaError`。

## 关键语义

- **三种长度互相独立**：ZWJ emoji 组合簇是 1 个字形簇，但码点数与 UTF-16 单元数更多；
  绝不会用码点数冒充簇数。代理对在 UTF-16 中占 2 个单元。
- **规范化等价但原位置不同**：`"café"`（预组合）与 `"cafe\u0301"`（组合重音）在
  `NFC` 下长度等价；但越界报错映射回原文时分别是 `[3,4)` 与 `[3,5)`。
- **范围映射**：对“原串前 i 个码点的前缀”分别规范化，取前缀长度构造单调不减边界数组；
  跨码点融合（如 `o + U+031B + U+0309 → U+1EDF`）时，映射区间覆盖全部参与融合的原码点，
  不丢失组合记号。
- **错误双路径**：每个错误同时给 `instancePath`（实例 JSON Pointer）与
  `rulePath`（规则 JSON Pointer）；字符串内问题附原文 UTF-16 `range` 与 `excerpt`。
- **拒绝项不静默删除**：`maxItems` 越界时，先报告越界索引，再对每个越界元素继续按
  `items` 校验并逐项报错；`bidiControl: deny` 对每个控制字符单独报错，原样保留。
- 校验过程不修改实例。

## 目录结构

```
src/unicode.ts    码点遍历、规范化、三种长度、字形簇与范围映射、Bidi 定位
src/schema.ts     子集定义与编译期检查（未知关键字拒绝）
src/validator.ts  手写校验器与错误结构
test/             node:test 测试（29 个）
examples/run.ts   运行样例
```

## 运行

```bash
node -v   # 需要 >= 22.6（使用 --experimental-strip-types）
npm test
npm run example
```

如本地安装了 TypeScript，可选用 `tsc -p tsconfig.json` 做额外类型检查（非必需）。

# 安全请求体解析包（防止 int64 标识精度丢失）

## 背景

`JSON.parse` 把所有数字解析成 IEEE-754 双精度浮点数。超过 `2^53` 的整数会被舍入：

```ts
JSON.parse('{"id":9007199254740993}').id; // 9007199254740992
```

如果网关用解析结果做对象授权检查、后端用 64 位整数处理同一标识，攻击者提交自己无权访问的
`9007199254740993`，网关实际检查的却是邻居对象 `9007199254740992`（攻击者自己拥有），从而越权。

本包不经过 `JSON.parse`：自带严格 JSON 语法解析器，数字先保留**原始文本**，再按字段声明表精确转换。

## 安装与导入

```ts
import { parseRequestBody, BodyParseError } from './index.ts';

const declarations = {
  objectId: 'id64',   // 64 位标识，输出规范化十进制字符串
  qty: 'safeInt',     // JS 安全整数，输出 number
  score: 'number',    // 普通数值，允许小数/指数
  name: 'string',     // 普通字符串
} as const;

const body = parseRequestBody(rawText, declarations);
```

`id64` 字段在输出中始终是字符串，授权检查直接比较字符串即可，全程不经过浮点数。

## 校验规则

| 字段类型 | 接受 | 拒绝 |
| --- | --- | --- |
| `id64` | 十进制 JSON 字符串（无前导零，int64 范围内），或绝对值 ≤ `2^53-1` 的整数字面量；输出规范化十进制字符串 | 指数写法（`1e3`）、小数、前导零、超出 int64 范围、布尔/null/对象 |
| `safeInt` | 安全范围内的纯十进制整数字面量，输出 `number` | 超范围整数、指数、小数、字符串 |
| `number` | 小数与指数形式，输出 `number` | `-0`/`-0.0`、解析为 `Infinity`（如 `1e999`）、非字面量 |
| `string` | JSON 字符串 | 其他类型 |
| 表外字段 | 安全范围内的普通 JSON 值（含嵌套对象/数组） | 超出安全整数范围的整数、非有限数、`-0` |

其他全局规则：

- 对象键重复一律拒绝（`duplicate_key`），不采用“后者覆盖前者”。
- 严格 RFC 8259 语法：拒绝尾随字符、注释、单引号、前导零数字等。
- 所有错误抛出 `BodyParseError`，带 `code`、JSON 路径 `path` 与字节偏移 `index`，便于日志与审计。
- 字符串形式的 `"-0"` 对 `id64` 规范化为 `"0"`（数学上的零，无符号问题）。
- 表外整数字段即使使用指数写法（如 `1e21`）也按整数口径检查，无法绕过安全整数上限。
- 输出对象使用 `null` 原型，声明表查找使用 `hasOwn`，`__proto__`、`constructor` 等键不会触发原型污染或原型链误判。

## 错误码

`syntax`、`duplicate_key`、`type_mismatch`、`id64_format`、`id64_overflow`、
`safeint_overflow`、`unsafe_undeclared_number`、`number_non_finite`、`negative_zero`。

## 测试

零运行时依赖，使用 Node 内置测试运行器：

```sh
node --experimental-strip-types --test test/
```

测试覆盖题目要求的全部向量：`9007199254740993` 精度保持、`1e3` 指数拒绝、`-0` 拒绝、
重复键拒绝、合法字符串标识、int64 边界（`±9223372036854775807/8`）以及越权场景端到端判定。

## 代码结构

- `src/raw-parser.ts` — 严格 JSON 解析器，数字保留原始文本，重复键检测。
- `src/index.ts` — 声明表驱动的类型转换与全部安全校验。
- `src/errors.ts` — `BodyParseError` 与错误码定义。
- `test/parser.test.ts` — 20 个测试用例。

源码使用 `.ts` 扩展名相对导入，可直接被 Node 22.6+/23 的类型擦除执行器导入；若用 `tsc`
构建到纯 JS，将相对导入的 `.ts` 后缀改为 `.js` 即可（NodeNext 标准写法）。

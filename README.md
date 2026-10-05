# cond-trim

按布尔条件裁剪源码的 TypeScript 工具。识别 `#if` / `#elif` / `#else` / `#endif`
指令，删除不成立的分支与指令行，并返回裁剪文本和**双向行号映射**。

## 特性

- **词法状态机**：逐字符扫描并跨行维护状态（代码 / 行注释 / 块注释 / 字符串 /
  模板字符串 `${}` 替换），只有处于顶层代码状态的行首指令才会生效——字符串里
  的伪指令、跨行块注释里的伪指令都被当作普通文本保留。不是逐行正则。
- **嵌套条件**：条件栈支持任意深度的嵌套与 `#elif` / `#else` 链。
- **短路表达式**：支持 `&&`、`||`、`!`、括号与 `true` / `false` 字面量；求值
  短路（`true || X`、`false && X` 不会触及 `X`）。死分支里的表达式只检查语法、
  不求值。
- **未知变量报错**：实际求值到未提供的变量时抛出 `TrimError`
  （`code: 'UNKNOWN_VARIABLE'`，携带行号）。
- **双向映射**：`oldToNew` / `toNewLine` 正向查询，被删除的行（指令行与死分支）
  明确返回 `null`；`newToOld` / `toOldLine` 反向查询，裁剪后的每一行都能映射回
  原文件。

## 用法

```ts
import { trimSource, TrimError } from './src/index.ts';

const result = trimSource(source, { DEBUG: true, BETA: false });

result.text;            // 裁剪后的文本（保留原始换行符 \n / \r\n）
result.toNewLine(5);    // 原文件第 5 行 -> 新文件行号；已删除则返回 null
result.toOldLine(3);    // 新文件第 3 行 -> 原文件行号
result.oldToNew;        // 1 基数组：旧行 -> 新行 | null（下标 0 不用）
result.newToOld;        // 1 基数组：新行 -> 旧行
```

行号均为 1 基。越界查询抛 `RangeError`。

### CLI

```sh
node src/cli.ts <file> [-D NAME[=true|false]]... [--map]
# 例：node src/cli.ts app.ts -D DEBUG=true -D BETA=false --map
```

裁剪结果输出到 stdout；`--map` 把映射 JSON 输出到 stderr。出错时 stderr 打印
`error [CODE]: line N: ...` 并以退出码 1 结束。

## 指令语法

- 指令行：行首（允许前导空白）为 `#if <expr>` / `#elif <expr>` / `#else` /
  `#endif`，允许行尾注释（`// ...`、`/* ... */`）。
- 未知的 `#xxx` 行（如 `#include`）视为普通代码，原样保留。
- 表达式：标识符、`true` / `false`、`!`、`&&`、`||`、括号。

## 错误

所有错误都是 `TrimError`（`code` + 1 基 `line`）：

| code | 含义 |
| --- | --- |
| `UNKNOWN_VARIABLE` | 条件求值遇到未提供的变量 |
| `UNCLOSED_CONDITION` | 文件结束时仍有未闭合的 `#if`（报 `#if` 所在行） |
| `UNEXPECTED_DIRECTIVE` | `#elif` / `#else` / `#endif` 没有匹配的 `#if` |
| `DUPLICATE_ELSE` | 同一条件块出现两个 `#else` |
| `ELIF_AFTER_ELSE` | `#elif` 出现在 `#else` 之后 |
| `INVALID_EXPRESSION` | 表达式语法错误、缺条件、`#else`/`#endif` 后有多余内容 |

## 运行测试

本仓库使用 Node >= 22.6 的原生 TypeScript 类型擦除（代码只含可擦除语法，
无需编译步骤）：

```sh
node --test test/*.test.ts
```

如安装了 TypeScript，可用 `tsc --noEmit` 做类型检查（见 `tsconfig.json`）。

## 已知限制

- 词法面向 C 风格语言（`//`、`/* */`、`'`、`"`、反引号模板）；不识别 JS 正则
  字面量，含 `/` 密集的正则可能干扰状态判断。
- 映射粒度为整行；指令必须独占一行。

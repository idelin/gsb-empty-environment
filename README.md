# cond-prune

按布尔条件裁剪源码的工具。裁剪后返回新文本与**双向位置映射**，被删除区间的查询明确返回空（`null` / `[]`），绝不做近似。

要求 Node.js >= 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`）。

## 指令语法

指令是位于行首（允许前导空白）的行注释，且扫描器必须处于**顶层代码状态**：

```ts
// #if DEBUG && MODE == "dev"
console.log('debug');
// #elif !RELEASE
console.log('staging');
// #else
console.log('prod');
// #endif
```

表达式支持 `&&`、`||`、`!`、括号、`==` / `!=`（标识符与字符串/数字/布尔字面量比较）以及 `true` / `false`。`&&` / `||` 短路求值；未命中的分支（含不活跃区域的嵌套条件与 `#elif`）完全不求值。被求值到的**未知变量会报错**（`UnknownVariableError`，带行号）。

## 词法状态（非逐行正则）

`src/lexer.ts` 是跨行的字符级状态机，跟踪 `code / lineComment / blockComment / string / template` 状态（含模板字符串 `${}` 插值）。因此字符串字面量里的 `"// #if X"`、跨行块注释里的伪指令都不会被当成真指令。

## API

```ts
import { pruneSource } from './src/index.ts';

const { text, mapping } = pruneSource(source, { vars: { DEBUG: true } });

mapping.toGeneratedLine(7);            // 原行号 -> 新行号；已删除 => null
mapping.toOriginalLine(2);             // 新行号 -> 原行号；越界 => null
mapping.toGeneratedPosition({ line: 7, column: 2 }); // 位置映射，列保持不变
mapping.toGeneratedRange(2, 5);        // 整段被删 => []
mapping.deletedRanges();               // 原文件坐标下的删除区间
```

错误（均为 `PruneError` 子类，带行号）：未闭合的 `#if`、无匹配的 `#else` / `#elif` / `#endif`、重复 `#else`、`#elif` 出现在 `#else` 之后、空条件、未知变量。

## CLI

```sh
node src/cli.ts input.ts -o out.ts --map map.json --var DEBUG=true --var MODE=dev
node src/cli.ts input.ts --vars vars.json
```

## 测试

```sh
npm test   # node --test test/*.test.ts
```

# 双向解析器与 AST 美化打印器

一个零依赖的 TypeScript 实现：表达式语言的**解析器**（文本 → AST）与
**美化打印器**（AST → 格式化文本），二者互为逆向，满足
`parse(prettyPrint(ast))` 与原 AST 结构完全同构。

## 组成

- `src/ast.ts` — AST 节点定义与共享优先级表（解析与打印共用，保证一致）。
- `src/doc.ts` — Wadler 风格 Doc 代数（`group` / `line` / `softline` /
  `indent`）：按行宽预算决定折行的排版内核。
- `src/parser.ts` — 词法分析 + Pratt 解析器（前缀/后缀/二元/三元运算，
  调用、成员、下标、数组、对象字面量）。
- `src/printer.ts` — AST → Doc → 文本。超长行在合法的运算符或逗号处
  折行并缩进；按优先级精确补括号，保证二次解析同构。

## 折行策略

- 二元运算链：同一优先级的左结合链扁平化为一组，超宽时在运算符后折行，
  续行缩进 2 空格。
- 调用参数 / 数组 / 对象：超宽时在逗号处折行，元素逐级缩进。
- 成员链：超宽时在 `.` 处折行并缩进。
- 三元表达式：超宽时在 `?` / `:` 处折行并缩进。
- 默认行宽 80 列，可通过 `prettyPrint(ast, width)` 调整。

## 运行要求

Node.js ≥ 22.6（使用内置 `--experimental-strip-types` 直接运行
TypeScript，无需编译步骤与任何第三方依赖；测试使用内置 `node:test`）。

```sh
npm test    # node --experimental-strip-types --test test/roundtrip.test.ts
npm run demo
```

或直接：

```sh
node --experimental-strip-types --test test/roundtrip.test.ts
node --experimental-strip-types examples/demo.ts
```

## 运行样例

`npm run demo` 构造一个深度嵌套的表达式 AST（三元 + 逻辑运算 + 对象/数组
字面量 + 成员链 + 调用），按 80 列打印后再解析校验：

```
--- pretty printed (80 cols) ---
response.status >= 200 && response.status < 300
  ? transformer
    .normalize(
      {
        user: {
          id: response.payload.userId,
          displayName: response.payload.name || "anonymous",
          tags: [alpha, beta, "tag-" + response.kind]
        },
        metrics: {
          score: weight * response.rawScore + bias,
          history: response.history.slice(0, response.history.length - 1)
        }
      },
      fallbacks[attemptIndex % fallbacks.length]
    )
  : logger.error("request failed", !retriable ? -1 : retryCount + 1)
--------------------------------
lines: 17, longest line: 73 columns
round trip OK: re-parsed AST is structurally identical, printing is idempotent
```

## 测试结果

`npm test`（Node v22.14.0）：

```
1..13
# tests 13
# pass 13
# fail 0
```

覆盖：源码片段双向往返、优先级括号、运算符/逗号/`.`/`?:` 各处折行的
精确排版、深度嵌套 AST 的 80 列预算约束、多行宽（40/60/80/120）往返、
字符串转义、负数字面量、空集合与零参调用、打印幂等性。

## 设计说明

- 解析器将 `-<数字字面量>` 折叠为负数数字字面量，因此规范 AST 中
  `-5` 表示为 `{ kind: 'num', value: -5 }`；打印器按此规范形式输出。
- 打印器只在优先级/结合性需要时补括号（如 `(a + b) * c`、`a - (b - c)`、
  `!(a && b)`），保证输出二次解析后 AST 完全一致。

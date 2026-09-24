# incremental-highlighter

自定义小语言（标识符/关键字、数字、字符串、行注释、跨行块注释）的增量词法着色器，TypeScript 实现，Node.js 23+ 原生运行（类型擦除），无第三方依赖。

## 运行

```sh
npm test     # Node.js 内置测试运行器
npm run demo # 运行样例
```

## 设计

- **每行缓存**：`entryStates[i]`（行入口状态：`Normal` / `InBlockComment`）+ 每行 token（偏移相对行首）。
- **增量传播**：`applyEdit(start, deleteCount, insertText)` 后，从首个受影响行重扫；逐行比较出口状态与旧缓存入口状态，一致即停，后续行直接复用缓存。返回 `invalidatedLines`（失效行范围）。
- **UTF-16 偏移**：所有对外偏移为 UTF-16 code unit（即 JS 字符串下标）；词法扫描按码点前进，token 边界绝不切开代理对；`applyEdit` 边界落在代理对中间时自动对齐。
- **行终止符**：LF、CRLF、孤立 CR 均支持。
- **字符串**：`\"`、`\\` 转义（转义引号不结束字符串），不跨行。

## API

```ts
const h = new IncrementalHighlighter(text);
const { invalidatedLines } = h.applyEdit(start, deleteCount, insertText);
h.getTokens();          // 全文档 token，绝对 UTF-16 偏移
h.getTokensForLine(i);  // 单行 token
h.getInvalidatedLines();// 上次编辑的失效行
```

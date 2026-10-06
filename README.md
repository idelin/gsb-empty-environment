# Incremental Highlighter

TypeScript 类语法的增量语法高亮库，处理跨行模板字符串与块注释。

## 设计

- **逐行词法状态**：每行保存 `inputState`（行首状态）与 `outputState`（行尾状态），
  状态取值为 `Code` / `BlockComment` / `Template`（见 `src/lexer.js` 的 `State`）。
  跨行构造（`/* ... */` 块注释、反引号模板字符串）通过状态在行间传播；
  未闭合的 `"`/`'` 字符串在行尾终止，不泄漏到下一行。
- **增量重算**：`editLine(index, newText)` 替换一行后，从该行起向后重新词法分析，
  直到某行产出的 token 与 outputState 均与已存值完全一致即停止——此时后续行必然不变。
  不按固定行数截断：若状态变化一直传播（如删除 `/*` 使后续 100+ 行变为代码），
  会重算到文档末尾。`lastRelexedLines` 记录上次编辑实际重算的行数。
- **UTF-16 范围**：token 的 `start`/`end` 为 UTF-16 码元偏移（即 JS 字符串索引，
  astral 字符计 2）。内部按行相对存储以避免行长变化导致的纯偏移失效；
  `getTokens()` 返回文档级绝对 UTF-16 范围。
- **全量核对**：`fullHighlight(text)` 从头词法分析整个文档，作为增量结果的基准；
  测试在每次编辑（删引号、删注释符、恢复）后断言增量结果与全量结果完全一致。

## 简化

模板字符串的 `${ ... }` 插值不解析，模板持续到下一个未转义反引号。

## 用法

```js
import { IncrementalHighlighter, fullHighlight } from './src/highlighter.js';

const h = new IncrementalHighlighter('const s = `a\nb`;\nlet x = 1;');
h.editLine(0, 'const s = `a');   // 删除闭合并传播状态变化
const tokens = h.getTokens();      // 绝对 UTF-16 范围
```

## 测试

```
npm test   # node test/run-tests.js
```

注：本环境为 Node 16 且无网络/TypeScript 工具链，故实现为带 JSDoc 类型标注的
ESM JavaScript（`tsc --checkJs` 可直接类型检查）。

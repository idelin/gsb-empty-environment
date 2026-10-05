# incremental-indent-parser

面向编辑器的增量缩进解析模块（TypeScript，零依赖）。每次只改动几行，就能更新语法块树，
未变化的子树保持对象身份与稳定 ID，可直接复用。

## 语言规则

- **空行**：纯空白行，是叶子节点，不影响缩进结构。
- **行注释**：`#` 到行尾；整行注释按自身缩进归位，不影响缩进栈。
- **括号续行**：`()[]{}` 未闭合期间的行是续行（`continuation`），不参与缩进结构。
- **空格缩进**：缩进大于上一有效行则开启子块。
- **Tab 直接报错**：行首空白中出现 Tab 产生 `tab-in-indent` 诊断（Tab 不计入缩进宽度，解析继续）。

## 错误恢复（缩进错误不会吞掉后续内容）

所有错误都降级为局部诊断，树照常生长：

| 诊断 | 触发 | 恢复策略 |
| --- | --- | --- |
| `tab-in-indent` | 行首空白含 Tab | Tab 按 0 宽度计，继续解析 |
| `unmatched-close` | 闭括号无匹配/不匹配 | 忽略该闭括号 |
| `unclosed-bracket` | 开括号到 EOF 未闭合 | 诊断挂在开括号行，后续行按续行处理 |
| `inconsistent-dedent` | 回退到不存在的缩进层级 | 恢复为最近一个更浅块的子级 |

## 增量机制

1. **词法（`src/lexer.ts`）**：行缓存只重新分词被编辑的行；括号状态扫描是对缓存事件的
   线性遍历，不触碰原文，维护每行 `depthBefore` 与括号诊断。
2. **语法（`src/parser.ts`）**：每版本重建树骨架，但节点经哈希一致化（hash-consing）
   工厂构造——结构相同的子树永远得到**同一个对象**。
3. **稳定 ID**：节点 ID 是内容寻址哈希 `H(kind, indent, text, 本行诊断, 子节点 IDs)`，
   与绝对位置无关；内容不变的子树跨编辑 ID 不变，且（同一 `IncrementalParser` 内）
   是同一个对象，可安全做 `===` 复用判断。
4. **受影响范围**：编辑区域 ∪ 行自身内容指纹（kind/indent/text/本行诊断，不含子孙）
   发生变化的行。子孙变化不会连坐块头行，行号平移也不算变化。

## API

```ts
import { IncrementalParser, fullParse } from './src/index.ts';

const parser = new IncrementalParser(text);
const result = parser.applyEdit({ startLine: 9, oldLineCount: 0, newLines: ['', ''] });
result.root;          // 新版本语法树（未变子树为旧对象）
result.affectedRange; // { startLine, endLine } 供编辑器重绘
result.diagnostics;   // 当前版本全部诊断（绝对行号）
result.stats;         // { retokenizedLines, totalNodes, reusedNodes, createdNodes }

fullParse(text);      // 全量解析，用作增量结果的对照基准
```

## 测试

需要 Node.js ≥ 23.6（原生 TypeScript type stripping）：

```sh
node --test 'test/*.test.ts'
```

测试覆盖（`test/incremental.test.ts`）：

- 全量解析：块 / 注释 / 空行 / 括号续行的结构；
- Tab 报错与 `inconsistent-dedent` 恢复后，后续块完整且子树 ID 与无错误版本一致；
- **插入空行**：影响范围仅覆盖插入点，只有祖先链 3 个节点新建，其余对象级复用；
- **删除闭括号**：后续行重判为续行、产生 `unclosed-bracket` 诊断，编辑点之前节点复用；
- **跨块粘贴**：被移动的行保持对象身份（内容寻址复用），未触碰子树不变；
- 每个增量场景都与 `fullParse` 全量解析做结构 + 节点 ID + 诊断三方对照。

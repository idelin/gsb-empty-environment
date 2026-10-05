# incremental-indent-parser

面向编辑器的增量缩进解析模块（TypeScript，零依赖）。每次只改动几行，就能更新语法块树，
并给出稳定的节点 ID、受影响范围和错误恢复。

## 语言规则

- **空行 / 行注释**：`#` 到行尾为注释；空行与注释是 trivia，不进入语法树。
- **空格缩进**：缩进决定块结构（子块 = 比父行更大的缩进）。Tab 出现在代码行缩进中
  **直接报错**，按每 Tab 4 列恢复后继续解析。
- **括号续行**：`()[]{}` 未闭合时，后续行是续行，忽略缩进，挂到打开括号的语句节点上。
- **错误恢复**：Tab 缩进、异常 dedent、未匹配的闭括号、未闭合的括号都会记录为
  结构化错误并挂在对应节点上——**任何缩进错误都不会让后续内容从树里消失**。

## 架构

| 文件 | 职责 |
| --- | --- |
| `src/lexer.ts` | 逐行分类（空行/注释/代码）、缩进与 Tab 错误、括号深度状态 |
| `src/parser.ts` | 缩进栈构建块树；哈希 consing 产生内容寻址的稳定节点 ID |
| `src/document.ts` | `IncrementalDocument`：行表 + 增量重词法 + 树复用 + 受影响范围 |
| `src/index.ts` | 导出；`parseFull`（全量对照）与 `serialize`（结构指纹） |

关键设计：

- **节点不带位置**。`SyntaxNode` 只含结构内容（文本、缩进、子节点、续行、错误），
  `id` 是内容哈希。位置放在每个版本的 `Occurrence` 表里。因此插入空行导致整体下移
  时，所有节点 ID 与对象都保持不变。
- **哈希 consing 复用**。共享的 `NodeCache` 让相同内容的子树——无论是上一次版本的，
  还是同一文件里另一处的（比如跨块粘贴的副本）——都返回**同一个对象**，复用可以用
  引用相等（`===`）直接证明。
- **增量重词法**。编辑后只重扫受影响的行，并仅在括号深度携带状态持续变化时向后传播，
  遇到第一个词法结果不变的保留行即停止。
- **受影响范围**。对比新旧两版 occurrence（同一节点 + 按编辑量平移后的同一行号），
  报告语法真正变化的行区间；位置平移不算变化。

## API

```ts
import { IncrementalDocument, parseFull, serialize } from './src/index.ts';

const doc = new IncrementalDocument('alpha\n  a1\nbeta');
const report = doc.applyEdit(2, 0, ['']); // 在第 2 行插入空行
// report.affected   -> { startLine: 2, endLine: 2 }   语法受影响的行
// report.relexed    -> 实际重扫的行区间
// report.reusedNodes / report.totalNodes              子树复用统计
// doc.root / doc.occurrences / doc.errors             新版本树、位置表、错误表
```

## 运行测试

需要 Node ≥ 23.6（或 ≥ 22.6 并保留 `--experimental-strip-types` 标志），
先切到新版 Node 再跑（例如 `nvm use 23` 或 `n 23`）：

```sh
npm test
```

测试将增量结果与 `parseFull` 全量解析逐一对照，覆盖：

- 插入空行 → 全树对象复用，受影响范围只有插入行；
- 删除闭括号 → 报 `unclosed` 错误，后续行保留为续行而不消失，重词法传播到状态重新同步；
- 跨块粘贴 → 粘贴的块与源块是**同一个对象**，其余子树全部复用；
- Tab 缩进、异常 dedent、未匹配闭括号的错误恢复；
- 节点 ID 在无关编辑后保持稳定。

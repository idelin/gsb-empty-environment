# branching-history

可分支的撤销历史库（TypeScript，零依赖，Node.js 内置测试运行器验证）。

## 特性

- **分支历史**：撤销后继续编辑会从当前节点长出新分支，旧分支保留；
  `switchTo(id)` 在分支间切换，文本与选择区一起恢复。
- **键入合并**：连续插入 / 连续退格自动合并为同一历史节点；
  光标移动（`setSelection`）、`undo`/`redo`、分支切换、
  显式事务边界 `commit()` 都会打断合并。
- **容量回收**：`maxNodes` 超限或手动 `compact()` 时，
  只把所有存活分支（叶子 + 当前节点）的公共前缀折叠为带快照的基线节点，
  不破坏任何存活分支的可回放性；压缩后每个仍可见的节点都可用 `textAt(id)` 回放。
- **UTF-16 偏移**：所有公开偏移均为 UTF-16 code unit；
  编辑范围与光标位置自动调整，绝不切开代理对；LF 与 CRLF 文本均可正确编辑。

## API 摘要

```ts
const doc = new BranchingHistory("", { maxNodes: 100 });
doc.insert("ab");        // 连续键入合并
doc.commit();            // 显式事务边界
doc.undo(); doc.redo();
doc.switchTo(id);        // 分支切换（恢复文本 + 选择区）
doc.branches();          // 存活分支列表
doc.compact();           // 压缩公共前缀为基线快照
doc.textAt(id);          // 回放任意可见节点
doc.replace(s, e, text); // 按 UTF-16 偏移替换，自动避开代理对
doc.setSelection(a, f);  // 移动光标/选区（打断合并）
```

## 运行

```sh
npm test    # node --test（Node ≥ 22.6，直接运行 .ts）
npm run demo
```

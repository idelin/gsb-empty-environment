# drawing-history

绘图编辑器的 TypeScript 历史库。

## 特性

- **事务合并**：连续同对象位移合并为一次撤销。仅当新事务与撤销栈顶事务相邻（中间无其他操作，即重做栈为空）且事务标签（`label`）与合并键（`mergeKey`，通常为对象 id）都相同时才合并。
- **撤销后分叉**：撤销之后提交新事务会清空重做栈，形成新的历史分支；撤销动作本身视为"中间操作"，因此不会与撤销前的事务合并。
- **容量淘汰**：`capacity` 限制撤销栈深度，超出时淘汰最旧事务。
- **保存点脏标记**：`markSaved()` 记录保存时的文档快照，`isDirty()` 通过比较当前文档与保存快照的内容判定（而非操作计数）。因此合并跨保存点、或保存点位置被容量淘汰后，只要内容回到保存状态即判定为干净。

## 结构

- `src/history.ts` — 泛型快照式历史库 `History<D>`（memento：每条事务保存 `before`/`after` 完整文档快照）。
- `src/editor.ts` — 绘图编辑器模型 `DrawingEditor`，`moveObject` 以 `label="move"`、`mergeKey=对象 id` 提交事务。
- `test/history.test.ts` — 以完整文档快照序列验证合并、分叉、淘汰与脏标记。

## 运行测试

需要 Node.js >= 22.6（`--experimental-strip-types`）：

```sh
npm test
```

# drawing-history

面向绘图编辑器的事务式撤销/重做历史库（TypeScript）。历史是一棵**完整文档快照**树。

## 特性

- **事务合并**：连续、可合并（`mergeable`）且事务标签相同（如 `move:obj-1`）的提交折叠为一次撤销；中间出现任何其他操作（不同标签的提交、undo/redo 导航）都会打断合并链。保存点**不**打断合并。
- **撤销后分叉**：undo 之后的新提交在树上开新分支，旧分支不丢失；`redo()` 默认走最新分支，`redo(index)` 显式选择。
- **容量淘汰**：超过 `capacity` 时，单链上提升最老条目为新基座（丢弃最旧的撤销步），分叉时裁剪最老的非当前路径叶子（丢弃废弃的重做分支）；当前路径永不破坏。
- **保存点脏标记（按内容）**：`markSaved()` 记录保存时的文档内容，`isDirty()` 用结构相等比较当前快照与保存内容。因此合并跨保存点、或保存节点被淘汰后，只要文档回到已保存内容即为干净。

## API

```ts
const h = new History<Doc>(initialDoc, { capacity: 100 });
h.commit(snapshot, { label: "move:obj-1", mergeable: true });
h.undo();            // => 上一快照 | undefined
h.redo();            // 最新分支；h.redo(0) 选第一个分支
h.branches();        // 当前位置的重做分支数
h.markSaved();
h.isDirty();
h.value; h.size; h.depth; h.canUndo(); h.canRedo();
```

## 目录

- `src/history.ts` — 规范源码（TypeScript）
- `dist/history.js` — 编译产物（CommonJS，测试直接引用；当前环境无 tsc，为手工同步，逻辑逐行对应）
- `test/run-tests.js` — 模拟绘图编辑器，全程以完整文档快照断言

## 运行

```sh
npm test            # node test/run-tests.js（无依赖，Node ≥ 14）
npm run typecheck   # 需要本机安装 typescript
```

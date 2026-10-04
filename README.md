# utf8-rope-editor

UTF-8 文本编辑库（TypeScript）。所有编辑偏移按 **Unicode 标量值**（码点）计数，
任何操作都不会切断多字节编码；非法编码（孤立代理项）会**整次拒绝**编辑。

## 设计

- **持久 rope**（`src/rope.ts`）：`insert`/`delete` 返回新 `Rope`，旧快照完全不可变，
  未修改的节点在版本间共享。每个节点同时维护 `bytes`（UTF-8 字节数）与
  `scalars`（标量数）；按高度做 AVL 平衡，`validate()` 可重算并校验全部不变量。
- **节点回收**（`RopeArena`）：节点引用计数；快照释放后引用归零的节点被回收进
  空闲池复用。`arena.live` 始终等于仍被存活快照引用的节点数。
- **撤销与分支历史**（`src/history.ts`）：每次编辑在当前快照下提交一个子快照；
  undo 后再次编辑会**开分支**而不是截断 redo 栈。`redo()` 走最新分支，
  `checkout(id)` 可跳到任意存活快照。`releaseSnapshot(id)` 摘除快照、把其子节点
  重新挂到父快照，并触发节点回收。
- **原子拒绝**（`src/utf8.ts`）：编辑前先校验整段文本为合法 UTF-8（无孤立代理项），
  非法则抛 `InvalidEncodingError`，不分配任何节点、历史不变。

## API 摘要

```ts
const editor = new TextEditor("初始文本");
editor.insert(0, "😀");        // 标量偏移；返回新快照 id
editor.delete(2, 5);           // 删除标量区间 [2, 5)
editor.undo(); editor.redo();  // 线性历史
editor.checkout(id);           // 分支跳转
editor.releaseSnapshot(id);    // 释放快照，回收不再共享的节点
editor.text; editor.length;    // 文本 / 标量数
editor.byteLength;             // UTF-8 字节数
editor.arena.live;             // 存活节点数
```

## 构建与测试

沙箱内没有 TypeScript 编译器，`scripts/build.mjs` 是一个零依赖的类型擦除器，
把 `src/*.ts` 转成 `dist/*.js`（仅支持 src/ 使用的语法子集，详见脚本头注释）。

```
npm test     # 构建 + 运行全部测试
```

测试（`test/`）包含一个**独立标量数组模型**的随机模糊对照：数千步随机
插入/删除/撤销/重做/分支跳转/快照释放，逐步比对文本、标量数、字节数，
并专项覆盖跨叶删除、空串编辑、四字节字符边界，以及快照释放后
`arena.live === 当前快照节点数` 的精确回收断言。

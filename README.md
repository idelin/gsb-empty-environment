# tab-edit — 多选区缩进编辑库

一个零依赖的 TypeScript 编辑库，实现代码编辑器的 Tab / Shift-Tab 语义：
多选区可共享行、缩进每行只执行一次、所有制表符按视觉列计算、
每次变更映射全部选区、撤销逐字节恢复。

## 文档模型

```ts
interface Selection { anchor: number; caret: number }   // caret < anchor 即反向选区
interface EditorState { text: string; selections: Selection[] }
```

- 文本是带 `\n` 换行的普通字符串（`\r` 未特殊处理）。
- 列为**视觉列**：制表符前进到下一个 `tabSize`（默认 4）的倍数，其余字符前进 1 列。

## 操作语义

| 操作 | 行为 |
| --- | --- |
| `insertTab(state, tabSize?)` | 全部为光标时：在每个光标处插入空格到下一个四列制表位（已有 `\t` 按视觉列计入）；只要有一个非空选区，就退化为 `indentLines` |
| `indentLines(state, tabSize?)` | 每个被触及行行首插入空格，使现有缩进对齐到下一个制表位 |
| `outdentLines(state, tabSize?)` | 每个被触及行最多删除一个制表位的前导空白：取累计视觉宽度 ≤ `tabSize` 的最长空白前缀（`"  \tx"` 删 3 字符、`"\t\tx"` 删 1 个制表符） |
| `undo(state, record)` / `redo(state, record)` | 用记录的正/逆编辑重放，撤销逐字节恢复原文本与原选区 |

行去重规则：

- 多个选区触及同一行时，该行只缩进/反缩进一次（`collectTouchedLines` 用集合去重）。
- 非空选区的终点恰好落在某行行首时，不触及该行。
- 同一偏移上的重复光标只插入一次。

## 选区映射

每个操作先产生一组有序、不重叠的原子编辑 `{start, end, inserted}`，
一次性应用后，所有选区端点经 `mapOffset` 统一映射：

- 行首插入点上的端点锚定在行首（bias `start`，列 0 的光标保持列 0）；
- 光标处 Tab 插入的端点移到插入文本之后（bias `end`）；
- 落在被删除空白内的端点夹紧到行首；
- 反向选区保持方向。

## 独立实现核对

`src/reference.ts` 是同一语义的**逐行独立实现**（拆行 → 逐行变换 →
在 (行, 列) 空间映射选区 → 重新拼接），与 `src/editor.ts` 的
偏移/编辑实现不共享任何代码。`test/verify.js` 用确定性用例 +
3000 轮随机属性测试交叉核对两者，重点覆盖：

- 重叠选区、共享行、重复光标、反向选区；
- 混合空白（空格/制表符各种排列）；
- 主实现与参考实现文本和选区完全一致；
- 撤销逐字节恢复、重做逐字节重放；
- 复制选区不改变结果文本（每行一次）；
- 光标 Tab 后必落在制表位上。

## 运行

```sh
npm test        # 无需安装依赖：scripts/strip.js 把 src/*.ts 机械剥离为 .build/*.js 后执行测试
npm run typecheck   # 装有 TypeScript 时做严格类型检查
```

说明：本环境只有 Node 16、无 tsc，因此源码使用受限 TS 子集
（接口、单行类型别名、函数签名与变量标注），由 `scripts/strip.js`
做机械类型剥离后直接运行；在正常工具链下 `src/` 即是标准 TypeScript。

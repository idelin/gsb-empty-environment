# patch-rebase

补丁重基工具：接收基于**同一文本版本**的两份补丁 `A`、`B`，把 `B` 重基到
`A` 之后执行（`rebasePatch(B, A, policy)`），输出：

- `patch` —— 重基后的新补丁，坐标系为「应用 `A` 之后的文本」；
- `dropped` —— 无法保留的原编辑（含原因与重叠对象），**绝不默默丢操作**：
  恒有 `patch.edits.length + dropped.length === B.edits.length`；
- `conflicts` —— `conflict` 策略下被判定为冲突的子集。

## 编辑模型

每个编辑把基文本的半开区间 `[start, end)` 替换为 `text`：

| 条件 | 含义 |
| --- | --- |
| `start < end`, `text ≠ ""` | 替换 |
| `start < end`, `text = ""` | 删除 |
| `start = end` | 插入 |

同一补丁内的编辑按 `start` 排序且不重叠；同一点允许多个插入，保持声明顺序。

## 区间交叠策略

- `left-priority`：被重基到其上的补丁（左）优先，`B` 中重叠的编辑被丢弃并
  记入 `dropped`（`reason: 'left-priority-override'`）。
- `conflict`：重叠编辑记入 `conflicts`（同时也在 `dropped` 中），由调用方
  决定如何处理；幸存编辑集合与 `left-priority` 相同。

「重叠」指基文本区间相交，包括**插入点严格落在对方替换/删除区间内部**。
落在区间端点上的插入不算冲突：起点端插入落在替换文本之前，终点端插入落在
之后。

## 同点插入排序

两个补丁在同一位置都有插入时，按 `(patchId, editId)` 字典序排列，因此无论
哪份补丁先应用，最终文本一致。

## 来源映射

`applyPatch` 返回 `{ text, provenance }`，`provenance[i]` 标明结果第 `i`
个字符的来源：基文本下标（`{kind:'base', index}`）或某个编辑产生的字符
（`{kind:'edit', patchId, editId, offset}`）。重基保留编辑的 `id` 与
`patchId`，因此来源映射贯穿两次应用。

## 穷举验证

`src/verify.ts` 在短字符串（字母表 `{a,b}`，长度 ≤ 4）上枚举所有长度 ≤ 2
的补丁及所有补丁对（含两种补丁 ID 大小关系），验证：

1. 无编辑被默默丢弃（上述恒等式）；
2. 重基结果对中间文本始终合法；
3. **无冲突时两种应用顺序汇合**：`apply(apply(base, A), rebase(B, A))` 与
   `apply(apply(base, B), rebase(A, B))` 的文本与来源映射完全一致；
4. `conflict` 策略标记的编辑集合与 `left-priority` 丢弃的完全一致。

## 运行

环境自带 node（≥16）；本仓库通过 `node_modules/typescript` 符号链接复用本机
已有的 TypeScript 编译器（离线环境）。

```sh
npm test          # 编译 + 单元测试 + 穷举验证（基串长度 ≤ 3）
npm run verify:full  # 完整穷举（基串长度 ≤ 4，约 5 分钟）
npm run demo
```

也可用 `VERIFY_MAX_LEN=N` 环境变量调整穷举的基串长度上限。

## API

```ts
import { applyPatch, rebasePatch } from './src';

const r = rebasePatch(patchB, patchA, 'left-priority', base.length);
const out = applyPatch(applyPatch(base, patchA).text, r.patch);
// r.dropped / r.conflicts: 无法保留的原编辑
```

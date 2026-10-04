# patch-rebase

补丁重基工具：接收基于**同一文本版本**的两份补丁 `left` 与 `right`，把
`right` 转移到 `left` 之后执行，使得

```
apply(apply(base, left), rebase(left, right))  ===  两条路径汇合（无冲突时）
apply(apply(base, right), rebase(right, left))
```

## 数据模型

- 文本为字符串；编辑 `Edit = { id, start, end, text }` 表示把基文本的
  `[start, end)` 区间替换为 `text`；`start === end` 即插入。
- 补丁 `Patch = { id, edits }`，edits 按 `(start, end)` 排序且不交叠；
  同一偏移处的多个插入按数组顺序依次生效。

## 重基语义（src/rebase.ts）

- **区间交叠**：两编辑的基文本区间严格相交（含边界相接不算；插入点严格
  落在对方区间内才算）时，按策略处理：
  - `left-priority`：左补丁优先，右编辑进入 `unpreserved` 并附原因；
  - `conflict`：记入 `conflicts`（同时也列入 `unpreserved`），调用方应
    视为重基失败。
- **插入同一点**：两份补丁在同一偏移的插入按 `(patchId, editId)` 字典序
  排列，与重基方向无关，保证两个方向汇合。
- **不默默丢操作**：守恒保证——右补丁的每个编辑恰好出现在
  `patch.edits`（带 `origin` 来源映射）或 `unpreserved` 之一。
- **来源映射**：每个重基后的编辑携带
  `origin: { patchId, editId }` 指回原编辑。

## 用法

需要 Node.js ≥ 23.6（原生运行 TypeScript；22.6+ 可加
`--experimental-strip-types`）。

```sh
# 单元测试 + 短字符串穷举验证
node --test test/rebase.test.ts test/exhaustive.test.ts

# 仅穷举验证（约 12 万补丁对，含两种补丁 ID 顺序）
node src/verify.ts

# CLI：从 stdin 读 JSON 任务，结果写 stdout；有冲突时退出码为 2
echo '{"base":"hello world","strategy":"conflict",
       "left":{"id":"p1","edits":[{"id":"a","start":6,"end":11,"text":"world"}]},
       "right":{"id":"p2","edits":[{"id":"b","start":0,"end":5,"text":"hi"}]}}' \
  | node src/cli.ts
```

库用法：

```ts
import { rebase } from './src/rebase.ts';
import { applyPatch } from './src/model.ts';

const result = rebase(left, right, 'conflict');
if (result.conflicts.length === 0) {
  const next = applyPatch(applyPatch(base, left), result.patch);
}
```

## 穷举验证（src/exhaustive.ts）

对字母表 `{a,b}` 上长度 ≤ 3 的全部基文本（可用 `maxLength` 调大）、由
替换/删除/插入组成的所有 0–2 编辑补丁，枚举全部补丁对并验证：

1. **汇合**：两个方向均无冲突时，两种应用顺序结果相等；
2. **守恒**：`kept + unpreserved === 原编辑数`，`origin` 与被保留编辑一一
   对应，插入不会退化为区间编辑；
3. **策略一致性**：无交叠时 `left-priority` 与 `conflict` 输出相同；
   有交叠时 `left-priority` 丢弃的正是冲突编辑。

每对补丁还在两种补丁 ID 顺序下各验证一遍，以覆盖同点插入的排序逻辑。
默认规模约 12 万对（<1s）；`maxLength: 4` 时约 111 万对（约 36s），已通过。

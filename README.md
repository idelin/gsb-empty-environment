# annotation-intervals

多层叠加的批注区间库（TypeScript）。在文本编辑（插入 / 删除 / 替换 / 批量 / 撤销）下
维护批注区间的映射、失效记录与覆盖查询，并按段输出活跃批注集合。

## 核心语义

**黏附位（stickiness）**：每个边界有 `left` / `right` 黏附位。在边界处插入文本时，
边界黏附到哪一侧就随哪一侧移动——因此区间插入是否扩展批注由左右黏附位决定：

- 在 `start` 处插入：`startSticky: 'left'` → 扩展（start 不动）；`'right'` → 不扩展。
- 在 `end` 处插入：`endSticky: 'right'` → 扩展（end 后移）；`'left'` → 不扩展。
- 默认 `(startSticky: 'right', endSticky: 'left')`，即边界插入不扩展。
- 零宽批注（`start === end`）是点批注，两个黏附位强制相同（以 `startSticky` 为准）。

**删除映射**：位置落在删除区间内则折叠到区间起点；落在其后则前移区间长度。

**失效记录（tombstone）**：

- 非零宽批注被删除区间完全覆盖 → 置为 `active: false`，记录保留在 `annotations()` 中。
- 零宽批注位置严格位于删除区间内部（`s < p < e`）→ 失效；落在删除边界上（`p === s`
  或 `p === e`）则存活并折叠到区间起点。
- 失效批注不再参与映射与查询；`undo()` 恢复该次编辑前的完整状态，可重新定位并复活。

**编辑与撤销**：

- `applyEdit` / `applyEdits`：`replace` 规范化为 delete+insert；批量编辑按顺序作用于
  中间文本状态，且整批作为一条历史记录。
- `undo()`：弹出最近一次（批）编辑，将文本与**完整批注表**恢复到该编辑应用前的快照
  （之后新增/删除的批注记录也一并回到快照状态）。

**覆盖查询 `query(start, end)`**：返回与 `[start, end)` 相交的活跃批注（按 ID 升序）。
零宽批注在 `start <= p < end` 时命中；点查询 `[p, p)` 命中位置为 `p` 的零宽批注以及
满足 `start <= p < end` 的区间批注。

**分段输出 `segments()`**：以所有活跃批注边界切分文本，输出每段的活跃批注 ID 集合
（升序）；相邻且集合相同的段合并；零宽批注输出为 `start === end` 的点段。结果按
`(start, end)` 排序。

## 文件

- `src/annotations.ts` — 区间库：`AnnotationStore`、`Edit`、`Segment`、`normalizeEdits`。
- `src/reference.ts` — 逐位置参考模型：用字符/间隙令牌（带唯一 uid）独立模拟同一语义，
  作为区间实现的核对 oracle。
- `src/verify.ts` — 核对器：显式场景（边界黏附扩展、同点零宽批注、批量替换、多层叠加）
  + 种子化随机操作组合（插入/删除/替换/批量/撤销/增删批注），每个操作后逐位置比对：
  每个字符位置的覆盖集合、每个间隙位置的零宽批注集合（区间直算与 `segments()` 展开
  两条路径）、失效 ID 集合、随机范围 `query` 结果。

## 运行

```sh
npm test            # 编译 + 显式场景 + 300 轮随机核对
node dist/verify.js 2000   # 更大规模随机核对
```

需要本机可用的 TypeScript 编译器（`node_modules/typescript`）与 Node.js。

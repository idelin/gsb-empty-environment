# bidi-line-layout

简化的单行双向文本布局库（TypeScript）。输入是已排好视觉顺序、各自带方向的字形
 run，库负责逐字形定位、点击命中测试与拖选几何计算。

## 数据模型

- `Glyph`：一个字形（或簇），含逻辑范围 `[start, end)` 与前进宽度 `width`；
  零宽字形（如组合符号）`width === 0`。
- `GlyphRun`：同方向字形的序列，字形按**逻辑（阅读）顺序**给出。
- `layoutLine(runs, originX?)`：run 按给定顺序横向排布（调用方已完成 bidi run
  排序），RTL run 内第一个逻辑字形位于 run 的右边缘。返回逐字形位置表
  `positions`（含每个字形的视觉 `x`、`width`、逻辑范围）。

## 命中测试

`hitTest(line, x)` 把点击横坐标映射为逻辑插入点 `{ offset, affinity }`：

- run 内按字形中点拆分左右半；RTL run 内逻辑方向镜像（视觉左半对应逻辑尾部）。
- 视觉 run 边界由点击落在边界的哪一侧裁决归属哪个 run（即哪个逻辑偏移）。
- 同一逻辑偏移可能拥有两个视觉插入位置（bidi 边界处），`affinity` 记录命中的是
  哪一个：`left` 取较小 x，`right` 取较大 x；无歧义时按点击所在半侧报告。
- `caretX(line, caret)` 是逆运算：歧义偏移按 affinity 取最小/最大候选 x。

## 拖选矩形

`selectionRects(line, anchor, focus)` 返回 `{ x, width }[]`，每个被覆盖的 run 一
段，按视觉顺序排列。逻辑连续的选区跨 bidi run 时视觉上可能分离（返回不相交的矩
形）。簇内部分偏移按字形宽度线性插值；零宽字形不产生选区宽度。

## 运行测试

需要 Node ≥ 23.6（原生类型擦除，无需编译）：

```sh
node --test test/*.test.ts   # 或 npm test
```

测试以逐字形位置表为核心 fixture（`test/fixtures.ts`）：LTR 连字簇 + RTL run（内
嵌零宽字形）+ LTR run，覆盖布局表、边界亲和性裁决、零宽命中与选区矩形。

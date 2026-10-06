# bidi-line-layout

简化文本行布局库（零依赖 TypeScript）：输入已按方向分组、排好视觉顺序的
glyph runs，提供点击命中测试与拖选矩形计算。

## 数据模型

- `Glyph`：逻辑字符范围 `[start, end)` + 前进宽度 `width`（可为 0，如组合符）。
- `GlyphRun`：同方向（`'ltr' | 'rtl'`）的 glyph 序列，按逻辑顺序存储。
- `TextLine`：runs 按**视觉顺序**排列，从 x = 0 起自左向右布局。

`layoutLine(line)` 生成逐字形位置表 `GlyphPosition[]`（按视觉 x 排序）：

- LTR run 内 glyph 自左向右推进；RTL run 内逻辑首个 glyph 位于最右。
- 每个 glyph 记录 `leftOffset` / `rightOffset`——视觉左/右边缘对应的逻辑
  偏移。RTL glyph 两者互换，因此 RTL 映射与边界亲和性都是显式的。

## API

- `layoutLine(line: TextLine): LineLayout` — 构建位置表（含行宽、逻辑长度）。
- `hitTest(layout, x): CaretPosition` — 点击横坐标 → 逻辑插入点
  `{ offset, affinity }`。glyph 按中点分左右半区，取较近边缘的偏移与亲和性；
  零宽 glyph 不可直接命中；越界点击钳制到最近的行边缘。方向边界处同一视觉 x
  对应两个逻辑位置，由 `affinity: 'left' | 'right'` 裁决。
- `selectionRects(layout, anchor, focus, height?, y?): Rect[]` — 逻辑选区 →
  视觉矩形列表（按视觉顺序）。混合方向选区可能分离为多个矩形；恰好相接的
  矩形会被合并。零宽 glyph 产生零宽矩形。

## 运行测试

需要 Node.js ≥ 22.6（依赖原生 type stripping，无需编译、无第三方依赖）：

```sh
npm test
```

`scripts/test.sh` 会自动在 `~/.nvm/versions/node` 中挑选满足版本要求的
Node。测试通过逐字形位置表（`test/layout.test.ts`）验证混合方向与零宽
glyph 的布局、命中与选区行为。

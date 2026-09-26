# bidi-visual-layout

对**已提供方向等级**（resolved embedding levels，偶级=LTR、奇级=RTL）的文本片段，计算
视觉顺序、光标移动映射、软折行双位置和跨视觉行选择。不实现完整 Unicode 双向分类，
不使用浏览器布局引擎；节点树与字形度量全部由夹具（fixture）注入。

## 设计要点

- **嵌套等级**：`visualOrder(levels)` 按 UBA L2 从最高等级向下逐层反转连续
  `level >= N` 的片段，支持任意深度的嵌套镜像。
- **独立逐片段排列参考**：`test/reorder.test.ts` 中另写了一个基于“嵌入树镜像”的
  递归实现（分段而不展平），与生产用迭代实现相互独立。
- **穷举往返验证**：枚举所有合法等级游走（起始 0/1，每步 ±1/不变，长度≤8，
  深度≤4，3000+ 序列）验证两种实现一致；布局层再穷举长度≤6、深度≤3 的序列，
  校验放置顺序、`2N` 条边、命中往返、物理左右行走覆盖每个槽一次。
- **字形簇原子性**：`splitGraphemes` 是最小实现（组合记号、ZWJ 序列、国旗 RI
  对），夹具也可直接提供 `clusters`；簇内部不存在任何光标位置。
- **边界双位置 + 显式 affinity**：
  - 方向片段交界处，同一物理位置挂两个逻辑光标（如偏移 3 和 6），命中结果
    `HitResult.carets` 返回**全部**候选，不压成唯一偏移；
  - 软折行把同一逻辑偏移拆到两个视觉行，用 `"forward" | "backward"` 显式区分。
- **逻辑左右 ≠ 视觉左右**：`moveVisual(caret,"right"/"left")` 按物理屏幕移动，
  RTL 片段内向右走逻辑序后退；`moveLogical(caret,"forward"/"backward")` 按偏移
  ±1 移动，视觉上可能在片段内跳。
- **选择不丢方向**：`VisualSelection` 保留 anchor/active 两个版本化 caret，
  `visualDirection` 保留拖拽方向，`rects()` 跨行绘制，`copiedText()` 始终按
  **逻辑顺序**输出；视觉选择在 bidi 下可能对应多个不连续逻辑跨度（`logicalSpans`）。
- **版本隔离**：`DocumentModel.updateLevels(...)` 返回新版本模型并把
  `version +1`；旧 caret/hit/selection 与新布局互操作时抛 `StaleLayoutError`，
  旧命中版本绝不与新布局混用。多值映射（一个边界 → 多个逻辑 caret）全程保留。

## 目录结构

```
src/
  types.ts       节点树 / 等级 / affinity / 度量接口
  grapheme.ts    最小字形簇切分
  model.ts       DocumentModel：树展平、等级继承、局部等级更新（版本化）
  reorder.ts    UBA L2 视觉重排
  layout.ts     BidiLayout：软折行、视觉槽位、Caret、命中、视觉/逻辑移动
  selection.ts  VisualSelection：方向保留、跨行矩形、逻辑序复制
test/           Node.js 内置 test runner（node:test）
examples/demo.ts 运行样例
```

## 运行

需要 Node ≥ 22.6（原生 TypeScript 类型擦除）；已在 Node 23.9.0 验证，无需安装
任何依赖。

```bash
npm test          # node --experimental-strip-types --test test/*.test.ts
npm run demo      # examples/demo.ts 的完整演示
```

## 样例（节选）

输入逻辑串 `abcDEFghi`，等级 `000111000`（D/E/F 为显式 RTL 片段）：

```
logical: abcDEFghi  levels: 000111000
visual : abcFEDghi
offset 3: x=30 affinity=backward | x=60 affinity=forward
offset 6: x=30 affinity=backward | x=60 affinity=forward
click x=30 -> carets 3 & 6  primary: 6/backward

Right walk  : 1/f -> 2/f -> 6/b -> 5/b -> 4/b -> 6/f -> 7/f -> 8/f -> 9/b
Forward walk: 1 -> 2 -> 3 -> 4 -> 5 -> 6
```

- 物理向右穿过 RTL 片段时偏移走 `6,5,4`（逻辑后退），逻辑 Forward 严格 `1..6`；
- 偏移 3 与 6 在 `x=30`、`x=60` 各有两条视觉边界；
- 软折行（宽 30）后偏移 3 的两个 affinity 分居第 0、1 行；
- 跨行选择 `bcDEFgh` 复制回逻辑顺序，矩形覆盖三行；
- 局部把 DEF 改成等级 0 后 `version=1`，旧 caret 立即 `StaleLayoutError`。

## 明确不做的事

- 不做 UBA 弱类型解析、括号镜像、数字处理、isololate/override 控制字符；
  输入必须是已解析等级。
- 不做 UAX#29 完整字形簇切分，需要时由夹具提供 `clusters`。
- 不渲染、不测量真实字体：`GlyphMetrics.clusterWidth` 由调用方提供。

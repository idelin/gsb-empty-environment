# 双向文本的视觉顺序与光标移动映射

在给定方向等级（embedding levels）的前提下，计算文本片段的视觉排列、
光标移动、命中测试与选择/复制。**不实现完整 Unicode 双向分类**：等级由
上游（夹具）提供，本仓库只做 level → 视觉序、几何与映射。不使用浏览器
布局引擎，节点树与字形度量均由夹具给出。

运行环境：Node.js ≥ 22.6（使用内置 TypeScript 类型擦除直接运行 `.ts`，
测试用内置 `node:test`，零第三方依赖）。本仓库用 nvm 的 Node v23 验证。

## 运行

```bash
npm test          # node --test，27 个用例
npm run demo      # examples/demo.ts 的可读样例
```

## 设计要点

- **给定等级，不做分类**：偶数级 LTR、奇数级 RTL（UAX #9 奇偶约定）。
- **等级 → 视觉序**：`reorderByLevels`（UAX L2，从最高级向下翻转），另给
  独立的逐片段递归参考实现 `reorderReference`（按最小级切分、奇级反转、
  递归处理子片段）。两者在穷举小等级序列上逐一比对，作为有意义的交叉验证。
- **字形簇不可停光标**：用 `Intl.Segmenter`（仅作切分工具，不作布局）得到
  grapheme cluster，ZWJ 家庭、旗帜序列等是一个原子簇，偏移以簇为单位，
  另附 UTF-16 code unit 偏移作为次级坐标。
- **显式 affinity 的双位置**：每个 caret 带 `{edge: 'leading'|'trailing',
  level}`；同一逻辑偏移在方向片段边界可有两个视觉 x（leading / trailing），
  软折行边界用 `lineBias: 'upper'|'lower'` 显式表示，双值**不压成唯一偏移**。
- **逻辑左右 ≠ 视觉左右**：RTL 簇的 leading 在其视觉右侧；RTL 段内“逻辑
  前进”是视觉向左。`moveVisual`（物理方向键，行内按 x 步进，离边时按同
  偏移的 upper/lower 双 home 跨行）与 `moveLogical`（偏移单调，边界处切换
  双 home）分别实现。
- **命中测试是多值的**：正好点在共享边界上时返回两个 caret；非边界点吸附
  到最近的唯一位置（平局取视觉左侧，确定且不丢偏移）。
- **选择保留方向、复制回逻辑序**：`Selection` 保存 anchor/head 两个完整
  caret（含偏移、affinity、行），`logicalDirection` 反映拖动方向，
  `visualRanges()` 给逐视觉行的高亮区间（含跨整行的边界情况），
  `copySelection()` 按 `min..max` 逻辑偏移还原成逻辑顺序文本。
- **局部等级更新与版本隔离**：`setNodeLevel` / 改行宽都会重建快照并
  递增 `version`；旧 caret / selection 再触碰新布局时抛 `StaleLayoutError`，
  旧命中版本绝不与新布局混用。

## 代码结构

- `src/types.ts` — 类型模型、奇偶→方向、`StaleLayoutError`
- `src/reorder.ts` — reverse-by-levels、独立递归参考实现、序列穷举器
- `src/clusters.ts` — 节点树拍平（span 等级继承）、grapheme 切分
- `src/layout.ts` — 软/硬折行、盒构造、双位置 caret 图
- `src/model.ts` — `BidiTextModel`、`Selection`：移动、命中、复制、版本
- `src/fixtures.ts` — 节点树与字形度量夹具
- `tests/*.test.ts` — `node:test` 用例（含穷举与往返性质）
- `examples/demo.ts` — 运行样例

## 穷举与往返验证

- 长度 0..6、等级 0..3 的全部 5461 个序列：两种独立重排实现结果一致；
  结果恒为合法排列。
- 长度 ≤5、等级 0..2 的所有序列：每个视觉 caret 在自身 x 命中必能映射回
  自身；每个逻辑边界至少有一个视觉 home；每行排列等于参考置换。
- 具体嵌套片段、纯 RTL 折行、ZWJ/旗帜簇、硬换行、跨行选择与版本失效均有
  断言覆盖。

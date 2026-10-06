# label-layout

TypeScript 标签布局库：给定固定锚点和每个标签至多 4 个候选位置（至多 12 个标签），
在画布内选出一组互不重叠的标签放置，使总权重最大；权重相同时使总位移最小。

## 语义

- **重叠判定**：仅当两个矩形的内部严格相交才算冲突；边缘或角相接**不算**重叠。
- **画布约束**：候选矩形必须完整落在 `[0, width] x [0, height]` 内，贴边允许。
- **目标（字典序）**：先最大化已放置标签的总权重，再最小化总位移
  （锚点到所放矩形最近点的欧氏距离）。
- **隐藏标签**：`hidden: true` 的标签完全不参与放置——不会被选中、权重不计入、
  不阻挡其他标签，也不会出现在冲突对中，因此无法“冒充”成功放置。

## API

```ts
import { solveLayout, bruteForceOptimum } from "label-layout";

const result = solveLayout(labels, { width: 100, height: 100 });
// result.placements        选中的放置（labelId、candidateIndex、rect、weight、displacement）
// result.totalWeight       总权重
// result.totalDisplacement 总位移
// result.conflicts         冲突对（候选集合存在重叠的标签对及重叠候选数）
// result.unplaced          未能放置的可见标签
// result.hidden            被排除的隐藏标签
```

- `solveLayout(labels, canvas)`：精确分支定界求解（12 标签 × 4 候选规模）。
- `bruteForceOptimum(labels, canvas)`：独立的全组合穷举，用于核对求解器结果。

## 开发

```sh
npm run build   # tsc 编译到 dist/
npm test        # 编译并运行测试（含对随机实例的全组合穷举核对）
```

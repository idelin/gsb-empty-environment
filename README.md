# label-layout

TypeScript 标签布局库：固定锚点、每个标签至多 4 个候选位置，在画布内选择
**不重叠** 的标签集合，目标按字典序：

1. 最大化已放置标签的 **总权重**；
2. 在权重最大的方案中，最小化 **总位移**（锚点到候选矩形最近边的欧氏距离）。

接边/接角不算重叠（严格面积相交才算）；贴画布边界算在画布内。
放不下的标签进入 `unplaced`（隐藏），绝不计入 `placed` 或总权重。

## 使用

```ts
import { fourCandidates, solveLabelLayout } from "./src/solver";
import { verifyLayout } from "./src/verify";

const input = {
  canvas: { x: 0, y: 0, w: 120, h: 90 },
  labels: [
    { id: "L0", anchor: { x: 15, y: 20 }, weight: 9,
      candidates: fourCandidates({ x: 15, y: 20 }, 22, 10, 1) },
    // ...至多 12 个标签
  ],
};

const result = solveLabelLayout(input);   // 分支定界精确求解
const report = verifyLayout(input, result); // 独立全组合穷举核对
```

- `solveLabelLayout` 返回 `placed`（含候选序号、位移）、`unplaced`（隐藏标签）、
  `totalWeight`、`totalDisplacement`。
- `computeConflictPairs(labels)` 输出候选级冲突对（A 的候选 i 与 B 的候选 j 重叠）。
- `verifyLayout` 做两件事：
  1. **一致性**：`placed`/`unplaced` 必须恰好划分全部标签（隐藏标签不能冒充
     已放置），候选在画布内、两两不重叠、上报总和与重算一致；
  2. **最优性**：独立穷举所有 `标签 -> {候选 | 丢弃}` 组合（仅按精确不可行
     剪枝），核对总权重与总位移均为最优。

## 脚本

```sh
bun run demo   # 12 标签示例：输出选择、冲突对并穷举核对
bun test       # 单元测试（含随机实例求解器 vs 穷举对拍）
```

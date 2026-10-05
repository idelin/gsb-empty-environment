# ordered-tree-diff

语法树差分工具：比较两棵**有序树**（节点含 `type`、`value` 与可选稳定 `id`），
产出最小代价的顺序补丁（插入 / 删除 / 更新 / 搬移），并附带独立校验应用器
与小树穷举校验器。TypeScript 实现，零运行时依赖。

## 快速开始

```bash
npm test        # 转译 src/*.ts 并运行穷举校验（约 5 万+ 用例）
npm run build   # 仅转译到 dist/
npm run typecheck   # 需要本机安装 typescript（tsc --noEmit）
```

```ts
import { diffTrees, applyPatch } from './src';

const a = { type: 'R', children: [
  { type: 'P', children: [{ type: 'K', id: 'k', children: [] }] },
  { type: 'Q', children: [] },
]};
const b = { type: 'R', children: [
  { type: 'P', children: [] },
  { type: 'Q', children: [{ type: 'K', id: 'k', children: [] }] },
]};

const { ops, optimal } = diffTrees(a, b);
// ops = [{ op: 'move', from: [0, 0], toParentPath: [1], toIndex: 0 }]
const result = applyPatch(a, ops); // 深等于 b；a 不被修改
```

## 匹配算法（`src/diff.ts`）

1. **稳定 ID 优先**：两棵树中 `id` 相同的节点首先配对（输入中 id 必须唯一，
   否则抛 `TreeDiffError`）。
2. **根节点强制配对**。
3. **结构匹配**：自顶向下，对每个已配对节点用最小代价 DP 对齐两棵子序列——
   更新代价 0/1/2（类型、值差异），插入/删除代价为子树节点数，
   已在别处配对的节点跳过代价为 0（即搬移）。DP 平局按固定规则打破
   （对角线优先，自末尾回溯），因此等价代价的方案产出**确定唯一**的补丁。
4. **闭包过滤**：丢弃祖先链未闭合到根的配对（回退为删除+插入）。
   可证明：只要匹配满足祖先闭包，按目标树先序生成的搬移**绝不会产生祖先环**
   ——搬移时目标父节点已就位，其祖先链直通根，而根不可能位于被搬子树内。

## 补丁语义（`src/types.ts`）

补丁是按顺序应用的操作序列，每个操作作用于前一个操作产生的树状态：

- `update`：替换节点的标量字段（`type` 总是设置；`value`/`id` 存在则设置、缺席则移除）。
- `insert`：在 `parentPath` 的 `index` 处插入整棵子树。
- `delete`：删除 `path` 处子树（根不可删）。
- `move`：把 `from` 处子树搬为 `toParentPath` 的第 `toIndex` 个子节点
  （`toIndex` 为摘除后的索引）；目标父节点不得位于被搬子树内。

## 预算中断

`diffTrees(a, b, { budget })` 以 DP 单元数计量匹配工作量。预算耗尽时匹配
提前停止，剩余差异以普通插入/删除/更新补齐——**返回的补丁始终完整可用**，
此时 `optimal` 为 `false`（未证最优）；正常完成时为 `true`。

## 独立应用器（`src/apply.ts`）

`applyPatch(tree, ops)` 不修改输入，逐条校验并应用操作：路径合法性、
索引范围、子树结构、搬移的祖先环检测（路径前缀判定），最后检查整树 id
唯一性。任何违规抛 `PatchApplyError`。

## 穷举校验（`src/exhaustive.ts`）

- 全部 ≤3 节点的有序树（类型 × 值、类型 × id、类型 × 值 × id 三组）两两差分；
  4 节点树抽样；另加 300 对带 id 的随机大树。
- 每对校验：两次运行结果一致（确定性）、深拷贝输入结果一致（与对象同一性无关）、
  补丁经独立应用器后精确等于目标、输入不被修改、无预算时 `optimal === true`、
  小预算下补丁仍完整可用。
- 金样本测试钉死等价方案的规范选择（平局打破规则）与搬移顺序。

## 构建说明

`scripts/build.mjs` 使用 Babel（`@babel/preset-typescript`）做转译；
本仓库的 `node_modules` 由本机 yarn 缓存离线解出（见 `scripts/extract-babel.sh`）。
有网络环境下可直接 `npm install typescript` 后用 `tsc` 构建。

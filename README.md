# token-move-diff

Token 级文本差异工具，支持**整块搬移识别**：把每个 token 区分为
`kept`（原位保留）、`moved`（随块搬移）、`deleted`（删除，仅旧序列）或
`inserted`（重新插入，仅新序列），并给出每个 token 的稳定来源标签。

## 运行

需要 Node.js >= 23.6（原生运行 TypeScript，无需编译）：

```sh
node --test test/diff.test.ts   # 或 npm test
node examples/demo.ts           # 或 npm run demo
```

## 用法

```ts
import { diffTokens, verifyDiff } from './src/index.ts';

const result = diffTokens(
  'a x b c d'.split(' '),
  'c d a NEW b'.split(' '),
  { budget: 100_000 },          // 可选：搜索预算（DP 状态扩展数）
);
// result.oldTokens / newTokens: 每个 token 的 label / status / blockId
// result.blocks: 搬移块（旧/新序列中的闭区间跨度）
// result.stats: kept/moved/deleted/inserted/blockCount/optimal/...
const check = verifyDiff(oldTokens, newTokens, result); // { valid, errors }
```

标签约定：旧 token 为 `O<旧下标>`；新序列中匹配到的 token 继承同一
`O<旧下标>` 标签，新插入的 token 获得 `I<n>` 标签。

## 算法

1. **匹配（保留最大化）**：按值分组、按出现次序配对的最大基数匹配。
   匹配上的 token 即"保留" token（区别于删除/重新插入）；该配对达到
   最大匹配数，且同值的配对互不交叉（确定性）。
2. **搬移块规划（DP）**：把每个匹配 token 分类为 `kept`（构成来源下标
   严格递增的主干）或 `moved`。目标按字典序：
   1. 最大化 kept token 数（即主干为最长递增子序列）；
   2. 最小化搬移块数；
   3. 按来源位置决胜：被移动 token 的来源下标之和最小（优先移动来源
      更靠前的 token）。
   相邻 moved token 合并为一块，当且仅当来源下标递增且两者之间没有
   其他匹配 token——因此块内可以容忍小修改（删除/插入的 token 落在
   块的跨度内），且各块的来源区间互不重叠。
3. **预算与回退**：`budget` 限制 DP 状态扩展数。预算耗尽时返回贪心
   方案（LIS 主干 + 其余移动），标记 `optimal: false`、
   `budgetExhausted: true`——仍是合法、可通过 `verifyDiff` 验证的方案。
4. **验证**：`verifyDiff` 独立检查标签稳定性、匹配一致性、主干递增、
   块成员连续性与来源区间不重叠、统计数字一致。

## 测试

`test/diff.test.ts` 覆盖：重复块、块内小修改（删除/插入落在搬移块
跨度内）、嵌套候选（交换对、整体搬移 vs 拆分）、预算耗尽回退、篡改
检测，以及对 300 组小规模随机输入用穷举搜索核对 DP 的最优性
（`src/exhaustive.ts`）。

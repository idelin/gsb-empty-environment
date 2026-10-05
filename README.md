# 评委分配器（judge-assignment）

为每份作品安排 **3 名评委**，同时满足专业匹配、工作量均衡与回避关系，
并且**任意一对评委共同评审的作品最多 2 份**（联合限制，不可忽略）。
目标：在所有可行分配中，使**评委工作量的最大差最小**。

## 约束模型

- 每份作品恰好 `judgesPerWork`（默认 3）名评委，评委专业须覆盖作品专业，且不与作品存在回避关系；
- 任意评委对共同评审的作品数 ≤ `maxPairCoJudging`（默认 2）；
- 目标函数：最小化 `max(load) − min(load)`（全体评委，含未参与评审者）；
- 规模上限：`maxWorks`（默认 12）份作品，超出返回 `unsupported`。

## 使用方法

```bash
npm install        # 安装 typescript（devDependency）
npm test           # 编译并运行测试
npm run demo       # 运行示例
```

```typescript
import { solve, verifyAssignment } from './src';

const result = solve({
  works: [{ id: 'w1', field: '结构' }, /* ...最多 12 份 */],
  judges: [{ id: 'j1', fields: ['结构'] }, /* ... */],
  conflicts: [{ judgeId: 'j1', workId: 'w1' }],
});

if (result.status === 'ok') {
  // result.assignment: workId -> [judgeId x 3]
  // result.maxLoadDiff: 已最小化的工作量最大差
  // result.pairCoJudging: 每个评委对的共评次数（均 <= 2）
} else if (result.status === 'infeasible') {
  // result.witness: 无解见证（见下）
}
```

## 返回结果

`status` 取值：

- `ok`：最优分配，附带工作量、评委对共评表；可用 `verifyAssignment` 独立复核全部约束；
- `infeasible`：无解，附**容量或回避见证**；
- `unsupported`：作品数超过规模上限（默认 12）；
- `undetermined`：搜索节点预算耗尽（默认 100 万节点/次，可用 `nodeLimit` 调整），理论上存在，12 份作品规模下实际不会触发。

## 无解见证（Witness）

评委对的联合限制会显著压缩可行域，因此见证全部从该限制导出：

1. **`work-conflict`（回避见证）**：某作品合格评委不足 3 名，列出合格评委与被回避排除的同专业评委；
2. **`capacity`（容量见证）**：存在作品子集 S，其合格评委集合 N(S) 在联合限制下容量不足。检查两个必要条件（按 |S| 升序枚举，保证极小）：
   - 评委席位上界：评委 j 每评一份作品消耗 K−1 个评委对名额，故 `load(j) ≤ ⌊P·(|N|−1)/(K−1)⌋`；
   - 评委对总数上界：每份作品需 C(K,2) 个评委对，每对最多共评 P 份；
3. **`combinatorial-core`（组合核心）**：上述必要条件均满足但精确搜索仍不可行时，用逐删除法求出极小不可行作品子集——核心本身不可行、删去任意一份即可行，是联合限制导致无解的严格见证（可用返回值中的作品子集重新调用 `solve` 复核）。

## 算法

- 回溯搜索：按 MRV（当前可行组合最少的作品优先）选择分支，组合按“当前负载低、评委对使用少”排序；
- 剪枝（均为必要条件，保证完备性）：每名评委的剩余可评量受评委对余量约束 `⌊residual/(K−1)⌋`，
  据此检查工作量下界赤字与剩余席位容量；
- 目标优化：总工作量固定为 3W，负载落在 `[lo, lo+D]` 内必有 `⌈avg⌉−D ≤ lo ≤ ⌊avg⌋`；
  按 D 升序枚举、首个可行 D 即最优（每次判定都是完备的）；
- 复杂度：作品数 ≤ 12，容量见证枚举 2^12 个子集；搜索在实际规模下毫秒级完成。

## 目录结构

- `src/types.ts`：公开类型（问题、结果、见证）；
- `src/internal.ts`：归一化、组合生成、回溯搜索器；
- `src/witness.ts`：容量见证与组合核心构造；
- `src/solver.ts`：`solve` 主流程（见证检查 → 可行性 → 分层优化）；
- `src/verify.ts`：`verifyAssignment` 独立校验器；
- `test/solver.test.ts`：13 个测试（规模限制、两类见证、最优性、联合限制、校验器、性能冒烟）；
- `examples/demo.ts`：三个场景示例（最优解、容量见证、回避见证）。

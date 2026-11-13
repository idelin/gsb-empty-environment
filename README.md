# 检查点治理与闪电贷抗性

这个包提供可直接导入的 TypeScript 治理宿主，核心约束是：**投票权只读提案创建位置 `P=(blockNumber,txIndex)` 之前的冻结快照，不读投票时的当前余额。**

## 使用

```ts
import {
  CheckpointStore,
  DelegationLedger,
  Governance,
} from "./index.ts";

const checkpoints = new CheckpointStore();
checkpoints.writeSnapshot({ alice: 100, bob: 50 }, 150, [10, 0]);

const delegations = new DelegationLedger();
delegations.setDelegate("alice", "bob", [10, 1]);

const governance = new Governance({
  checkpoints,
  delegations,
  config: {
    generation: 3,
    quorumNumerator: 1,
    quorumDenominator: 2,
    votingDelayBlocks: 1,
    votingPeriodBlocks: 100,
    queueDelayBlocks: 0,
    queueWindowBlocks: 1_000,
    executionDelayBlocks: 10,
    executionWindowBlocks: 1_000,
  },
});

const proposal = governance.createProposal({
  id: "p-1",
  proposer: "alice",
  position: [20, 0],
  action: { target: "0xabc", data: "0x1234" },
});

governance.castVote("p-1", "bob", "for", [21, 0]);
governance.queueProposal("p-1", [121, 0]);
governance.executeProposal(
  "p-1",
  [131, 0],
  proposal.actionDigest,
  proposal.configGeneration,
);
```

所有金额接受 `bigint | number | decimal string`，内部统一用 `bigint`，读接口返回 `bigint`。位置可写成 `{ blockNumber, txIndex }`、`{ block, txIndex }` 或 `[blockNumber, txIndex]`。

## 检查点语义

- `recordBalance(account, position, balance)` 写账户检查点。
- `recordTotalSupply(position, totalSupply)` 写总供应量检查点。
- 同一 `(blockNumber,txIndex)` 重复写入会覆盖旧值。
- `balanceAt(account, P)` 与 `totalSupplyAt(P)` 严格查询 `P` 之前最后一个值，不包含 `P` 本身。
- `transfer`、`mint`、`burn` 会同时维护账户和总供应量检查点。
- `writeSnapshot` 适合从链上日志批量导入同一区块/交易后的完整状态。

## 委托

- 每个账户最多一个 outgoing delegate，因此无环图是带方向森林。
- 写入会产生自环或任意有向环的委托时抛出 `DELEGATION_CYCLE`。
- 提案创建时冻结 `P` 之前的余额和委托图。之后的余额、铸造/销毁、委托变更都不影响该提案。
- 投票权按委托链末端归属，每个底层账户余额只计一次。`a -> b -> c` 时，`c` 的票权为 `a+b+c`，`a` 和 `b` 自身不能重复使用这些余额投票。

## 提案时间线

块号区间均为半开区间 `[startBlock,endBlock)`：

- 投票延迟：`[P.blockNumber, P.blockNumber + votingDelayBlocks)`
- 投票期：`[voteStart, voteStart + votingPeriodBlocks)`
- 排队期：`[voteEnd + queueDelayBlocks, voteEnd + queueDelayBlocks + queueWindowBlocks)`
- 执行期：`[queuedAt.blockNumber + executionDelayBlocks, queuedAt.blockNumber + executionDelayBlocks + executionWindowBlocks)`

提案状态依次为 `pending`、`active`、`succeeded|defeated`、`queued`、`executed`。投票通过必须同时满足：

```text
forVotes >= ceil(snapshotTotalSupply * quorumNumerator / quorumDenominator)
forVotes > againstVotes
```

每个账户每个提案只能投一次。重复投票抛 `VOTE_ALREADY_CAST`，投票后的同一交易余额变化不会改变已计算权重。

## 执行绑定

提案冻结 `actionDigest` 和创建时的 `configGeneration`。执行时校验：

- 排队状态和执行块区间；
- 动作摘要与提案绑定摘要一致；
- 调用方提供的配置代次与提案代次一致；
- 当前治理配置代次仍与提案代次一致。

任一不一致都会在执行前抛错，不会改变状态。动作摘要使用稳定键排序的 canonical JSON 与 SHA-256。

## 闪电贷审查

```ts
import {
  currentBalanceReviewCallback,
  snapshotReviewCallback,
  reviewFlashLoanScenario,
} from "./index.ts";

const current = currentBalanceReviewCallback(); // 找到 shortest witness
const snapshot = snapshotReviewCallback();       // null

const { currentBalanceWitness, snapshotModelSafe } = reviewFlashLoanScenario();
```

审查器最多使用 4 个账户和同块 2 笔交易空间。默认模型返回 `borrow -> vote -> repay` 的最短序列，其中：

- `voteWeightObserved` 包含临时借入余额；
- `balanceBefore === balanceAfter` 且 `netBalanceRestored === true`；
- 基线结果 `defeated`，攻击后 `passed`；
- 快照模型下同一序列不改变结果，因此返回 `null`。

可以传入自定义 `FlashLoanModel`，包括 `snapshotBalances`、`againstVoters`、`lender`、`minTransactions`、`maxTransactions` 等字段；超过 4 个账户会拒绝。设置 `minTransactions: 2` 可强制返回同块 `txIndex=0/1` 的两笔交易见证。

## 测试

```bash
npm test
```

测试覆盖严格位置查询、同位置覆盖、同块 `txIndex`、委托环、委托变更、铸造/销毁、重复投票、精确法定票数、执行摘要与配置代次绑定，以及闪电贷最短见证。

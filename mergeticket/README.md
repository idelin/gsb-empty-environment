# mergeticket：把批准绑定到候选、基线与策略版本的合并票据

## 要解决的问题

代码托管服务在批准之后重排合并队列时，可能把**没人审过的改动**合进
目标分支。典型路径：

1. PR-A、PR-B 都基于基线 `R` 获得批准并进入队列；
2. 队列重排，PR-B 被提到队首并先合入，目标分支基线推进为 `B`；
3. PR-A 的批准仍被复用，于是 `A` 被合到它从未被审查过的新基线 `B` 上。

只按"树内容相同就放行"也不够：同一棵树挂在**不同父提交**下是另一个
提交，父链里可能带着没审过的历史。

## 核心设计：批准冻结为三元组

`Approve` 不记录 PR 编号，而是把批准时刻的现场快照冻结到票据
（`mergeticket/service.go` 的 `Ticket`）：

- `CandidateCommit` + `CandidateTree`：被审过的**精确候选提交**及其树；
- `Baseline`：批准时目标分支指向的基线提交；
- `PolicyVersion`：批准时生效的策略版本。

`Consume` 在**同一把互斥锁的临界区**内完成现场核对与状态迁移，三项
逐一比对，任一漂移即把票据永久置为 `invalid`：

| 漂移 | 哨兵错误 |
| --- | --- |
| 候选提交/树与批准时不同（含同树不同父） | `ErrStaleCandidate` |
| 目标基线已推进 | `ErrBaselineAdvanced` |
| 策略版本已升级 | `ErrPolicyChanged` |

失效是终态：重试只会得到包装了 `ErrTerminal` 的错误，**重试永远不会
合入旧候选**。

## 单一可观察顺序

- 所有状态迁移（批准/合入/失效/撤回）与队列重排都追加到同一条事件
  日志 `journal`，`Event.Seq` 在整个 `Service` 内单调递增；
- 状态迁移与事件追加在同一临界区原子完成，`Seq` 顺序即真实发生顺序；
- 观察者通过 `WaitForSequence(afterSeq, timeout)` 在同一个 `sync.Cond`
  上等待，所有消费者看到的是同一条全序；
- 并发 `Consume` 与 `Cancel` 竞争同一张票据时，日志中恰有一个终态事件，
  分支引用与票据状态始终与该事件一致。

## 内存提交图

`Graph`（`mergeticket/graph.go`）用 SHA-256 截断哈希做内容寻址：

- 树哈希只取决于文件内容；提交哈希由**父提交 + 树**共同决定；
- 因此相同树、不同父提交必然得到不同提交哈希；
- 引用（分支/基线）只允许快进移动，祖先关系沿父链线性可查，
  用于模拟重排、基线推进与合入。

## 测试与场景映射

| 测试 | 验证内容 |
| --- | --- |
| `TestGraph_ContentAddressingAndAncestry` | 同树同哈希、同树不同父不同提交、快进与祖先判定 |
| `TestApproveAndConsume_HappyPath` | 三元组快照、正常合入、已合入票据重试幂等且不追加事件 |
| `TestReorderThenBaselineAdvance_StaleTicketRejected` | 重排后基线推进，旧票据失效，连续重试不会合入旧候选 |
| `TestSameTreeDifferentParent_NotInterchangeable` | 同树不同父的提交不能复用旧批准，需独立审查后再合 |
| `TestPolicyVersionBump_InvalidatesOldApprovals` | 策略升级使旧批准失效，新版本下重新批准才可合 |
| `TestConcurrentConsume_OnlyOneWins` | 16 个并发消费者恰好一个合入，其余拿到终态错误 |
| `TestConsumeCancelRace_SingleObservableOutcome` | 40 张票据上消费/撤回竞争，每张恰有一个终态事件且引用一致 |
| `TestWaitForSequence_SharedOrder` | 观察者按 `Seq` 全序收到事件，无新事件时超时 |

运行：

```sh
go test -race -count=1 ./mergeticket/
```

# 重入调用能否突破提款上限

`netsec` 是一个可导入的 Go 交易解释包，只实现本题的内存模型。它不做 EVM 字节码兼容，也不访问链上状态；调用方给出账户初始余额、合约现金和允许的单次提款额，包负责执行程序、枚举攻击者调用树、报告最短超额转账并给出可独立回放的证据。

## 内存模型

- 提交态只有账户余额 `balance`、合约现金 `cash` 和累计已提交收款额 `received`。
- 每个调用帧进入时缓存当前账户余额；`balanceGE` 和 `balanceAdd` / `balanceSub` 使用该帧缓存值。
- `balanceAdd` / `balanceSub` 计算后立即把帧缓存的新值发布到提交态，因此“先扣减、后外调”的子调用能看到扣减结果。
- `cashSend` 按本帧提款额扣减合约现金，追加一条转账记录并累加已提交收款额。
- 余额、现金、提款额均在 `[0,255]` 内；加减越界、现金不足、显式 `revert` 都使当前帧失败。
- `externalCallback` 由攻击者选择 `Enter(amount)`（重入同一入口）或 `ReturnNow()`（立即成功返回）。入口帧深度为 1，最多 4 层，所有帧共享 100 条指令预算。
- 子调用的静态参数 `ContinueOnChildFailure` / `RevertOnChildFailure` 决定父帧在子失败后继续还是整体回滚。父帧回滚会一并撤销已成功子调用的余额和转账。
- 指令预算耗尽时结果为 `unknown`，绝不把回滚前的暂态转账算作攻击成功。

这个缓存并发布余额的细节是“先外调、后扣减”能产生超额转账的关键：外层帧在回调前没有发布扣减，重入帧仍看到旧余额；外层恢复后用自己的旧缓存再次扣减，因此不会因全局余额已经减少而下溢，最后一笔发布可能覆盖重入帧的结果。

## 指令

`Program` 最多 30 条指令，`pc` 从 0 开始。

| 指令 | 含义 |
| --- | --- |
| `OpBalanceGE` / `Check(target)` | 当前帧余额大于等于提款额时跳到 `target`，否则执行下一条 |
| `OpBalanceAdd` / `AddBalance()` | 当前帧余额加本帧提款额，发布到提交态，越界 revert |
| `OpBalanceSub` / `SubBalance()` | 当前帧余额减本帧提款额，发布到提交态，越界 revert |
| `OpCashSend` / `CashSend()` | 扣合约现金、记录本帧收款额；现金不足 revert |
| `OpExternalCallback` / `ExternalCallback(mode)` | 攻击者选择重入或立即返回 |
| `OpReturn` / `Return()` | 当前帧成功返回 |
| `OpRevert` / `Revert()` | 当前帧回滚并失败 |

`target == len(program)` 表示跳到程序末尾，解释器按成功返回处理。

## 搜索

```go
program := netsec.Program{
    netsec.Check(2),
    netsec.Return(),
    netsec.CashSend(),
    netsec.ExternalCallback(netsec.ContinueOnChildFailure),
    netsec.SubBalance(),
    netsec.Return(),
}
cfg := netsec.DefaultConfig(100, 255, []int{100, 50, 10})
result, err := netsec.Search(program, cfg)
```

`Search` 的排序规则：

1. 调用帧数最少（根帧计 1 帧，立即返回不新建帧）；
2. 调用帧数相同时，按提款额序列字典序；
3. 同一序列再按执行步数稳定排序，仅用于确定性输出。

`Attack.Calls` 是整个调用树的节点数，`Attack.Depth` 是最大嵌套层数；顺序回调的兄弟帧会增加 `Calls` 但不会增加 `Depth`。

`SearchResult.Status` 为 `attack`、`safe` 或 `unknown`。只有 `attack` 的 `Attack.Received > cfg.InitialBalance`，且根帧最终成功提交。只要存在一条可达执行超过 100 步且未发现攻击，状态就是 `unknown`，不会报 `safe`。

## 独立回放与证据

```go
execution, err := netsec.Replay(program, cfg, result.Attack.Plan)
report, err := netsec.VerifyReplay(program, cfg, result.Attack)
```

`Execution` 包含：

- `Root`：完整调用树，每个帧有 `Entry`、`Exit`、`BeforeRollback`、`AfterRollback` 快照；
- `Transfers`：所有 `cashSend` 记录，回滚的记录保留并标为 `rolled_back`；
- `RollbackEvidence()`：按调用层汇总的回滚证据，列出回滚前后快照和被撤销的转账。

`VerifyReplay` 从初始状态重新执行 `Attack.Plan`，比较提交结果、提款额序列、步数和转账日志，不依赖搜索过程的中间状态。

## 已验证场景

`go test ./...` 覆盖：

- 先外调、后扣减的两层重入攻击：`received = 200 > initial 100`；
- 最短调用树与提款额序列 tie-break；
- 先扣减的安全版本没有攻击；
- 子调用 revert 只回滚子帧，父帧按 `continue` 继续；
- 父帧失败撤销已成功子调用的转账；
- 合约现金不足时 revert 且没有提交转账；
- 攻击者选择立即返回时不产生重入攻击；
- 100 步预算耗尽返回 `unknown`；
- 搜索结果的独立回放一致性。

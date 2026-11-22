# redemption — 匿名票据核销宿主的交易验收包

纯 Go 标准库实现，无外部依赖。宿主负责注入两类密码学校验（发行方签名、
匿名兑换证明），本包负责除此之外所有可独立核对的交易结构与最优性判定。

## 模型

- 票据仅显露：发行方、活动代次、公开承诺值、整数面额、一次性核销标识。
- 交易 `Transaction` 花费若干输入（发行票据或前序交易的找零输出），
  产生服务权益 `ServiceEntitlement` 与找零输出 `ChangeOutput`。
- 守恒按 `(发行方, 活动代次)` 分别成立：输入面额之和 = 找零之和 + 服务成本之和，
  价值不跨发行方、不跨活动移动。
- 剩余限额 `Quota` 同样按 `(发行方, 活动)` 约束选中交易的服务成本合计。

## 可接受子集的规则

1. 选中交易引用的全部未确认前驱必须同时选中（闭包）。
2. 同一核销标识（nullifier）在确认前缀与选中候选中至多花费一次；
   同一找零 outpoint 至多被花费一次。竞争分支可复用标识，但不能同时入选。
3. 选中集合的依赖图必须无环——环无法自筹资金。
4. 不得与最终确认前缀冲突（复用其已花费的标识或 outpoint 的候选永久屏蔽）。
5. 每个 `(发行方, 活动)` 的服务成本不超过剩余限额。

## 目标与决胜

- 最大化选中交易完成的服务权益总数（`ServiceEntitlement.Units` 之和）。
- 同值时按选中交易 ID 的升序序列字典序决胜（严格前缀更小）。
- 候选至多 22 笔，用 32 位掩码编码子集。

## 求解与最优性证书

- `Solve` 用分支定界搜索，决策序为交易 ID 升序，因此位掩码 one-wins
  比较与 ID 序列字典序完全等价，tie 剪枝可证明精确。
- 每个剪枝节点记录原因（`bound` / `prefix_conflict` /
  `missing_dependency` / `infeasible` / `infeasible_leaf`）与松弛上界。
- `VerifySolution` 不调用求解器，仅依据账本与 `Feasible` 原语重建证书树、
  独立复核每个剪枝原因，并以归纳法证明根节点最优；同时校验问题摘要
  （`CanonicalDigest`，SHA-256 长度前缀编码）防止证书被挪用到其他实例。
- 证书刻意不包含签名与证明材料——这些由宿主注入的验证器在 `Prepare`
  阶段完成，属于密码学职责而非组合最优性职责。
- `BruteForce` 全枚举 2^N 子集，作为测试中的独立 oracle。

## 导入导出

- `EncodeProblem` / `DecodeProblem`、`EncodeSolution` / `DecodeSolution`
  提供 JSON 往返；摘要在往返后保持不变。

## 文件

| 文件 | 职责 |
| --- | --- |
| `types.go` | 领域类型与错误哨兵 |
| `ledger.go` | 结构校验、签名/证明注入、账本与依赖派生 |
| `feasibility.go` | 子集可行性（闭包、环、双花、限额） |
| `solver.go` | 分支定界求解与证书节点生成 |
| `cert.go` | 证书与剪枝原因类型 |
| `verify.go` | 独立证书核验 |
| `digest.go` | 确定性问题摘要 |
| `brute.go` | 全枚举 oracle |
| `encode.go` | JSON 编解码 |

## 测试

小集合全枚举覆盖四个规定依赖图场景：

- `TestChangeSharing`：找零被两个竞争下游共享，只能花一次。
- `TestSameNullifierDifferentNotes`：同标识、异票面的两个分支互斥。
- `TestConfirmedPrefixConflict`：与确认前缀冲突的候选被永久屏蔽。
- `TestZeroCostCycle`：零服务成本的找零闭环被环检测拒绝。

另有 200 组随机实例对拍、决胜、JSON 往返、逐 key 限额、闭包、摘要防篡改、
22 笔上限及最坏规模证书大小等测试。

```
go test ./...
```

# 隐私发布差异最小解释

两版脱敏策略输出不同，审核需要定位哪些规则共同造成可识别性变化。实现供Go测试宿主调用的解释包，输入至多十八项开关规则和确定性匿名度回调，求能复现“候选集合缩成单人”的最小规则改动集合；不假定风险随规则数单调。给出每次调用摘要及删除任一改动后的证据，用全子集核对。

## 包 API

包名 `netsec`，入口 `Explain(ExplainRequest) (*ExplainReport, error)`，位于
`explanation.go`，无第三方依赖，可直接被 Go 测试宿主 import 调用。

- `RuleConfig{RuleID, Enabled}`：单项开关规则在某一策略版本中的状态。
- `AnonymityReport{CandidateSetSize}`：匿名度回调结果；`CandidateSetSize == 1`
  即“候选集合缩成单人”（`Identified()` 判定）。
- `AnonymityFunc`：确定性回调，输入一份完整规则配置，输出匿名度报告。
- `ExplainRequest{Baseline, Target, Evaluate}`：两版策略（规则顺序、ID 必须
  一致）与回调；规则数上限 `MaxRules = 18`。

## 算法与保证

1. 只枚举两版之间状态不同的规则（至多 18 项），对其全部 `2^n` 个子集逐一
   调用回调；空子集即基线，全集即目标版本，另显式再评估一次目标配置。
2. 不假定任何单调性：每个子集都独立评估并记录，非单调的规则交互被精确处理。
3. 用位掩码 DP（对每个置位逐一移除，`O(n·2^n)`）判定“是否存在仍命中的严格
   子掩码”，从而求出**全部包含最小**（inclusion-minimal）命中集合，并给出
   `MinimumCardinality`；结果按基数、掩码排序，输出确定。
4. `ExplainReport.MinimalSets` 中每个最小集都带 `DeletionProof`：删除其中
   任一规则后的掩码、候选集合大小与是否仍可识别。这些证据直接取自全子集
   枚举的既有调用，不重复调用回调。
5. `ExplainReport.Invocations` 保存每一次回调的摘要（序号、全局位掩码、
   候选集合大小、是否单人、错误），`InvocationRecord.String()` 可直接写审计
   日志；`Config(mask)` / `RuleIDs(mask)` 可由掩码重建当时的完整配置。

回调错误不会中断枚举，而是记录在对应摘要（`CandidateSetSize = -1`）中，
错误子集不会被当作命中证据。

## 测试

`explanation_test.go` 覆盖：

- 刻意构造的非单调预言机（目标版本本身不命中，但存在最小命中子集）；
- 独立暴力实现的随机差分测试（40 组随机非单调谓词，全子集交叉核对）；
- 18 项规则的完整 `2^18 + 1` 次调用计数与删除证据；
- 单调阈值、零差异、基线已单人、回调错误、入参校验等边界。

```sh
go test -race ./...
go test -cover ./...
```

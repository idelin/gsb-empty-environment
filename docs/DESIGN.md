# 能力隔离验收包 `capgate`

供 Go 构建宿主调用的供应链门禁库与命令行。输入旧、新两版锁文件依赖图，逐阶段、逐平台/特征开关穷举，判定升级在哪些**互不重叠的条件区域**下新增了能够完成敏感（发布）操作的构建实例，并对每条授予链做独立语义核验。

## 1. 输入模型

- `Job.Platforms`：平台位；`Job.FeatureFlags`：特征开关位；`Job.Stages`：有序构建阶段；`SensitiveStage`：唯一允许完成敏感操作的发布阶段。
- 节点 `Node`：以 `ID` 唯一标识，带锁定内容 `Digest`。宿主工具与目标包可以同名，但 `ID`/`Digest` 不同即视为不同节点。
- 边 `Edge`：带平台/开关选择子 `Platform`、所属 `Stage`、`Optional + GateFlag/GateOn` 可选门控、传输摘要 `TransportDigest`，以及可选的半份授权 `Half`。
- `Half`：拆分授权的一半，绑定 `TxnID`（签发事务）、`Digest`（接收内容摘要）、`HalfIndex`（0/1）、签发 `Stage` 与 `Transport`。
- `LeakAttempt`：经由共享环境变量（`env`）或可写缓存目录（`writable_cache`）把前一阶段临时凭据带入后续阶段的尝试，必须全部拒绝。

## 2. 合法组合与穷举

- 默认目标平台**互斥**：一个合法组合恰好有一个平台位为真（`singleBit`），再与全部特征开关位做笛卡尔积。需要多平台并存时可显式置 `allow_multi_platform`。
- 选择子在解析期转为位掩码；同一位出现矛盾（既要求真又要求假）则该边条件**不可满足**，在任何组合下都不激活，而不是静默放行。
- 可选边必须声明门控开关；门控位并入条件掩码。

## 3. 逐阶段定点执行（含条件环）

每个组合分别对旧图、新图独立执行，阶段之间不共享任何半份状态：

1. 仅在当前阶段、当前组合下激活的边参与；携带 `Half` 的激活边作为半份种子。
2. 半份沿同阶段活动边做定点传播。每条半份携带“已访问节点集合”，再次遇到环上节点即截断并记录 `cycle_revisit_skipped`，保证条件环下重复访问必然收敛、不会死循环。
3. 半份在每个接收节点重新校验摘要：半份 `Digest` 必须等于接收节点锁定摘要，否则记 `digest_mismatch`（即依赖降权/别名冒用拒绝），且半份 `Stage` 必须等于所在边阶段，否则记 `half_stage_mismatch`。
4. `LeakAttempt` 在目标阶段合并前统一记 `cross_stage_leak_blocked`；泄漏凭据不会作为种子，因此永远无法完成发布。

## 4. 合并规则（发布阶段）

在 `SensitiveStage`，按“接收实例 + 内容摘要”归集到达的半份：

- 同一摘要存在多个 `TxnID` → `cross_transaction_merge`，拒绝。
- 同一事务下未集齐 `HalfIndex` 0 与 1 → `incomplete_half_set`，拒绝。
- 集齐后再校验：两半份同事务、同接收实例、摘要均绑定到该实例、路径每跳传输摘要齐全。全部成立才产出 `GrantChain`。
- 授权只认“发布阶段 + 内容摘要”，包名或别名不构成授权依据。

## 5. 增量、条件区域与覆盖证明

- 能力键为 `节点ID@阶段`。新增能力 = 新图可完成而旧图不能完成的实例。
- 组合按“新增能力签名”分组；每组用平台分配 + 特征开关真值表的递归 Shannon 展开，分解成**互不重叠的立方体 `Cube`**（未指定位即 don't-care）。
- 所有区域的立方体两两互斥，并完整覆盖全部合法组合；`Coverage` 给出总组合数、被覆盖数、是否完整、是否互斥以及无差异证据。
- 旧图与新图完全等价时输出单个空增量区域与完整覆盖证明（`NoDelta=true`），而不是空报告。

## 6. 独立语义核验

`verify.go` 不复用引擎的合并结论，而是拿每条 `GrantChain` 的逐跳路径回到原始图重新走一遍：

- 种子边必须携带匹配 `TxnID/Digest/HalfIndex` 的半份；后续转发边只校验传输证据、条件激活与接收摘要，不得把在途半份重新绑定。
- 同事务、同接收实例、摘要绑定、传输链完整、发布阶段正确。
- 区域互斥与全覆盖、跨阶段泄漏拒绝、旧图半份不得拼进新链（新链每个半份键必须存在于新图边）、摘要绑定、跨事务无效、同名宿主/目标隔离。

## 7. 命令行用法

```sh
go run ./cmd/capgate testdata/demo_job.json        # 文件入参
cat job.json | go run ./cmd/capgate -              # 标准输入
```

输出 JSON `Report`：`regions[].cubes`（互斥条件区域）、`regions[].chains`（独立核验过的授予链）、`semantic_checks`、`coverage`、`denials`。

## 8. 验收点到测试的映射

| 验收点 | 测试 |
| --- | --- |
| 小平台位穷举 + 条件互斥 | `TestExhaustiveAndMutex`、`TestContradictoryConditionIsUnsatisfiable` |
| 可选依赖特征开关门控 | `TestOptionalGateFlag` |
| 跨阶段环境变量/缓存泄漏拒绝 | `TestCrossStageLeakDenied` |
| 宿主工具与目标包同名隔离 | `TestSameNameHostAndTarget` |
| 依赖降权（摘要不符）拒绝 | `TestDependencyDowngradeRejected` |
| 跨事务合并无效 | `TestCrossTransactionMergeInvalid` |
| 无差异覆盖证明 | `TestNoDeltaCoverageProof` |
| 旧配置两条路径半份不得拼接 | `TestOldPathHalvesNotSpliced` |
| 条件环重复访问收敛 | `TestConditionalCycleConverges` |
| CLI 端到端 | `TestDemoJobEndToEnd` |

运行：

```sh
go vet ./...
go test -race -count=1 ./...
go test -cover ./...
```

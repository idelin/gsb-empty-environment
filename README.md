# cardinalguard

按指标名限制标签组合数量的遥测接收限制器。纯 Go 标准库实现，事件在本地
合成、可由 JSON 夹具回放；不接监控平台，不做展示看板。

## 设计

- **两级基数额度**：每个租户（`TenantQuota`）与每个（租户， 指标）
  （`MetricQuota`）各有一组存活系列上限；白名单条目（租户/指标通配 +
  标签子集匹配）命中的系列直接准入且不占额度。
- **确定性溢出桶**：额度耗尽时，新组合折叠进该（租户， 指标）唯一的溢出
  桶，桶身份使用保留标签 `__overflow__`；真实标签键禁止 `__` 前缀
  （`ValidateLabels`），因此桶不会与真实同名标签组合冲突。被折叠的样本
  计入 `Folded`/桶计数，绝不无声丢弃。
- **可撤回贡献**：溢出桶按原始系列键记录贡献（样本数、字节、标签、首次
  刻度）。额度释放时按 FIFO 公平晋升为存活系列，统计随贡献一起迁移；
  标签更正可撤回贡献并改挂到新组合——不会"计入额度后又丢其统计"。
- **过期回收与代际隔离**：系列按逻辑刻度租约过期，`Reap` 回收并释放额
  度，回收后允许同组合新系列准入。迟到样本（刻度早于当前代际创建刻度，
  或到达时租约已过期）计入 `Late`，绝不并入新代际。
- **原子额度调整**：样本、回收、标签更正全部在同一把互斥锁内完成，并发
  下额度计数与账本一致（`TestConcurrentAdjustmentsAreAtomic`，`-race`
  通过）。
- **字节账本**：`Ledger` 以与存活索引完全相同的键记录存活字节，另设
  溢出/迟到/过期三类累计，校验器逐键比对索引与账本。

## 不变量（每个事件后校验，见 `verify.go`）

1. 接收量守恒：`received == live + overflow + late + expired`（样本与字节）。
2. 账本一致：存活系列索引与字节账本逐键相等。
3. 额度安全：非白名单存活系列不超过租户/指标两级额度，内部计数与索引一致。
4. 公平分配：溢出桶非空当且仅当额度耗尽（晋升是即时且 FIFO 的）。
5. 桶完整性：贡献记录之和等于桶总量，FIFO 队列与贡献集合一致。

## 事件与回放

事件类型：`sample` / `reap` / `correct` / `checkpoint`（携带完整期望状
态）。`SynthWorkload` 以种子确定性地合成事件流；`SaveFixture`/
`LoadFixture` 落盘为 JSON 夹具；`Replay` 逐事件应用并在每个事件后校验
全部不变量。`testdata/replay_fixture.json` 是手工推算的夹具，检查点精
确钉住：接收量守恒、键序等价、白名单旁路、过期代际隔离（late=2）、标
签更正转移与 FIFO 公平晋升（promoted=4）。

## 运行

```console
$ go test ./...
ok  	cardinalguard	0.739s
?   	cardinalguard/cmd/demo	[no test files]

$ go test -race ./...   # 并发原子性
ok  	cardinalguard	10.955s

$ go run ./cmd/demo -seed 99 -events 500 -tenant-quota 4 -metric-quota 2
config: tenant_quota=4 metric_quota=2 ttl=15 whitelist=[tenant-0/metric-0]
replayed 500 events (seed=99)
--- statistics ---
received:         404 samples  15973 bytes
live:              10 samples    397 bytes in 10 series
overflow:           5 samples    171 bytes (folded total 200)
late:               9 samples    260 bytes
expired:          380 samples  15145 bytes
promoted:         192 series from overflow
corrected:          1 label corrections
--- conservation ---
live+overflow+late+expired = 404, received = 404
--- invariants ---
OK: all invariants held after every event
```

`go run ./cmd/demo -save fixture.json` 可把合成工作流存成可回放夹具。

## 测试（15 个，全部通过）

- 组合身份：`TestCanonicalKeyOrderInsensitive`（键序不同视同组合）、
  `TestReservedLabelRejected`（保留标签拒绝 + 桶身份不冲突）。
- 额度与折叠：`TestQuotaFoldAndCount`、`TestTenantQuotaAppliesAcrossMetrics`、
  `TestWhitelistBypassesQuota`。
- 生命周期：`TestExpiryReclaimsAndAllowsNewSeries`、
  `TestLateSampleGenerationIsolation`、`TestOverflowPromotionIsFairFIFO`。
- 标签更正：`TestCorrectionTransfersStatsWithoutLoss`、
  `TestCorrectionOfOverflowContributionIsRevocable`、
  `TestCorrectionToFullQuotaFoldsWithoutLoss`。
- 并发与回放：`TestConcurrentAdjustmentsAreAtomic`、`TestReplayFixture`、
  `TestReplaySynthesizedWorkloads`（20 个种子逐事件校验）、
  `TestSynthIsDeterministic`。

## 文件

- `series.go` — 标签校验、保序无关的规范化键、溢出桶身份。
- `limiter.go` — 两级额度、准入/折叠/晋升、过期回收、标签更正。
- `ledger.go` — 字节账本。
- `verify.go` — 不变量校验器。
- `events.go` — 事件类型、夹具读写、回放器。
- `synth.go` — 确定性事件合成器。
- `cmd/demo/main.go` — 运行样例。
- `testdata/replay_fixture.json` — 手工推算的回放夹具。

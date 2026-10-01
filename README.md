# telemetry — 基数限制器（纯标准库 Go）

按指标名限制标签组合数量的遥测接收器。事件在本地合成、可由夹具回放；
不接任何监控平台，也没有展示看板。

## 设计

- **标签规范化**（`labels.go`）：标签按键排序，键顺序不同视为同一组合；
  空键、重复键、`__` 保留前缀键一律拒绝。溢出聚合桶使用保留键
  `__overflow__`，因此永远不会与真实同名标签冲突。
- **两层额度**（`limiter.go`）：每个租户一个总额度，租户内每个指标一个
  额度；白名单系列（`Config.Allow`）不占用额度。指标级额度保证公平分配：
  高基数指标折叠自己，不会挤占其他指标的系列。
- **溢出聚合桶**：超额样本折叠进指标级溢出桶并计数（`FoldedTotal`），
  绝不无声丢弃。每个来源组合的贡献单独记录；当该组合之后被录取
  （额度释放、白名单、标签更正）时，贡献被原子撤回迁入新系列，
  不会"计入新系列后又丢其统计"。
- **过期回收**：系列按滑动 TTL 过期，回收把统计封存为历史代际
  （`ArchivedGeneration`），额度与字节账本在同一把锁内原子归还，
  回收后允许新系列进入。迟到样本在旧代际过期后开启新代际，
  代际号在指标内全局唯一，新旧代际统计互不混淆。
- **原子并发**：接收、过期回收、标签更正全部在同一把互斥锁下完成，
  额度调整、账本移动与贡献撤回不会出现中间态。
- **一致性核验**（`snapshot.go`）：`Verify` 检查接收量守恒
  （接收 = 存活 + 溢出 + 封存，对样本数/字节/值分别成立）、
  存活系列索引与字节账本一致、代际隔离、折叠账目守恒
  （折叠总数 = 溢出桶中可撤回贡献 + 已撤回数）与额度上限。
- **事件回放**（`events.go`）：`SynthEvents(seed, n)` 确定性合成事件流，
  `Replay` 依序回放并产出快照；同一事件流回放结果完全一致。

## 运行样例

```
$ go run ./cmd/demo
replaying 1200 synthesized events (tenant quota=6, metric quota=3, ttl=2s)

tenant tenant-a: live(counted)=6 ledger=3589B
  cache_hits     live=1 overflow=7 folded=51 retracted=44 archived=84 gens (178 samples) received=188 rejected=0
  http_requests  live=3 overflow=6 folded=63 retracted=57 archived=108 gens (181 samples) received=196 rejected=0
  queue_depth    live=2 overflow=6 folded=44 retracted=38 archived=74 gens (162 samples) received=171 rejected=0
tenant tenant-b: live(counted)=5 ledger=4027B
  cache_hits     live=3 overflow=4 folded=39 retracted=35 archived=75 gens (172 samples) received=179 rejected=0
  http_requests  live=1 overflow=15 folded=64 retracted=49 archived=100 gens (160 samples) received=179 rejected=0
  queue_depth    live=1 overflow=9 folded=42 retracted=33 archived=81 gens (177 samples) received=187 rejected=0

all invariants hold: conservation, generation isolation, ledger consistency, quota fairness
```

## 测试结果

```
$ go test -race -count=1 ./...
ok  	telemetry	1.721s
```

16 个测试全部通过（含 `-race`）：

- 标签顺序无关、重复键/保留前缀拒绝（溢出桶不冲突）
- 指标级与租户级额度折叠、白名单绕过且不占额度
- 过期回收释放额度允许新系列、迟到样本开启新代际且旧代际封存隔离
- 溢出贡献撤回迁入新系列、标签更正迁移统计与额度（合并/录取/再折叠）
- 公平分配：高基数指标不挤占其他指标
- 合成事件回放确定性、多种子不变量、手工夹具守恒/代际隔离核验
- 并发接收+更正+回收下不变量保持、接收样本零丢失

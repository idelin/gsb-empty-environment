# 网络策略等价压缩

防火墙发布层希望合并规则以减少硬件条目，但不能改变首匹配语义。写供Go发布服务调用的规则压缩包，条件为源与目标IPv4区间、端口区间及协议集合，动作为允许或拒绝；生成决策等价的分片规则并保留来源解释，条目预算不足拒绝发布。用区间端点诱导的有限格点逐一核验，覆盖默认拒绝、嵌套例外及数值上界，不要求最少条目。

## 包入口

模块路径 `example.org/netsec/uid1288`，包名 `netsec`，仅依赖 Go 标准库。

- `Compress(rules, opts)`：把有序首匹配规则编译成两两不交的分片（`Shard`），每个分片带 `SourceIndex`、`SourceID` 与 `Explanation`。
- `Verify(rules, plan)`：在区间端点诱导的有限格点上逐点比对规则决策与分片决策，并校验分片互不重叠、来源引用合法。
- `Publish(rules, opts)`：编译 + 预算检查 + 格点核验的一体化发布入口，任何一步失败即拒绝发布。

## 语义与保证

- 条件维度：源/目标 IPv4 闭区间、源/目标端口闭区间、IP 协议号集合；动作仅允许/拒绝。
- 首匹配：规则按输入顺序求值，未命中任何规则的流量默认拒绝（`ActionDeny`）。
- 端点分胞：所有区间端点把四维空间切成基本胞，胞内不存在规则边界，决策恒定；同来源、同动作、同协议集的相邻胞沿四个维度扫描合并。
- 来源解释：不同规则产生的区域即使动作相同也不合并；默认拒绝分片标记 `SourceIndex = -1`、`SourceID = "default-deny"`（仅 `IncludeDefaultDeny` 时物化）。
- 预算：`Options.MaxEntries` 为硬件条目上限，超出返回 `ErrBudgetExceeded`；零值表示不限制。
- 格点核验：切点同时来自规则和分片，每个基本胞探下界与上界两个角点，覆盖嵌套例外、默认拒绝与数值上界（IPv4 `2^32-1`、端口 `2^16-1`、协议 `255`，切点用 `uint64` 避免上界 +1 溢出）。

## 典型用法

```go
plan, report, err := netsec.Publish(rules, netsec.Options{
    MaxEntries:         4096,
    IncludeDefaultDeny: false,
})
if errors.Is(err, netsec.ErrBudgetExceeded) {
    // 预算不足，放弃本次发布
}
if errors.Is(err, netsec.ErrVerificationFailed) {
    // 格点核验发现决策分歧，拒绝下发
}
_ = report.OK()
for _, shard := range plan.Shards {
    // shard.SrcIP / DstIP / SrcPort / DstPort / Protocols / Action
    // shard.SourceID + shard.Explanation 用于审计回溯
}
```

## 测试

`go test ./...` 覆盖单条合并、嵌套首匹配例外、数值上界、预算拒绝、非法规则、分片重叠/篡改检出，以及 60 组随机规则的格点 + 随机包等价性质（覆盖率约 92%）。

# ASPA关系缺口放过路由泄漏

路由服务器用客户到提供商授权关系检测路径泄漏，若把未知关系当作有效，攻击者可把同伴或提供商路由转给另一提供商。给Go路由宿主实现题内ASPA路径判定包：数据集把customerAS映射到providerAS集合并带datasetVersion，AS号为1至2^32-1且0和保留范围由宿主拒绝；AS_PATH只接受AS_SEQUENCE，按传播方向从起源到当前会话对端排列，当前本地AS不在路径中，重复AS为环并拒绝。会话关系由宿主给当前邻居相对本地是Customer、Provider或Peer。对从Customer学到的路径，除首跳会话真实性外可导出给任意关系；从Provider或Peer学到时，路径必须呈“若干customer→provider上坡边，至多一条peer横边，随后若干provider→customer下坡边”，而ASPA数据只证明customer→provider边。逐相邻AS分类Valid/Invalid/Unknown：明确未授权且customer有完整ASPA记录为Invalid，customer无记录为Unknown；任何Invalid拒绝，Unknown按策略Strict拒绝、Monitor只告警且不得宣称有效。路径prepend相邻相同AS先按宿主上限压缩后保留次数证据。输出首个谷形违例或数据缺口，数据集切换原子重算受影响路由。用小关系图全路径枚举参考核对provider-peer-provider泄漏、客户路由、未知记录、prepend和版本切换。

## 使用

```go
dataset, err := netsec.NewDataset("2026-11-14T00:00:00Z", netsec.Records{
	65000: {65001},
})
if err != nil {
	return err
}

validator, err := netsec.NewValidator(dataset, netsec.Options{
	Policy:     netsec.Strict,
	MaxPrepend: 64,
})
if err != nil {
	return err
}

result := validator.Validate(
	netsec.SequencePath(65000, 65001),
	netsec.Session{
		LocalASN:    65010,
		NeighborASN: 65001,
		Relation:    netsec.Provider,
	},
)
if result.Verdict != netsec.VerdictValid || !result.Accepted {
	return fmt.Errorf("route rejected: %s (%s)", result.Verdict, result.FirstIssue)
}
```

`Evaluation.Hops` 给出逐相邻 AS 的 `Direction` 和 `Valid`/`Invalid`/`Unknown`
分类。`FirstIssue`、`FirstInvalid` 和 `FirstDataGap` 分别给出最早问题、最早
Invalid 和最早数据缺口；`HopIndex` 对应当前路径的相邻边（末端边指向本地 AS）。

## 判定规则

- 只接受 `AS_SEQUENCE`；`AS_SET`、confederation segment、空路径和非法 ASN 均拒绝。
- AS_PATH 方向为起源到当前邻居；本地 AS 不得出现在路径中，非相邻重复 AS 视为环。
- 连续相同 AS 在拓扑判定前压缩；`PrependRun.Count` 保留次数证据，超过
  `MaxPrepend` 时以 `Truncated` 标记。
- Customer 会话只校验邻居与末段 AS，随后允许导出，不因内部 ASPA 缺口否定路径。
- Provider/Peer 会话要求 `上坡* [peer] 下坡*`。下坡边由右端 customer 的 ASPA
  反向证明；ASPA 不证明 peer 边，因此没有宿主会话证明的 peer 边记为 Unknown。
- `Strict` 拒绝 Unknown；`Monitor` 接受 Unknown 但保持 `VerdictUnknown` 并设置
  `Warning=true`，不会把未知路径宣称为 Valid。

## 原子版本切换

```go
store, err := netsec.NewRouteStore(dataset, netsec.Options{Policy: netsec.Strict})
// ...
report, err := store.SwapDataset(nextDataset)
```

`SwapDataset` 在锁内重算包含变更 customer AS 的路由，然后一次发布新的
dataset/routes 快照。`Snapshot` 的读者只会看到旧或新快照，不会看到版本混搭；
`report.Revalidated` 列出重算的路由，`report.Changes` 列出 verdict 变化。

## 验证

```sh
go test ./...
go test -race ./...
go vet ./...
```

测试包含小关系图上的全 simple-path 枚举参考核对，覆盖 provider-peer-provider
泄漏、Customer 导出、未知/未授权记录、prepend 证据和原子版本切换。

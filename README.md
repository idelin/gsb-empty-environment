# CT gossip树头摄入准入

CT 客户端之间交换签名树头（STH）以发现日志给不同人看不同视图，攻击者可灌入大量已签名但过期的 STH 挤掉新条目，或用伪造签名栽赃日志分叉。为Go聚合服务写供 gossip 接收端调用的 STH 摄入组件，验签由调用方注入：先验签再入库，时间戳晚于接收时间超过 5 分钟或早于 MMD 加 24 小时窗口的丢弃，每个来源对每个日志的条目数设上限，淘汰只能淘汰同一来源的条目，保留每个日志最新的 STH。用海量旧 STH、未验签条目、窗口边界和多来源挤占验证，另一来源的新条目始终保留。

## 用法

```go
store, err := netsec.NewIngestor(
	netsec.VerifyFunc(func(ctx context.Context, logID string, sth netsec.STH) error {
		return verifyWithLogKey(ctx, logID, sth) // 调用方注入日志验签逻辑
	}),
	netsec.WithMaxEntriesPerSourceLog(16),
)
if err != nil {
	return err
}

// MMD 是日志配置，需在摄入该日志的 STH 前注册。
if err := store.RegisterLog("ct-log-id", 24*time.Hour); err != nil {
	return err
}

result, err := store.Ingest(ctx, netsec.IngestRequest{
	LogID:      "ct-log-id",
	SourceID:   "gossip-peer-a",
	STH:        sth,
	ReceivedAt: observedAt, // 留空则使用组件时钟
})
```

`result.Status` 是 `accepted`、`duplicate` 或具体的拒绝原因。验签失败时
`err` 会包装 `netsec.ErrSignatureVerification`，且存储不会发生任何变化。

## 准入与保留规则

- 先做请求和日志配置校验，再做时间窗口检查；验收通过后才调用注入的验签器。验签成功后才会修改存储。
- 接收时间由 `IngestRequest.ReceivedAt` 固定一次；为零时使用注入的时钟（默认 `time.Now`）。
- 时间戳严格晚于 `ReceivedAt + 5m` 的拒绝，等于边界接受；严格早于 `ReceivedAt - (MMD + 24h)` 的拒绝，等于边界接受。
- 每个 `(SourceID, LogID)` 独立保存最多 `MaxEntriesPerSourceLog` 条。淘汰只发生在该来源自己的桶内，按时间戳、树大小排序，旧条目先淘汰。
- 桶满时，比桶内最旧条目还旧的候选直接以 `rejected_capacity` 丢弃且不改变状态；因此大量旧 STH 无法挤掉该来源的新 STH。
- 每个来源的最新 STH 始终保留，其他来源的桶不受影响。`Latest` 返回一个日志跨来源的最新 STH，`LatestFromSource` 返回指定来源的最新 STH。
- 完全相同的 STH 在同一来源内幂等；同一 STH 经不同来源上报时按来源分别保留。
- `Ingestor` 可并发调用；验签不在锁内执行，存储快照在验签前复制，调用方或验签器后续修改切片不会改变已验证入库的数据。

## 测试

```sh
go test ./...
go test -race ./...
```

测试覆盖海量过期/窗口内旧 STH、验签失败与伪造分叉、5 分钟和 MMD+24 小时的精确边界、多来源独立配额、跨来源挤占，以及并发摄入。

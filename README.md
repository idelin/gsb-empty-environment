# 数据主体请求副本追踪组件

`example.org/netsec/q0516` 提供一个可导入的 Go 组件，用于协调删除或导出个人数据的数据主体请求。它不会把“主库已处理”误当成“请求已完成”：每个已登记的副本系统都必须返回与请求、主体和系统匹配的回执，请求才可能进入 `completed` 状态。

## 能力

- 请求按主体、数据范围、动作（`export` / `delete`）和截止时限登记。
- 登记前强制进行身份核验，身份证明与主体 ID 绑定；核验失败不会产生任何请求状态。
- 主库、备份、缓存、搜索索引和派生系统全部登记为必达副本；构造时会拒绝缺少任一标准角色的库存，逐项处理并保存回执。没有数据的系统也应返回记录数为零的回执，避免“无数据即跳过”。
- 回执会校验请求 ID、系统 ID、主体 ID 和动作，伪造、错投或跨请求回执不会被接受。
- 失败项按 `MaxAttempts` 与 `RetryBackoff` 重试；超过预算后状态为 `blocked`，仍会在缺失系统列表中暴露。
- `Tick` 扫描超期请求并发送一次告警；告警投递失败时保留状态，后续 `Tick` 重试，且不会重复告警。

副本处理器必须按 `RequestID` 保持幂等，因为失败、超时或回执校验失败都可能触发重试；`CopyTask.Attempt` 可用于观测尝试次数。

## 使用

```go
verifier := netsec.NewMemoryIdentityVerifier()
if err := verifier.Enroll(netsec.Subject{ID: "alice"}, subjectSecret); err != nil {
	log.Fatal(err)
}

tracker, err := netsec.NewTracker(netsec.Config{
	Verifier: verifier,
	Systems: []netsec.System{
		{ID: "primary-eu", Kind: netsec.SystemPrimary, Handler: primaryHandler},
		{ID: "backup-eu", Kind: netsec.SystemBackup, Handler: backupHandler},
		{ID: "redis-cache", Kind: netsec.SystemCache, Handler: cacheHandler},
		{ID: "search-cluster", Kind: netsec.SystemSearchIndex, Handler: indexHandler},
		{ID: "analytics", Kind: netsec.SystemDerived, Handler: derivedHandler},
	},
	AlertSink:    alertSink,
	MaxAttempts:  5,
	RetryBackoff: 30 * time.Second,
})
if err != nil {
	log.Fatal(err)
}

req, err := tracker.Register(ctx, netsec.Registration{
	Subject:  netsec.Subject{ID: "alice"},
	Kind:     netsec.RequestDelete,
	Scope:    netsec.Scope{Categories: []string{"profile", "orders"}},
	Deadline: time.Now().Add(72 * time.Hour),
	Proof:    netsec.IdentityProof{SubjectID: "alice", Secret: subjectSecret},
})
if err != nil {
	log.Fatal(err)
}

status, err := tracker.Process(ctx, req.ID)
if err != nil {
	log.Fatal(err)
}
if err := tracker.VerifyComplete(req.ID); err != nil {
	var incomplete *netsec.IncompleteError
	if errors.As(err, &incomplete) {
		log.Printf("still missing: %v (blocked: %v)", incomplete.Missing, incomplete.Blocked)
	}
}

receipts, _ := tracker.Receipts(req.ID)
_ = status
_ = receipts
```

副本处理器实现 `CopyHandler`，通常只需从任务生成基础回执并补充处理结果：

```go
type handler struct{}

func (handler) Process(ctx context.Context, task netsec.CopyTask) (netsec.Receipt, error) {
	receipt := task.Receipt()
	receipt.RecordsAffected = deleteSubjectData(ctx, task.Subject.ID, task.Scope.Categories)
	receipt.Evidence = "storage-scan-2026-11-14"
	return receipt, nil
}
```

部署时由调度器周期性调用 `tracker.ProcessAll` 和 `tracker.Tick`。`Status` 和 `Receipts` 可用于对账，`VerifyComplete` 是向请求方确认“全部副本已处理”的唯一前置条件。

## 验证

测试覆盖副本遗漏、身份冒用、超期告警、合法请求、回执防伪与告警投递重试：

```sh
go test -race ./...
```

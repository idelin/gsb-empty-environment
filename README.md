# chunkedtx

一个分块事务装配库：一个事务可跨多个消息块传输，只有所有块连续齐全
且总摘要匹配时才提交发布。

## 模型

- 每个 `Chunk` 携带事务 id（`TxID`）、序号（`Seq`，从 0 开始）、
  最终块标记（`Final`）与负载（`Payload`）。
- 最终块必须携带 `TotalDigest`（完整负载拼接后的 SHA-256），非最终块
  该字段必须为零。
- 事务在其最早一个块的 `ExpiresAt` 过期；零值表示不过期。

## 提交规则

- 只有当序号 `0..finalSeq` 全部暂存（连续无缺块）且拼接负载的摘要与
  最终块声明的 `TotalDigest` 一致时才提交；乱序到达不会产生提前发布。
- 相同 `(TxID, Seq)` 的重复块若内容一致则为幂等空操作；若内容冲突
  （或出现第二个不同的最终块），事务**永久失败**，之后任何属于该事务
  的事件都被拒绝（`ErrTxFailed`），无法复活。
- 摘要不匹配同样导致永久失败。

## 有界预算与过期

- `Config.MaxStagedBytes` 限制所有在途事务的全局暂存字节数，超限的块
  被拒绝（`ErrBudgetExceeded`）且不留残余状态；提交或失败立即归还预算。
- 到达时已过期的块被拒绝（`ErrChunkExpired`）；在途事务超过截止时间
  后在下次 `Ingest` 或显式 `Reap()` 时永久失败（`ErrTxExpired`）。
- 已提交事务的迟到事件返回 `ErrTxCommitted`，不会再次发布。

## 用法

```go
a, _ := chunkedtx.New(chunkedtx.Config{MaxStagedBytes: 1 << 20}, func(c chunkedtx.Commit) {
    // 发布已提交的事务
})
out, err := a.Ingest(chunk)
```

`Assembler` 可安全并发使用。测试注入了假时钟，覆盖乱序、缺块、重复、
冲突、摘要不匹配、预算超限、过期与迟到事件等场景：

```sh
go test ./... -race
```

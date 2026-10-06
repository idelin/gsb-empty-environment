# chunkedtx

Go 库：跨多个消息块的事务暂存与原子提交。

## 语义

- 一个事务由若干 `Chunk` 组成，每块携带 `TxID`、序号 `Seq`、最终块标记 `Final` 与负载。
- 最终块必须携带整个拼接负载的总摘要（SHA-256，`DigestOf` 计算）。
- **提交条件**：`0..finalSeq` 连续齐全 + 已见最终块 + 拼接负载的总摘要匹配。三者缺一，绝不发布。
- **重复冲突**：同一 `TxID+Seq` 内容完全相同的重投是幂等的；任何字段不一致（负载、Final 标记、摘要）或最终序号冲突，都会使事务**永久失败**（`ErrTxFailed`），此后该事务的任何块都无法使其提交。
- **有界预算**：所有在途事务的暂存字节共享一个全局预算，超限返回 `ErrBudgetExceeded`（该块不入暂存，可重试）；提交、失败、过期都会释放预算。
- **过期**：事务自首个块起算 TTL，到期未完成即过期并释放预算；过期事务留有墓碑，迟到的块返回 `ErrExpired`，不会另起新事务而提前/延迟发布。
- **至多一次发布**：已提交事务同样留有墓碑，重投返回 `ErrAlreadyCommitted`，不会二次发布。

## 用法

```go
s := chunkedtx.New(1<<20, time.Minute) // 全局预算 1MiB，事务 TTL 1 分钟

payload := []byte("...")
digest := chunkedtx.DigestOf(payload)

// 块可以乱序到达；只有最后一块补齐且摘要匹配时 committed 才为 true。
p, committed, err := s.Add(chunkedtx.Chunk{
    TxID: "tx-1", Seq: 0, Payload: part0,
})
p, committed, err = s.Add(chunkedtx.Chunk{
    TxID: "tx-1", Seq: 1, Final: true, Digest: digest, Payload: part1,
})
if committed {
    publish(p) // p 为按序拼接的完整负载
}
```

`Sweep()` 主动清理过期事务与墓碑（`Add` 时也会惰性过期被触及的事务）；`Stats()` 返回当前暂存字节数与在途事务数。测试可通过 `WithClock` 注入时钟。

## 测试

```sh
go test ./chunkedtx/
```

# 安全会话分叉检测

复制的客户端快照可能从同一个会话序号发出两条不同的已签消息。实现供Go服务调用的分叉监视包，签名验证由注入接口给出，记录会话代、序号、父摘要与内容摘要；同父不同内容构成冲突证据，缺失父记录只标待定。跨分片合并应幂等且顺序无关，用乱序、重复和回滚快照测试。

## 实现

包位于 `fork/forkwatch`（导入路径 `example.org/netsec/q0479/fork/forkwatch`），
只依赖标准库，核心类型与语义如下：

- `SignedMessage`：携带会话代 `Generation`、序号 `Sequence`、父摘要
  `ParentDigest`、内容摘要 `ContentDigest` 以及待验签的 `Payload`/`Signature`。
- `Verifier`：签名验证注入接口，另提供 `VerifierFunc` 适配器；验签失败的消息
  返回 `ErrInvalidSignature` 且不落库，空内容摘要返回 `ErrInvalidDigest`。
- `Monitor`：单个分片的线程安全监视器，按内容摘要去重存储 `Record`。
  状态在读取时派生而不是持久化：
  - `linked`：根消息（父摘要为空）或父记录已存在；
  - `pending`：父记录尚未观察到，仅标记待定，不算冲突；
  - `conflict`：同一 `(代, 序号, 父摘要)` 位置出现两个不同内容摘要，
    即同父不同内容的分叉证据，由 `Conflicts()` 返回。
- `Snapshot`：分片可序列化状态（JSON 下摘要为 base64 字符串），只含已验签记录。
  合并是以内容摘要为键的集合并，因此天然幂等、与合并顺序无关；纯函数
  `MergeSnapshots` 与方法 `Monitor.MergeSnapshot`/`MergeSnapshots` 均可使用。

## 用法示例

```go
verifier := forkwatch.VerifierFunc(func(payload, signature []byte) error {
    return ed25519.Verify(publicKey, payload, signature)
})
m := forkwatch.NewMonitor("shard-a", verifier)

if err := m.Observe(msg); err != nil {
    // ErrInvalidSignature / ErrInvalidDigest，消息不会被记录
    return err
}

for _, c := range m.Conflicts() {
    log.Printf("fork detected gen=%d seq=%d candidates=%d",
        c.Generation, c.Sequence, len(c.ContentDigests))
}

// 跨分片：导出、传输、合并，顺序任意且可重复
snapshot := m.Snapshot()
raw, _ := json.Marshal(snapshot)
var remote forkwatch.Snapshot
_ = json.Unmarshal(raw, &remote)
_ = collector.MergeSnapshot(remote)
```

## 测试

`fork/forkwatch/monitor_test.go` 覆盖任务要求的全部场景：

- 乱序投递：链尾先到、链头后到，最终状态与顺序投递一致，待定自动转为已链接；
- 重复快照：重复 `Observe` 与自合并均为幂等，记录不重复；
- 回滚快照：两个分片各自持有同一位置的不同已签消息，单分片无冲突，
  合并回滚快照后产生冲突证据，重复合并状态不变；
- 顺序无关：对三个分片快照的全部 6 种排列做合并，结果与派生状态完全一致；
- 另含根冲突、跨分片待定消解、JSON 往返、并发读写竞态检测。

运行：

```sh
go test -race ./...
```

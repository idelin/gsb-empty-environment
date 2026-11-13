# 多设备列表一致性客户端

`netsec` 是可直接导入的 Go 包，用于检测不可信消息目录对同一账号设备列表的
分叉、回滚和设备隐藏。模块路径为 `example.org/netsec/q0153`，包名为
`netsec`。

## 安全模型

- `AccountState` 包含 `AccountID`、严格递增的 `Version`、按 `DeviceID`
  排序的设备项，以及前一完整列表的 `PreviousDigest`。
- 设备项包含 `DeviceID`、Ed25519 身份公钥、`AddedVersion` 和可选的
  `RemovedVersion`。移除项永久保留为墓碑，设备 ID 永不复用。
- 账户主密钥签名覆盖确定性规范编码，包含账号、版本、前摘要、设备总数及
  每个设备的完整字段。
- 新版本必须恰为已提交版本加一，且 `PreviousDigest` 必须等于已提交摘要；
  活跃设备不能换公钥，移除设备不能复活或复用 ID。
- 列表和该列表叶的透明日志存在证明都验证通过后才提交。缺失或暂不可用的
  证明只暂存列表，不授权任何新设备。
- 会话每次发送和接收都重新检查当前已提交列表，因此提交移除后已有会话立即
  停止新发送。

## 快速使用

```go
masterPub, masterPriv, _ := ed25519.GenerateKey(rand.Reader)
devicePub, _, _ := ed25519.GenerateKey(rand.Reader)

list, _ := netsec.NewAccountState(
    "alice", 1, netsec.ZeroDigest,
    netsec.NewDevice("phone", devicePub, 1),
)
signed, _ := netsec.SignDeviceList(masterPriv, list)

// proof is produced by the transparent log for this list digest.
proof, _ := netsec.BuildInclusionProof(leaves, leafIndex, checkpoint)

client, _ := netsec.NewClient("alice", masterPub,
    netsec.ProofVerifierFunc(func(cp netsec.LogCheckpoint,
        p netsec.LogInclusionProof, digest netsec.Digest) error {
        // Host policy: verify the checkpoint signature and witness policy.
        return nil
    }),
)

result, err := client.Apply(signed, &proof)
if err != nil || !result.Committed() {
    // Reject or retry; nothing was authorized.
}

session, _ := client.OpenSession("phone")
if session.CanSend() {
    _ = session.Send()
}
```

目录先给列表、后给证明时，可直接暂存：

```go
result, err := client.Apply(signedV2) // proof omitted
// result.Status == netsec.UpdatePendingProof
result, err = client.ApplyProof(&proofV2)
// 只有此时新设备才会出现在 CanReceiveFrom/OpenSession 中。
```

## 观察回执与分叉证据

```go
receipt, _ := client.Observe("alice-contact", contactPriv)
other := fetchReceiptFromContact()
result, err := client.CheckObservation(other, contactPub)
if errors.Is(err, netsec.ErrForkDetected) {
    data, _ := netsec.ExportForkEvidence(result.Evidence)
    // 持久化/上报 data。客户端已冻结新会话。
}
```

同版本不同 `ListDigest`，或相同 `CheckpointDigest` 下出现不同列表摘要时，
客户端生成 `ForkEvidence` 并冻结。较旧但一致的观察返回
`ObservationStale`，不会误判为分叉。导出的证据可重新解析并验证：

```go
evidence, _ := netsec.ParseForkEvidence(data)
_ = evidence.Verify(masterPub, map[netsec.ContactID]ed25519.PublicKey{
    "alice-contact": contactPub,
})
```

## 透明日志格式

- 列表叶：`SHA-256(0x00 || listDigest)`。
- 内部节点：`SHA-256(0x01 || left || right)`，分割点与 RFC 6962 一致。
- `LogCheckpoint` 绑定 `LogID`、`TreeSize` 和 `RootHash`；
  `CheckpointDigest` 是这四个值域分隔后的摘要。
- 客户端本地验证 Merkle 路径，同时调用宿主 `ProofVerifier` 验证检查点
  签名、日志身份、见证策略等。宿主返回 `ErrProofPending` 或
  `ErrLogUnavailable` 时只暂存，其他错误拒绝。
- 客户端拒绝 `TreeSize` 回退；相同 `TreeSize` 但根不同也拒绝。

## 测试

```sh
go test ./...
go test -race ./...
```

测试内置恶意目录，覆盖两个客户端分叉列表、攻击设备隐藏、延迟日志证明、
检查点回滚、remove 后重加同 ID、版本跳跃、联系人回执乱序、同检查点互斥
观察，以及合法的设备轮换。

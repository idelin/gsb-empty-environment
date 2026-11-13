# 门限签名份额防双签账本

一个可直接导入 Go 共识宿主的 `netsec` 包。成员清单固定，每个成员使用
Ed25519 公钥和唯一 ID；账本只接收成员真实签名的 prepare/commit 份额。

## 安全规则

同一成员的状态按高度、阶段、轮次和父块独立校验：

- 同高度、同阶段只能支持一个区块。不同区块即构成双签，与轮次无关。
- 同高度不同阶段不互相构成双签。
- commit 必须已有同高度、同区块且 `prepare.round <= commit.round` 的 prepare。
- 同一高度已发布事件的轮次不得倒退。
- 高度 `h` 的 prepare，其父摘要必须是该成员低于 `h` 的最大已 commit 高度的区块摘要；中间高度允许缺席。
- 缺失前置的份额进入暂存；前置补到后按依赖顺序自动发布。
- 两份真实签名冲突时，冻结成员并保存不可覆盖的最小证据（仅两份份额）。

## 快速使用

```go
cfg := netsec.Config{
    ChainID:       "chain-1",
    GenesisDigest: genesis,
    Members: []netsec.Member{
        netsec.NewMember("alice", alicePub),
        netsec.NewMember("bob", bobPub),
    },
}

ledger, err := netsec.OpenLedger("./consensus-state", cfg)
if err != nil {
    return err
}
defer ledger.Close()

share := netsec.Share{
    ChainID:       cfg.ChainID,
    MemberID:      "alice",
    Height:        42,
    Round:         1,
    Phase:         netsec.Prepare,
    BlockDigest:   block,
    ParentDigest:  parent,
}
share, err = netsec.SignShare(alicePriv, share)
if err != nil {
    return err
}

result, err := ledger.Submit(share)
switch result.Status {
case netsec.StatusPublished:
    // result.Published 可能包含本次份额及被解锁的暂存份额。
case netsec.StatusPending:
    // 缺前置，已持久化暂存。
case netsec.StatusDoubleSigned:
    // result.Evidence 可交给第三方独立验证。
}
```

`NewLedger(cfg)` 提供纯内存模式。`SubmitBatch` 会先把整批真实签名份额写入
各自的成员 WAL 并 `fsync`，再开始状态变更。

## 证据验证

```go
func audit(members []netsec.Member, ev netsec.DoubleSignEvidence) error {
    return netsec.VerifyEvidence(members, ev)
}
```

验证器只需要固定的成员公钥清单。证据只包含两份签名份额；若没有对应成员的
私钥，无法伪造能让 `VerifyEvidence` 通过的证据。普通的分叉（不同成员在同一
高度签了不同区块）不会产生任何成员的双签证据。

## 持久化与崩溃恢复

`OpenLedger` 的数据目录结构：

```text
ledger.json                        固定链 ID、成员公钥、创世父摘要
members/<sha256(member-id)>/
    <hash>.events.log              追加式 WAL，逐条长度+校验和
    <hash>.state.json              原子重命名的成员快照
```

每个成员独立维护已签索引、暂存份额、已发布份额和冻结证据。快照损坏、快照
丢失、批次处理中途崩溃或日志尾部只写了半条记录时，重新打开会从 WAL 重放
未应用事件，并截断不完整的尾记录；重复份额按签名摘要幂等去重。冻结后的证据
只读，后到的冲突份额不能替换它。

## 测试

```sh
go test ./...
go test -race ./...
```

测试全部使用真实 Ed25519 密钥，覆盖：乱序 prepare/commit、同高度不同轮、
错误的跨高度父项、允许中间高度缺席、同高度不同阶段、commit 前置、轮次回退、
两份冲突份额、证据 JSON 验证与篡改、无私钥伪造、普通分叉、重复投递、并发提交、
重启恢复、丢失快照与撕裂日志尾部恢复。

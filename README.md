# 审计日志选择性披露校验包

这是可导入 Go 审计宿主的 `disclosure` 包。它面向“正文可依法擦除、记录与提交顺序不可移动”的追加日志：读端可以只拿到部分叶、批量包含证明和一段索引证明，但仍能核对某租户在指定时段内的全部有效记录。

## 数据模型

- 日志叶使用连续、不可重排的全局 `Seq`；`Checkpoint.BaseSeq` 支持从任意序号开始，后续序号必须连续。
- 每个版本叶承诺 `Tenant`、闭区间 `[EventStart, EventEnd]`、`BodyCommitment` 和 `PrevDigest`。
- `KindCorrection` 引用旧版本，可以用新版本替换有效租户或时间范围。
- `KindTombstone` 只追加授权擦除标记，不创建新版本，也不移动旧 `Seq`。
- 同一记录只能存在一条更正链，不能从同一旧版本分叉。
- 签名检查点同时承诺追加日志根和有效记录索引根。索引按 `(Tenant, Start, End, RootSeq)` 排序，`RootSeq` 保证同一时刻的多条记录也有确定顺序。

## 校验保证

`VerifyDisclosure` 同时检查以下条件：

- 返回的每个叶都由同一检查点的日志包含证明覆盖，正文与正文承诺一致。
- 索引区间的每个位置连续且可证明，左右相邻边界也被证明；任意漏掉一项都会失败。
- 每个有效索引项都能回溯到同一检查点内的原始事件叶；缺少更正祖先或墓碑时直接失败。
- 更正链最新版本、索引元数据和根序号一致，过期版本不能冒充当前有效记录。
- 墓碑授权绑定被擦除叶的摘要，并可由宿主提供的外部授权验证器检查。
- 日志根和索引根必须属于同一检查点；跨根拼接不会通过。

结果严格分为三组：

- `Definite`：有效事件区间完全落在查询区间内。
- `Possible`：有效事件区间与查询区间相交，但没有完全落入。
- `Erased`：相交记录已取得合法擦除墓碑。

正文未披露或已擦除只影响 `BodyKnown`/`Body`，不会把记录从结果中删除。

## 使用

```go
import (
    "crypto/ed25519"

    disclosure "prescreen.local/ddd-1565-20261122-v2"
)

// 1. 宿主侧追加并封存叶。
event := disclosure.Leaf{
    Seq:        0,
    Kind:       disclosure.KindEvent,
    Tenant:     "tenant-a",
    EventStart: 100,
    EventEnd:   120,
    Body:       []byte("audit event"),
}
if err := event.Seal(); err != nil {
    return err
}

leaves := []disclosure.Leaf{event}
checkpoint, err := disclosure.BuildCheckpoint(leaves)
if err != nil {
    return err
}
if err := disclosure.SignCheckpoint(privateKey, &checkpoint); err != nil {
    return err
}

// 2. 服务端生成部分披露。
response, err := disclosure.BuildDisclosure(
    leaves,
    checkpoint,
    disclosure.Query{Tenant: "tenant-a", From: 90, To: 110},
)
if err != nil {
    return err
}

// 3. 读端校验。VerifyCheckpoint 必填时才具备发布者认证。
result, err := disclosure.VerifyDisclosure(response, disclosure.VerifyOptions{
    VerifyCheckpoint:   disclosure.Ed25519CheckpointVerifier(publicKey),
    AuthorizeTombstone: disclosure.Ed25519TombstoneAuthorizer(erasurePublicKey),
})
if err != nil {
    return err
}
_ = result.Definite
_ = result.Possible
_ = result.Erased
```

构造后续版本时，引用目标必须已封存：

```go
correction := disclosure.Leaf{
    Seq:        1,
    Kind:       disclosure.KindCorrection,
    Tenant:     "tenant-b",
    EventStart: 105,
    EventEnd:   110,
    PrevDigest: event.Digest(),
    RefSeq:     disclosure.SeqRef(event.Seq),
    Body:       []byte("corrected metadata"),
}
if err := correction.Seal(); err != nil {
    return err
}
```

墓碑授权可绑定目标摘要：

```go
authorization, err := disclosure.SignTombstoneAuthorization(erasurePrivateKey, event.Digest())
tombstone := disclosure.Leaf{
    Seq:           2,
    Kind:          disclosure.KindTombstone,
    PrevDigest:    event.Digest(),
    RefSeq:        disclosure.SeqRef(event.Seq),
    Authorization: authorization,
}
```

## 完整重建

服务端可以用完整日志独立构建检查点，并与已发布检查点逐字段比较：

```go
checkpoint, err := disclosure.BuildCheckpoint(fullLeaves)
index, err := disclosure.BuildIndex(fullLeaves)
```

测试覆盖了以下小型日志场景：

- 更正把记录移出旧租户、移入新租户，或缩窄时间范围。
- 同一时刻存在多条记录时，索引根仍唯一且有序。
- 索引区间只漏掉边界或中间一项时校验失败。
- 已擦除叶被后续更正引用时，更正仍进入同一检查点，但记录保持 `Erased` 且正文未知。
- 未披露正文但不是擦除状态时，记录仍出现在 `Definite` 或 `Possible`。
- 日志根与索引根来自不同检查点时校验失败。

运行测试：

```sh
go test ./...
go test -race ./...
```

## 信任边界

树根绑定可以发现混合日志与索引的拼接，但不能替代检查点签名认证。生产读端应配置 `VerifyOptions.VerifyCheckpoint`，并为擦除操作配置 `AuthorizeTombstone`。默认的墓碑策略只要求授权字段非空，适用于测试或由日志签名者直接承担擦除授权的情形。

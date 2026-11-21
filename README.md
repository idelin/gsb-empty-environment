# recovery

一个可嵌入 Go 备份宿主的加密索引恢复协议。存储宿主只接触固定长度密文帧和公开 I/O
事件（区域/槽号、长度、阶段、attempt、retry）；租户块、租约和事务内容不会穿过
`StableStore` 边界。

## 稳定布局

只有两个固定容量的区域 `RegionA`、`RegionB`。每个区域预先划分为互不重叠的租户
lane：

```text
region A: [ tenant X lane ][ tenant Y lane ][ ... ]
region B: [ tenant X lane ][ tenant Y lane ][ ... ]
```

每个租户在自己的 lane 内采用 A/B 双缓冲。写入新快照时只覆盖**非活动** lane；旧活动
快照始终保留，因此任意单次断电最多留下“旧快照 + 新前缀”，不会为每个故障点复制整份
索引。恢复计划固定为：

- 读取本租户 lane 的两个区域，共 `2 * LaneSlots` 次逻辑读取；
- 写入 `0` 次；
- 总读取字节数 `2 * LaneSlots * FrameSize`；
- 每个槽最多 `MaxRetries + 1` 次尝试。

恢复不执行修复写，也不因内容提前结束扫描，因此外部租户的块数、租约和密文长度不会
改变公开轨迹。

## 提交流水线

一个事务按以下顺序写入非活动 lane：

```text
header -> block[0..n) -> prepare -> signed decision -> ack receipt
```

- header、每个 block、prepare、decision、receipt 都是 AES-256-GCM 固定长度加密帧；
- AEAD 的 AAD 绑定租户、区域和物理槽号，复制到其他槽或区域会成为缓存别名攻击并
  验证失败；
- 每帧 `prev` 绑定前一帧密文哈希，prepare/decision/receipt 绑定逐级 root；
- transaction 由租户签名密钥 HMAC 签名；decision 和 receipt 再各自签名；
- receipt 绑定 decision 哈希、快照哈希、租约、世代和事务 token。

只有完整且签名正确的 `header + blocks + prepare + decision + receipt` 链才构成提交
证据。旧代次帧、截断帧、部分写入、槽别名以及只有 decision 没有 receipt 的批次都
不能替代 receipt。

`Commit` 只在 receipt 写入得到 durable ack 后返回。事务在断电前没有 receipt 时：

- 通用恢复完全忽略它，表现为整体回滚；
- 若完整 decision 已持久化，只有持有同一签名事务的 `Resolve(txn)` 能补写 receipt
  并取回结果；同 ID 但不同 nonce/签名的 token 返回 `ErrTxnMismatch`。

## 恢复与租约

`Recover` 只扫描本租户 lane。每个有效 receipt 链都会：

1. 验证事务签名、decision 签名、receipt 签名；
2. 验证帧顺序、`prev` 链、root、快照哈希；
3. 验证块均属于本租户且 block id 不重复；
4. 只接受与请求的 `(tenant, lease epoch, lease id)` 完全一致的提交；
5. 在 `MinGeneration` 以上选择最大世代。

因此：

- 同一租约下的旧日志回放由宿主提供的单调 `MinGeneration` 锚点拒绝；
- 删除后换新租约时，旧租约的页面和租约不会进入新租约的恢复结果；
- 同一快照链即使同时留在两个区域，也只会产生一个恢复结果和一个 apply 回调。

`RecoverAndApply` 对选中的 receipt 调用一次回调；同一 Engine 实例会抑制同租约的
相同或更旧世代再次 apply。跨进程的精确一次落地需要宿主用 `receipt.Txn` 作为幂等
键，将自身状态与“已应用事务 ID”原子提交。调用方必须保证事务 ID 唯一；同一 ID 的
分叉提交会被判为 `ErrAmbiguous`。

`MinGeneration` 必须是宿主维护的单调值。若攻击者能同时替换两个区域的所有新证据，
仅靠本地认证日志无法识别回滚。

## 有界容量

一个快照占用 `len(blocks) + 4` 个帧（header、prepare、decision、receipt）。lane
容量不足时 `Stage` 或 `Commit` 返回 `ErrCapacity`，不会写出不完整的新提交；旧活动
快照仍可精确恢复。单块超过帧容量返回 `ErrBlockTooLarge`。

## 使用示例

```go
tenant := recovery.TenantIDFromString("tenant-a")
lease := recovery.Lease{
    Tenant: tenant,
    Epoch:  1,
    ID:     recovery.LeaseIDFromString("lease-a-1"),
}
keys := recovery.Keyring{
    tenant: recovery.NewKeyPair(masterKey, tenant),
}
store, _ := recovery.NewMemStore(24, 1024)
layout := recovery.Layout{
    RegionSlots: 24,
    Lanes: []recovery.LaneSpec{
        {Tenant: tenant, Start: 0, Slots: 12},
        // 其他租户使用不重叠的固定 lane。
    },
}
engine, _ := recovery.New(store, keys, recovery.Config{
    Layout:     layout,
    FrameSize:  1024,
    MaxRetries: 2,
})

txn, _ := keys.SignTransaction(recovery.SignedTxn{
    ID:             recovery.TxIDFromString("txn-1"),
    Tenant:         tenant,
    LeaseEpoch:     lease.Epoch,
    LeaseID:        lease.ID,
    BaseGeneration: 0,
    Nonce:          [16]byte{1},
})
batch, _ := engine.Begin(txn)
_ = batch.Stage(recovery.Block{
    Tenant:  tenant,
    ID:      recovery.BlockIDFromString("page-1"),
    Version: 1,
    Data:    []byte("opaque index page"),
})
_, _ = batch.Commit()

result, _ := engine.Recover(recovery.RecoveryRequest{
    Tenant:        tenant,
    Lease:         lease,
    MinGeneration: 1,
})
fmt.Println(result.Receipt.Generation, len(result.Snapshot.Blocks))
```

## 独立崩溃前缀校验

`VerifyCrashPrefixes` 接收一组相互独立的原始双区镜像。每个 case 会从密文重新创建
两个全新 store 和 engine，并检查：

- 两侧完整 recovery I/O 事件逐字段一致；
- 恢复出的 receipt 和快照一致；
- 每个 acknowledged case 恰好调用一次 apply；
- 所有读取槽号均在目标租户 lane 内，未发生跨租户读取；
- 返回块均属于目标租户；
- 结果符合调用方给出的精确世代、事务和块内容。

测试利用成对 `MemStore` 枚举：

- 提交和删除的每一个写 ack 前缀；
- 第一次断电前缀后再次提交的每一个第二次断电前缀；
- 删除后新租约提交的每一个前缀；
- 每个 lane 槽的部分写入；
- 两个区域分别/同时重放旧日志；
- 容量从 0 到超限的每个块数；
- 读写瞬态重试和永久失败。

运行：

```sh
go test ./...
go test -race ./...
go vet ./...
```

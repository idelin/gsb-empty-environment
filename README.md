# 元数据日志重放伪造旧授权主体

文件系统日志若记录已解析的“允许”结果而非不可变操作身份，崩溃重放时可能以旧主体权限完成未提交写。给Go文件宿主实现可导入的授权日志事务包：每笔事务记录txID、调用者principalID和credentialGeneration、策略摘要和授权回执摘要、对象ID/代次、操作类型、参数摘要、预期旧元数据摘要及新元数据补丁；日志记录Prepare、DataStable、Commit，均带前记录摘要形成链并由宿主HMAC。在线执行先按当前凭据和策略授权，再写Prepare，数据持久后写DataStable，元数据CAS匹配旧摘要后写Commit。恢复时从最后可信检查点验证链；只有完整Commit且对象旧摘要或已应用新摘要匹配才幂等完成，只有Prepare/DataStable的事务一律回滚临时数据，不在恢复期重新按principal名字查权限，也不把旧授权迁移到新对象代次。策略或凭据变化不影响已Commit结果，但未Commit不得继续。日志截断、重复txID异内容、链分叉和HMAC失败使后续记录隔离，不能挑一支继续。用可撕裂日志和对象号复用测试每个写入前缀、撤权后重启、同名主体新代次及并发元数据CAS，未提交操作永不成为可见写。

## 包接口

模块根包为 `netsec`，可直接导入：

```go
host, err := netsec.Open("/var/lib/filehost", netsec.Options{
    HMACKey:    hostKey,
    Authorizer: authority, // 当前凭据 + 当前策略
    Applier:    netsec.ReplacePatch{},
})
defer host.Close()

err = host.CreateObject(ctx, netsec.ObjectRef{ID: "inode-42", Generation: 3},
    oldMetadata, oldData)

receipt, err := host.Execute(ctx, netsec.WriteRequest{
    TxID:                 "tx-01",
    PrincipalID:          "alice",
    CredentialGeneration: 7,
    Object:               netsec.ObjectRef{ID: "inode-42", Generation: 3},
    Operation:            netsec.OpWrite,
    Params:               requestParams,
    OldMetadataDigest:    netsec.DigestBytes(oldMetadata),
    Patch:                newMetadata,
    Data:                 fileData,
})
```

`Options.Applier` 决定补丁语义；默认 `ReplacePatch{}` 把 patch 当作完整新元数据，`JSONMergePatch{}` 提供 RFC 7396 风格合并。`Authority` 是内置的在线策略/凭据实现，也可自行实现 `Authorizer`。
`OldMetadataDigest` 为零值时表示在对象锁内读取当前摘要作为 CAS 前置条件。`Data == nil` 表示只改元数据；非 nil 的空切片表示写入空数据。

## 事务与恢复顺序

1. 在线授权使用请求断言的凭据代次；`Authorization.HasCredentialGeneration` 为真时必须与当前代次一致。
2. 写入 `Prepare`（含完整不可变操作身份与授权回执摘要）。
3. 数据写入暂存区并 `fsync` 后写 `DataStable`。
4. 按旧元数据摘要做 CAS 暂存，然后写 `Commit`。
5. Commit 持久后先发布数据、再发布元数据。崩溃发生在 Commit 后将由恢复幂等完成。

恢复只读取日志、检查点和对象存储：不会调用 `Authorizer`，不会按 principal 名称查询权限，也不会把旧对象代次的授权应用到复用后的新代次。缺少 Commit 的事务只回滚暂存数据；完整 Commit 仅在对象代次一致且元数据为记录的旧摘要或新摘要时完成。

## 日志完整性

- 每条记录带前一记录摘要和宿主 HMAC；payload 单独带摘要。
- 尾部撕裂按崩溃处理，截断到最后一条完整记录。
- 完整记录的 HMAC/摘要失败、链断裂、链分叉、重复 txID 异内容、回执摘要不匹配，都会隔离该记录及全部后续记录；宿主拒绝继续追加，不会选择某一分支。
- `Checkpoint` 生成宿主 HMAC 的受信前缀；若日志已不含检查点记录，恢复失败关闭。

## 测试

`go test ./...` 覆盖：

- 单事务 Prepare/DataStable/Commit 每个字节前缀的撕裂日志，以及所有帧边界；
- 撤权后重启、同名主体新凭据代次；
- 对象号复用后旧授权不迁移；
- 并发元数据 CAS 只有一个成功；
- 重复 txID 异内容、链分叉、HMAC 失败和回执重放隔离；
- 检查点缺失和受信前缀恢复。

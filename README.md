# 路径ORAM回滚后重复访问同一路径

客户端用路径ORAM隐藏块访问，恶意服务器若回滚树或丢掉一次写回，会让位置图和真实树分叉并泄露重复路径。给Go存储宿主实现可导入的客户端包，本题模型固定：二叉树高h为2至10，每桶容量Z为1至8，逻辑块含blockID、版本、固定长度数据（全树一致且由配置给出）和HMAC；客户端位置图把每块映射到叶号，stash保存解密块。每次Read/Write先从宿主随机源为目标块选择新叶并持久化位置图意图，再读取旧叶整条路径；每个桶由AEAD回调认证并在附加数据绑定树ID、节点索引、桶代次和客户端单调事务号。路径中有效块合入stash时，同blockID只接受最高连续版本，冲突同版本异内容拒绝；目标操作完成后按从叶到根的固定顺序尽量把stash块放入其叶路径可覆盖的最深桶，写回整条路径并取得服务端签名根回执，最后原子提交位置图、stash、根与事务号。任一读、写、回执或持久化失败不得交付数据，恢复按意图与服务端根决定重放同一事务，不能重新抽叶。实现内存恶意服务端，可回滚任一历史树、删桶或重放旧回执；测试每个故障点、stash超过配置上限时事务失败且状态不提交；覆盖stash溢出、重复块和连续访问同一blockID，核对成功事务的服务端可见路径只来自预先抽取叶且回滚永不被接受。

# 实现

模块路径为 `example.org/netsec/q0104`，包名为 `netsec`，可直接导入。

## 使用

```go
block, _ := aes.NewCipher(aeadKey)
aesGCM, _ := cipher.NewGCM(block)

server, _ := netsec.NewMemoryServer("orders", 6)
store := netsec.NewMemoryStore()
cfg := netsec.Config{
    TreeID:         "orders",
    Height:         6,
    BucketCapacity: 4,
    BlockDataSize:  64,
    StashCapacity:  128,
    AEAD:           aesGCM,              // crypto/cipher.AEAD
    MACKey:         macKey,              // 至少 16 字节
    RootPublicKey:  server.PublicKey(),
}
client, err := netsec.NewClient(cfg, server, store)
written, err := client.Write(42, data)
read, err := client.Read(42)
```

`New`/`NewClient` 会校验配置；空 `StateStore` 时生成并上传全树空桶，取得
签名创世根。已有状态时会先按 journal 恢复，再要求服务端签名根与客户端已提交
根、事务号完全一致，否则返回 `ErrRollback` 或 `ErrServerStateMismatch`。

## 协议要点

- 叶路径固定：`CommitPath` 只接受与预抽叶严格一致的 leaf-to-root 路径。
- 位置意图：每个 Read/Write 先原子持久化 `Intent{OldLeaf,NewLeaf,...}`，之后
  才读取旧叶路径。恢复只使用该意图，不会重新调用随机源抽叶。
- 桶认证：`BucketAdditionalData` 绑定 tree ID、node、generation、写入事务号；
  nonce 由 AD 与明文确定性派生，恢复重放产生逐字节相同的桶。
- 块认证：`Block.MAC` 为 HMAC-SHA256，覆盖 tree ID、blockID、version、data。
- 版本规则：同 blockID 下同版本异内容拒绝；stash 已有 v 时只接受 v+1，或
  路径中同时携带完整 v+1..max 链的更高版本；旧副本忽略。
- 驱逐：固定按路径 leaf-to-root 扫描，每个桶按 blockID 升序尽量放入其叶路径
  可覆盖的最深桶，桶容量为 Z。
- 根：服务端根是每个桶 `BucketDigest` 的 XOR；客户端用读到的旧桶摘要和写回
  新桶摘要增量计算期望根，并校验 ed25519 签名回执中的 tree ID、txn、root。
- 提交：位置图、stash、根、事务号通过单次 `StateStore.Commit` 原子替换。

## 失败与恢复

- `Intent` 持久化失败：不发生任何服务端读写。
- 读、stage、commit、回执、最终持久化失败：不返回块数据，已提交状态不变；
  journal 保留，`Recover`/下一次访问按原 old/new leaf 重放。
- 服务端已提交而本地最终提交失败：`Recover` 用服务端签名根与 `Prepared.NewRoot`
  比对后补提交本地状态。
- 服务端回滚、删桶、写入分叉、旧回执重放：签名根或增量根不一致，事务不提交。
- stash 峰值超过 `StashCapacity`：返回 `ErrStashOverflow`，服务端无写入，已提交
  位置图/stash/根/事务号均不改变。

`StashCapacity` 限制一次事务中读路径合入后、目标操作后的 stash 峰值；配置为 0
时使用保守默认值 1024。实际部署应按 `(Height+1)*BucketCapacity` 以上的 stash
上界取值。

## 恶意服务端与测试

`MemoryServer` 提供 `RollbackTo`（任意历史事务）、`DeleteBucket`、
`SetBucketRaw`、`ServerFaults`（读/stage/commit/root/初始化失败，单桶写回丢弃，
旧回执重放）以及 `AccessLog`/`CommitLog` 审计。测试覆盖：

- 每个读、写、回执、持久化故障点及恢复；
- stash 溢出时事务不提交；
- 同版本异内容冲突、连续版本和同一 blockID 连续访问；
- 每条成功事务的 read/commit 路径只来自持久化意图中的预抽叶；
- 所有历史树回滚、删桶、丢写回和回执重放均不被接受。

运行：`go test ./...` 或 `go test -race ./...`。

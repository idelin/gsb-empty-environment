# 认证扇区层

`netsec` 是一个可直接导入的 Go 加密卷扇区层。逻辑扇区固定为 4096 字节；底层设备按 8192 字节物理记录读写，额外空间用于认证头、AEAD 标签和记录扩展。

## 快速使用

```go
dev := netsec.NewMemoryDevice(256)
vol, err := netsec.OpenVolume(dev, netsec.Options{
    VolumeID:      42,
    KeyGeneration: 1,
    MasterKey:     masterKey, // 建议 32 字节随机值
})
if err != nil {
    panic(err)
}

if err := vol.Write(7, plaintext4096); err != nil {
    panic(err)
}
plaintext4096, err := vol.Read(7)
```

主要接口：

- `OpenVolume` / `NewVolume`：格式化空白设备或打开已有卷。
- `Write` / `Read`：写入和读取一个逻辑扇区。
- `Relocate` / `RelocateAll`：解密后换物理扇区并递增 epoch 重新加密。
- `RotateKey`：用递增 key generation 重新加密全部映射。
- `Reclaim`：清零并回收不再被任何有效根引用的物理记录。
- `Reload`：重新读取状态日志；用于从状态写入不确定的故障中恢复。
- `State` / `Mappings` / `WriteEpoch`：查询已提交状态。

## 设备布局

设备的前 8 个物理记录是状态日志，之后的记录用于映射树和数据：

| 区域 | 内容 |
| --- | --- |
| `0..7` | 带 HMAC 的状态记录，序号单调递增 |
| `8..N-1` | 不可变映射树节点与新写入/搬迁的数据记录 |

状态记录包含 `volumeID`、`keyGeneration`、预留 epoch、提交 epoch、根修订号和映射根引用。每次状态转换都先写入一个非当前槽，校验 `Sync` 与回读摘要后才成为当前状态；旧槽在确认新槽可用后失效。

## 写入协议

1. 从状态日志持久化预留 `writeEpoch + 1`。64 位 epoch 为 `MaxUint64` 时，所有需要新 epoch 的写、搬迁和轮换都返回 `ErrWriteEpochExhausted`，绝不回绕。
2. 从无引用列表分配物理扇区，并设置新的 `physicalGeneration`。
3. 用 `HMAC-SHA256(nonceKey, volumeID||keyGeneration||logicalSector||writeEpoch)[:12]` 生成 GCM nonce。
4. AEAD 附加数据固定绑定：卷 ID、key generation、逻辑扇区、物理扇区、物理代次、write epoch、明文长度。
5. 写入密文后按 SHA-256 回读比对；失败则不提交根。
6. 在映射 B+ 树中复制受影响路径并持久化新节点，所有新节点同样做 HMAC 和回读校验。
7. 最后提交新的状态根。根未提交时，旧根继续有效。

读取时固定本次操作的根引用，逐层校验节点 HMAC、节点物理代次和子引用；数据记录先核对物理扇区/代次，再校验 AEAD，任何一步失败都不会交付明文。

## 搬迁、轮换与回收

- 搬迁必须先用当前 key 解密数据，再用同一逻辑扇区和新 epoch、新物理代次重新加密；不会复制原密文。
- key 轮换对每个映射执行同样的解密、重加密和递增 epoch。轮换中断时，旧 key generation 与旧根仍可读。
- 数据与树节点只有在旧状态槽失效、当前根不再引用且物理内容回读为零后才进入空闲列表。
- 物理 ID 可以复用，但复用时的代次一定前进，因此旧密文即使被写回也无法通过代次和 AEAD 校验。

## 故障注入

`MemoryDevice` 提供 `CopySector`、`SwapSectors`、`Snapshot`、`RestoreSnapshot` 以及 `SetFaultInjector`。注入器可模拟读、写、`Sync` 失败，并可用 `TornBytes` 表示只落盘前缀的撕裂写。

`go test -race ./...` 覆盖密文复制/交换、物理 ID 复用、搬迁中断、key 轮换中断、epoch 撕裂、根提交撕裂、状态 `Sync` 不确定、树分裂和重开恢复。

## 回滚边界

卷内会检测：

- 状态日志序号、epoch、key generation、根修订号回退；
- 只回滚根但保留更新 epoch 物理记录的情况；
- 旧根引用的物理记录已被清零的情况。

如果攻击者能把整个设备（包括全部状态槽和物理记录）原子回滚到旧快照，本地存储无法凭空知道更新的历史。宿主应把最近确认的 epoch 放在设备之外的可信单调锚点，并通过 `Options.MinimumWriteEpoch` 在打开卷时传入；低于该值的持久状态会被拒绝。

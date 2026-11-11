# batchlease

传感器采集 SDK 在掉电后偶尔会把已经上报（承诺）过的数据再次分配给另一批。本包为 Go
客户端实现了一套基于追加日志（append-only WAL）的**持久批次租约**，保证重开 SDK 后仍能
按原批次识别每一条已承诺的记录。

## 解决的问题

- 掉电后已上报数据被重新分派给新批次（重复上报 / 批次身份丢失）。
- 写入中途掉电造成日志尾部撕裂（半帧、坏魔数、CRC 不符、零填充）。
- 日志压缩（compaction）在任意阶段被杀死，留下临时文件或半完成的替换。

## 核心抽象

- `BatchSummary`：批次摘要，由连续序列号区间 `[FirstSeq, LastSeq]` 与内容摘要 `Hash` 构成。
- `Token`：领取批次时发放的令牌，绑定 `BatchID` 与当时的**启动代（epoch）**。
- `Lease`：一条已持久化的租约，包含令牌与批次摘要。
- `Store`：追加日志存储，线程安全，用 `Open` 打开、`Close` 关闭。

## 工作流程

```go
s, _ := batchlease.Open("./lease-dir")
defer s.Close()

// 1. 领取批次：令牌绑定当前启动代与批次摘要，区间不得与已有租约重叠。
lease, err := s.Acquire("batch-2026-11-11", batchlease.BatchSummary{
    FirstSeq: 100,
    LastSeq:  199,
    Hash:     digest, // 应用层对该区间记录的摘要
})

// 2. 确认上传：只能按连续顺序推进已持久化的高水位，且令牌必须匹配。
for seq := uint64(100); seq <= 199; seq++ {
    if err := s.Confirm(lease.Token, seq); err != nil { ... }
}

// 3. 掉电重开后，按序列号找回它原本所属的批次，杜绝重新分派。
s2, _ := batchlease.Open("./lease-dir")
batchID, ok := s2.Identify(150) // -> "batch-2026-11-11"
```

## 持久化与正确性保证

- **追加 + fsync**：每个变更（epoch、租约、确认）都追加为独立帧并在 API 返回前
  `fsync`，确认只推进**已持久化的连续**高水位（`ErrNonContiguous`）。
- **启动代绑定**：每次 `Open` 都生成严格递增的新 epoch 并落盘；令牌携带 epoch，
  不属于持久化租约的伪造令牌会被 `ErrInvalidToken` 拒绝。
- **区间互斥**：新批次的序列号区间与任一现存租约重叠即返回 `ErrOverlap`，从根上阻止
  已承诺数据被重新分配。
- **自校验帧**：每帧含魔数、类型、长度、IEEE CRC-32 与尾部标记。
  - 尾部撕裂（短帧 / 坏魔数 / CRC 不符 / 坏尾标 / 零填充）在重开时自动截断到最后一个
    完好帧，保留其之前的全部已提交状态。
  - 日志**中间**损坏（损坏帧之后仍存在完好帧）被判定为硬损坏，`Open` 直接报错，绝不
    静默丢弃已提交历史。
- **故障隔离**：写入或同步失败后存储进入 `failed` 态（错误包装 `ErrFailed`），拒绝后续
  变更，直到重开，避免内存与磁盘状态分叉。

## 压缩（compaction）

`Compact` 把全部存活状态重写为单个快照帧，采用原子替换协议：

1. 在 `lease.log.tmp` 写完整快照并 fsync；
2. `rename` 原子覆盖 `lease.log`；
3. fsync 目录项保证替换持久化。

- 替换前任一阶段失败：旧日志毫发无损，临时文件在下次 `Open` 时清理，存储仍可用。
- 替换阶段失败：存储置为 failed，重开后无论落在旧日志还是新快照，都能恢复一致状态。

## 故障注入

通过 `WithFaultHook` 在每个文件系统操作点**逐点**注入故障：

| 注入点 | 时机 |
| --- | --- |
| `OpCreate` | 打开 / 创建日志或压缩临时文件之前 |
| `OpWrite` | 向日志或快照文件写入帧之前 |
| `OpSync` | 数据文件 fsync 之前 |
| `OpRename` | 压缩原子替换之前 |
| `OpDirSync` | 替换后目录 fsync 之前 |

hook 返回普通错误会在该操作执行前中止；在 `OpWrite` 返回 `ErrShortWrite` 可模拟半帧
短写。测试中的 `TestFaultMatrix` 会枚举场景内全部注入点，重开 SDK 并断言：恢复出的
租约令牌与摘要不变、每条承诺记录仍能按原批次 `Identify`、确认高水位是合法连续前缀、
重叠区间仍被拒绝。

## 目录结构

- `record.go`：帧格式与各记录类型的二进制编解码（含 CRC 校验）。
- `batchlease.go`：存储生命周期、日志回放、领取 / 确认 / 查询、故障处理。
- `compact.go`：快照压缩与原子替换。
- `lock_unix.go` / `lock_other.go`：基于 `flock` 的目录独占锁（跨平台回退）。
- `*_test.go`：撕裂恢复、硬损坏、压缩中断、逐点故障矩阵与重开身份识别等测试。

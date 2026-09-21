# framechain

防篡改帧链协议（仅标准库）。每帧携带：**序号**、**前帧摘要**（SHA-256）、
**自身 CRC-32 校验**，帧与帧首尾相连成链。

## 帧格式（大端）

```
magic    4B   "FCH1"
seq      8B   uint64 序号
prev     32B  前一帧的 SHA-256 摘要（创世帧为全零）
paylen   4B   uint32 载荷长度（上限 MaxPayload = 1MiB，分配前检查）
payload  --   载荷
crc32    4B   对 magic..payload 的 IEEE CRC-32
```

帧摘要 = 对完整编码（含 CRC）的 SHA-256。

## 语义

- **任意分块**：`Parser.Feed` 接受任意大小的字节块；缓冲区不够只表示
  "缺数据"（等更多输入），绝不报错；magic/CRC 错、长度超额才是
  `ErrBadData`（坏数据）。长度额度在任何分配之前检查。
- **末帧截断**：`Parser.Finish()` 返回未完成的尾部字节数；仅末尾截断
  可视为"未完成"。**中间缺帧**在 `Chain.Add` 处报 `ErrSeqGap`。
- **断链定位**：`FindBreak` 返回第一个链接失败的帧下标（-1 = 完整）。
  交换相邻帧即使各自 CRC 正确，也会因 prev 摘要/序号不匹配而断链。
- **重复帧**：字节级重发被忽略（`dup=true`，不入链）；同序号不同内容
  报 `ErrDuplicate`。
- **检查点后缀**：`VerifySuffix(cp, frames)` 仅当首帧序号 = cp.Seq+1
  且 prev = cp.Digest 时接受后缀；错误检查点报 `ErrCheckpoint`。
  `NewChainFrom(cp)` 可从检查点继续增量续链。

## 运行

```sh
go test ./...     # 测试
go run ./cmd/demo # 演示
```

# chainframe

纯 Go 标准库实现的**分帧 + 链式校验**字节流解析器。每帧带序号、前帧摘要和
自身校验；字节输入可任意分块；能区分“缺数据”和“坏数据”，并能从检查点
验证后缀、定位断链。

## 帧线格式

所有整数为大端：

| 字段 | 长度 | 说明 |
|---|---:|---|
| magic | 2 | `0xCF 0xC7` 同步字节 |
| seq | 8 | 帧序号（链内连续，从 0 开始） |
| prevDigest | 32 | 前一帧完整线字节的 SHA-256；首帧为全 0 |
| length | 4 | payload 长度 |
| payload | length | 数据体 |
| crc | 4 | CRC-32/IEEE，覆盖 magic 之后的全部字节 |

- `prevDigest` 把帧串成链：交换相邻帧或缺中间帧时，即使每帧 CRC 正确，
  链接也对不上。
- CRC 只保证单帧完整性，`prevDigest + seq` 保证顺序与连续性。
- 固定开销 50 字节/帧（`HeaderLen 46` + `TrailerLen 4`）。

## 核心 API

- `Frame.Encode()` / `DecodeFrame(b, maxPayload)`：单帧编解码。
- `Parser`：流式有状态解析；`Append/Write` 可按**任意大小、任意边界**喂字节，
  `Next()` 每次产出一个事件。
  - `EventFrame`：新接受的链内帧。
  - `EventDuplicate`：与已接受帧**逐字节相同**的重传，不会推进序号/检查点。
- `NewParserAt(cp)`：从检查点起验证后缀；检查点摘要格式错误直接构造失败。
- `Checkpoint{Seq, LastDigest}`：检查点；`NewBuilderAt(cp)` 是生产端镜像，
  便于生成合法后缀。

## 错误语义（缺数据 ≠ 坏数据）

非致命（解析器仍可继续）：

- `ErrNeedMoreData`：当前帧字节不全。**只有末尾截断的帧**会走到这里——
  它是“未完成”，不是损坏；补齐字节后可继续解析。
- `io.EOF`：`Close()` 后缓冲为空，正常结束。

致命坏数据（解析器进入不可恢复状态，之后一律返回同一错误）：

- 线错误：`ErrBadMagic`、`ErrPayloadTooLarge`、`ErrCRC`、`ErrClosed`。
- 链错误，包装为 `*ChainError{Kind, Offset, Got, Want}`：
  - `ErrSequenceGap`：中间缺帧（`f.Seq > want`），必须报错，不允许跳过。
  - `ErrBrokenLink`：`prevDigest` 与前驱不一致（CRC 全对也会报）。
  - `ErrConflictingDuplicate`：同序号但内容不同。
  - `ErrReordered`：落后超过一个位置的旧帧。
- `Offset` 是出错帧在逻辑输入流中的起始字节偏移，可直接定位断链位置。

长度额度在**分配 payload 之前**检查：声明长度超过 `maxPayload`
（默认 1 MiB，可用 `WithMaxPayload` 调整）立即返回 `ErrPayloadTooLarge`，
即使帧尚未收全也不会申请内存。

## 运行

```sh
go test ./... -v        # 单元测试（含随机分块性质测试）
go test -race ./...     # 竞态检测
go test -fuzz=FuzzRoundTrip -fuzztime=10s .
go run ./example        # 运行样例
```

# framestream

带长度帧的双向流状态机库（Go）。与传输层解耦：调用方负责在 `Endpoint`
与底层连接之间搬运字节，状态机只负责帧的编解码与方向状态管理。

## 帧格式

```
| uint32 大端长度 N | N 字节载荷 |
```

## 状态机

每个端点有两个独立的方向状态：

- 发送方向：`SendOpen` →（`CloseSend`）→ `SendHalfClosed`；`Reset` 后进入 `SendReset`。
- 接收方向：`RecvOpen` →（对端干净结束）→ `RecvClosed`；`Reset` 后进入 `RecvReset`。

两个方向独立推进：本端 `CloseSend` 只关闭发送，**继续接收**对端剩余帧；
`Reset` 则立即终止两个方向，丢弃待发送字节与半帧，之后所有操作返回 `ErrReset`。

## API 摘要

- `Send(payload)`：排队一帧（半关闭/重置后报错）。
- `CloseSend()`：半关闭发送方向；调用方应把 `Pending()` 冲刷到连接后关闭写侧。
- `Pending()` / `Consume(n)`：取出/确认已写出的编码字节。
- `Input(p)`：喂入从连接读到的字节，完整帧按序触发 `onFrame` 回调。
  接受任意读边界：一帧可跨任意多次调用，一次调用也可含多帧。
- `InputEOF()`：连接读侧到达 EOF 时调用，用于判定结束方式。
- `Reset()`：立即终止双向。

## 正常结束 vs 对方未发完

`InputEOF()` 的返回值区分两种情况：

- **正常结束**：返回 `nil`。EOF 恰好落在帧边界上（解析缓冲为空），
  说明对端发完了所有帧并干净地关闭了写侧，接收方向进入 `RecvClosed`。
- **对方未发完**：返回 `*TruncatedError`。EOF 落在帧中间（头部或载荷不完整），
  说明对端在最后一帧发完之前就消失了。错误携带 `Have`（已收到的半帧字节数）
  与 `Want`（完整帧总长度；若连 4 字节头都没收齐则为 0，表示总长未知）。
  截断之前已完整的帧不受影响，均已正常投递。

## 测试

`framestream/framestream_test.go` 用字节级切分验证任意读边界：

- `TestByteAtATime`：逐字节喂入整个流。
- `TestAllSplitPoints` / `TestAllTripleSplits`：枚举所有单/双切分点，结果必须一致。
- `TestTruncatedEOF`：在帧内每个可能位置截断，均须报告 `TruncatedError` 且位置准确。
- `TestHalfClose` / `TestReset` / `TestCleanEOF`：方向状态机语义。

运行（Go 1.11 需显式开启 module 并指定可写缓存）：

```sh
GO111MODULE=on GOCACHE=/private/tmp/gocache go test ./...
```

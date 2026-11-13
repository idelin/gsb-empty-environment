# 警报不得拆断握手

一条握手报文中间插入close_notify，前后两半仍拼成合法报文。警报结束连接，未完成的握手报文必须丢弃，不能和后续字节拼接。完整握手报文之后的close_notify正常结束。可导入的Go状态机接TLS栈送来的状态。在握手报文中间插入警报再补上后半段。

## 用法

`Machine.FeedRecord` 接收 TLS 栈已经解码的记录类型和记录内容。完整握手报文会从调用结果返回；未完成的报文留在缓冲区中。

```go
import netsec "example.org/netsec/q0238"

machine := netsec.NewMachine()

// 前半段握手报文。
messages, err := machine.FeedRecord(netsec.RecordTypeHandshake, firstHalf)
// messages 为空，前半段仍在缓冲区。

// 报文中间收到 close_notify。
err = machine.FeedRecord(netsec.RecordTypeAlert, []byte{
	netsec.AlertLevelWarning, netsec.AlertCloseNotify,
})
// err == nil，连接正常关闭，未完成的握手报文被丢弃。

// 后半段不能与前半段拼接，也不会产生握手报文。
messages, err = machine.FeedRecord(netsec.RecordTypeHandshake, secondHalf)
// errors.Is(err, netsec.ErrClosed) == true，messages 为空。
```

## 行为

- 握手报文按 TLS 的 1 字节类型加 3 字节长度头重组，可跨多个 handshake 记录。
- 任意警报都会结束连接并清空未完成的握手缓冲。`close_notify` 正常返回 `nil`，其他警报返回 `*AlertError`。
- 连接关闭后，`FeedRecord`、`FeedHandshake` 和 `FeedAlert` 都返回 `ErrClosed`，不会读取或拼接新字节。
- 完整握手报文会先返回；之后收到 `close_notify` 属于正常关闭。

运行测试：

```sh
go test ./...
```

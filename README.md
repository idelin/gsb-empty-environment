# HTTP二代续帧互斥

网关收到HEADERS后，只允许同一流的CONTINUATION接续到END_HEADERS，不能穿插其他帧。写供Go接收宿主调用的组装包，头块字节与解码后字段分别限额，关闭或重置流不能保留可被复用的半块。用所有切帧位置和恶意穿插序列验证错误连接立即停止发布，已完成其他请求不被改写。

## 包结构

- 根包 `netsec`（`assembler.go`）：供接收宿主调用的 HEADERS/CONTINUATION
  组装状态机，负责续帧互斥、双限额、流与连接级清理。
- `hpack`：零外部依赖的最小 RFC 7541 解码器（静态表、动态表、Huffman、
  全部字面量/索引表示）。`hpack/tables_data.go` 由 `internal/gentables`
  从本地工具链 vendor 的参考实现提取生成，可用
  `GOROOT=$(go env GOROOT) go run ./internal/gentables` 重新生成。

## 宿主接入

宿主按连接读帧，剥掉 HEADERS 的填充/优先级字节后，把帧交给
`Assembler.HandleFrame`：

```go
dec := hpack.NewDecoder(4096, 4096)
asm := netsec.New(dec, netsec.NewDefaultLimits())

res, err := asm.HandleFrame(netsec.Frame{
    Type: netsec.FrameHeaders, StreamID: id,
    Payload: blockFragment, EndHeaders: endHeaders,
})
```

仅在收到 END_HEADERS 的那一帧返回一次非空 `*Result`。

## 安全模型

- 续帧互斥：HEADERS 未带 END_HEADERS 时，只接受同流 CONTINUATION；
  异流 CONTINUATION、第二个 HEADERS、DATA/SETTINGS 等任何穿插帧，以及
  无 HEADERS 的 CONTINUATION，均返回 `ProtocolError`。组装器随即永久
  关闭，后续所有帧以 `ErrConnectionClosed` 拒绝，宿主应发 GOAWAY。
- 本流 RST_STREAM（`FrameReset`）或宿主调用 `ResetStream`/`CloseStream`
  会丢弃该流的半块并返回 `StreamError`；异流 RST_STREAM 在开块期间仍按
  穿插处理。`Close` 丢弃全部半块并关闭连接。
- 双限额独立生效：`MaxBlockBytes` 在拼接阶段按累计字节拒绝（解码前），
  `MaxFields` 在 HPACK 解码后按字段数拒绝；两者都是流级错误，不污染连接。
- 每个片段都复制进内部缓冲，宿主回收帧缓冲不会改写已组装内容；半块在
  任何错误/重置/关闭路径上都不保留，无法被后续请求复用。
- 已发布结果只追加不改写；测试用 `PublishedSnapshot` 证明恶意序列后既有
  请求保持不变。

## 验证

- `TestAllSplitPositions`：对头块的每个内部字节边界各做一次两帧切分，
  并补充 run length 1..4 的多帧切分，plain 与 Huffman 两种编码都覆盖。
- `TestInterleaveSequences`：8 类恶意穿插序列逐一验证 `ProtocolError`、
  组装器永久关闭、停止发布且既有结果不变。
- 限额、畸形 HPACK、RST_STREAM/关闭清理、宿主缓冲复用等均有专项测试。
- `go test -race ./...` 通过；根包与 hpack 语句覆盖率分别约 99%。

# h2guard — HTTP/2 原始帧安全解析与 DoS/走私防御状态机

`h2guard` 是一个零外部依赖（仅标准库）的可导入 Go 包，直接解析
RFC 9113 的 9 字节帧头与载荷，并内置简化 HPACK（RFC 7541）解码器，
专为反向代理 / API 网关在把请求交给业务栈之前做安全拦截而设计。
包内不导入、不包装任何外部 HTTP/2 库，也不发起任何网络访问。

## 防御能力

1. **帧序与头块状态机**
   - `HEADERS`/`PUSH_PROMISE` 未置 `END_HEADERS` 时，下一帧必须是同一流
     ID 的 `CONTINUATION`；跨流帧、同流非 `CONTINUATION`、连接级帧交错
     一律 `PROTOCOL_ERROR (0x1)` + `GOAWAY`。
   - 单头块 `CONTINUATION` 帧数硬上限（`Config.MaxContinuationFrames`，
     默认 16）与累计字节硬上限（`Config.MaxHeaderBlockBytes`，默认 1 MiB），
     超限返回 `ENHANCE_YOUR_CALM (0xb)` + `GOAWAY`，拦截 CONTINUATION Flood。
   - 无开放头块时收到 `CONTINUATION` 同样是 `PROTOCOL_ERROR`。

2. **伪头与字段规范校验（请求走私面）**
   - 拒绝重复伪头、普通头之后出现伪头、未知伪头，强制 `:method`/`:scheme`/
     `:path` 必备（`OPTIONS *` 例外）。
   - 拒绝大写头名，拒绝头名/头值中的 `CR`/`LF`/`NUL`。
   - 硬拒逐跳头 `connection`、`proxy-connection`、`keep-alive`、
     `transfer-encoding`、`upgrade`，以及非 `trailers` 值的 `te`。
   - `Content-Length` 解析、冲突值检测，并与实际 `DATA` 帧载荷字节逐帧比对：
     中途超出立即报错，`END_STREAM` 时总量不符按走私报文 `PROTOCOL_ERROR` +
     `GOAWAY`。

3. **Rapid Reset（CVE-2023-44487）滑动窗口检测**
   - 记录窗口内客户端开流（`HEADERS`）与“响应完成前主动 `RST_STREAM`”事件，
     同时满足**绝对次数**与**重置占比**双阈值才触发，避免误伤。
   - 客户端已发 `END_STREAM` 后的 `RST_STREAM` 不计入攻击特征。
   - 触发即返回 `ENHANCE_YOUR_CALM (0xb)`，`Parser.GoAwayFrame()` 给出可直接
     写回连接的 `GOAWAY`（含 last stream id 与调试信息），随后解析器进入
     关闭态拒绝后续帧。
   - 时钟可通过 `Config.Now` 注入，窗口过期行为可确定性测试。

## 包结构

| 文件 | 职责 |
| --- | --- |
| `frame.go` | 9 字节帧头解析 `ReadFrame`，`GOAWAY`/`RST_STREAM`/`SETTINGS` 等帧构造 |
| `errors.go` | `ConnError`（连接级，必 GOAWAY）与 `StreamError`（流级，RST 后继续） |
| `hpack.go` | 简化 HPACK 解码器：静态表 61 项、动态表、整数/字符串表示；Huffman 一律 `COMPRESSION_ERROR` |
| `validate.go` | 伪头顺序/唯一性、非法字节、逐跳头、`Content-Length` 校验 |
| `rapidreset.go` | 滑动窗口 Rapid Reset 检测器（次数 + 占比双阈值） |
| `parser.go` / `dispatch.go` / `block.go` / `frames.go` | 连接级安全状态机 |

## 最小用法

```go
parser := h2guard.NewParser(nil) // 使用 DefaultConfig 的保守阈值
err := parser.Run(conn, func(ev *h2guard.Event) {
    // 仅安全事件会到达这里：EventHeaders / EventData / EventRSTStream ...
    switch ev.Type {
    case h2guard.EventHeaders:
        _ = ev.Headers.Method // 已完成全部伪头与字段校验
    }
}, func(se *h2guard.StreamError) {
    conn.Write(h2guard.BuildRSTStream(se.StreamID, se.Code)) // 流级错误不断连
})
if err != nil {
    if ce, ok := h2guard.AsConnError(err); ok {
        conn.Write(parser.GoAwayFrame()) // 连接级错误：发 GOAWAY 后断连
        _ = ce
    }
}
```

需要自定义阈值或注入时钟时传入 `*Config`（字段为零值会回退默认值）。

## HPACK 范围说明

解码器完整支持索引表示、带/不带索引与永不索引字面量、动态表大小更新、
多字节整数与 H=0 字符串；Huffman（H=1）字符串按设计直接返回
`COMPRESSION_ERROR`，以保持代码体量可审计。网关在自身 `SETTINGS`
`SETTINGS_HEADER_TABLE_SIZE` 协商范围内使用，所有测试构造的头块均为 H=0。

## 测试

`go test ./...`（含 `-race`）覆盖 40+ 用例，关键对抗测试：

- `TestContinuationFlood` / `TestHeaderBlockByteCap`：洪泛与字节上限触发 `0xb` + GOAWAY
- `TestCrossStreamInterleave`：四种交错形态全部 `PROTOCOL_ERROR` + GOAWAY
- `TestPseudoHeaderViolations` / `TestFieldNameValueSmuggling`：伪头与走私字段
- `TestContentLengthSmuggling`：少报藏 body、多报短 body、中途超限、冲突值
- `TestRapidResetTripsGOAWAY` / `TestRapidResetGOAWAYPayload`：CVE-2023-44487 触发 `0xb` + GOAWAY
- `TestRapidResetRatioGuard` / `TestRapidResetSlidingWindowExpiry`：占比保护与窗口过期

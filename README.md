# HTTP跨版本边界一致性

代理把HTTP/2请求转换为HTTP/1消息时，伪头与普通头冲突可能改变下游解释。实现供Go代理调用的转换包，伪头必须先于普通头且唯一，拒绝连接专用字段，authority与Host必须一致；正文长度按实际数据帧累计。完整验证后才发布下游请求，用原始帧序列测试重复伪头、尾部首部夹带长度与中途结束。

## 包结构

`netsec` 仅依赖 Go 标准库，在没有 `golang.org/x/net` 的环境下自带 HTTP/2
帧层（`frame.go`）、HPACK 解码器（`hpack.go`、`huffman.go`）。

- `connection.go`：连接级多路复用，强制 HEADERS/CONTINUATION 序列连续且
  属于同一条流（RFC 7540 §6.2/§6.10）。
- `convert.go`：单流状态机，缓冲首部与正文，验证全部通过后才产出请求。
- `validate.go`：首部/尾部安全规则与 `http.Request` 构造。
- `http1.go`：确定性的 HTTP/1.1 线路序列化（`WriteHTTP1`）。

## 验证规则

- 伪首部只能是 `:method`、`:scheme`、`:path`、`:authority`，必须全部位于
  普通首部之前，且每个只能出现一次。
- 拒绝连接专用字段：`Connection`、`Keep-Alive`、`Proxy-Connection`、
  `Transfer-Encoding`、`Upgrade`、`Trailer`；`TE` 仅允许 `trailers`。
- `:authority` 与 `Host` 同时存在时必须逐字一致；缺失时互相补全。
- 正文长度只按实际 DATA 帧负载累计（排除填充），与声明的
  `Content-Length` 不一致即拒绝；重复且冲突的 `Content-Length` 拒绝。
- 尾部（trailer）禁止夹带伪首部、`Content-Length` 及所有连接专用字段；
  合法尾部在 HTTP/1 侧以 chunked 编码发送。
- 首部名称/值含 NUL、CR、LF 或大写名称一律拒绝。
- 流在 END_STREAM 之前、RST_STREAM 之后或任何校验失败时都不会发布下游
  请求；失败返回对应的 RST_STREAM 错误码（`ErrorCode`）。

## 调用示例

```go
conn := netsec.NewConnection(tlsConn, 4096)
for {
    conv, err := conn.NextFrame()
    if err != nil {
        // 帧级错误：发送 GOAWAY 并关闭连接
        break
    }
    if conv != nil && conv.Ready() {
        req := conv.Request()      // 完整验证后的 *http.Request
        _ = conv.WriteHTTP1(downstreamConn) // 或直接用 http.Client 发送
    } else if err := conv.Err(); err != nil {
        // 流级错误：回 RST_STREAM，错误码取 conv.ErrorCode()
    }
}
```

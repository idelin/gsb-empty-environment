# 连接关闭正文边界

代理对没有长度字段的响应以连接关闭定界，不能把同一连接的后续响应错当正文。实现供Go转发宿主调用的响应序列包，按方法与状态码决定无正文、定长或关闭定界；进入关闭定界后禁止复用连接，提前关闭定长正文为截断。用本地脚本服务器测试HEAD、信息响应、隧道成功与截断，核对每字节归属。

## 包结构

- `reader.go`：响应序列读取器 `Reader`、分帧枚举 `Framing`，以及按方法/状态码/
  长度头部决定分帧方式的规则（RFC 9112）。
- `bodies.go`：四种正文读取器——定长、关闭定界、隧道透传与 chunked 状态机。
- `*_test.go`：基于 `net.Pipe` 的进程内脚本服务器与字节流测试，逐字节核对归属。

## 分帧规则

`Reader.ReadResponse(method)` 按以下顺序判定每个最终响应：

- `1xx` 信息响应：无正文，作为独立响应返回，不改变连接复用状态。
- `HEAD`、`204`、`304`：无正文；头部里的 `Content-Length` 只作描述，不消费线上字节。
- `CONNECT` 的 `2xx`：隧道模式，连接立刻标记为不可复用，之后字节原样透传。
- `Transfer-Encoding: chunked`：按块定界；与 `Content-Length` 同时出现判协议错误。
- 其它 `Transfer-Encoding`：无法自定界，回退为关闭定界。
- 有 `Content-Length`：定长；提前 EOF 返回 `ErrTruncated`，连接禁止复用。
- 没有任何长度字段：关闭定界，正文持续到连接关闭，连接禁止复用。

连接是否可复用由 `Reader.Reusable()` 暴露。HTTP/1.1 默认保持连接、
`Connection: close` 关闭；HTTP/1.0 默认关闭、`Connection: keep-alive` 保持。
正文未读完或未 `Close` 时调用下一次 `ReadResponse` 会直接报错，避免字节归属混乱。

## 典型用法

```go
r := netsec.NewReader(conn)
for r.Reusable() {
    resp, err := r.ReadResponse(req.Method)
    if errors.Is(err, io.EOF) {
        break
    }
    // resp.Framing 为 netsec.FramingLength / FramingClose / FramingChunked ...
    if _, err := io.Copy(dst, resp.Body); err != nil {
        // ErrTruncated：定长/chunked 正文被对端提前关闭，连接不可复用
    }
    resp.Body.Close()
}
```

隧道切换后，先用 `Reader.BufferedReader()` 取走已缓冲的前导字节，再直接在原连接上收发。

## 测试

```sh
go test -race -count=1 ./...
```

脚本服务器测试覆盖：HEAD 不吞后续响应、100/103 信息响应、CONNECT 隧道成功、
定长与 chunked 截断、关闭定界、HTTP/1.0 与 `Connection` 复用规则，以及逐字节归属
（含 1 字节小读、分段到达与 CL+TE 走私头拒绝）。

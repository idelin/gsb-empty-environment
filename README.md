反向代理与 WAF 网关在转发 HTTP/1.1（RFC 9112）持久连接请求至异构后端时，若对同时携带 `Content-Length` 与 `Transfer-Encoding`、畸形 `Transfer-Encoding` 混淆（如 `Transfer-Encoding: chunked, identity`、前导空格/制表符/折行 `obs-fold`、大小写变形）、多重冲突 `Content-Length` 或非法 Chunked 编码（负尺寸、十六进制前导垃圾、缺失 `CRLF`、畸形 Chunk Extension 与 Trailer 注入）处理宽松，攻击者可发动 CL.TE / TE.CL / TE.TE 请求走私，绕过路径鉴权或污染下一用户响应队列。为 Go 网关安全宿主实现可导入的原始字节级 HTTP/1.1 持久流防走私解析与规范化组件，严禁联网与严禁调用 `net/http.ReadRequest` 替代底层校验。组件直接从原始字节流逐帧切分并严格裁决：（1）请求行与头块硬校验：仅允许精确 `CRLF` 行终止（硬拒绝裸 `LF` 或裸 `CR` 防行边界混淆）、拒绝 `obs-fold` 折行头、拒绝头名含空白/控制字符或冒号前空白；（2）帧界定防走私硬门禁：同时存在 `Transfer-Encoding` 与 `Content-Length` 一律硬拒绝（400 且强制关闭连接，严禁仅删其一放行）；存在多个 `Content-Length` 时即使值相同也按策略拒绝或硬拒冲突值，且 `Content-Length` 仅允许纯 ASCII 十进制数字（无符号无空白无溢出）；`Transfer-Encoding` 仅允许唯一合法 `chunked`（拒绝任意混淆、未知编码或非末位 `chunked`）；（3）严格 Chunked 解码：仅允许合法十六进制长度（上限受控）、校验每块后精确 `CRLF`、硬拒绝超长/含控制字符的 Chunk Extension、仅放行白名单安全 Trailer 头（禁止 Trailer 注入 `Host`/`Content-Length`/`Authorization`）。附 CL.TE、TE.CL、裸 LF 走私、`obs-fold`、多重 CL 与 Trailer 注入对抗测试。

## 实现

可导入包位于 `antismuggle/`，完全基于原始字节解析，不依赖、也不调用
`net/http.ReadRequest`，无任何联网行为。

### 核心类型

- `antismuggle.NewConn(r io.Reader, cfg *Config) *Conn`：包裹持久连接字节流。
- `Conn.Next() (*Request, error)`：逐帧切分并严格裁决；干净帧边界返回 `io.EOF`。
- `Request.Body`：identity 原始字节或 chunked 解码后的字节流，EOF 精确落在帧边界。
- `Normalize(req)`：完整读取并校验 body，输出规范化帧，统一为单个
  `Content-Length`，剥离 `Transfer-Encoding`/`Trailer`，可安全转发异构后端。
- 所有协议违规返回 `*antismuggle.Error`，`StatusCode()` 恒为 400、
  `CloseConnection` 恒为 `true`；违规后 `Conn` 永久毒化，强制关连。

### 严格裁决点

- 行终止仅接受精确 `CRLF`；裸 `LF`、裸 `CR`、`obs-fold` 折行立即毒化连接。
- 头名必须为 RFC 9110 token，拒绝空白/控制字符/冒号前空白；请求行三要素硬校验。
- CL 与 TE 共存即硬拒（绝不删头放行）；多重 CL 默认即使相同也拒绝，
  CL 值仅接受无符号、无空白、无溢出的纯 ASCII 十进制数字。
- TE 仅接受唯一精确小写 `chunked`（大小写兼容需显式开启），拒绝列表、未知编码、
  非末位 chunked，以及 `Connection: Transfer-Encoding` 这类 TE.TE 混淆。
- chunked 逐字节校验十六进制尺寸（受控上限）、每块后精确 `CRLF`、
  chunk extension 长度与控制字符，Trailer 走白名单且永久封禁
  `Host`/`Content-Length`/`Authorization` 等 hop/安全头。

### 用法

```go
cfg := antismuggle.DefaultConfig()
cfg.AllowedTrailers = []string{"x-trace"}
conn := antismuggle.NewConn(tcpConn, cfg)

for {
    req, err := conn.Next()
    if err == io.EOF {
        return // 干净的 keep-alive 关闭
    }
    var se *antismuggle.Error
    if errors.As(err, &se) {
        // 写 400 并关闭 TCP，禁止复用该连接
        return
    }

    normalized, err := antismuggle.Normalize(req)
    if err != nil {
        return // 解码阶段违规，同样关连
    }
    backend.Write(normalized.Bytes())
}
```

### 测试

`go test ./...`（含 `-race`）覆盖 CL.TE、TE.CL、裸 LF/CR、`obs-fold`、
多重/冲突 CL、TE 混淆、非法 chunk 尺寸/终止符/extension、Trailer 注入、
逐字节读取与流水线第二帧定位等对抗与功能场景。

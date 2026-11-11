# wsupgrade

反向代理看到客户端带Upgrade头就直接转成双向隧道，上游实际返回非101时，攻击者借此走私请求。给Go反向代理实现WebSocket升级处理器：只有上游返回101，且Upgrade、Connection与Sec-WebSocket-Accept都匹配时才切换为隧道，否则按普通HTTP响应处理后关闭；升级前后已缓冲的字节不能丢失或错发给另一方。用伪造的上游测试200、426、错误的Accept和半关闭。

## 设计

`Proxy` 实现了 `http.Handler`，对带 `Connection: Upgrade` 的请求走加固的升级路径
（`proxyUpgrade`），其余请求按普通反向代理转发。

升级路径的关键安全性质：

- **验证前绝不劫持客户端连接**。先与上游建立原始连接、读出上游响应并完成全部校验，
  通过后才 `Hijack` 客户端。因此任何失败都仍走普通 HTTP 响应通道，不会停留在无法回退的
  裸 socket 上。
- **严格校验 101**（`validateUpgradeResponse`，依据 RFC 6455 4.2.2）：
  - `Connection` 含 `upgrade` token；
  - `Upgrade` 与请求的协议大小写不敏感匹配；
  - `Sec-WebSocket-Accept` 必须等于 `base64(SHA1(Sec-WebSocket-Key + GUID))`。
- **非 101 响应**（如 200、426）原样作为普通 HTTP 响应回写，并强制 `Connection: close`
  关闭客户端连接，使流水线里夹带的走私请求无法被当作新请求处理。
- **畸形 101**（Accept 错误/缺失、协议不匹配、Connection 缺 token）回 502 并关闭连接，
  上游随后发来的任何“隧道”字节都不会转发给客户端。
- **缓冲字节不丢失、不串端**。两端均用 `bufferedConn` 包装，切换隧道前已读入
  `bufio.Reader` 的字节会先被排空再读底层连接；客户端的早期数据与上游 101 后立即发出的
  帧都按序送达正确的对端。
- **半关闭**（`tunnel`/`copyAndClose`）。一个方向读到 EOF 时只对该方向 `CloseWrite`，
  另一方向继续传输，模拟 TCP 半关闭语义；出错才整体关闭。

## 用法

```go
target, _ := url.Parse("http://upstream.example")
proxy := wsupgrade.NewProxy(target)
http.ListenAndServe(":8080", proxy)
```

`ws`/`http` 走明文 TCP，`wss`/`https` 自动在原始连接上做 TLS 握手。

## 测试

`wsupgrade_test.go` 用进程内全双工管道（带半关闭与超时语义，避免依赖沙箱禁用的回环端口）
驱动真实的 `http.Server`，以“伪造上游”覆盖：

- `TestUpstream200_NoTunnel`：200 普通响应 + 夹带走私字节，验证不开隧道、响应完整、连接关闭；
- `TestUpstream426_NoTunnel`：426 作为普通响应回写且剥离 hop-by-hop 头；
- `TestMalformed101_Rejected`：错误/缺失 Accept、错误 Upgrade、Connection 缺 token，均回 502；
- `TestValidUpgrade_TunnelPreservesBufferedBytes`：合法 101 下双向缓冲字节按序保留；
- `TestHalfClose`：客户端半关闭后上游收到 EOF 且反向仍可发送。

运行：`go test -race ./...`

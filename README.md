# TLS降级哨兵与FALLBACK_SCSV

中间人让客户端的高版本握手失败，诱使它以更低版本重试，再利用旧版本的弱点。为Go TLS 实现写可导入的降级防护包，服务端与客户端各有一个入口：服务端自身最高支持 TLS 1.3 却协商出 TLS 1.2 时，ServerHello.random 末 8 字节须为 44 4F 57 4E 47 52 44 01，协商出 TLS 1.1 或更低时为 44 4F 57 4E 47 52 44 00；服务端自身最高只到 TLS 1.2 时不写哨兵；客户端自己支持 TLS 1.3 却协商到 TLS 1.2 或更低时，必须检查 random 末 8 字节，等于上述任一哨兵即按 illegal_parameter 终止；ClientHello 的密码套件含 TLS_FALLBACK_SCSV（0x5600）而其 legacy_version 低于服务端最高支持版本时，服务端发 inappropriate_fallback 并终止；客户端只有在自行降级重试时才可带该套件，首次握手不得带。用 1.2 哨兵、1.1 哨兵、SCSV 加低版本、首次握手带 SCSV 和仅支持 1.2 的合法握手验证。

## 用法

包路径为 `example.org/netsec/q0772`，包名 `netsec`。服务端和客户端入口都返回
`*tls.Conn`，可以直接调用 `Handshake`、`Read`、`Write`。

```go
import "example.org/netsec/q0772"

// 服务端：检查 FALLBACK_SCSV，并按自身最高版本写入降级哨兵。
server := netsec.Server(rawServerConn, serverConfig)

// 首次握手：绝不会发送 TLS_FALLBACK_SCSV。
client := netsec.Client(rawClientConn, clientConfig)

// 明确的降级重试：仅此入口会在 ClientHello 中加入 TLS_FALLBACK_SCSV。
retry := netsec.ClientFallback(rawClientConn, lowerVersionConfig)
```

也可以用函数选项：`netsec.Client(conn, cfg, netsec.WithFallback())`。

协议层辅助函数可用于自定义 TLS 栈或单元测试：

- `ServerSentinelFor(serverMax, negotiated)` / `ApplyServerSentinel`
- `ValidateServerRandom(clientMax, negotiated, random)`
- `HasFallbackSCSV` / `ValidateFallbackSCSV`
- `MaxSupportedVersion`

服务端入口的 SCSV 判断使用 ClientHello 的 `legacy_version`（不是
`supported_versions` 中协商出的版本），因此 TLS 1.3 客户端若错误地在首次握手
携带 SCSV，也会收到 `inappropriate_fallback`。

## 验证

`go test ./...` 覆盖：

- TLS 1.3 服务端协商 TLS 1.2 时写入 `DOWNGRD\x01`；
- TLS 1.3 服务端协商 TLS 1.1 时写入 `DOWNGRD\x00`；
- 降级重试携带 SCSV 且 `legacy_version` 低于服务端最高版本时返回
  `inappropriate_fallback`；
- 降级重试携带 SCSV 且 `legacy_version` 等于服务端最高版本时完成握手；
- 首次握手即使配置中包含 SCSV 也不会发送；
- 仅支持 TLS 1.2 的服务端与 TLS 1.3 客户端合法协商到 TLS 1.2，且不写哨兵；
- TLS 1.3 客户端收到任一哨兵时发送 `illegal_parameter`。

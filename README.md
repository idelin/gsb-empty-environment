# TLS降级哨兵与FALLBACK_SCSV

中间人让客户端的高版本握手失败，诱使它以更低版本重试，再利用旧版本的弱点。为Go TLS 实现写可导入的降级防护包，服务端与客户端各有一个入口：服务端自身最高支持 TLS 1.3 却协商出 TLS 1.2 时，ServerHello.random 末 8 字节须为 44 4F 57 4E 47 52 44 01，协商出 TLS 1.1 或更低时为 44 4F 57 4E 47 52 44 00；服务端自身最高只到 TLS 1.2 时不写哨兵；客户端自己支持 TLS 1.3 却协商到 TLS 1.2 或更低时，必须检查 random 末 8 字节，等于上述任一哨兵即按 illegal_parameter 终止；ClientHello 的密码套件含 TLS_FALLBACK_SCSV（0x5600）而其 legacy_version 低于服务端最高支持版本时，服务端发 inappropriate_fallback 并终止；客户端只有在自行降级重试时才可带该套件，首次握手不得带。用 1.2 哨兵、1.1 哨兵、SCSV 加低版本、首次握手带 SCSV 和仅支持 1.2 的合法握手验证。

## 用法

```go
serverConfig := netsec.ServerConfig(&tls.Config{
	Certificates: []tls.Certificate{cert},
	MinVersion:   tls.VersionTLS12,
	MaxVersion:   tls.VersionTLS13,
})
ln, err := netsec.Listen("tcp", ":443", serverConfig)
```

```go
clientConfig := netsec.ClientConfig(&tls.Config{
	ServerName: "example.com",
	RootCAs:    roots,
})
conn, err := netsec.DialWithFallback("tcp", "example.com:443", clientConfig)
```

服务端入口是 `Server` / `NewListener` / `Listen`，客户端入口是 `Client` /
`DialWithFallback`。`DialWithFallback` 的首次握手不带
`TLS_FALLBACK_SCSV`；只有高版本握手失败后的降级重试才会在
ClientHello 中加入 `0x5600`。

也可直接使用策略函数：

- `DowngradeSentinel(serverMax, negotiated)` 返回应写入的末 8 字节。
- `CheckServerRandom(clientMax, negotiated, random)` 检查客户端哨兵。
- `CheckFallbackSCSV(serverMax, clientLegacy, suites)` 检查 RFC 7507。
- `ClientCipherSuites(base, fallbackRetry)` 构造首次或重试套件列表。

## 验证

`go test ./...` 覆盖 TLS 1.2 哨兵、TLS 1.1 哨兵、仅支持 TLS 1.2 的合法握手、
客户端对两种哨兵的 `illegal_parameter` 终止、低版本 ClientHello 携带
`TLS_FALLBACK_SCSV` 时的 `inappropriate_fallback`，以及首次握手不带 SCSV、
降级重试才带 SCSV。

# ALPN跨协议严格匹配

本包为共享同一张证书的 HTTPS、SMTP 与隐式 TLS FTP 监听器提供逐监听器
ALPN 白名单。监听器只接受自己声明的协议；客户端携带 ALPN 但没有交集时
发送 RFC 7301 的 `no_application_protocol` 致命告警，不在 TLS 之上发送任何
协议横幅。没有携带 ALPN 的客户端只有在监听器显式配置 `LegacyProtocol` 时
才被接受。

```go
policy, err := netsec.NewPolicy([]string{"smtp"}, "smtp")
config, err := netsec.ConfigureTLS(baseTLSConfig, policy)
ln, err := netsec.NewListener(rawTCPListener, baseTLSConfig, policy)

for {
    conn, err := ln.Accept() // TLS 握手已经完成
    if err != nil {
        return err
    }
    go serveSMTP(conn) // 此时才可以写 "220 ..." 横幅
}
```

`ConfigureTLS` 可用于自定义 `tls.Server` 流程，但 `NewListener` 额外保证
`Accept` 返回前握手已经完成。跨协议客户端（例如只提供 `http/1.1` 的浏览器）
打到 SMTP 端口会在 TLS 握手阶段收到 alert 120，首个应用层字节永远不会是
SMTP 的 `220` 横幅。

同一张通配证书同时给HTTPS、SMTP和FTP隐式TLS端口使用，攻击者让浏览器把HTTPS请求发到邮件端口，借跨协议反射读取内容。给Go TLS前端写供各监听器握手回调调用的ALPN严格匹配包：每个监听器声明可接受的ALPN集合，客户端提供了ALPN而无交集时按RFC 7301发出no_application_protocol致命告警，完全未提供ALPN的只允许调用方列出的遗留协议；握手完成前不得写任何应用层横幅。用http/1.1打到SMTP监听器、空ALPN、多值交集和合法握手验证首个应用层字节。

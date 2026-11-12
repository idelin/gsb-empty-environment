# Gemini TOFU证书更替判定

Gemini 客户端首次连接记下服务器证书指纹，之后若证书变化，攻击者在路径上换证书就能冒充，而正常续期又必须放行。给Go客户端写供连接建立后调用的指纹固定包，按本题规定：以 SPKI 的 SHA-256 为指纹，按主机名与端口记录首次出现的时间与证书到期时间；指纹相同则通过；指纹变化时，仅当已记录证书已过期，或新证书由调用方信任的链验证通过时才接受并更新记录，否则返回指纹变化错误且不更新；证书里的主机名必须匹配所连主机，不匹配拒绝，不论指纹是否已记录；时间由调用方注入，到期时刻本身视为已过期；记录只在握手完全成功后写入。用换证书、续期、到期时刻边界、主机名不符与握手中途失败验证。

## 用法

```go
roots := x509.NewCertPool()
roots.AddCert(caCert)

pinner := netsec.NewPinner(roots)

conn, err := tls.Dial("tcp", "example.com:443", &tls.Config{
    ServerName: "example.com",
})
if err != nil {
    return err
}
defer conn.Close()

if err := pinner.CheckConnection(conn, "example.com", 443, clock()); err != nil {
    return err
}
```

也可以只用 `tls.Client` 完成握手后调用 `CheckConn(conn, now)`，此时主机名取
`ConnectionState.ServerName`，端口取远端端口。所有检查都以调用方传入的
`time.Time` 为准；记录使用 `MemoryStore`，也可通过 `WithStore` 注入持久化实现。

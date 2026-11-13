# 外部PSK与恢复分开

外部预共享密钥走了恢复用的绑定密钥。外部PSK和恢复PSK使用不同的绑定密钥派生；用错一类必须失败。按外部PSK派生的绑定值可以通过。可导入的Go库供TLS栈调用。用恢复密钥去验外部PSK的绑定值。

## API

这个包实现了 RFC 8446 4.2.11.2 的 TLS 1.3 PSK binder 派生，并按 PSK
来源选择不同的 binder key 标签：

- `PSKExternal` -> `"ext binder"`
- `PSKResumption` -> `"res binder"`
- `PSKImported` -> `"imp binder"`（RFC 9258）

```go
psk := []byte("provisioned out of band")
transcript := sha256.Sum256(truncatedClientHello) // 或传完整 ClientHello

binder := netsec.ComputeBinder(psk, transcript[:], netsec.PSKExternal, sha256.New)
ok := netsec.VerifyBinder(psk, transcript[:], binder, netsec.PSKExternal, sha256.New)
```

`ComputeBinder` 和 `VerifyBinder` 也接受已经派生好的 binder key（无类型参数时，
长度为 Hash.length 的第一个切片按 binder key 处理）。`VerifyBinder` 对
binder 值使用常量时间比较；用恢复 PSK 的标签或 binder key 验证外部 PSK
binder 会失败。

# 证书压缩字典隔离

Go TLS 服务端证书压缩包（RFC 8879）。它把编码后的 Certificate 消息体压缩为
CompressedCertificate(25)，并强制租户、字典、缓存和长度边界。

## 握手路径

```go
registry := netsec.NewDictionaryRegistry()
_ = registry.Register("tenant-a", "a.example.com", 7, tenantDictionary)

server := netsec.NewServer(netsec.ServerConfig{
    Dictionaries: registry,
    Cache:        netsec.NewCompressionCache(1024),
    Preference:   []netsec.Algorithm{netsec.AlgorithmZlib},
})

connection := server.NewConnection("tenant-a", "a.example.com")
result, err := connection.CompressCertificateFromExtension(
    certificateMessageBody,
    clientCompressCertificateExtensionBody,
    7, // 0 表示不使用字典
)
if err != nil {
    // 若 err 匹配 netsec.ErrBadCertificate，发送 bad_certificate(42)。
}
handshakeMessage, _ := result.MarshalHandshake()
// result.HandshakeType 为 25（压缩）或 11（未压缩回退）。
```

`netsec.MarshalCertificateMessage` 可从 DER 证书链构造 `certificateMessageBody`。
`netsec.ParseCompressedCertificate` 与 `netsec.DecompressCertificate` 校验并解压
收到的消息。

## 安全约束

- 只选择客户端 `compress_certificate(27)` 扩展中列出且服务端已实现的算法；
  未列出时回退到未压缩 Certificate。
- 缓存键严格为 `(算法, 字典 ID, 证书链字节 SHA-256)`。字典 ID 在
  `DictionaryRegistry` 中全局唯一，且绑定唯一租户和 SNI，因此该键不会跨租户
  混淆字典。
- 字典查找同时校验租户和 SNI；声明其他租户或 SNI 的字典 ID 返回
  `ErrCrossTenantDictionary`，不会进入压缩或缓存路径。
- `uncompressed_length` 超过 `2^24-1` 直接拒绝。解压输出通过
  `limit = declared + 1` 读取；实际长度不等于声明长度时返回
  `ErrBadCertificate`（可用 `AlertForError` 映射为告警）。
- `Connection` 记录发送状态；第二次压缩证书返回
  `ErrCompressedCertificateAlreadySent`。
- CompressedCertificate 总长度（含 8 字节固定头）不小于原文 Certificate 消息
  体时返回未压缩 Certificate，且不占用连接的一次压缩额度、不写入缓存。

## 算法支持

内置 zlib(1)。Brotli(2) 与 zstd(3) 没有 Go 标准库实现，可通过
`CodecRegistry.Register` 注入；未注入的算法不会因为客户端列出而被使用。

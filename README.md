# 证书压缩字典隔离

TLS 服务端为减小握手而缓存压缩后的证书链，攻击者若能让不同租户共用同一个压缩字典或缓存条目，或声明一个巨大的解压长度，就能探测他人的证书内容或耗尽内存。本包为 Go TLS 服务端提供供握手路径调用的 RFC 8879 证书压缩实现。

## 安全边界

- 服务端只在客户端 `compress_certificate` 扩展列出的算法中选择，并按服务端偏好顺序挑选第一个双方都支持的算法。
- 缓存键为 `(Algorithm, DictionaryID, SHA-256(certificate chain bytes))`。缓存条目还按规范化后的 SNI 分租户存放，因此不同租户即使复用同一字典 ID 也不会命中彼此的缓存。
- 字典通过 `DictionaryStore.Register` 按租户注册，`NewConnection` 只用客户端 SNI 对应租户的字典。请求其他租户的字典 ID 返回 `ErrCrossTenantDictionary`；同 ID 可存在于多个租户，但只会解析当前租户的内容。
- `CompressedCertificate.uncompressed_length` 不得超过 `MaxUncompressedLength`（2^24−1）；解压时最多读取声明长度加 1 字节，最终必须恰好等于声明值，否则返回可用 `AlertForError` 映射为 `bad_certificate` 的错误。
- 每个 `Connection` 最多成功发送一个压缩证书；压缩后负载不严格小于原证书消息时回退到未压缩证书。

内置算法只有标准库可实现的 zlib（RFC 算法 1）。Brotli 和 zstd 的算法号保留；可通过 `CodecRegistry.Register` 注入实现。

## 握手路径

```go
store := netsec.NewDictionaryStore()
_ = store.Register("tenant-a.example", "a-2026", dictionaryBytes)

server := netsec.NewServer(netsec.ServerConfig{
    Dictionaries: store,
    Algorithms:   []netsec.Algorithm{netsec.AlgorithmZlib},
})

offer, err := netsec.ParseClientOffer(sni, compressExtBody, dictionaryExtBody)
conn, err := server.NewConnection(offer)
wire, compressed, err := conn.SendCertificate(certificateMessageBytes)
// compressed == true: wire is a CompressedCertificate body.
// compressed == false: wire is the original Certificate message body.
```

客户端字典扩展 `0xFE01` 的格式为本包约定：`uint16 total_length`，随后重复 `uint8 id_length` + ID 字节。字典内容永不直接由客户端提供。

## 验证

`go test ./...` 覆盖客户端未列出算法、跨租户字典拒绝、缓存租户隔离、解压长度偏大/偏小、24 位上限、压缩变大回退及单连接单次发送。

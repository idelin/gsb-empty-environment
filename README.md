# mdoc阅读器认证

阅读器证书没有对应的扩展用途，却能让设备对会话转录签名。ReaderAuth证书必须带阅读器用途，且签名覆盖的会话转录包含交接、阅读器密钥和设备密钥。用途和转录都匹配的阅读器认证接受。可导入的Go库供钱包调用。换一张没有该用途的证书并改转录中的交接字节。

## 用法

```go
import "example.org/netsec/q0123"

// 钱包侧：验证阅读器认证
err := netsec.VerifyReaderAuth(readerCert, readerAuth, netsec.SessionTranscript{
    DeviceEngagement: engagementBytes, // 含设备密钥
    ReaderKey:        eReaderKeyBytes, // 阅读器密钥
    Handover:         handoverBytes,   // 交接结构
}, itemsRequestBytes, &netsec.VerifyOptions{Roots: roots})
```

- 证书必须携带阅读器扩展用途 OID `1.0.18013.5.1.6`，否则返回 `ErrReaderUsageMissing`。
- 签名必须覆盖包含交接、阅读器密钥和设备密钥的会话转录，否则返回 `ErrSignatureMismatch`。
- 转录任一要素缺失返回 `ErrTranscriptIncomplete`。

阅读器侧用 `netsec.SignReaderAuth` 生成 COSE_Sign1 阅读器认证。

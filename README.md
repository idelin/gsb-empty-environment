# 时间戳令牌印记与签发证书用途

签名制品的时间戳令牌可能出自攻击者的假时间戳服务，或借别的文件的令牌证明签名早于证书过期。为Go验签库写可导入的 RFC 3161 令牌核对包：messageImprint 的算法与摘要须等于待证数据摘要，nonce 等于请求 nonce，policy OID 在白名单；签发证书的 EKU 须为唯一的 critical id-kp-timeStamping；genTime 在证书有效期内，加上 accuracy 不晚于验证时刻。

## 实现

可导入包位于 `rfc3161`（仅依赖 Go 标准库），入口为 `rfc3161.Verify`：

```go
info, err := rfc3161.Verify(tokenDER, rfc3161.Options{
    HashAlgorithm:   crypto.SHA256,
    HashedMessage:   artifactDigest,
    Nonce:           requestNonce,          // nil 时令牌也必须不含 nonce
    PolicyWhitelist: []string{"1.2.3.4.5"}, // 不允许为空（fail-closed）
    Roots:           trustedTSARoots,      // nil 则跳过证书链校验
    CurrentTime:     time.Now(),            // 零值取 time.Now
})
```

`Verify` 按顺序强制以下检查，任一失败即返回带哨兵错误（`ErrMessageImprint`、
`ErrNonce`、`ErrPolicy`、`ErrEKU`、`ErrTime`、`ErrSignature`、`ErrChain`、
`ErrInvalidToken`）的包装错误，可用 `errors.Is` 判定：

- 结构必须是单 SignerInfo 的 CMS SignedData，封装 `id-ct-TSTInfo`，且签名证书
  内嵌于令牌证书袋并匹配 SignerIdentifier（IssuerAndSerialNumber 或 SKI）。
- `messageImprint` 的算法 OID 与摘要值同时匹配待证数据摘要；算法不得带参数。
- TSTInfo nonce 与请求 nonce 严格相等（双方都缺省也算匹配，单边出现即拒绝）。
- TSTInfo policy OID 必须命中调用方白名单。
- 签名证书的 EKU 扩展必须存在、标记为 critical、且只含 `id-kp-timeStamping`
  （1.3.6.1.5.5.7.3.8）一个用途。
- `genTime` 落在证书 NotBefore/NotAfter 之内；`genTime + accuracy` 不得晚于
  验证时刻，防止借 accuracy 窗口把未来时间伪装成已签发。
- CMS 签名与 message-digest 属性按 RFC 5652 校验，支持 RSA PKCS#1 v1.5、
  RSA-PSS、ECDSA、Ed25519；提供 Roots 时再做 timeStamping EKU 的证书链校验。

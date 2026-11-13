# RSA PKCS#1 v1.5签名的严格重建比较

公钥指数为 3 时，宽松解析 DigestInfo 后再接任意字节，攻击者可以不知道私钥就构造出能通过的签名。为Go验签包写可导入的 RSA PKCS#1 v1.5 验证：公钥运算得到的整块 EM 须逐字节等于 00 01 FF…FF 00 加 DigestInfo，FF 至少 8 个且填满，DigestInfo 只接受 SHA-256 的 OID 与 NULL 参数，尾部不得有任何多余字节；e 小于 65537 的公钥拒绝。用立方根构造的伪造签名、尾部垃圾和合法签名验证。

## 用法

```go
digest := sha256.Sum256(message)
if err := netsec.VerifyPKCS1v15SHA256(pub, digest[:], signature); err != nil {
	// 验签失败
}
```

也可以使用与 `crypto/rsa` 参数形式兼容的调用：

```go
err := netsec.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], signature)
```

实现只接受 SHA-256，并要求完整解码后的消息逐字节匹配标准编码。公钥指数小于 65537 时返回 `ErrInvalidPublicKey`。

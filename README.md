# gem签名剥离与重名成员

gem 的 tar 里有 metadata.gz、data.tar.gz、checksums.yaml.gz 及对应 .sig，攻击者剥掉签名让低安全策略放行，或放两份同名 data.tar.gz 使校验与解包读到不同内容。为Go镜像服务实现可导入的 gem 校验包：成员名重复即拒；HighSecurity 要求 metadata 与 data 都有有效 RSA-SHA256 签名且证书链终止于信任根，MediumSecurity 对已签名者校验但允许整体未签名，只剥掉其中一个 .sig 一律拒绝；checksums.yaml 里的 SHA512 须与实际成员一致。用剥签名、重名成员、半签和错链验证。

## 使用

```go
roots := x509.NewCertPool()
roots.AddCert(rootCA)

// source 可以是 io.Reader、[]byte 或文件路径。
err := netsec.ValidateGem(gemBytes, netsec.HighSecurity, roots)
```

需要逐项查看已验证内容时使用：

```go
gem, err := netsec.InspectGem(gemBytes, netsec.HighSecurity, roots)
// gem.Members、gem.Checksums、gem.Signatures
```

外部证书格式（例如 `.sig` 只保存裸 RSA 签名）可通过 `Options` 提供：

```go
err := netsec.ValidateGem(gemBytes, netsec.Options{
    Security:   netsec.HighSecurity,
    Roots:      roots,
    Signer:     leaf,
    SignerChain: []*x509.Certificate{intermediate},
})
```

支持 PKCS#7/CMS detached SignedData（签名内嵌证书链）、PEM（证书与签名块）
以及证书加裸签名的组合。CMS 只接受 SHA-256 摘要和 RSA PKCS#1 v1.5 签名。

## 安全规则

- 外层 tar 的成员名先做路径归一化；重复名称、绝对路径和 `..` 路径直接拒绝。
- 整包只解析一次，签名和 SHA-512 校验都读取同一份 `Members` 字节。
- HighSecurity：缺少 `metadata.gz.sig` 或 `data.tar.gz.sig` 即 `ErrUnsigned`。
- MediumSecurity：两者都缺失时允许无签名包；只缺一个时返回 `ErrPartialSignature`。
- 出现 `checksums.yaml.gz.sig` 时必须验证；它存在而核心两个签名缺失也视为半签。
- `checksums.yaml.gz` 必须包含 `metadata.gz`、`data.tar.gz` 的 SHA-512，且每个
  SHA-512 条目都必须对应真实成员并通过常量时间比较。

## 测试

```sh
go test ./...
```

测试覆盖有效签名、整体剥签名、半签、重复/归一化重名成员、错误证书链、
成员篡改、SHA-512 不匹配、裸 RSA 签名和 PEM 签名。

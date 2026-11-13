# 洋葱描述符签名链与计数回滚

攻击者向 Tor v3 HSDir 上传伪造或回滚的洋葱服务描述符。为Go中继写可导入的描述符准入包：hs-descriptor 版本须为 3，签名证书须由当前时段的 blinded key 签发且未过期，签名覆盖固定前缀加正文，revision-counter 须大于缓存值；验签由调用方注入。用改版本、证书过期、改正文、counter 回滚验证。

## 包说明

`netsec` 是可导入的 HSDir 描述符准入包，模块路径为 `example.org/netsec/q0854`。

- `admit.go`：准入策略与按 blinded key 隔离的 revision-counter 缓存（`Admitter`）。
- `descriptor.go`：v3 文本描述符与 Tor Ed25519 二进制证书（cert-spec.txt §4）解析。
- `admit_test.go`：基于标准库 `crypto/ed25519` 的注入验签器与全部拒绝场景测试。

## 准入规则

`Admitter.Admit(desc, currentBlindedKey)` 按顺序强制：

1. `hs-descriptor` 版本必须为 `3`，否则返回 `ErrUnsupportedVersion`；
2. 证书类型必须为 signing-key（type 4）、密钥类型 Ed25519，且未过期
   （过期时间须严格晚于当前时间），否则返回 `ErrCertificateExpired` 或
   `ErrMalformedDescriptor`；
3. 证书签名必须由“当前时段”的 blinded key 验过，否则返回
   `ErrCertificateSignature`——时段切换后旧 blinded key 签发的证书即被拒；
4. 描述符签名必须在固定前缀 `Tor onion service descriptor sig v3`
   拼接正文之上验证通过，否则返回 `ErrBadSignature`；
5. `revision-counter` 必须严格大于该 blinded key 的缓存值，否则返回
   `ErrRevisionRollback`；通过后缓存值才更新。

所有密码学操作经 `Verifier` 接口注入，包本身不绑定具体密钥库或共识实现：

```go
type Verifier interface {
    VerifyDescriptorCert(cert *SigningCertificate, blindedKey ed25519.PublicKey) error
    VerifyDescriptorSignature(signingKey ed25519.PublicKey, signedMessage, signature []byte) error
}
```

## 用法

```go
admitter := netsec.NewAdmitter(relayVerifier) // 可选 netsec.WithClock(...)

desc, err := netsec.ParseDescriptor(rawDescriptorBytes)
if err != nil {
    return err // ErrMalformedDescriptor
}
if err := admitter.Admit(desc, currentPeriodBlindedKey); err != nil {
    return err // 用 errors.Is 匹配上述哨兵错误
}
// 准入成功，可写入 HSDir 缓存。
```

## 测试

`go test ./...` 覆盖：改版本（2/4）、证书过期（含到期边界）、改正文与翻转
签名字节、counter 回滚与等值重放，另加错误时段 blinded key、证书类型错误、
按 blinded key 隔离的缓存、畸形输入以及固定前缀绑定等用例。
